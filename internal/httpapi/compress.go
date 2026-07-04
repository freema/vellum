package httpapi

import (
	"compress/gzip"
	"net/http"
	"strings"
	"sync"
)

// gzipPool reuses gzip writers across requests — allocation-free steady state.
var gzipPool = sync.Pool{
	New: func() any {
		w, _ := gzip.NewWriterLevel(nil, gzip.BestSpeed)
		return w
	},
}

// compress wraps a handler with on-the-fly gzip for clients that accept it.
// Only text-like content types are compressed (JSON, HTML, JS, CSS, SVG…);
// range requests and already-encoded responses pass through untouched. The
// MCP endpoint is never wrapped by callers — its SSE stream must not be
// buffered.
func compress(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") ||
			r.Header.Get("Range") != "" {
			next.ServeHTTP(w, r)
			return
		}
		gw := &gzipResponseWriter{ResponseWriter: w}
		defer gw.close()
		next.ServeHTTP(gw, r)
	})
}

// gzipResponseWriter lazily switches to gzip on the first write, once the
// status and content type are known.
type gzipResponseWriter struct {
	http.ResponseWriter
	gz     *gzip.Writer
	status int
	plain  bool // pass-through: not compressible
}

func (g *gzipResponseWriter) WriteHeader(status int) {
	if g.status != 0 {
		return
	}
	g.status = status
	h := g.Header()
	if status == http.StatusNoContent || status == http.StatusNotModified ||
		h.Get("Content-Encoding") != "" || !compressible(h.Get("Content-Type")) {
		g.plain = true
		g.ResponseWriter.WriteHeader(status)
		return
	}
	h.Del("Content-Length")
	h.Set("Content-Encoding", "gzip")
	h.Add("Vary", "Accept-Encoding")
	g.ResponseWriter.WriteHeader(status)
	gz := gzipPool.Get().(*gzip.Writer)
	gz.Reset(g.ResponseWriter)
	g.gz = gz
}

func (g *gzipResponseWriter) Write(b []byte) (int, error) {
	if g.status == 0 {
		if g.Header().Get("Content-Type") == "" {
			g.Header().Set("Content-Type", http.DetectContentType(b))
		}
		g.WriteHeader(http.StatusOK)
	}
	if g.gz == nil {
		return g.ResponseWriter.Write(b)
	}
	return g.gz.Write(b)
}

// Flush supports handlers that stream (none of the wrapped ones do today).
func (g *gzipResponseWriter) Flush() {
	if g.gz != nil {
		_ = g.gz.Flush()
	}
	if f, ok := g.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (g *gzipResponseWriter) close() {
	if g.gz != nil {
		_ = g.gz.Close()
		gzipPool.Put(g.gz)
		g.gz = nil
	}
}

// compressible reports whether a content type benefits from gzip.
func compressible(ct string) bool {
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	ct = strings.TrimSpace(ct)
	if strings.HasPrefix(ct, "text/") {
		return true
	}
	switch ct {
	case "application/json", "application/javascript", "application/x-javascript",
		"application/xml", "application/wasm", "image/svg+xml":
		return true
	}
	return false
}
