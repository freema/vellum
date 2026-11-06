// Package config loads vellum configuration from environment variables.
// vellum has no config database: everything is env (and later vellum.yaml).
package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Sharing modes for VELLUM_SHARING. Sharing is the only feature that answers
// an unauthenticated request, so it is off unless asked for, and the middle
// setting exists to keep the decision to publish with the human.
const (
	// SharingOff registers no public route and no share tool.
	SharingOff = "off"
	// SharingUI serves public links, but only the workspace can mint them.
	SharingUI = "ui"
	// SharingOn additionally gives agents the share_note/unshare_note tools.
	SharingOn = "on"
)

// DefaultHost is the HTTP listen address when VELLUM_HOST is unset: loopback
// only, so a vault without auth is never exposed to the network by accident.
const DefaultHost = "127.0.0.1"

// Config holds the runtime configuration.
type Config struct {
	// Host is the HTTP listen address (env VELLUM_HOST, default 127.0.0.1).
	// The default keeps a bare binary reachable only from its own machine,
	// since auth is off by default; the Docker image sets 0.0.0.0.
	Host string
	// Port is the HTTP listen port (env PORT).
	Port string
	// VaultPath is the root directory of the markdown vault (env VELLUM_VAULT_PATH).
	VaultPath string

	// InitStructure creates inbox/projects/archive in an empty vault at
	// startup (env VELLUM_INIT_STRUCTURE, default true).
	InitStructure bool
	// InboxDir, ProjectsDir, ArchiveDir name the conventional directories
	// (env VELLUM_INBOX_DIR / VELLUM_PROJECTS_DIR / VELLUM_ARCHIVE_DIR).
	InboxDir    string
	ProjectsDir string
	ArchiveDir  string
	// ContextFile is the file name of a folder's description note
	// (env VELLUM_CONTEXT_FILE, default _index.md).
	ContextFile string
	// Ranker picks the search ranking while BM25F rolls out (env
	// VELLUM_RANKER=bm25f|legacy, default bm25f). Goes once the legacy
	// ranker is removed.
	Ranker string

	// Rescan is how often the in-memory index checks the disk for notes
	// changed outside vellum (env VELLUM_RESCAN, a Go duration such as 5s
	// or 1m, or a plain number of seconds; off or 0 disables; default 5s).
	// The check is lazy: it runs before an index read once the interval has
	// passed, never in the background. 0 means off.
	Rescan time.Duration
	// RescanInvalid holds an unparseable VELLUM_RESCAN value, so main can
	// warn about it; Rescan then falls back to the default.
	RescanInvalid string

	// Curator enables the suggest_*/find_* MCP tools
	// (env VELLUM_CURATOR=on|off, default off).
	Curator bool

	// Sharing controls public, read-only links to single notes
	// (env VELLUM_SHARING, default off). See the Sharing* constants.
	Sharing string

	// Notify enables the periodic SMTP task digest (env VELLUM_NOTIFY=on|off,
	// default off). SMTP_* settings are read by the notify package.
	Notify bool

	// Sentry error reporting (off unless SENTRY_DSN is set). SentryEnvironment
	// tags events (env SENTRY_ENVIRONMENT, default "production").
	SentryDSN         string
	SentryEnvironment string

	// AllowedOrigins are browser origins allowed to reach /mcp
	// (env VELLUM_ALLOWED_ORIGINS, comma-separated).
	AllowedOrigins []string

	// CORSOrigins get CORS response headers (env CORS_ORIGINS; defaults to
	// AllowedOrigins). Kept separate to recycle openclaw-mcp env names.
	CORSOrigins []string

	// OAuth (PHY-112, env names recycled from openclaw-mcp).
	AuthEnabled  bool     // AUTH_ENABLED (default false — warn loudly)
	ClientID     string   // VELLUM_CLIENT_ID (default "vellum")
	ClientSecret string   // VELLUM_CLIENT_SECRET (>= 32 chars when auth on)
	IssuerURL    string   // VELLUM_ISSUER_URL (public URL behind a proxy)
	RedirectURIs []string // VELLUM_REDIRECT_URIS (empty = allow any)
	TrustProxy   bool     // TRUST_PROXY (behind Caddy/Traefik)
}

