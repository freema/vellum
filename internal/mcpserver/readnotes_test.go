package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/freema/vellum/internal/vault"
)

// readNotesWire decodes read_notes' structured result, keeping each entry's
// raw keys so a test can tell an absent key from an empty one.
type readNotesWire struct {
	Notes []struct {
		readNotesItem
		Raw map[string]json.RawMessage `json:"-"`
	} `json:"notes"`
	Summary readNotesSummary           `json:"summary"`
	Raw     map[string]json.RawMessage `json:"-"`
}

func (s *readSession) readNotes(args map[string]any) (readNotesWire, wireResult) {
	s.t.Helper()
	res := s.call("read_notes", args)
	if res.IsError {
		s.t.Fatalf("read_notes %v: %s", args, res.Text)
	}
	var out readNotesWire
	if err := json.Unmarshal(res.Structured, &out); err != nil {
		s.t.Fatal(err)
	}
	if err := json.Unmarshal(res.Structured, &out.Raw); err != nil {
		s.t.Fatal(err)
	}
	var raw struct {
		Notes []map[string]json.RawMessage `json:"notes"`
	}
	if err := json.Unmarshal(res.Structured, &raw); err != nil {
		s.t.Fatal(err)
	}
	for i := range out.Notes {
		out.Notes[i].Raw = raw.Notes[i]
	}
	return out, res
}

func (s *readSession) readNotesErr(args map[string]any) string {
	s.t.Helper()
	res := s.call("read_notes", args)
	if !res.IsError {
		s.t.Fatalf("read_notes %v succeeded, want an error: %s", args, res.Structured)
	}
	return res.Text
}

func keysOf(m map[string]json.RawMessage) []string {
	return slices.Sorted(maps.Keys(m))
}

func smallNote(i int) string {
	return fmt.Sprintf("---\ntags: [p%d]\n---\n# Note %d\n\nBody of note %d, see [[note-%d]].\n", i, i, i, i+1)
}

func fiveNotes() (map[string]string, []string) {
	notes := map[string]string{}
	var paths []string
	for i := 1; i <= 5; i++ {
		p := fmt.Sprintf("projects/p/note-%d.md", i)
		notes[p] = smallNote(i)
		paths = append(paths, p)
	}
	return notes, paths
}

// Five existing paths: one call, five entries with content and hash, in the
// order asked, and a text block a text-only client can act on.
func TestReadNotesFive(t *testing.T) {
	notes, paths := fiveNotes()
	s := newReadSession(t, notes)
	order := []string{paths[3], paths[0], paths[4], paths[1], paths[2]}
	out, res := s.readNotes(map[string]any{"paths": order})

	if len(out.Notes) != 5 {
		t.Fatalf("entries = %d, want 5: %s", len(out.Notes), res.Structured)
	}
	wantBytes := 0
	var wantText strings.Builder
	for i, e := range out.Notes {
		note, err := s.vault.Read(order[i])
		if err != nil {
			t.Fatal(err)
		}
		if e.Path != order[i] || e.Content == nil || *e.Content != note.Content || e.Hash != note.Hash ||
			e.Title != note.Title || strings.Join(e.Tags, ",") != strings.Join(note.Tags, ",") {
			t.Errorf("entry %d = %+v, want the note %s", i, e.readNotesItem, order[i])
		}
		if got, want := keysOf(e.Raw), []string{"content", "hash", "path", "tags", "title"}; strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("entry %d keys = %v, want %v (no body, no duplicated metadata)", i, got, want)
		}
		wantBytes += len(note.Content)
		fmt.Fprintf(&wantText, "==> %s <==\nhash: %s\ntags: %s\n\n%s", note.Path, note.Hash, strings.Join(note.Tags, ", "), note.Content)
	}
	if out.Summary != (readNotesSummary{Read: 5, Bytes: wantBytes}) {
		t.Errorf("summary = %+v, want 5 read, %d B", out.Summary, wantBytes)
	}
	if _, ok := out.Raw["summary"]; !ok {
		t.Error("summary missing")
	}
	if strings.Contains(string(out.Raw["summary"]), "matched") {
		t.Errorf("matched is only for a pattern: %s", out.Raw["summary"])
	}
	fmt.Fprintf(&wantText, "5 notes read, %d B\n", wantBytes)
	if res.Text != wantText.String() {
		t.Errorf("text =\n%s\nwant\n%s", res.Text, wantText.String())
	}

	// Each entry carries the hash an edit needs.
	if _, err := s.session.CallTool(context.Background(), &mcp.CallToolParams{Name: "append_to_note", Arguments: map[string]any{
		"path": order[0], "content": "more\n", "expected_hash": out.Notes[0].Hash,
	}}); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.vault.Read(order[0]); !strings.HasSuffix(n.Content, "more\n") {
		t.Error("edit with the hash from read_notes did not apply")
	}
}

