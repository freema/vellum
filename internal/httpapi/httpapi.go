// Package httpapi provides the HTTP surface of vellum: health endpoint,
// the /mcp mount, and later the JSON REST API for the SPA (/api/*).
package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/freema/vellum/internal/auth"
)

// Options configure the router.
type Options struct {
	// MCPHandler, when set, is mounted at /mcp behind the origin check.
	MCPHandler http.Handler
	// AllowedOrigins are the browser origins allowed to call /mcp.
	// Requests without an Origin header (CLI clients) always pass.
	AllowedOrigins []string
	// Auth, when set, mounts the OAuth endpoints and guards /mcp with
	// bearer verification. Nil = auth disabled, everything open.
	Auth *auth.Provider
	// CORSOrigins get CORS response headers (browser clients).
	CORSOrigins []string
}

// NewRouter returns the root HTTP handler.
func NewRouter(version string, opts Options) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealthz(version, opts.Auth != nil))
	if opts.MCPHandler != nil {
		h := opts.MCPHandler
		if opts.Auth != nil {
			h = opts.Auth.RequireBearer(h)
		}
		mux.Handle("/mcp", originCheck(opts.AllowedOrigins, h))
	}
	if opts.Auth != nil {
		opts.Auth.Routes(mux)
	}
	return auth.CORS(opts.CORSOrigins, mux)
}

// originCheck rejects browser cross-origin requests whose Origin is not
// allowlisted. Non-browser clients (no Origin header) are unaffected;
// authentication is a separate layer (PHY-112).
func originCheck(allowed []string, next http.Handler) http.Handler {
	set := map[string]bool{}
	for _, o := range allowed {
		set[o] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" && !set[origin] {
			http.Error(w, "origin not allowed", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func handleHealthz(version string, authEnabled bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":  "ok",
			"version": version,
			"auth":    authEnabled,
		})
	}
}
