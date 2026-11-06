package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/freema/vellum/internal/vault"
)

// readSession serves a fresh vault through a recording client, so a test
// can see the exact bytes of every tool result.
type readSession struct {
	t       *testing.T
	session *mcp.ClientSession
	rec     *payloadRecorder
	vault   *vault.Vault
}

func newReadSession(t *testing.T, notes map[string]string) *readSession {
	t.Helper()
	v, err := vault.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for p, c := range notes {
		if err := v.Write(p, c, vault.WriteOptions{}); err != nil {
			t.Fatal(err)
		}
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
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "read-test", Version: "0"}, nil).
		Connect(ctx, recordingTransport{Transport: ct, rec: rec}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return &readSession{t: t, session: cs, rec: rec, vault: v}
}

// wireResult is a tools/call result as it travelled: structuredContent as
// raw bytes, plus the concatenated text blocks.
type wireResult struct {
	Structured json.RawMessage
	Text       string
	IsError    bool
}

func (s *readSession) call(name string, args map[string]any) wireResult {
	s.t.Helper()
	s.rec.take()
	if _, err := s.session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args}); err != nil {
		s.t.Fatalf("CallTool(%s): %v", name, err)
	}
	var wire struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StructuredContent json.RawMessage `json:"structuredContent"`
		IsError           bool            `json:"isError"`
	}
	if err := json.Unmarshal(s.rec.take(), &wire); err != nil {
		s.t.Fatal(err)
	}
	out := wireResult{Structured: wire.StructuredContent, IsError: wire.IsError}
	for _, c := range wire.Content {
		if c.Type == "text" {
			out.Text += c.Text
		}
	}
	return out
}

// read calls read_note and decodes its structured result; it fails the
// test on a tool error.
func (s *readSession) read(args map[string]any) (partOut, wireResult) {
	s.t.Helper()
	res := s.call("read_note", args)
	if res.IsError {
		s.t.Fatalf("read_note %v: %s", args, res.Text)
	}
	var out partOut
	if err := json.Unmarshal(res.Structured, &out); err != nil {
		s.t.Fatal(err)
	}
	return out, res
}

// readErr calls read_note expecting a tool error and returns its text.
func (s *readSession) readErr(args map[string]any) string {
	s.t.Helper()
	res := s.call("read_note", args)
	if !res.IsError {
		s.t.Fatalf("read_note %v succeeded, want an error: %s", args, res.Structured)
	}
	return res.Text
}

// partOut decodes read_note's structured result, keeping track of which
// optional keys were present.
type partOut struct {
	Path       string   `json:"path"`
	Content    *string  `json:"content"`
	Links      []string `json:"links"`
	Tags       []string `json:"tags"`
	Hash       string   `json:"hash"`
	StartLine  int      `json:"startLine"`
	EndLine    int      `json:"endLine"`
	TotalLines *int     `json:"totalLines"`
	Truncated  bool     `json:"truncated"`
	Headings   []string `json:"headings"`
	Raw        map[string]json.RawMessage
}

func (p *partOut) UnmarshalJSON(b []byte) error {
	type plain partOut
	if err := json.Unmarshal(b, (*plain)(p)); err != nil {
		return err
	}
	return json.Unmarshal(b, &p.Raw)
}

// longNote is a note with frontmatter and n numbered body lines, a heading
// every 25 lines. Body line k reads "line k of the body".
func longNote(n int) string {
	var b strings.Builder
	b.WriteString("---\ntitle: Long\ntags: [long, test]\n---\n# Long\n")
	for k := 1; k <= n; k++ {
		if k%25 == 0 {
			fmt.Fprintf(&b, "## Part %d\n", k/25)
			continue
		}
		fmt.Fprintf(&b, "line %d of the body, see [[other]]\n", k)
	}
	return b.String()
}

// legacyReadNoteOut is read_note's structured result before NSO-569, kept
// here verbatim so the plain read can be compared byte for byte.
type legacyReadNoteOut struct {
	Path        string         `json:"path"`
	Title       string         `json:"title"`
	Content     string         `json:"content"`
	Frontmatter map[string]any `json:"frontmatter,omitempty"`
	Tags        []string       `json:"tags,omitempty"`
	Links       []string       `json:"links,omitempty"`
	Hash        string         `json:"hash"`
	Size        int64          `json:"size"`
	ModTime     time.Time      `json:"modTime"`
}