// One bad path is an error on its own entry; the others still come back.
func TestReadNotesPerEntryErrors(t *testing.T) {
	notes, paths := fiveNotes()
	s := newReadSession(t, notes)
	out, res := s.readNotes(map[string]any{"paths": []string{paths[0], "projects/p/missing.md", paths[1], "projects/p/n.txt"}})
	if len(out.Notes) != 4 {
		t.Fatalf("entries = %+v", out.Notes)
	}
	for _, i := range []int{0, 2} {
		if out.Notes[i].Content == nil || out.Notes[i].Error != "" {
			t.Errorf("entry %d = %+v, want content", i, out.Notes[i].readNotesItem)
		}
	}
	for i, want := range map[int]string{1: "note not found", 3: "not a markdown file"} {
		e := out.Notes[i]
		if !strings.Contains(e.Error, want) || e.Content != nil || e.Hash != "" || e.Skipped {
			t.Errorf("entry %d = %+v, want only the error %q", i, e.readNotesItem, want)
		}
		if got := keysOf(e.Raw); strings.Join(got, ",") != "error,path" {
			t.Errorf("error entry keys = %v", got)
		}
	}
	if out.Summary.Read != 2 || out.Summary.Errors != 2 || out.Summary.Skipped != 0 {
		t.Errorf("summary = %+v", out.Summary)
	}
	if !strings.Contains(res.Text, "==> projects/p/missing.md <==\nerror: note not found") ||
		!strings.HasSuffix(res.Text, "4 notes: 2 read ("+fmt.Sprint(out.Summary.Bytes)+" B), 0 skipped, 2 errors\n") {
		t.Errorf("text = %s", res.Text)
	}
}

// A note over max_bytes_per_note is skipped whole, with its size, line
// count and how to read it instead; the notes around it are read.
func TestReadNotesSkipsLargeNote(t *testing.T) {
	big := longNote(400) // about 14 kB, over the 10 kB default
	notes, paths := fiveNotes()
	notes["projects/p/big.md"] = big
	s := newReadSession(t, notes)
	if len(big) <= readNotesPerNote {
		t.Fatalf("fixture note is %d B, want it over %d", len(big), readNotesPerNote)
	}

	out, res := s.readNotes(map[string]any{"paths": []string{paths[0], "projects/p/big.md", paths[1]}})
	e := out.Notes[1]
	if !e.Skipped || e.Content != nil || e.Size != int64(len(big)) || e.TotalLines != len(vault.SplitLines(big)) {
		t.Errorf("big entry = %+v, want skipped with size %d", e.readNotesItem, len(big))
	}
	if !strings.Contains(e.Reason, "max_bytes_per_note (10240 B)") || !strings.Contains(e.Reason, "read_note with outline") {
		t.Errorf("reason = %q", e.Reason)
	}
	if got := keysOf(e.Raw); strings.Join(got, ",") != "path,reason,size,skipped,totalLines" {
		t.Errorf("skipped entry keys = %v", got)
	}
	if out.Notes[0].Content == nil || out.Notes[2].Content == nil {
		t.Error("notes around the skipped one were not read")
	}
	if out.Summary.Read != 2 || out.Summary.Skipped != 1 {
		t.Errorf("summary = %+v", out.Summary)
	}
	// Nothing of the big note is in either channel: skipped, not cut.
	if strings.Contains(res.Text, "line 1 of the body") || bytes.Contains(res.Structured, []byte("line 1 of the body")) {
		t.Error("a part of the skipped note was returned")
	}
	if !strings.Contains(res.Text, fmt.Sprintf("==> projects/p/big.md <==\nskipped: %d B, %d lines, over max_bytes_per_note", len(big), e.TotalLines)) {
		t.Errorf("text = %s", res.Text)
	}

	// A higher limit reads it, a lower one skips the small notes too.
	out, _ = s.readNotes(map[string]any{"paths": []string{"projects/p/big.md"}, "max_bytes_per_note": len(big)})
	if out.Notes[0].Content == nil || *out.Notes[0].Content != big {
		t.Error("max_bytes_per_note equal to the size should read the note")
	}
	out, _ = s.readNotes(map[string]any{"paths": paths[:2], "max_bytes_per_note": 10})
	if out.Summary.Skipped != 2 || out.Summary.Read != 0 || out.Summary.Bytes != 0 {
		t.Errorf("tiny limit summary = %+v", out.Summary)
	}
}

