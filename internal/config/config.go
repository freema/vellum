// Package config loads vellum configuration from environment variables.
// vellum has no config database: everything is env (and later vellum.yaml).
package config

import (
	"os"
	"strings"
)

// Config holds the runtime configuration.
type Config struct {
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

	// Curator enables the suggest_*/find_* MCP tools
	// (env VELLUM_CURATOR=on|off, default off).
	Curator bool

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
		Port:          getenv("PORT", "8080"),
		VaultPath:     getenv("VELLUM_VAULT_PATH", "./vault"),
		InitStructure: getbool("VELLUM_INIT_STRUCTURE", true),
		InboxDir:      getenv("VELLUM_INBOX_DIR", "inbox"),
		ProjectsDir:   getenv("VELLUM_PROJECTS_DIR", "projects"),
		ArchiveDir:    getenv("VELLUM_ARCHIVE_DIR", "archive"),
		Curator:       getenv("VELLUM_CURATOR", "off") == "on",
		Notify:        getbool("VELLUM_NOTIFY", false),
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
	if cfg.IssuerURL == "" {
		// Without a public URL the OAuth metadata can only point at
		// localhost — fine for local use, must be overridden behind a proxy.
		cfg.IssuerURL = "http://localhost:" + cfg.Port
	}
	return cfg
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
