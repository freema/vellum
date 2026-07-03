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
	"syscall"
	"time"

	"github.com/freema/vellum/internal/config"
	"github.com/freema/vellum/internal/httpapi"
)

// version is injected at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	healthcheck := flag.Bool("healthcheck", false, "probe the running server's /healthz and exit (used by Docker HEALTHCHECK)")
	showVersion := flag.Bool("version", false, "print version and exit")
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

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           httpapi.NewRouter(version),
		ReadHeaderTimeout: 10 * time.Second,
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
