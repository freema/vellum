// Package httpapi provides the HTTP surface of vellum: health endpoint,
// the /mcp mount, and later the JSON REST API for the SPA (/api/*).
package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"strings"

	"github.com/freema/vellum/internal/activity"
	"github.com/freema/vellum/internal/auth"
	"github.com/freema/vellum/internal/obs"
)

// Options configure the router.
type Options struct {
	// MCPHandler, when set, is mounted at /mcp behind the origin check.
	MCPHandler http.Handler
	// API, when set, mounts the JSON REST surface under /api/*.
	API *API
	// SPA, when set, serves static files with an index.html fallback for
	// anything that is not /api/*, /mcp or an OAuth route (PHY-116 supplies
	// the embedded dist).
	SPA fs.FS
	// AllowedOrigins are the browser origins allowed to call /mcp and
	// /api. Same-origin requests and requests without an Origin header
	// (CLI clients) always pass.
	AllowedOrigins []string
	// Auth, when set, mounts the OAuth endpoints and guards /mcp + /api
	// with bearer verification. Nil = auth disabled, everything open.
	Auth *auth.Provider
	// CORSOrigins get CORS response headers (browser clients).
	CORSOrigins []string
	// Activity, when set, records MCP sessions and tool calls hitting /mcp.
	Activity *activity.Recorder
}

// NewRouter returns the root HTTP handler.
func NewRouter(version string, opts Options) http.Handler {
	apiVersion = version
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealthz(version, opts.Auth != nil))
	guard := func(h http.Handler) http.Handler {
		if opts.Auth != nil {
			h = opts.Auth.RequireBearer(h)
		}
		return originCheck(opts.AllowedOrigins, h)
	}
	if opts.MCPHandler != nil {
		mux.Handle("/mcp", guard(mcpRecord(opts.Activity, opts.MCPHandler)))
	}
	if opts.API != nil {
		apiMux := http.NewServeMux()
		opts.API.routes(apiMux)
		mux.Handle("/api/", guard(apiMux))
	}
	if opts.Auth != nil {
		opts.Auth.Routes(mux)
	}
	if opts.SPA != nil {
		mux.HandleFunc("GET /favicon.ico", faviconHandler(opts.SPA))
		mux.Handle("/", spaHandler(opts.SPA))
	}
	return auth.CORS(opts.CORSOrigins, recoverAndReport(opts.Activity, mux))
}

// recoverAndReport turns a panic into a 500, reports it to Sentry (when
// enabled) and records an error event into the activity feed so operators see
// MCP/API breakage in the workspace UI, not only in the Sentry dashboard.
func recoverAndReport(rec *activity.Recorder, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				err := fmt.Errorf("panic on %s %s: %v", r.Method, r.URL.Path, v)
				obs.Capture(err, map[string]string{"path": r.URL.Path, "method": r.Method})
				if rec != nil {
					rec.Record(activity.Event{
						Source: "system", Actor: "vellum", Kind: "error",
						Target: r.URL.Path, Detail: fmt.Sprintf("%v", v),
					})
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error":"internal error"}`))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// spaHandler serves the embedded SPA: real files as-is, unknown *routes* fall
// back to index.html for client-side routing. A missing path that looks like a
// static asset (its last segment has an extension) returns 404 instead of the
// HTML shell — otherwise e.g. /favicon.ico would return HTML and MCP clients
// could not discover a real icon.
func spaHandler(dist fs.FS) http.Handler {
	fileServer := http.FileServerFS(dist)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path != "" {
			if f, err := dist.Open(path); err == nil {
				_ = f.Close()
				fileServer.ServeHTTP(w, r)
				return
			}
			base := path
			if i := strings.LastIndexByte(path, '/'); i >= 0 {
				base = path[i+1:]
			}
			if strings.Contains(base, ".") {
				http.NotFound(w, r)
				return
			}
		}
		http.ServeFileFS(w, r, dist, "index.html")
	})
}

// faviconHandler serves the vellum SVG mark for /favicon.ico so clients that
// probe the classic path (rather than the HTML <link rel="icon">) get an icon.
func faviconHandler(dist fs.FS) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f, err := dist.Open("favicon.svg")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		_, _ = io.Copy(w, f)
	}
}

// originCheck rejects browser cross-origin requests whose Origin is not
// allowlisted. Same-origin requests (the embedded SPA) and non-browser
// clients (no Origin header) are unaffected; authentication is separate.
func originCheck(allowed []string, next http.Handler) http.Handler {
	set := map[string]bool{}
	for _, o := range allowed {
		set[o] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && !set[origin] && !sameOrigin(origin, r.Host) {
			http.Error(w, "origin not allowed", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// sameOrigin reports whether the Origin header points at this server.
func sameOrigin(origin, host string) bool {
	rest, ok := strings.CutPrefix(origin, "https://")
	if !ok {
		rest, ok = strings.CutPrefix(origin, "http://")
	}
	return ok && rest == host
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
