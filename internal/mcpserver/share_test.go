package mcpserver

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/freema/vellum/internal/share"
)

// openShares attaches a live share store to a session's own vault. tools
// mirrors VELLUM_SHARING: true is "on", false is "ui".
func openShares(t *testing.T, tools bool) func(*Deps) {
	t.Helper()
	return func(d *Deps) {
		s, err := share.Open(d.Vault.Root())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close() })
		d.Shares = s
		d.ShareTools = tools
		d.WebsiteURL = "https://vellum.example.com"
	}
}

// sharingSession builds a session and hands back the store it was built with,
// which is only knowable once newSession has run the mutator.
func sharingSession(t *testing.T, mutate func(*Deps)) (*mcp.ClientSession, *share.Store) {
	t.Helper()
	var store *share.Store
	s := newSession(t, func(d *Deps) {
		mutate(d)
		store = d.Shares
	})
	return s, store
}

func TestShareToolsAreOptIn(t *testing.T) {
	// Sharing off: the tools do not exist at all.
	names := toolNames(t, newSession(t))
	for _, n := range names {
		if n == "share_note" || n == "unshare_note" {
			t.Fatalf("%s is registered with sharing off", n)
		}
	}

	// VELLUM_SHARING=ui: links work, but minting stays with the human.
	uiOnly, _ := sharingSession(t, openShares(t, false))
	for _, n := range toolNames(t, uiOnly) {
		if n == "share_note" {
			t.Fatal("share_note is registered in ui-only mode")
		}
	}

	// VELLUM_SHARING=on: both tools show up.
	on, _ := sharingSession(t, openShares(t, true))
	got := toolNames(t, on)
	var found []string
	for _, n := range got {
		if n == "share_note" || n == "unshare_note" {
			found = append(found, n)
		}
	}
	sort.Strings(found)
	if strings.Join(found, ",") != "share_note,unshare_note" {
		t.Fatalf("share tools = %v, want both", found)
	}
}

func toolNames(t *testing.T, s *mcp.ClientSession) []string {
	t.Helper()
	res, err := s.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(res.Tools))
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	return names
}

func TestShareNoteTool(t *testing.T) {
	s, store := sharingSession(t, openShares(t, true))

	var written writeNoteOut
	call(t, s, "write_note", map[string]any{
		"path": "projects/p/plan.md", "content": "# Plan\n\nbody\n",
	}, &written)

	var out shareNoteOut
	call(t, s, "share_note", map[string]any{"path": "projects/p/plan.md"}, &out)
	if out.Token == "" {
		t.Fatal("no token returned")
	}
	if want := "https://vellum.example.com/s/" + out.Token; out.URL != want {
		t.Fatalf("url = %q, want %q", out.URL, want)
	}
	if out.Reused {
		t.Fatal("a first share reported itself as reused")
	}
	if out.ExpiresAt != "" {
		t.Fatalf("expires_at = %q, want empty without expires_in", out.ExpiresAt)
	}
	if _, ok := store.Get(out.Token); !ok {
		t.Fatal("the token is not in the store")
	}

	// Sharing again hands back the same link rather than a second one.
	var again shareNoteOut
	call(t, s, "share_note", map[string]any{"path": "projects/p/plan.md", "expires_in": "7d"}, &again)
	if again.Token != out.Token {
		t.Fatalf("token changed on re-share: %q → %q", out.Token, again.Token)
	}
	if !again.Reused {
		t.Fatal("re-share did not report itself as reused")
	}
	when, err := time.Parse(time.RFC3339, again.ExpiresAt)
	if err != nil {
		t.Fatalf("expires_at = %q: %v", again.ExpiresAt, err)
	}
	if d := time.Until(when); d < 6*24*time.Hour || d > 8*24*time.Hour {
		t.Fatalf("7d expiry landed %v away", d)
	}

	// And it can be taken back.
	var off unshareNoteOut
	call(t, s, "unshare_note", map[string]any{"path": "projects/p/plan.md"}, &off)
	if !off.Unshared {
		t.Fatal("unshare_note reported nothing to revoke")
	}
	if _, ok := store.Get(out.Token); ok {
		t.Fatal("the token survived unshare_note")
	}

	// Revoking a note that was never shared is not an error, just a no-op.
	call(t, s, "unshare_note", map[string]any{"path": "projects/p/plan.md"}, &off)
	if off.Unshared {
		t.Fatal("unshare_note claimed to revoke a link that did not exist")
	}
}

