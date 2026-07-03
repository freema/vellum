// Package httpapi provides the HTTP surface of vellum: health endpoint,
// and later the JSON REST API for the SPA (/api/*).
package httpapi

import (
	"encoding/json"
	"net/http"
)

// NewRouter returns the root HTTP handler.
func NewRouter(version string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealthz(version))
	return mux
}

func handleHealthz(version string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status":  "ok",
			"version": version,
		})
	}
}
