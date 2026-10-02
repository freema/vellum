package activity

import (
	"testing"
	"time"
)

func fixedClock(t time.Time) func() time.Time { return func() time.Time { return t } }

func TestTouchAndSessions(t *testing.T) {
	base := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	r := New()
	r.now = fixedClock(base)

	r.Touch("sk-1", "", "Claude Code", "CLI", "write_note")
	r.Touch("sk-1", "", "Claude Code", "CLI", "read_note")
	r.Touch("sk-2", "", "claude.ai", "Web", "") // no tool → no call counted

	sessions := r.Sessions()
	if len(sessions) != 2 {
		t.Fatalf("sessions = %d, want 2", len(sessions))
	}
	var s1 *SessionView
	for i := range sessions {
		if sessions[i].ID == "sk-1" {
			s1 = &sessions[i]
		}
	}
	if s1 == nil {
		t.Fatal("sk-1 missing")
	}
	if s1.Calls != 2 {
		t.Errorf("calls = %d, want 2", s1.Calls)
	}
	if s1.LastTool != "read_note" {
		t.Errorf("lastTool = %q, want read_note", s1.LastTool)
	}
	if s1.Status != "active" {
		t.Errorf("status = %q, want active", s1.Status)
	}
	if got := r.TotalCalls(); got != 2 {
		t.Errorf("totalCalls = %d, want 2", got)
	}
}

func TestSessionGoesIdle(t *testing.T) {
	base := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	r := New()
	r.now = fixedClock(base)
	r.Touch("sk-1", "", "x", "y", "read_note")

	r.now = fixedClock(base.Add(5 * time.Minute)) // past idleAfter
	if s := r.Sessions(); len(s) != 1 || s[0].Status != "idle" {
		t.Fatalf("expected 1 idle session, got %+v", s)
	}
}

func TestRevokeClient(t *testing.T) {
	r := New()
	r.Touch("sk-1", "mcp-a", "x", "y", "read_note")
	r.Touch("sk-2", "mcp-a", "x", "y", "") // the same client after a token refresh
	r.Touch("sk-3", "mcp-b", "z", "y", "")
	if got, ok := r.ClientOf("sk-2"); !ok || got != "mcp-a" {
		t.Fatalf("ClientOf(sk-2) = %q, %v", got, ok)
	}
	if n := r.RevokeClient("mcp-a"); n != 2 {
		t.Fatalf("RevokeClient dropped %d sessions, want 2", n)
	}
	if s := r.Sessions(); len(s) != 1 || s[0].ID != "sk-3" {
		t.Fatalf("sessions after revoke = %+v", s)
	}
	if _, ok := r.ClientOf("sk-1"); ok {
		t.Fatal("revoked session still known")
	}
	if n := r.RevokeClient(""); n != 0 {
		t.Fatalf("RevokeClient(\"\") dropped %d sessions", n)
	}
}

func TestEventsFilterAndRingCap(t *testing.T) {
	base := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	r := New()
	r.now = fixedClock(base)
	r.Record(Event{Source: "mcp", Actor: "Claude Code", Kind: "write", Target: "a.md"})
	r.Record(Event{Source: "curator", Actor: "Curator", Kind: "tag", Target: "b.md"})

	if got := len(r.Events("all")); got != 2 {
		t.Errorf("all events = %d, want 2", got)
	}
	if got := len(r.Events("curator")); got != 1 {
		t.Errorf("curator events = %d, want 1", got)
	}
	if r.Events("")[0].Source != "curator" {
		t.Error("expected newest-first ordering")
	}
	if r.CuratorChangesSince(24*time.Hour) != 1 {
		t.Error("curator changes count wrong")
	}

	for i := 0; i < maxEvents+50; i++ {
		r.Record(Event{Source: "mcp", Kind: "read"})
	}
	if got := len(r.Events("all")); got != maxEvents {
		t.Errorf("ring not capped: %d, want %d", got, maxEvents)
	}
}