// canonicalJSON is v as go-sdk puts it on the wire: the SDK decodes a
// handler's output into a generic value before sending it, so object keys
// arrive sorted.
func canonicalJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(generic)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestReadNotePlainUnchanged(t *testing.T) {
	notes := map[string]string{
		"a/long.md":  longNote(80),
		"a/plain.md": "# Plain <b> & \"quotes\"\n\nNo frontmatter, a < b & c > d.\n",
		"a/empty.md": "",
		"a/crlf.md":  "---\r\ntags: [x]\r\n---\r\n# C\r\n\r\ntext\r\n",
	}
	s := newReadSession(t, notes)
	for p := range notes {
		note, err := s.vault.Read(p)
		if err != nil {
			t.Fatal(err)
		}
		legacy := canonicalJSON(t, legacyReadNoteOut{
			Path: note.Path, Title: note.Title, Content: note.Content, Frontmatter: note.Frontmatter,
			Tags: note.Tags, Links: note.Links, Hash: note.Hash, Size: note.Size, ModTime: note.ModTime,
		})
		header := "path: " + note.Path + "\nhash: " + note.Hash + "\n"
		if len(note.Tags) > 0 {
			header += "tags: " + strings.Join(note.Tags, ", ") + "\n"
		}
		wantText := header + fmt.Sprintf("lines: %d\n\n", lineCount(note.Content)) + note.Content

		// Without the new parameters, and with all of them at their zero
		// value, the result is what read_note returned before.
		for _, args := range []map[string]any{
			{"path": p},
			{"path": p, "start_line": 0, "max_lines": 0, "section": "", "outline": false, "line_numbers": false},
		} {
			res := s.call("read_note", args)
			if res.IsError {
				t.Fatalf("%v: %s", args, res.Text)
			}
			if string(res.Structured) != string(legacy) {
				t.Errorf("%v structuredContent changed:\n got %s\nwant %s", args, res.Structured, legacy)
			}
			if res.Text != wantText {
				t.Errorf("%v text changed:\n got %q\nwant %q", args, res.Text, wantText)
			}
		}
	}
}

func TestReadNoteLineRange(t *testing.T) {
	content := longNote(200)
	s := newReadSession(t, map[string]string{"long.md": content})
	lines := vault.SplitLines(content)
	full, err := s.vault.Read("long.md")
	if err != nil {
		t.Fatal(err)
	}

	out, res := s.read(map[string]any{"path": "long.md", "start_line": 120, "max_lines": 40})
	if want := strings.Join(lines[119:159], ""); out.Content == nil || *out.Content != want {
		t.Fatalf("content = %v, want lines 120-159:\n%s", out.Content, want)
	}
	if out.StartLine != 120 || out.EndLine != 159 || out.TotalLines == nil || *out.TotalLines != len(lines) || !out.Truncated {
		t.Errorf("range fields = start %d end %d total %v truncated %v, want 120 159 %d true",
			out.StartLine, out.EndLine, out.TotalLines, out.Truncated, len(lines))
	}
	if out.Hash != full.Hash {
		t.Errorf("partial read hash %s, want the whole note's %s", out.Hash, full.Hash)
	}
	wantHead := fmt.Sprintf("path: long.md\nhash: %s\ntags: long, test\nlines: 120-159 of %d (truncated, continue with start_line 160)\n\n", full.Hash, len(lines))
	if res.Text != wantHead+*out.Content {
		t.Errorf("text =\n%q\nwant\n%q", res.Text, wantHead+*out.Content)
	}

	// start_line alone reads to the end; nothing was cut.
	out, _ = s.read(map[string]any{"path": "long.md", "start_line": 190})
	if *out.Content != strings.Join(lines[189:], "") || out.EndLine != len(lines) || out.Truncated {
		t.Errorf("start_line alone = %+v", out)
	}
	// A range that ends exactly at the end of the file is not truncated.
	out, _ = s.read(map[string]any{"path": "long.md", "start_line": len(lines) - 4, "max_lines": 5})
	if out.EndLine != len(lines) || out.Truncated {
		t.Errorf("range to the last line = %+v", out)
	}
	// max_lines alone starts at line 1, frontmatter included.
	out, _ = s.read(map[string]any{"path": "long.md", "max_lines": 3})
	if *out.Content != "---\ntitle: Long\ntags: [long, test]\n" || out.StartLine != 1 || out.EndLine != 3 || !out.Truncated {
		t.Errorf("max_lines alone = %+v", out)
	}

	// Past the end: no error, empty content, the line count to go by.
	out, res = s.read(map[string]any{"path": "long.md", "start_line": 5000, "max_lines": 10})
	if out.Content == nil || *out.Content != "" || out.TotalLines == nil || *out.TotalLines != len(lines) || out.Truncated {
		t.Errorf("past the end = %+v", out)
	}
	if _, ok := out.Raw["endLine"]; ok {
		t.Errorf("past the end reports endLine: %s", res.Structured)
	}
	if !strings.Contains(res.Text, fmt.Sprintf("lines: none (start_line 5000 is past the end, the note has %d)", len(lines))) {
		t.Errorf("past-the-end text = %q", res.Text)
	}
}

