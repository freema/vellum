package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// patch_note's operations and heading paths (NSO-575) through the MCP
// layer; internal/vault/patch_test.go covers the edits byte for byte.

// patchNote has two "Notes" sections under different projects.
const patchNote = "---\ntags: [work]\n---\n# Work\n\n## Project A\n\n### Notes\n\na1\n\n## Project B\n\n### Notes\n\nb1\n"

func (s *readSession) patch(args map[string]any) patchNoteOut {
	s.t.Helper()
	res := s.call("patch_note", args)
	if res.IsError {
		s.t.Fatalf("patch_note %v: %s", args, res.Text)
	}
	return decode[patchNoteOut](s.t, res)
}

func (s *readSession) patchErr(args map[string]any) string {
	s.t.Helper()
	path := args["path"].(string)
	before := s.noteOnDisk(path)
	res := s.call("patch_note", args)
	if !res.IsError {
		s.t.Fatalf("patch_note %v succeeded, want an error: %s", args, res.Structured)
	}
	if after := s.noteOnDisk(path); after != before {
		s.t.Fatalf("a failed patch_note wrote the note:\n%s", after)
	}
	return res.Text
}

// Without operation and with a unique heading, patch_note replaces the
// section as before and answers with path and hash, now plus the lines
// the new content occupies.
func TestPatchNoteDefaultReplace(t *testing.T) {
	note := "# T\n\n## Plan\n\nold\n\n## Log\n"
	s := newReadSession(t, map[string]string{"p.md": note})
	out := s.patch(map[string]any{"path": "p.md", "section": "plan", "content": "new\nplan", "expected_hash": sha256Hex(note)})
	want := "# T\n\n## Plan\nnew\nplan\n## Log\n"
	if got := s.noteOnDisk("p.md"); got != want {
		t.Fatalf("note = %q, want %q", got, want)
	}
	if out != (patchNoteOut{Path: "p.md", Hash: sha256Hex(want), StartLine: 4, EndLine: 5}) {
		t.Errorf("out = %+v", out)
	}
}

// Acceptance: an ambiguous name is an error naming both candidates with
// path and line, the note stays as it was; the heading path picks one.
func TestPatchNoteAmbiguityAndPath(t *testing.T) {
	s := newReadSession(t, map[string]string{"w.md": patchNote})
	msg := s.patchErr(map[string]any{"path": "w.md", "section": "Notes", "content": "x"})
	for _, want := range []string{"section is ambiguous", `"Work > Project A > Notes" (line 8)`, `"Work > Project B > Notes" (line 14)`, "heading paths"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q misses %s", msg, want)
		}
	}
	// read_note's section read gives the same answer.
	if res := s.call("read_note", map[string]any{"path": "w.md", "section": "Notes"}); !res.IsError || !strings.Contains(res.Text, "ambiguous") {
		t.Errorf("read_note of an ambiguous section = %+v", res)
	}

	out := s.patch(map[string]any{"path": "w.md", "section": "Project B > Notes", "operation": "append", "content": "b2"})
	want := strings.Replace(patchNote, "b1\n", "b1\nb2\n", 1)
	if got := s.noteOnDisk("w.md"); got != want {
		t.Fatalf("note = %q, want %q", got, want)
	}
	if out.Hash != sha256Hex(want) || out.StartLine != 17 || out.EndLine != 17 {
		t.Errorf("out = %+v", out)
	}
	part := decode[readNoteOut](t, s.call("read_note", map[string]any{"path": "w.md", "section": "Project A > Notes"}))
	if part.Content == nil || *part.Content != "### Notes\n\na1\n\n" {
		t.Errorf("read_note of the path = %+v", part)
	}
}

// Every operation is reachable through the tool, the reported lines hold
// the content, and an unknown operation or empty insert is refused.
func TestPatchNoteOperations(t *testing.T) {
	note := "# T\n\n## A\n\na1\n\n## B\nb1\n"
	cases := []struct {
		op, content, want string
		first, last       int
	}{
		{"append", "a2", "# T\n\n## A\n\na1\na2\n\n## B\nb1\n", 6, 6},
		{"prepend", "a0", "# T\n\n## A\n\na0\na1\n\n## B\nb1\n", 5, 5},
		{"insert_before", "## Z\nz", "# T\n\n## Z\nz\n\n## A\n\na1\n\n## B\nb1\n", 3, 4},
		{"insert_after", "## A2\nx", "# T\n\n## A\n\na1\n\n## A2\nx\n\n## B\nb1\n", 7, 8},
		{"replace", "r", "# T\n\n## A\nr\n## B\nb1\n", 4, 4},
	}
	for _, c := range cases {
		s := newReadSession(t, map[string]string{"n.md": note})
		out := s.patch(map[string]any{"path": "n.md", "section": "A", "operation": c.op, "content": c.content})
		if got := s.noteOnDisk("n.md"); got != c.want {
			t.Errorf("%s: note = %q, want %q", c.op, got, c.want)
		}
		if out.StartLine != c.first || out.EndLine != c.last || out.Hash != sha256Hex(c.want) {
			t.Errorf("%s: out = %+v, want lines %d-%d", c.op, out, c.first, c.last)
		}
	}

	s := newReadSession(t, map[string]string{"n.md": note})
	if msg := s.patchErr(map[string]any{"path": "n.md", "section": "A", "operation": "upsert", "content": "x"}); !strings.Contains(msg, "insert_after") {
		t.Errorf("unknown operation: %q", msg)
	}
	if msg := s.patchErr(map[string]any{"path": "n.md", "section": "A", "operation": "append", "content": "\n"}); !strings.Contains(msg, "content must not be empty") {
		t.Errorf("empty append: %q", msg)
	}
}

// Repeating an append writes again, so patch_note is no longer marked
// idempotent; it stays destructive (replace discards content).
func TestPatchNoteSchema(t *testing.T) {
	s := newSession(t)
	res, err := s.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range res.Tools {
		if tool.Name != "patch_note" {
			continue
		}
		a := tool.Annotations
		if a.DestructiveHint == nil || !*a.DestructiveHint || a.IdempotentHint || a.ReadOnlyHint {
			t.Errorf("annotations = %+v, want destructive and not idempotent", a)
		}
		schema, _ := json.Marshal(tool.InputSchema)
		var in struct {
			Required   []string                   `json:"required"`
			Properties map[string]json.RawMessage `json:"properties"`
		}
		_ = json.Unmarshal(schema, &in)
		if strings.Join(in.Required, ",") != "path,section,content" {
			t.Errorf("required = %v, want the old path, section, content", in.Required)
		}
		if _, ok := in.Properties["operation"]; !ok {
			t.Error("operation parameter missing")
		}
		return
	}
	t.Fatal("patch_note is not registered")
}
