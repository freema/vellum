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

	// AllowedOrigins are browser origins allowed to reach /mcp
	// (env VELLUM_ALLOWED_ORIGINS, comma-separated).
	AllowedOrigins []string
}

// Load reads configuration from the environment, applying defaults.
func Load() Config {
	return Config{
		Port:          getenv("PORT", "8080"),
		VaultPath:     getenv("VELLUM_VAULT_PATH", "./vault"),
		InitStructure: getbool("VELLUM_INIT_STRUCTURE", true),
		InboxDir:      getenv("VELLUM_INBOX_DIR", "inbox"),
		ProjectsDir:   getenv("VELLUM_PROJECTS_DIR", "projects"),
		ArchiveDir:    getenv("VELLUM_ARCHIVE_DIR", "archive"),
		AllowedOrigins: getlist("VELLUM_ALLOWED_ORIGINS",
			[]string{"https://claude.ai", "https://claude.com"}),
	}
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