// Once max_total_bytes is spent, every remaining note is skipped, even one
// that would still fit.
func TestReadNotesTotalBudget(t *testing.T) {
	notes, paths := fiveNotes()
	s := newReadSession(t, notes)
	size := len(notes[paths[0]])
	limit := 2*size + size/2 // room for two notes and a half
	out, res := s.readNotes(map[string]any{"paths": paths, "max_total_bytes": limit})
	for i, e := range out.Notes {
		read := i < 2
		if read != (e.Content != nil) || read == e.Skipped {
			t.Errorf("entry %d = %+v, want read=%v", i, e.readNotesItem, read)
		}
		if !read && !strings.Contains(e.Reason, fmt.Sprintf("max_total_bytes (%d B) already spent", limit)) {
			t.Errorf("entry %d reason = %q", i, e.Reason)
		}
	}
	if out.Summary.Read != 2 || out.Summary.Skipped != 3 || out.Summary.Bytes > limit {
		t.Errorf("summary = %+v (limit %d)", out.Summary, limit)
	}
	if !strings.HasSuffix(res.Text, fmt.Sprintf("5 notes: 2 read (%d B), 3 skipped, 0 errors\n", out.Summary.Bytes)) {
		t.Errorf("text summary = %q", res.Text[strings.LastIndex(strings.TrimSuffix(res.Text, "\n"), "\n")+1:])
	}

	// Strict order: a small note after an exhausted budget stays skipped.
	notes["projects/p/tiny.md"] = "x\n"
	s = newReadSession(t, notes)
	out, _ = s.readNotes(map[string]any{"paths": []string{paths[0], paths[1], "projects/p/tiny.md"}, "max_total_bytes": size + 10})
	if out.Notes[0].Content == nil || !out.Notes[1].Skipped || !out.Notes[2].Skipped {
		t.Errorf("order not kept after the budget ran out: %+v", out.Notes)
	}
}