func TestReadNoteSearchLineHitsMatch(t *testing.T) {
	notes := map[string]string{
		"fm.md":   longNote(60) + "the zanzibar clause is here\n" + longNote(10),
		"crlf.md": "---\r\ntitle: Crlf\r\ntags: [a]\r\n---\r\n# Crlf\r\n\r\nfirst\r\nsecond quokka line\r\n",
	}
	s := newReadSession(t, notes)
	for word, path := range map[string]string{"zanzibar": "fm.md", "quokka": "crlf.md"} {
		var found searchNotesOut
		res := s.call("search_notes", map[string]any{"query": word})
		if err := json.Unmarshal(res.Structured, &found); err != nil {
			t.Fatal(err)
		}
		if len(found.Results) != 1 || found.Results[0].Path != path || len(found.Results[0].Snippets) == 0 {
			t.Fatalf("search %q = %+v", word, found.Results)
		}
		line := found.Results[0].Snippets[0].Line
		out, _ := s.read(map[string]any{"path": path, "start_line": line, "max_lines": 1})
		if out.Content == nil || !strings.Contains(*out.Content, word) || out.StartLine != line || out.EndLine != line {
			t.Errorf("%s: start_line %d from search read %v, want the line with %q", path, line, out.Content, word)
		}
		out, _ = s.read(map[string]any{"path": path, "start_line": line, "max_lines": 1, "line_numbers": true})
		if want := fmt.Sprintf("%d: ", line); !strings.HasPrefix(*out.Content, want) || !strings.Contains(*out.Content, word) {
			t.Errorf("%s: numbered line = %q, want prefix %q", path, *out.Content, want)
		}
	}
}

func TestReadNoteSectionMatchesPatch(t *testing.T) {
	content := "---\ntitle: S\n---\n# S\n\nintro\n\n## Plan\n\nplan text\n\n### Detail\n\n```\n## not a heading\n```\n\ndetail\n\n## Log\n\nentry\n"
	s := newReadSession(t, map[string]string{"s.md": content})
	full, err := s.vault.Read("s.md")
	if err != nil {
		t.Fatal(err)
	}

	out, res := s.read(map[string]any{"path": "s.md", "section": "plan"}) // case-insensitive, like patch_note
	lines := vault.SplitLines(content)
	if out.StartLine != 8 || out.EndLine != 19 {
		t.Fatalf("section Plan = lines %d-%d, want 8-19 (up to ## Log, across ### Detail and the fence)", out.StartLine, out.EndLine)
	}
	if want := strings.Join(lines[7:19], ""); *out.Content != want {
		t.Fatalf("section content = %q, want %q", *out.Content, want)
	}
	if out.Hash != full.Hash || out.Truncated || *out.TotalLines != len(lines) {
		t.Errorf("section fields = %+v", out)
	}
	if !strings.Contains(res.Text, "section: plan\nlines: 8-19 of 22\n\n## Plan\n") {
		t.Errorf("section text = %q", res.Text)
	}

	// patch_note with the hash from the section read replaces exactly the
	// lines after the heading that the read returned.
	if r := s.call("patch_note", map[string]any{"path": "s.md", "section": "Plan", "content": "NEW", "expected_hash": out.Hash}); r.IsError {
		t.Fatalf("patch_note with the section read's hash: %s", r.Text)
	}
	after, err := s.vault.Read("s.md")
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.Join(lines[:8], "") + "NEW\n" + strings.Join(lines[19:], ""); after.Content != want {
		t.Errorf("patched note =\n%q\nwant\n%q", after.Content, want)
	}

	// max_lines caps a section and says where it stopped. After the patch
	// "## Log" is line 10.
	out, res = s.read(map[string]any{"path": "s.md", "section": "Log", "max_lines": 1})
	if *out.Content != "## Log\n" || !out.Truncated || out.StartLine != 10 || out.EndLine != 10 {
		t.Errorf("capped section = %+v", out)
	}
	if !strings.Contains(res.Text, "(truncated, continue with start_line 11)") {
		t.Errorf("capped section text = %q", res.Text)
	}
}

