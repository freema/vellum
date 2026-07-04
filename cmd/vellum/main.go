// Command vellum is a lightweight, self-hosted MCP server over a folder
// of markdown files.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	vellum "github.com/freema/vellum"
	"github.com/freema/vellum/internal/activity"
	"github.com/freema/vellum/internal/auth"
	"github.com/freema/vellum/internal/config"
	"github.com/freema/vellum/internal/httpapi"
	"github.com/freema/vellum/internal/mcpserver"
	"github.com/freema/vellum/internal/notify"
	"github.com/freema/vellum/internal/obs"
	"github.com/freema/vellum/internal/vault"
)

// version is injected at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	healthcheck := flag.Bool("healthcheck", false, "probe the running server's /healthz and exit (used by Docker HEALTHCHECK)")
	showVersion := flag.Bool("version", false, "print version and exit")
	mcpStdio := flag.Bool("mcp-stdio", false, "serve MCP over stdio instead of HTTP (for local `claude mcp add`)")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	cfg := config.Load()

	if *healthcheck {
		os.Exit(runHealthcheck(cfg))
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	flushSentry, err := obs.Init(obs.Config{
		DSN:         cfg.SentryDSN,
		Environment: cfg.SentryEnvironment,
		Release:     version,
	})
	if err != nil {
		logger.Warn("sentry init failed", "error", err)
	}
	defer flushSentry()
	if obs.Enabled() {
		logger.Info("sentry error reporting enabled", "environment", cfg.SentryEnvironment)
	}

	v, err := openVault(cfg)
	if err != nil {
		logger.Error("open vault", "error", err)
		os.Exit(1)
	}
	if cfg.InitStructure {
		structure := vault.Structure{
			Inbox:    cfg.InboxDir,
			Projects: cfg.ProjectsDir,
			Archive:  cfg.ArchiveDir,
		}
		created, err := v.InitStructure(structure)
		if err != nil {
			logger.Error("init vault structure", "error", err)
			os.Exit(1)
		}
		if created {
			logger.Info("initialized empty vault", "dirs",
				[]string{cfg.InboxDir, cfg.ProjectsDir, cfg.ArchiveDir})
		}
	}

	index := vault.NewIndex(v)
	start := time.Now()
	if err := index.Build(); err != nil {
		logger.Error("build metadata index", "error", err)
		os.Exit(1)
	}
	logger.Info("metadata index built", "notes", index.Len(), "took", time.Since(start).String())

	mcpSrv := mcpserver.New(mcpserver.Deps{
		Vault:    v,
		Index:    index,
		Searcher: vault.NewScanSearcher(v, index),
		Structure: vault.Structure{
			Inbox:    cfg.InboxDir,
			Projects: cfg.ProjectsDir,
			Archive:  cfg.ArchiveDir,
		},
		Version: version,
		Curator: cfg.Curator,
	})

	if *mcpStdio {
		logger.Info("serving MCP over stdio", "vault", cfg.VaultPath)
		if err := mcpSrv.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
			logger.Error("mcp stdio server failed", "error", err)
			os.Exit(1)
		}
		return
	}

	var authProvider *auth.Provider
	if cfg.AuthEnabled {
		authProvider, err = auth.NewProvider(auth.Config{
			Enabled:      true,
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			IssuerURL:    cfg.IssuerURL,
			RedirectURIs: cfg.RedirectURIs,
			TrustProxy:   cfg.TrustProxy,
		})
		if err != nil {
			logger.Error("auth configuration invalid", "error", err)
			os.Exit(1)
		}
		defer authProvider.Close()
		logger.Info("oauth enabled", "client_id", cfg.ClientID, "issuer", authProvider.Issuer())
	} else {
		logger.Warn("AUTH IS DISABLED — /mcp is open to anyone who can reach it")
	}

	recorder := activity.New()
	structure := vault.Structure{
		Inbox:    cfg.InboxDir,
		Projects: cfg.ProjectsDir,
		Archive:  cfg.ArchiveDir,
	}
	srv := &http.Server{
		Addr: ":" + cfg.Port,
		Handler: httpapi.NewRouter(version, httpapi.Options{
			MCPHandler: mcpserver.Handler(mcpSrv),
			API: &httpapi.API{
				Vault:     v,
				Index:     index,
				Searcher:  vault.NewScanSearcher(v, index),
				Structure: structure,
				Activity:  recorder,
				Endpoint:  strings.TrimRight(cfg.IssuerURL, "/") + "/mcp",
				Curator:   cfg.Curator,
			},
			SPA:            vellum.DistFS(),
			AllowedOrigins: cfg.AllowedOrigins,
			Auth:           authProvider,
			CORSOrigins:    cfg.CORSOrigins,
			Activity:       recorder,
		}),
		ReadHeaderTimeout: 10 * time.Second,
	}

	notifyCtx, stopNotify := context.WithCancel(context.Background())
	defer stopNotify()
	if cfg.Notify {
		ncfg := notify.FromEnv(cfg.IssuerURL)
		if ncfg.Valid() {
			logger.Info("smtp digest enabled", "interval", ncfg.Interval.String(), "to", ncfg.To)
			go notify.New(ncfg, index, logger).Loop(notifyCtx)
		} else {
			logger.Warn("VELLUM_NOTIFY is on but SMTP is not fully configured — digest disabled (need SMTP_HOST, SMTP_FROM, SMTP_TO)")
		}
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("vellum starting", "version", version, "port", cfg.Port, "vault", cfg.VaultPath)
		errCh <- srv.ListenAndServe()
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-errCh:
		logger.Error("server failed", "error", err)
		os.Exit(1)
	case sig := <-stop:
		logger.Info("shutting down", "signal", sig.String())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("shutdown failed", "error", err)
		os.Exit(1)
	}
}

// openVault creates the vault directory if needed and opens it.
func openVault(cfg config.Config) (*vault.Vault, error) {
	if err := os.MkdirAll(cfg.VaultPath, 0o755); err != nil {
		return nil, err
	}
	return vault.New(cfg.VaultPath)
}

// runHealthcheck hits the local /healthz endpoint. It exists so the distroless
// image (no shell, no curl) can use the binary itself as its Docker healthcheck.
func runHealthcheck(cfg config.Config) int {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://localhost:" + cfg.Port + "/healthz")
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck:", err)
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, "healthcheck: unexpected status", resp.Status)
		return 1
	}
	return 0
}
