// Package activity records what touches the vault at runtime — MCP tool
// calls, connected clients and curator actions — in memory, so the workspace
// UI can show a live Connections panel and an Activity/curator log without a
// database. Everything here is best-effort and bounded: a fixed-size event
// ring and a session map that the HTTP layer populates. Nothing is persisted.
package activity

import (
	"sort"
	"sync"
	"time"
)

// maxEvents caps the in-memory event ring.
const maxEvents = 300

// Event is one thing that happened to the vault.
type Event struct {
	ID      string    `json:"id"`
	Source  string    `json:"source"` // mcp | curator | user
	Actor   string    `json:"actor"`  // client name, "Curator" or "You"
	Kind    string    `json:"kind"`   // write|read|search|move|delete|tag|link|organize|archive|summary
	Target  string    `json:"target"` // note title/path or a phrase
	Detail  string    `json:"detail"`
	Pending bool      `json:"pending,omitempty"`
	At      time.Time `json:"at"`
}

// Session is a live (or recently seen) MCP client, keyed by its bearer token.
type Session struct {
	ID        string    `json:"id"` // short, non-secret handle
	Name      string    `json:"name"`
	Kind      string    `json:"kind"`
	FirstSeen time.Time `json:"firstSeen"`
	LastSeen  time.Time `json:"lastSeen"`
	LastTool  string    `json:"lastTool,omitempty"`
	Calls     int       `json:"calls"`
	revoked   bool
}

// Recorder is a concurrency-safe, bounded store of events and sessions.
type Recorder struct {
	mu       sync.Mutex
	events   []Event
	sessions map[string]*Session
	seq      int64
	now      func() time.Time
}

// New returns an empty Recorder using the wall clock.
func New() *Recorder {
	return &Recorder{sessions: map[string]*Session{}, now: time.Now}
}

// idleAfter is how long since LastSeen before a session is reported "idle".
const idleAfter = 90 * time.Second

// Record appends an event, assigning it an id and timestamp. The oldest
// events are dropped once the ring is full.
func (r *Recorder) Record(ev Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	ev.ID = "e" + itoa(r.seq)
	if ev.At.IsZero() {
		ev.At = r.now()
	}
	r.events = append([]Event{ev}, r.events...)
	if len(r.events) > maxEvents {
		r.events = r.events[:maxEvents]
	}
}

// Touch upserts a session by key and, when tool is non-empty, counts a call.
// key is a stable non-secret handle (e.g. a token hash prefix).
func (r *Recorder) Touch(key, name, kind, tool string) {
	if key == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.sessions[key]
	now := r.now()
	if s == nil {
		s = &Session{ID: key, Name: name, Kind: kind, FirstSeen: now}
		r.sessions[key] = s
	}
	if name != "" {
		s.Name = name
	}
	if kind != "" {
		s.Kind = kind
	}
	s.LastSeen = now
	s.revoked = false
	if tool != "" {
		s.LastTool = tool
		s.Calls++
	}
}

// Revoke drops a session. Reports whether one was removed.
func (r *Recorder) Revoke(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.sessions[id]; !ok {
		return false
	}
	delete(r.sessions, id)
	return true
}

// Events returns a copy filtered by source ("", "all" or a source name),
// newest first.
func (r *Recorder) Events(source string) []Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Event, 0, len(r.events))
	for _, e := range r.events {
		if source != "" && source != "all" && e.Source != source {
			continue
		}
		out = append(out, e)
	}
	return out
}

// SessionView is a Session enriched with a derived status for the API.
type SessionView struct {
	Session
	Status string `json:"status"` // active | idle
}

// Sessions returns non-revoked sessions, most recently seen first, each with
// an active/idle status derived from LastSeen.
func (r *Recorder) Sessions() []SessionView {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	var out []SessionView
	for _, s := range r.sessions {
		if s.revoked {
			continue
		}
		status := "active"
		if now.Sub(s.LastSeen) > idleAfter {
			status = "idle"
		}
		out = append(out, SessionView{Session: *s, Status: status})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastSeen.After(out[j].LastSeen) })
	return out
}

// TotalCalls sums calls across live sessions.
func (r *Recorder) TotalCalls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, s := range r.sessions {
		if !s.revoked {
			n += s.Calls
		}
	}
	return n
}

// CuratorChangesSince counts curator events newer than d ago.
func (r *Recorder) CuratorChangesSince(d time.Duration) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	cutoff := r.now().Add(-d)
	n := 0
	for _, e := range r.events {
		if e.Source == "curator" && e.At.After(cutoff) {
			n++
		}
	}
	return n
}

// itoa is a tiny base-10 formatter avoiding an strconv import churn.
func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