func TestShareNoteToolRejects(t *testing.T) {
	s, store := sharingSession(t, openShares(t, true))
	call(t, s, "write_note", map[string]any{"path": "a.md", "content": "# A\n"}, nil)

	if msg := callErr(t, s, "share_note", map[string]any{"path": "nope.md"}); msg == "" {
		t.Fatal("sharing a missing note produced no message")
	}
	if msg := callErr(t, s, "share_note", map[string]any{"path": "../escape.md"}); msg == "" {
		t.Fatal("sharing a path outside the vault produced no message")
	}
	for _, bad := range []string{"5m", "10y", "soon"} {
		if msg := callErr(t, s, "share_note", map[string]any{"path": "a.md", "expires_in": bad}); msg == "" {
			t.Fatalf("expires_in=%q was accepted", bad)
		}
	}
	if store.Len() != 0 {
		t.Fatalf("a rejected call still minted %d link(s)", store.Len())
	}
}

// A link must survive the note being filed somewhere else, and must die with
// the note itself — whichever side the change came from.
func TestShareTracksMoveAndDelete(t *testing.T) {
	s, store := sharingSession(t, openShares(t, true))
	call(t, s, "write_note", map[string]any{"path": "inbox/draft.md", "content": "# Draft\n"}, nil)

	var out shareNoteOut
	call(t, s, "share_note", map[string]any{"path": "inbox/draft.md"}, &out)

	call(t, s, "move_note", map[string]any{"from": "inbox/draft.md", "to": "projects/p/final.md"}, nil)
	got, ok := store.Get(out.Token)
	if !ok {
		t.Fatal("the link died when the note moved")
	}
	if got.Path != "projects/p/final.md" {
		t.Fatalf("share path = %q, want the new location", got.Path)
	}

	call(t, s, "delete_note", map[string]any{"path": "projects/p/final.md"}, nil)
	if _, ok := store.Get(out.Token); ok {
		t.Fatal("the link outlived the note it pointed at")
	}
}

func TestShareInstructionsOnlyWhenPublishing(t *testing.T) {
	if got := instructions(Deps{}); strings.Contains(got, "share_note") {
		t.Fatal("instructions mention sharing with the tools off")
	}
	d := Deps{ShareTools: true, Shares: &share.Store{}}
	if got := instructions(d); !strings.Contains(got, "share_note") {
		t.Fatal("instructions do not mention sharing with the tools on")
	}
}

func TestParseShareTTL(t *testing.T) {
	ok := map[string]time.Duration{
		"":      0,
		"never": 0,
		"24h":   24 * time.Hour,
		"7d":    7 * 24 * time.Hour,
		"4w":    28 * 24 * time.Hour,
		"1h":    time.Hour,
		"365d":  365 * 24 * time.Hour,
	}
	for in, want := range ok {
		got, err := parseShareTTL(in)
		if err != nil {
			t.Fatalf("parseShareTTL(%q): %v", in, err)
		}
		if got != want {
			t.Fatalf("parseShareTTL(%q) = %v, want %v", in, got, want)
		}
	}
	for _, in := range []string{"5m", "0d", "400d", "-7d", "tomorrow", "7 days"} {
		if got, err := parseShareTTL(in); err == nil {
			t.Fatalf("parseShareTTL(%q) = %v, want an error", in, got)
		}
	}
}