// Paths outside the vault get read_note's own error; hidden paths, which
// list_notes and search_notes never show, are refused too.
func TestReadNotesPathValidation(t *testing.T) {
	notes, paths := fiveNotes()
	s := newReadSession(t, notes)
	root := s.vault.Root()
	for _, p := range []string{".obsidian/workspace.md", "projects/.drafts/d.md"} {
		abs := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte("secret\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	symlinked := os.Symlink(filepath.Join(root, ".obsidian"), filepath.Join(root, "visible")) == nil

	outside := []string{"../escape.md", "../../etc/passwd.md", "/etc/passwd.md", "projects/../../x.md"}
	hidden := []string{".obsidian/workspace.md", "projects/.drafts/d.md"}
	if symlinked {
		hidden = append(hidden, "visible/workspace.md")
	}
	ask := append(append([]string{paths[0]}, outside...), hidden...)
	out, res := s.readNotes(map[string]any{"paths": ask})
	if out.Notes[0].Content == nil {
		t.Error("the valid path was not read")
	}
	for i, p := range outside {
		e := out.Notes[1+i]
		// Same validation as read_note: the same error text.
		want := s.readErr(map[string]any{"path": p})
		if e.Error == "" || !strings.Contains(want, e.Error) || !strings.Contains(e.Error, "invalid path") {
			t.Errorf("%s: read_notes error %q, read_note error %q", p, e.Error, want)
		}
	}
	for i, p := range hidden {
		e := out.Notes[1+len(outside)+i]
		if !strings.Contains(e.Error, "invalid path") || e.Content != nil {
			t.Errorf("%s: entry = %+v, want refused", p, e.readNotesItem)
		}
	}
	if out.Summary.Read != 1 || out.Summary.Errors != len(outside)+len(hidden) {
		t.Errorf("summary = %+v", out.Summary)
	}
	if strings.Contains(res.Text, "secret") || bytes.Contains(res.Structured, []byte("secret")) {
		t.Error("a hidden note leaked")
	}
}

func TestReadNotesPattern(t *testing.T) {
	notes := map[string]string{
		"projects/x/b.md":        "# B\n",
		"projects/x/a.md":        "# A\n",
		"projects/x/sub/deep.md": "# Deep\n",
		"projects/y/plan.md":     "# Plan Y\n",
		"projects/z/plan.md":     "# Plan Z\n",
		"projects/z/other.md":    "# Other\n",
		"inbox/n.md":             "# N\n",
	}
	s := newReadSession(t, notes)
	if err := os.MkdirAll(filepath.Join(s.vault.Root(), "projects", "x", ".hidden"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.vault.Root(), "projects", "x", ".dot.md"), []byte("dot\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	pathsOf := func(out readNotesWire) string {
		var ps []string
		for _, e := range out.Notes {
			ps = append(ps, e.Path)
		}
		return strings.Join(ps, ",")
	}
	for pattern, want := range map[string]string{
		"projects/x/*.md":      "projects/x/a.md,projects/x/b.md", // alphabetical, one folder, no hidden
		"projects/*/plan.md":   "projects/y/plan.md,projects/z/plan.md",
		"projects/x/[ab].md":   "projects/x/a.md,projects/x/b.md",
		"projects/x/?.md":      "projects/x/a.md,projects/x/b.md",
		"./projects/z/oth*.md": "projects/z/other.md",
		"*/n.md":               "inbox/n.md",
		"projects/x/a.md":      "projects/x/a.md",
	} {
		out, _ := s.readNotes(map[string]any{"pattern": pattern})
		if got := pathsOf(out); got != want {
			t.Errorf("pattern %s = %s, want %s", pattern, got, want)
		}
		if out.Summary.Matched != len(out.Notes) || out.Summary.Read != len(out.Notes) {
			t.Errorf("pattern %s summary = %+v", pattern, out.Summary)
		}
	}

	out, res := s.readNotes(map[string]any{"pattern": "projects/x/*.txt"})
	if len(out.Notes) != 0 || res.Text != "no notes match projects/x/*.txt\n" {
		t.Errorf("no match = %+v %q", out, res.Text)
	}
	if _, ok := out.Raw["notes"]; !ok || string(out.Raw["notes"]) != "[]" {
		t.Errorf("no match notes = %s, want []", out.Raw["notes"])
	}

	for _, c := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"pattern": "projects/**/*.md"}, "** is not supported"},
		{map[string]any{"pattern": "projects/x/.hidden/*.md"}, "hidden path"},
		{map[string]any{"pattern": ".obsidian/*.md"}, "hidden path"},
		{map[string]any{"pattern": "projects/[x/*.md"}, "syntax error"},
		{map[string]any{"pattern": "../*.md"}, "invalid path"},
		{map[string]any{"pattern": "nowhere/*.md"}, "note not found"},
		{map[string]any{"pattern": "projects/x/*.md", "paths": []string{"projects/x/a.md"}}, "not both"},
		{map[string]any{}, "give paths"},
		{map[string]any{"paths": []string{}}, "give paths"},
		{map[string]any{"paths": make([]string, 21)}, "at most 20 paths per call, got 21"},
		{map[string]any{"paths": []string{"inbox/n.md"}, "max_total_bytes": -1}, "must not be negative"},
		{map[string]any{"paths": []string{"inbox/n.md"}, "max_bytes_per_note": -5}, "must not be negative"},
	} {
		if msg := s.readNotesErr(c.args); !strings.Contains(msg, c.want) {
			t.Errorf("%v: error %q, want it to mention %q", c.args, msg, c.want)
		}
	}
}

// A pattern matching more than 20 notes reads the first 20 and says how
// many matched.
func TestReadNotesPatternCap(t *testing.T) {
	notes := map[string]string{}
	for i := 0; i < 25; i++ {
		notes[fmt.Sprintf("many/n%02d.md", i)] = fmt.Sprintf("# N%d\n", i)
	}
	s := newReadSession(t, notes)
	out, res := s.readNotes(map[string]any{"pattern": "many/*.md"})
	if len(out.Notes) != readNotesMaxNotes || out.Notes[0].Path != "many/n00.md" || out.Notes[19].Path != "many/n19.md" {
		t.Errorf("entries = %d, first %s", len(out.Notes), out.Notes[0].Path)
	}
	if out.Summary.Matched != 25 || out.Summary.Read != 20 {
		t.Errorf("summary = %+v", out.Summary)
	}
	if !strings.HasSuffix(res.Text, "the pattern matched 25 notes, only the first 20 were read; narrow it for the rest\n") {
		t.Errorf("text tail = %q", res.Text[len(res.Text)-120:])
	}
}

