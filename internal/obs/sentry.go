// Package obs wires optional Sentry error reporting. It is disabled unless
// SENTRY_DSN is set; when enabled it captures panics and any error passed to
// Capture, so operators can see when something breaks. The DSN is never
// hardcoded — vellum is open source, so each deployment sets its own in the
// environment.
package obs

import (
	"time"

	"github.com/getsentry/sentry-go"
)

var enabled bool

// Config is the Sentry configuration read from the environment.
type Config struct {
	DSN         string
	Environment string
	Release     string
}

// Init initializes Sentry when a DSN is present and returns a flush function
// to defer. With no DSN it is a no-op (returns a no-op flush).
func Init(cfg Config) (func(), error) {
	if cfg.DSN == "" {
		return func() {}, nil
	}
	if err := sentry.Init(sentry.ClientOptions{
		Dsn:         cfg.DSN,
		Environment: cfg.Environment,
		Release:     cfg.Release,
	}); err != nil {
		return func() {}, err
	}
	enabled = true
	return func() { sentry.Flush(2 * time.Second) }, nil
}

// Enabled reports whether Sentry is active.
func Enabled() bool { return enabled }

// Capture sends an error to Sentry with optional tags. No-op when disabled.
func Capture(err error, tags map[string]string) {
	if !enabled || err == nil {
		return
	}
	sentry.WithScope(func(scope *sentry.Scope) {
		for k, v := range tags {
			scope.SetTag(k, v)
		}
		sentry.CaptureException(err)
	})
}

// CaptureMessage sends a message-level event. No-op when disabled.
func CaptureMessage(msg string) {
	if !enabled {
		return
	}
	sentry.CaptureMessage(msg)
}
