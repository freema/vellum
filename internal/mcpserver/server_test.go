package mcpserver

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/freema/vellum/internal/vault"
)

// newSession spins up the full MCP server over an in-memory transport and
// returns a connected client session — the same wire format as production,
// minus the network.
func newSession(t *testing.T) *mcp.ClientSession {
	t.Helper()
	v, err := vault.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ix := vault.NewIndex(v)
	if err := ix.Build(); err != nil {
		t.Fatal(err)
	}
	server := New(Deps{
		Vault:     v,
		Index:     ix,
		Searcher:  vault.NewScanSearcher(v, ix),
		Structure: vault.DefaultStructure(),
		Version:   "test",
	})

	st, ct := mcp.NewInMemoryTransports()
	ctx := context.Background()
	serverSession, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
	session, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// call invokes a tool and decodes its structured content into out.
func call(t *testing.T, s *mcp.ClientSession, name string, args map[string]any, out any) *mcp.CallToolResult {
	t.Helper()
	res, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}
	if res.IsError {
		var text string
		for _, c := range res.Content {
			if tc, ok := c.(*mcp.TextContent); ok {
				text += tc.Text
			}
		}
		t.Fatalf("tool %s returned error: %s", name, text)
	}
	if out != nil {
		raw, err := json.Marshal(res.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatalf("decode %s output: %v", name, err)
		}
	}
	return res
}

// callErr invokes a tool expecting a tool-level error.
func callErr(t *testing.T, s *mcp.ClientSession, name string, args map[string]any) string {
	t.Helper()
	res, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}
	if !res.IsError {
		t.Fatalf("tool %s should have failed", name)
	}
	var text string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text += tc.Text
		}
	}
	return text
}

func TestToolSurface(t *testing.T) {
	s := newSession(t)
	res, err := s.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	want := []string{
		"add_tags", "append_to_note", "delete_note", "get_backlinks",
		"list_notes", "list_tags", "list_tasks", "move_note", "patch_note",
		"prepend_to_note", "read_note", "remove_tags", "search_notes",
		"set_status", "write_note",
	}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("tools = %v\nwant %v", names, want)
	}
}

func TestCRUDFlow(t *testing.T) {
	s := newSession(t)

	// write_note without a path falls back to the inbox with a slug name.
	var w struct{ Path, Hash string }
	call(t, s, "write_note", map[string]any{
		"content": "---\ntitle: Flow Note\ntags: [flow]\n---\n# Flow Note\n\nSee [[other]].\n",
	}, &w)
	if w.Path != "inbox/flow-note.md" || w.Hash == "" {
		t.Fatalf("write_note = %+v", w)
	}

	var note struct {
		Path string   `json:"path"`
		Tags []string `json:"tags"`
		Hash string   `json:"hash"`
	}
	call(t, s, "read_note", map[string]any{"path": w.Path}, &note)
	if note.Hash != w.Hash || len(note.Tags) != 1 {
		t.Fatalf("read_note = %+v", note)
	}

	// Conflict-safe update: stale hash must fail with a conflict.
	msg := callErr(t, s, "write_note", map[string]any{
		"path": w.Path, "content": "new", "expected_hash": "deadbeef",
	})
	if !strings.Contains(msg, "hash mismatch") {
		t.Errorf("conflict message = %q", msg)
	}

	// append + patch keep the note usable.
	call(t, s, "append_to_note", map[string]any{"path": w.Path, "content": "## Log\n\nentry"}, nil)
	call(t, s, "patch_note", map[string]any{"path": w.Path, "section": "Log", "content": "patched entry"}, nil)

	var after struct{ Content string `json:"content"` }
	call(t, s, "read_note", map[string]any{"path": w.Path}, &after)
	if !strings.Contains(after.Content, "## Log\npatched entry") {
		t.Errorf("patched content = %q", after.Content)
	}

	// move + delete round out CRUD; the index follows along.
	call(t, s, "move_note", map[string]any{"from": w.Path, "to": "projects/p/flow.md"}, nil)
	var listing struct{ Notes []struct{ Path string } }
	call(t, s, "list_notes", map[string]any{"recursive": true}, &listing)
	if len(listing.Notes) != 1 || listing.Notes[0].Path != "projects/p/flow.md" {
		t.Fatalf("list after move = %+v", listing)
	}
	call(t, s, "delete_note", map[string]any{"path": "projects/p/flow.md"}, nil)
	call(t, s, "list_notes", map[string]any{"recursive": true}, &listing)
	if len(listing.Notes) != 0 {
		t.Fatalf("list after delete = %+v", listing)
	}
}

func TestSearchTagsAndBacklinks(t *testing.T) {
	s := newSession(t)
	call(t, s, "write_note", map[string]any{
		"path": "notes/target.md", "content": "# Target\n\nthe needle is here\n",
	}, nil)
	call(t, s, "write_note", map[string]any{
		"path": "notes/linker.md", "content": "---\ntags: [linked]\n---\nSee [[target]].\n",
	}, nil)

	var search struct{ Results []struct{ Path string } }
	call(t, s, "search_notes", map[string]any{"query": "needle"}, &search)
	if len(search.Results) != 1 || search.Results[0].Path != "notes/target.md" {
		t.Fatalf("search = %+v", search)
	}

	var tags struct{ Tags []struct{ Tag string; Count int } }
	call(t, s, "list_tags", nil, &tags)
	if len(tags.Tags) != 1 || tags.Tags[0].Tag != "linked" {
		t.Fatalf("list_tags = %+v", tags)
	}

	var bl struct{ Backlinks, Links []string }
	call(t, s, "get_backlinks", map[string]any{"path": "notes/target.md"}, &bl)
	if len(bl.Backlinks) != 1 || bl.Backlinks[0] != "notes/linker.md" {
		t.Fatalf("backlinks = %+v", bl)
	}

	var mut struct{ Tags []string }
	call(t, s, "add_tags", map[string]any{"path": "notes/target.md", "tags": []string{"fresh"}}, &mut)
	if len(mut.Tags) != 1 || mut.Tags[0] != "fresh" {
		t.Fatalf("add_tags = %+v", mut)
	}
	call(t, s, "remove_tags", map[string]any{"path": "notes/target.md", "tags": []string{"fresh"}}, &mut)
	if len(mut.Tags) != 0 {
		t.Fatalf("remove_tags = %+v", mut)
	}
}

func TestTaskFlow(t *testing.T) {
	s := newSession(t)
	call(t, s, "write_note", map[string]any{
		"path": "projects/vellum/todo.md", "content": "# Todo\n",
	}, nil)

	call(t, s, "set_status", map[string]any{"path": "projects/vellum/todo.md", "status": "in-progress"}, nil)

	var tasks struct {
		Tasks []struct{ Path, Title, Status string }
	}
	call(t, s, "list_tasks", map[string]any{"status": "in-progress", "project": "vellum"}, &tasks)
	if len(tasks.Tasks) != 1 || tasks.Tasks[0].Status != "in-progress" {
		t.Fatalf("list_tasks = %+v", tasks)
	}

	callErr(t, s, "set_status", map[string]any{"path": "projects/vellum/todo.md", "status": "bogus"})
}

func TestPathTraversalBlockedOverMCP(t *testing.T) {
	s := newSession(t)
	msg := callErr(t, s, "read_note", map[string]any{"path": "../../etc/passwd.md"})
	if !strings.Contains(msg, "invalid path") {
		t.Errorf("traversal error = %q", msg)
	}
	callErr(t, s, "write_note", map[string]any{"path": "../escape.md", "content": "x"})
}