// outline_only gives each note read_note's outline, key for key.
func TestReadNotesOutlineOnly(t *testing.T) {
	notes, paths := fiveNotes()
	notes["long.md"] = longNote(100)
	s := newReadSession(t, notes)
	out, res := s.readNotes(map[string]any{"paths": []string{"long.md", paths[0]}, "outline_only": true})
	for i, p := range []string{"long.md", paths[0]} {
		_, single := s.read(map[string]any{"path": p, "outline": true})
		entry, err := json.Marshal(out.Notes[i].Raw)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(entry, canonicalJSON(t, json.RawMessage(single.Structured))) {
			t.Errorf("%s outline entry =\n%s\nread_note outline =\n%s", p, entry, single.Structured)
		}
		// The text is read_note's outline text without its path line.
		if want := "==> " + p + " <==\n" + strings.TrimPrefix(single.Text, "path: "+p+"\n"); !strings.Contains(res.Text, want) {
			t.Errorf("%s outline text missing:\n%s\nin\n%s", p, want, res.Text)
		}
	}
	if strings.Contains(res.Text, "line 1 of the body") {
		t.Error("outline_only returned content")
	}
	if out.Summary.Read != 2 || out.Summary.Bytes == 0 {
		t.Errorf("summary = %+v", out.Summary)
	}
}

func TestReadNotesOutputSchema(t *testing.T) {
	s := newSession(t)
	res, err := s.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range res.Tools {
		if tool.Name != "read_notes" {
			continue
		}
		in, _ := json.Marshal(tool.InputSchema)
		outSchema, _ := json.Marshal(tool.OutputSchema)
		if bytes.Contains(outSchema, []byte(`"body"`)) {
			t.Errorf("output schema has body: %s", outSchema)
		}
		var schema struct {
			Required []string `json:"required"`
		}
		_ = json.Unmarshal(in, &schema)
		if len(schema.Required) != 0 {
			t.Errorf("every read_notes parameter must be optional, required = %v", schema.Required)
		}
		for _, p := range []string{"paths", "pattern", "max_bytes_per_note", "max_total_bytes", "outline_only"} {
			if !bytes.Contains(in, []byte(`"`+p+`"`)) {
				t.Errorf("input schema misses %s: %s", p, in)
			}
		}
		return
	}
	t.Fatal("read_notes not registered")
}

// The measurement's "read five related" scenario: one read_notes call
// instead of five read_note calls, and no more bytes in either channel.
func TestReadFiveRelatedPayloadBudget(t *testing.T) {
	fx := writePayloadVault(t, t.TempDir())
	v, err := vault.New(fx.root)
	if err != nil {
		t.Fatal(err)
	}
	ix := vault.NewIndex(v)
	if err := ix.Build(); err != nil {
		t.Fatal(err)
	}
	server := New(Deps{Vault: v, Index: ix, Searcher: vault.NewScanSearcher(v, ix), Structure: vault.DefaultStructure(), Version: "test"})
	st, ct := mcp.NewInMemoryTransports()
	ctx := context.Background()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	rec := &payloadRecorder{}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "read-notes-test", Version: "0"}, nil).
		Connect(ctx, recordingTransport{Transport: ct, rec: rec}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	s := &readSession{t: t, session: cs, rec: rec, vault: v}

	var before payloadStats
	for _, p := range fx.related {
		res := s.call("read_note", map[string]any{"path": p})
		before.add(payloadStats{Calls: 1, Structured: len(res.Structured), Text: len(res.Text)})
	}
	out, res := s.readNotes(map[string]any{"paths": fx.related})
	after := payloadStats{Calls: 1, Structured: len(res.Structured), Text: len(res.Text)}
	if out.Summary.Read != 5 {
		t.Fatalf("read %d of 5: %+v", out.Summary.Read, out.Summary)
	}
	if after.Structured > before.Structured || after.Text > before.Text {
		t.Errorf("read_notes sends structured %d / text %d B, five read_note calls %d / %d B",
			after.Structured, after.Text, before.Structured, before.Text)
	}
	t.Logf("five read_note: %d calls, %d B; read_notes: 1 call, %d B", before.Calls, before.Result(), after.Result())
}
