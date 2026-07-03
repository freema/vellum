// Package config loads vellum configuration from environment variables.
// vellum has no config database: everything is env (and later vellum.yaml).
package config

import "os"

// Config holds the runtime configuration.
type Config struct {
	// Port is the HTTP listen port (env PORT).
	Port string
	// VaultPath is the root directory of the markdown vault (env VELLUM_VAULT_PATH).
	VaultPath string
}

// Load reads configuration from the environment, applying defaults.
func Load() Config {
	return Config{
		Port:      getenv("PORT", "8080"),
		VaultPath: getenv("VELLUM_VAULT_PATH", "./vault"),
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
