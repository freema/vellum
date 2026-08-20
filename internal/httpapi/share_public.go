package httpapi

import (
	"io/fs"
	"net"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/freema/vellum/internal/share"
	"github.com/freema/vellum/internal/vault"
)

// ---- the anonymous side: /s/{token} ----
//
// This is the only part of vellum that answers without a bearer token, so it
// is deliberately narrow: three GET routes, no query parameters, no way to
// enumerate, and nothing served that the holder of the link was not given.
// The token is the whole capability — everything else about the vault (paths,
// folder structure, other notes, frontmatter) stays invisible.

// publicShares serves the read-only pages behind a share link.
type publicShares struct {
	store *share.Store
	vault *vault.Vault
	// spa is the embedded workspace bundle, whose index.html boots the
	// reader. A plain `go build` has none; then the markdown is served
	// directly so the link still works.
	spa     fs.FS
	limiter *ipLimiter
}

// PublicMount is the URL prefix of the public routes.
const PublicMount = "/s/"

func (p *publicShares) handler(trustProxy bool) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /s/{token}", p.handlePage)
	mux.HandleFunc("GET /s/{token}/note", p.handleNote)
	mux.HandleFunc("GET /s/{token}/raw", p.handleRaw)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		publicHeaders(w)
		if !p.limiter.allow(clientIP(r, trustProxy)) {
			http.Error(w, "too many requests", http.StatusTooManyRequests)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// publicHeaders apply to everything under /s/. A share link is a capability
// carried in a URL, so the two things that matter are keeping it out of
// search indexes and out of the Referer header of anything the note links to.
func publicHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Cache-Control", "no-store")
}

// resolve maps the request's token to a live share and its note. It reports
// the failure to the client itself, so a handler only continues on success.
//
// An unknown token, an expired one and a malformed one are all a plain 404:
// the difference is not the visitor's business. A note that has been deleted
// is a 410, because there the link was real and the content is what is gone.
func (p *publicShares) resolve(w http.ResponseWriter, r *http.Request) (share.Share, *vault.Note, bool) {
	token := r.PathValue("token")
	if !share.PlausibleToken(token) {
		p.linkNotFound(w, r)
		return share.Share{}, nil, false
	}
	sh, ok := p.store.Get(token)
	if !ok {
		p.linkNotFound(w, r)
		return share.Share{}, nil, false
	}
	note, err := p.vault.Read(sh.Path)
	if err != nil {
		writeErrorPage(w, r, http.StatusGone, errorPage{
			Code:  "410",
			Label: "HTTP 410 · Gone",
			Title: "This note is gone",
			Body:  "The note behind this link was deleted from the vault. The link itself was valid.",
		})
		return share.Share{}, nil, false
	}
	return sh, note, true
}

func (p *publicShares) linkNotFound(w http.ResponseWriter, r *http.Request) {
	writeErrorPage(w, r, http.StatusNotFound, errorPage{
		Code:  "404",
		Label: "HTTP 404 · Not found",
		Title: "This link doesn't work",
		Body:  "It may have been revoked, or it may have expired. Ask whoever shared it with you for a new one.",
	})
}

// handlePage serves the reader. The token is checked before the shell is
// rendered so a dead link is an honest 404 rather than an app that loads and
// then fails.
func (p *publicShares) handlePage(w http.ResponseWriter, r *http.Request) {
	sh, note, ok := p.resolve(w, r)
	if !ok {
		return
	}
	p.store.RecordView(sh.Token)
	if p.spa == nil {
		writeMarkdown(w, note, false)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	http.ServeFileFS(w, r, p.spa, "index.html")
}

// publicNote is everything the reader gets. Frontmatter never appears here —
// Body is the note without it — so private keys in a shared note's header
// stay in the vault.
type publicNote struct {
	Title     string    `json:"title"`
	Name      string    `json:"name"` // file stem, used for the download name
	Body      string    `json:"body"`
	Tags      []string  `json:"tags,omitempty"`
	Updated   string    `json:"updated"` // relative, e.g. "2h"
	UpdatedAt time.Time `json:"updatedAt"`
	Words     int       `json:"words"`
	Expires   string    `json:"expires,omitempty"` // relative, e.g. "6d"
}

// handleNote backs the reader with JSON. It does not count a view: the page
// that fetches it already did, and counting both would double every visit.
func (p *publicShares) handleNote(w http.ResponseWriter, r *http.Request) {
	sh, note, ok := p.resolve(w, r)
	if !ok {
		return
	}
	out := publicNote{
		Title:     note.Title,
		Name:      strings.TrimSuffix(path.Base(note.Path), path.Ext(note.Path)),
		Body:      note.Body,
		Tags:      note.Tags,
		Updated:   reltime(time.Since(note.ModTime)),
		UpdatedAt: note.ModTime,
		Words:     len(strings.Fields(note.Body)),
	}
	if sh.ExpiresAt != nil {
		out.Expires = reltime(time.Until(*sh.ExpiresAt))
	}
	writeJSON(w, http.StatusOK, out)
}

// handleRaw hands over the markdown itself — the point of a vault of plain
// files is that what you share is a file somebody can keep.
func (p *publicShares) handleRaw(w http.ResponseWriter, r *http.Request) {
	sh, note, ok := p.resolve(w, r)
	if !ok {
		return
	}
	p.store.RecordView(sh.Token)
	writeMarkdown(w, note, true)
}

func writeMarkdown(w http.ResponseWriter, note *vault.Note, attach bool) {
	name := path.Base(note.Path)
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	if attach {
		// The filename is quoted and comes from a vault path, which cannot
		// contain a quote or a newline by the time it is written.
		w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(note.Body))
}

// ---- rate limiting ----

// ipLimiter is a fixed-window request counter per client IP, protecting the
// one endpoint that anyone on the internet can reach.
type ipLimiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	seen   map[string]*ipWindow
	now    func() time.Time
}

type ipWindow struct {
	start time.Time
	n     int
}

func newIPLimiter(limit int, window time.Duration) *ipLimiter {
	return &ipLimiter{limit: limit, window: window, seen: map[string]*ipWindow{}, now: time.Now}
}

func (l *ipLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if w, ok := l.seen[ip]; ok && now.Sub(w.start) < l.window {
		w.n++
		return w.n <= l.limit
	}
	// Sweeping only when the map has grown keeps the common path to one map
	// lookup; the table is bounded by the number of distinct recent IPs.
	if len(l.seen) > 4096 {
		for k, w := range l.seen {
			if now.Sub(w.start) >= l.window {
				delete(l.seen, k)
			}
		}
	}
	l.seen[ip] = &ipWindow{start: now, n: 1}
	return true
}

// clientIP is the address rate limiting counts against. Behind a proxy the
// socket is always the proxy, so X-Forwarded-For is used — but only when the
// operator has said there is a proxy, since the header is client-controlled.
func clientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			if i := strings.IndexByte(xff, ','); i >= 0 {
				xff = xff[:i]
			}
			if ip := strings.TrimSpace(xff); ip != "" {
				return ip
			}
		}
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