// Load reads configuration from the environment, applying defaults.
func Load() Config {
	cfg := Config{
		Host:              getenv("VELLUM_HOST", DefaultHost),
		Port:              getenv("PORT", "8080"),
		VaultPath:         getenv("VELLUM_VAULT_PATH", "./vault"),
		InitStructure:     getbool("VELLUM_INIT_STRUCTURE", true),
		InboxDir:          getenv("VELLUM_INBOX_DIR", "inbox"),
		ProjectsDir:       getenv("VELLUM_PROJECTS_DIR", "projects"),
		ArchiveDir:        getenv("VELLUM_ARCHIVE_DIR", "archive"),
		ContextFile:       getenv("VELLUM_CONTEXT_FILE", "_index.md"),
		Ranker:            strings.ToLower(strings.TrimSpace(getenv("VELLUM_RANKER", "bm25f"))),
		Curator:           getenv("VELLUM_CURATOR", "off") == "on",
		Sharing:           sharingMode(os.Getenv("VELLUM_SHARING")),
		Notify:            getbool("VELLUM_NOTIFY", false),
		SentryDSN:         os.Getenv("SENTRY_DSN"),
		SentryEnvironment: getenv("SENTRY_ENVIRONMENT", "production"),
		AllowedOrigins: getlist("VELLUM_ALLOWED_ORIGINS",
			[]string{"https://claude.ai", "https://claude.com"}),
		AuthEnabled:  getbool("AUTH_ENABLED", false),
		ClientID:     getenv("VELLUM_CLIENT_ID", "vellum"),
		ClientSecret: os.Getenv("VELLUM_CLIENT_SECRET"),
		IssuerURL:    os.Getenv("VELLUM_ISSUER_URL"),
		RedirectURIs: getlist("VELLUM_REDIRECT_URIS", nil),
		TrustProxy:   getbool("TRUST_PROXY", false),
	}
	cfg.CORSOrigins = getlist("CORS_ORIGINS", cfg.AllowedOrigins)
	var ok bool
	if cfg.Rescan, ok = rescanInterval(os.Getenv("VELLUM_RESCAN")); !ok {
		cfg.RescanInvalid = os.Getenv("VELLUM_RESCAN")
	}
	if cfg.IssuerURL == "" {
		// Without a public URL the OAuth metadata can only point at
		// localhost — fine for local use, must be overridden behind a proxy.
		cfg.IssuerURL = "http://localhost:" + cfg.Port
	}
	return cfg
}

// SharingEnabled reports whether public share links are served at all.
func (c Config) SharingEnabled() bool { return c.Sharing != SharingOff }

// ShareTools reports whether agents may mint links over MCP.
func (c Config) ShareTools() bool { return c.Sharing == SharingOn }

// sharingMode normalizes VELLUM_SHARING. Anything unrecognized reads as off:
// a typo must not be the reason a vault starts answering anonymous requests.
func sharingMode(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "on", "true", "1", "yes":
		return SharingOn
	case "ui", "web":
		return SharingUI
	default:
		return SharingOff
	}
}

// DefaultRescan is the VELLUM_RESCAN default. Measured: a rescan of an
// unchanged 5,000-note vault costs a few milliseconds of stat calls, so
// checking at most every five seconds is invisible next to a search.
const DefaultRescan = 5 * time.Second

// rescanInterval parses VELLUM_RESCAN: empty means the default, off (or 0,
// 0s, false, no) disables, otherwise a Go duration or a whole number of
// seconds. ok is false for anything else, which falls back to the default
// rather than silently turning the rescan off.
func rescanInterval(raw string) (d time.Duration, ok bool) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	switch raw {
	case "":
		return DefaultRescan, true
	case "off", "0", "false", "no", "none":
		return 0, true
	}
	if n, err := strconv.Atoi(raw); err == nil && n > 0 {
		return time.Duration(n) * time.Second, true
	}
	if d, err := time.ParseDuration(raw); err == nil && d >= 0 { // 0s = off
		return d, true
	}
	return DefaultRescan, false
}

func getlist(key string, fallback []string) []string {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	var out []string
	for _, s := range strings.Split(raw, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getbool(key string, fallback bool) bool {
	switch os.Getenv(key) {
	case "1", "true", "TRUE", "True", "yes", "on":
		return true
	case "0", "false", "FALSE", "False", "no", "off":
		return false
	}
	return fallback
}
