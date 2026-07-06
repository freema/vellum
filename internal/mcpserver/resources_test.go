package mcpserver

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/freema/vellum/internal/vault"
)

// resourceHarness is newSession plus a seeded vault and configurable client
// options (needed for notification handlers).
type resourceHarness struct {
	session *mcp.ClientSession
	deps    Deps
}

func newResourceHarness(t *testing.T, clientOpts *mcp.ClientOptions, seed map[string]string) *resourceHarness {
	t.Helper()
	v, err := vault.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for path, content := range seed {
		if err := v.Write(path, content, vault.WriteOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	ix := vault.NewIndex(v)
	if err := ix.Build(); err != nil {
		t.Fatal(err)
	}
	deps := Deps{
		Vault:     v,
		Index:     ix,
		Searcher:  vault.NewScanSearcher(v, ix),
		Structure: vault.DefaultStructure(),
		Version:   "test",
	}
	server := New(deps)

	st, ct := mcp.NewInMemoryTransports()
	ctx := context.Background()
	serverSession, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, clientOpts)
	session, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return &resourceHarness{session: session, deps: deps}
}

func TestNoteURIRoundTrip(t *testing.T) {
	for _, p := range []string{
		"inbox/note.md",
		"projects/x/deep/nested.md",
		"inbox/poznámka s mezerou.md", // diacritics + space must survive encoding
	} {
		uri := noteURI(p)
		got, ok := noteURIPath(uri)
		if !ok || got != p {
			t.Errorf("round trip %q -> %q -> (%q, %v)", p, uri, got, ok)
		}
	}
	for _, uri := range []string{
		"vellum://note/", "vellum://tags/x", "https://evil.example/note.md", "note/inbox/x.md", "",
	} {
		if _, ok := noteURIPath(uri); ok {
			t.Errorf("noteURIPath(%q) should not parse", uri)
		}
	}
}

func TestResourcesListReadAndTemplate(t *testing.T) {
	ctx := context.Background()
	h := newResourceHarness(t, nil, map[string]string{
		"inbox/seed.md": "# Seed\n\nhello resource\n",
	})

	// The seeded note is listed with its metadata.
	list, err := h.session.ListResources(ctx, &mcp.ListResourcesParams{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Resources) != 1 {
		t.Fatalf("resources = %d, want 1", len(list.Resources))
	}
	r := list.Resources[0]
	if r.URI != "vellum://note/inbox/seed.md" || r.Name != "inbox/seed.md" ||
		r.Title != "Seed" || r.MIMEType != "text/markdown" {
		t.Errorf("resource = %+v", r)
	}

	// Reading a listed resource returns the raw markdown.
	rd, err := h.session.ReadResource(ctx, &mcp.ReadResourceParams{URI: r.URI})
	if err != nil {
		t.Fatal(err)
	}
	if len(rd.Contents) != 1 || !strings.Contains(rd.Contents[0].Text, "hello resource") ||
		rd.Contents[0].MIMEType != "text/markdown" {
		t.Errorf("contents = %+v", rd.Contents)
	}

	// A note on disk but not (yet) in the index is served via the template.
	if err := h.deps.Vault.Write("inbox/direct.md", "# Direct\n", vault.WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.session.ReadResource(ctx, &mcp.ReadResourceParams{URI: "vellum://note/inbox/direct.md"}); err != nil {
		t.Errorf("template read failed: %v", err)
	}

	// A missing note reads as resource-not-found, not a server error.
	if _, err := h.session.ReadResource(ctx, &mcp.ReadResourceParams{URI: "vellum://note/inbox/nope.md"}); err == nil {
		t.Error("read of a missing note must fail")
	}

	// The template itself is advertised.
	tpls, err := h.session.ListResourceTemplates(ctx, &mcp.ListResourceTemplatesParams{})
	if err != nil {
		t.Fatal(err)
	}
	if len(tpls.ResourceTemplates) != 1 || tpls.ResourceTemplates[0].URITemplate != "vellum://note/{+path}" {
		t.Errorf("templates = %+v", tpls.ResourceTemplates)
	}
}

func TestResourceSubscriptionLifecycle(t *testing.T) {
	ctx := context.Background()
	updated := make(chan string, 16)
	listChanged := make(chan struct{}, 16)
	h := newResourceHarness(t, &mcp.ClientOptions{
		ResourceUpdatedHandler: func(_ context.Context, req *mcp.ResourceUpdatedNotificationRequest) {
			updated <- req.Params.URI
		},
		ResourceListChangedHandler: func(_ context.Context, _ *mcp.ResourceListChangedRequest) {
			listChanged <- struct{}{}
		},
	}, nil)

	uri := "vellum://note/inbox/watched.md"
	// Subscribing to a note that does not exist yet is allowed (watch-then-create).
	if err := h.session.Subscribe(ctx, &mcp.SubscribeParams{URI: uri}); err != nil {
		t.Fatal(err)
	}
	// Anything that is not a note URI is rejected.
	if err := h.session.Subscribe(ctx, &mcp.SubscribeParams{URI: "https://example.com/x"}); err == nil {
		t.Error("subscribe to a foreign URI must fail")
	}

	waitUpdate := func(step string) {
		t.Helper()
		select {
		case got := <-updated:
			if got != uri {
				t.Fatalf("%s: notified about %q, want %q", step, got, uri)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: no resources/updated notification", step)
		}
	}
	drainListChanged := func() int {
		n := 0
		for {
			select {
			case <-listChanged:
				n++
			case <-time.After(100 * time.Millisecond):
				return n
			}
		}
	}

	// Create, edit (same title), delete — a notification for each, but the
	// content-only edit must not re-announce the resource list.
	call(t, h.session, "write_note", map[string]any{"path": "inbox/watched.md", "content": "# Watched\n\nv1\n"}, nil)
	waitUpdate("create")
	if n := drainListChanged(); n != 1 {
		t.Errorf("list_changed after create = %d, want 1", n)
	}

	call(t, h.session, "append_to_note", map[string]any{"path": "inbox/watched.md", "content": "v2\n"}, nil)
	waitUpdate("edit")
	if n := drainListChanged(); n != 0 {
		t.Errorf("list_changed after content-only edit = %d, want 0", n)
	}

	call(t, h.session, "delete_note", map[string]any{"path": "inbox/watched.md"}, nil)
	waitUpdate("delete")
	if n := drainListChanged(); n != 1 {
		t.Errorf("list_changed after delete = %d, want 1", n)
	}
}

func TestServerInfoAdvertisesCapabilities(t *testing.T) {
	h := newResourceHarness(t, nil, nil)
	init := h.session.InitializeResult()

	if !strings.Contains(init.Instructions, "expected_hash") ||
		!strings.Contains(init.Instructions, "vellum://note/") {
		t.Errorf("instructions missing vault conventions: %q", init.Instructions)
	}
	caps := init.Capabilities
	if caps.Logging != nil {
		t.Error("logging capability advertised but vellum never emits log messages")
	}
	if caps.Tools == nil {
		t.Error("tools capability missing")
	}
	if caps.Resources == nil || !caps.Resources.Subscribe {
		t.Errorf("resources capability = %+v, want subscribe:true", caps.Resources)
	}
}
