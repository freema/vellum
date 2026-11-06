package mcpserver

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/freema/vellum/internal/vault"
)

// NSO-562: a subscriber of vellum://note/{path} hears about an edit made
// outside vellum once the index's rescan has run, which the next index
// read (here search_notes) triggers after the interval.
func TestSubscriptionSeesOutsideEdit(t *testing.T) {
	ctx := context.Background()
	updated := make(chan string, 16)
	h := newResourceHarness(t, &mcp.ClientOptions{
		ResourceUpdatedHandler: func(_ context.Context, req *mcp.ResourceUpdatedNotificationRequest) {
			updated <- req.Params.URI
		},
	}, map[string]string{"inbox/watched.md": "# Watched\n\nv1\n"})
	const every = 20 * time.Millisecond
	h.deps.Index.SetRescanInterval(every)

	uri := "vellum://note/inbox/watched.md"
	if err := h.session.Subscribe(ctx, &mcp.SubscribeParams{URI: uri}); err != nil {
		t.Fatal(err)
	}
	abs := filepath.Join(h.deps.Vault.Root(), "inbox", "watched.md")
	if err := os.WriteFile(abs, []byte("# Watched\n\nv2, edited in another editor\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	time.Sleep(2 * every)
	var out struct{ Results []struct{ Path string } }
	call(t, h.session, "search_notes", map[string]any{"query": "edited"}, &out)
	if len(out.Results) != 1 || out.Results[0].Path != "inbox/watched.md" {
		t.Errorf("search after the outside edit = %+v", out.Results)
	}
	select {
	case got := <-updated:
		if got != uri {
			t.Errorf("notified about %q, want %q", got, uri)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no resources/updated after the outside edit")
	}
}

// NSO-562: a note renamed outside vellum keeps its share link when the
// rescan can tell it was a move (same file name and size, the only pair).
// A note deleted outside vellum keeps its link (it answers 410 Gone until a
// note is back at the path); only a delete through vellum revokes it.
func TestShareFollowsOutsideMove(t *testing.T) {
	var deps Deps
	s, store := sharingSession(t, func(d *Deps) {
		openShares(t, true)(d)
		deps = *d
	})
	call(t, s, "write_note", map[string]any{"path": "inbox/draft.md", "content": "# Draft\n"}, nil)
	var out shareNoteOut
	call(t, s, "share_note", map[string]any{"path": "inbox/draft.md"}, &out)

	root := deps.Vault.Root()
	if err := os.MkdirAll(filepath.Join(root, "projects", "p"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, "inbox", "draft.md"), filepath.Join(root, "projects", "p", "draft.md")); err != nil {
		t.Fatal(err)
	}
	stats, err := deps.Index.Reconcile()
	if err != nil {
		t.Fatal(err)
	}
	if stats != (vault.ReconcileStats{Moved: 1}) {
		t.Fatalf("reconcile = %+v, want one move", stats)
	}
	got, ok := store.Get(out.Token)
	if !ok || got.Path != "projects/p/draft.md" {
		t.Fatalf("share after the outside move = %+v (alive %v), want projects/p/draft.md", got, ok)
	}

	if err := os.Remove(filepath.Join(root, "projects", "p", "draft.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := deps.Index.Reconcile(); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Get(out.Token); !ok {
		t.Error("an outside delete revoked the link")
	}
}