func TestReadNoteOutline(t *testing.T) {
	content := longNote(100)
	s := newReadSession(t, map[string]string{"long.md": content})
	full, err := s.vault.Read("long.md")
	if err != nil {
		t.Fatal(err)
	}
	out, res := s.read(map[string]any{"path": "long.md", "outline": true})
	if _, ok := out.Raw["content"]; ok {
		t.Error("outline carries content")
	}
	if _, ok := out.Raw["links"]; ok {
		t.Error("outline carries links")
	}
	for _, k := range []string{"modTime", "title"} {
		if _, ok := out.Raw[k]; ok {
			t.Errorf("outline carries %s", k)
		}
	}
	for _, k := range []string{"path", "frontmatter", "tags", "hash", "size", "totalLines", "headings"} {
		if _, ok := out.Raw[k]; !ok {
			t.Errorf("outline misses %q: %s", k, res.Structured)
		}
	}
	if out.Hash != full.Hash || *out.TotalLines != len(vault.SplitLines(content)) {
		t.Errorf("outline hash/lines = %s/%d", out.Hash, *out.TotalLines)
	}
	want := []string{"5: # Long", "30: ## Part 1", "55: ## Part 2", "80: ## Part 3", "105: ## Part 4"}
	if !reflect.DeepEqual(out.Headings, want) {
		t.Errorf("headings = %+v, want %+v", out.Headings, want)
	}
	wantText := fmt.Sprintf("path: long.md\nhash: %s\ntags: long, test\nlines: %d\n\n---\ntitle: Long\ntags: [long, test]\n---\n"+
		"5: # Long\n30: ## Part 1\n55: ## Part 2\n80: ## Part 3\n105: ## Part 4\n", full.Hash, *out.TotalLines)
	if res.Text != wantText {
		t.Errorf("outline text =\n%q\nwant\n%q", res.Text, wantText)
	}
	// Every heading line number reads back as that heading.
	for _, h := range out.Headings {
		num, heading, _ := strings.Cut(h, ": ")
		line, err := strconv.Atoi(num)
		if err != nil {
			t.Fatalf("heading %q: %v", h, err)
		}
		got, _ := s.read(map[string]any{"path": "long.md", "start_line": line, "max_lines": 1})
		if *got.Content != heading+"\n" {
			t.Errorf("line %d = %q, want heading %q", line, *got.Content, heading)
		}
	}

	// The outline wins over every other option, even a conflicting pair.
	again, _ := s.read(map[string]any{"path": "long.md", "outline": true, "section": "nope", "start_line": 3, "max_lines": -1, "line_numbers": true})
	if !reflect.DeepEqual(again.Headings, out.Headings) || again.Content != nil {
		t.Errorf("outline with other options = %+v", again)
	}

	s2 := newReadSession(t, map[string]string{"flat.md": "just text\n"})
	_, res = s2.read(map[string]any{"path": "flat.md", "outline": true})
	if !strings.HasSuffix(res.Text, "lines: 1\n\n(no headings)\n") {
		t.Errorf("outline without headings = %q", res.Text)
	}
}

// TestReadNoteOutlineSize: the outline of the measurement's 620-line note
// (51 headings, under its real path) stays under 2 kB in each channel,
// counting a kB as 1000 bytes.
func TestReadNoteOutlineSize(t *testing.T) {
	fx := writePayloadVault(t, t.TempDir())
	v, err := vault.New(fx.root)
	if err != nil {
		t.Fatal(err)
	}
	note, err := v.Read(fx.longNote)
	if err != nil {
		t.Fatal(err)
	}
	s := newReadSession(t, map[string]string{fx.longNote: note.Content})
	out, res := s.read(map[string]any{"path": fx.longNote, "outline": true})
	if *out.TotalLines < 500 || len(out.Headings) < 40 {
		t.Fatalf("fixture note has %d lines, %d headings", *out.TotalLines, len(out.Headings))
	}
	t.Logf("outline of a %d-line note with %d headings: structuredContent %d B, text %d B",
		*out.TotalLines, len(out.Headings), len(res.Structured), len(res.Text))
	if len(res.Structured) >= 2000 || len(res.Text) >= 2000 {
		t.Errorf("outline is %d B structured, %d B text; want each under 2 kB", len(res.Structured), len(res.Text))
	}
}

