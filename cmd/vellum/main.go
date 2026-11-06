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
	"github.com/freema/vellum/internal/share"
	"github.com/freema/vellum/internal/vault"
)

// version is injected at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	// Nothing but -version may write to stdout: in stdio mode it carries
	// JSON-RPC only, so every message, even a startup error, goes to stderr.
	opts, err := parseArgs(os.Args[1:], os.Stderr)
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		os.Exit(2)
	}

	if opts.version {
		fmt.Println(version)
		return
	}

	cfg := config.Load()

	if opts.healthcheck {
		os.Exit(runHealthcheck(cfg))
	}

	cfg.VaultPath, err = resolveVaultPath(opts.vault, os.Getenv("VELLUM_VAULT_PATH"), opts.stdio)
	if err == nil {
		// HTTP mode creates its vault as it always has (the Docker volume may
		// start empty); stdio mode only with an explicit -init.
		err = ensureVaultDir(cfg.VaultPath, !opts.stdio || opts.initVault)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "vellum:", err)
		os.Exit(1)
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

	v, err := vault.New(cfg.VaultPath, vault.WithContextFile(cfg.ContextFile))
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
	// Notes changed outside vellum (an editor, git, a second vellum
	// process) reach the index through a lazy rescan before index reads.
	if cfg.RescanInvalid != "" {
		logger.Warn("ignoring VELLUM_RESCAN, using the default",
			"value", cfg.RescanInvalid, "default", config.DefaultRescan.String())
	}
	index.SetRescanInterval(cfg.Rescan)
	if cfg.Rescan == 0 {
		logger.Info("index rescan is off (VELLUM_RESCAN=off): changes made outside vellum show after a restart")
	}

	// One searcher for MCP and the REST API — they share the content cache.
	searcher := vault.NewScanSearcher(v, index)
	if err := searcher.SetRanker(cfg.Ranker); err != nil {
		// VELLUM_RANKER only exists to compare rankers during the BM25F
		// rollout; a typo there must not keep the server from starting.
		logger.Warn("ignoring VELLUM_RANKER, using bm25f", "error", err)
	} else if cfg.Ranker == vault.RankerLegacy {
		logger.Info("search uses the legacy ranker (VELLUM_RANKER=legacy)")
	}

	// Public share links (VELLUM_SHARING, off by default). The store is
	// opened even for the stdio path: a link must keep pointing at its note
	// when that note is moved or deleted, wherever the change came from.
	var shares *share.Store
	if cfg.SharingEnabled() {
		shares, err = share.Open(cfg.VaultPath)
		if err != nil {
			// A vault whose link list is unreadable still serves everything
			// else; refusing to boot over it would be the worse trade.
			logger.Error("sharing disabled: cannot open the share store", "error", err)
			shares = nil
		} else {
			defer func() { _ = shares.Close() }()
			logger.Info("public share links enabled",
				"mode", cfg.Sharing, "links", shares.Len(), "tools", cfg.ShareTools())
		}
	}

	mcpSrv := mcpserver.New(mcpserver.Deps{
		Vault:    v,
		Index:    index,
		Searcher: searcher,
		Structure: vault.Structure{
			Inbox:    cfg.InboxDir,
			Projects: cfg.ProjectsDir,
			Archive:  cfg.ArchiveDir,
		},
		Version:    version,
		Curator:    cfg.Curator,
		WebsiteURL: cfg.IssuerURL,
		Shares:     shares,
		ShareTools: cfg.ShareTools(),
	})

	if opts.stdio {
		logger.Info("serving MCP over stdio", "vault", v.Root(), "notes", index.Len())
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
	}

	ln, err := listen(cfg)
	if err != nil {
		logger.Error("listen", "addr", listenAddr(cfg), "error", err)
		os.Exit(1)
	}
	addr := boundAddr(cfg, ln)
	switch {
	case cfg.AuthEnabled:
	case isLoopback(ln.Addr()):
		logger.Info("auth is disabled; /mcp is reachable from this machine only", "addr", addr)
	default:
		logger.Warn("AUTH IS DISABLED — /mcp on "+addr+" is open to anyone who can reach it", "addr", addr)
	}

	recorder := activity.New()
	structure := vault.Structure{
		Inbox:    cfg.InboxDir,
		Projects: cfg.ProjectsDir,
		Archive:  cfg.ArchiveDir,
	}
	api := &httpapi.API{
		Vault:     v,
		Index:     index,
		Searcher:  searcher,
		Structure: structure,
		Activity:  recorder,
		Endpoint:  strings.TrimRight(cfg.IssuerURL, "/") + "/mcp",
		Curator:   cfg.Curator,
		Shares:    shares,
		Sharing:   cfg.Sharing,
		PublicURL: cfg.IssuerURL,
	}
	if authProvider != nil {
		api.Revoker = authProvider // a nil *auth.Provider would be a non-nil interface
	}
	srv := &http.Server{
		Addr: addr,
		Handler: httpapi.NewRouter(version, httpapi.Options{
			MCPHandler:     mcpserver.Handler(mcpSrv),
			API:            api,
			SPA:            vellum.DistFS(),
			AllowedOrigins: cfg.AllowedOrigins,
			Auth:           authProvider,
			CORSOrigins:    cfg.CORSOrigins,
			Activity:       recorder,
			Shares:         shares,
			Vault:          v,
			TrustProxy:     cfg.TrustProxy,
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
		logger.Info("vellum starting", "version", version, "addr", addr, "vault", v.Root(), "notes", index.Len())
		errCh <- srv.Serve(ln)
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

// runHealthcheck hits the local /healthz endpoint. It exists so the distroless
// image (no shell, no curl) can use the binary itself as its Docker healthcheck.
func runHealthcheck(cfg config.Config) int {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(healthcheckURL(cfg))
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