func TestReadNoteLineNumbersOnly(t *testing.T) {
	content := "---\ntags: [a]\n---\n# T\n\nbody"
	s := newReadSession(t, map[string]string{"n.md": content})
	out, res := s.read(map[string]any{"path": "n.md", "line_numbers": true})
	if want := "1: ---\n2: tags: [a]\n3: ---\n4: # T\n5: \n6: body"; *out.Content != want {
		t.Errorf("numbered = %q, want %q", *out.Content, want)
	}
	for _, k := range []string{"startLine", "endLine", "totalLines", "truncated", "headings"} {
		if _, ok := out.Raw[k]; ok {
			t.Errorf("whole-note numbered read carries %q", k)
		}
	}
	if !strings.HasSuffix(res.Text, "lines: 6\n\n"+*out.Content) {
		t.Errorf("numbered text = %q", res.Text)
	}
	// Numbers on a range start at the range.
	out, _ = s.read(map[string]any{"path": "n.md", "start_line": 4, "max_lines": 2, "line_numbers": true})
	if *out.Content != "4: # T\n5: \n" {
		t.Errorf("numbered range = %q", *out.Content)
	}
}

func TestReadNoteOptionErrors(t *testing.T) {
	s := newReadSession(t, map[string]string{"s.md": "# Top\n\n## Alpha\na\n## Beta\nb\n"})
	msg := s.readErr(map[string]any{"path": "s.md", "section": "Alpha", "start_line": 3})
	if !strings.Contains(msg, "section and start_line cannot be combined") {
		t.Errorf("section+start_line error = %q", msg)
	}
	msg = s.readErr(map[string]any{"path": "s.md", "section": "Gamma"})
	for _, want := range []string{"section not found", `"Gamma"`, `"Top"`, `"Alpha"`, `"Beta"`} {
		if !strings.Contains(msg, want) {
			t.Errorf("missing-section error %q lacks %s", msg, want)
		}
	}
	for _, args := range []map[string]any{
		{"path": "s.md", "start_line": -1},
		{"path": "s.md", "max_lines": -5},
	} {
		if msg := s.readErr(args); !strings.Contains(msg, "must not be negative") {
			t.Errorf("%v error = %q", args, msg)
		}
	}
	if msg := s.readErr(map[string]any{"path": "missing.md", "outline": true}); !strings.Contains(msg, "not found") {
		t.Errorf("missing note outline error = %q", msg)
	}
}

func TestInstructionsMentionPartialReads(t *testing.T) {
	got := instructions(Deps{})
	for _, want := range []string{"outline", "start_line", "search_notes"} {
		if !strings.Contains(got, want) {
			t.Errorf("instructions lack %q:\n%s", want, got)
		}
	}
}

// The long-note-section scenario before NSO-569 (whole-note read_note,
// after NSO-570), recorded in docs/payload-baseline.md.
const (
	longNoteSectionBefore           = 64488
	longNoteSectionBeforeStructured = 32678
)

// TestLongNoteSectionPayloadBudget fails if getting one section out of the
// long note costs more than 20 % of the whole-note read it replaced, in
// total and in structuredContent alone (what Claude Code passes on).
func TestLongNoteSectionPayloadBudget(t *testing.T) {
	report := runPayload(t)
	for _, r := range report.Scenarios {
		if r.Name != "long-note-section" {
			continue
		}
		if limit := longNoteSectionBefore * 20 / 100; r.Result() > limit {
			t.Errorf("long-note-section returns %d B, want at most %d (20 %% of %d B)", r.Result(), limit, longNoteSectionBefore)
		}
		if limit := longNoteSectionBeforeStructured * 20 / 100; r.Structured > limit {
			t.Errorf("long-note-section structuredContent is %d B, want at most %d (20 %% of %d B)", r.Structured, limit, longNoteSectionBeforeStructured)
		}
		return
	}
	t.Fatal("long-note-section scenario missing")
}
