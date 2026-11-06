package mcpserver

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/freema/vellum/internal/vault"
)

// toolText returns the concatenated text blocks of a tool result.
func toolText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	var text string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text += tc.Text
		}
	}
	return text
}

// structuredKeys returns the top-level keys of a result's structuredContent.
func structuredKeys(t *testing.T, res *mcp.CallToolResult) map[string]json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatal(err)
	}
	return keys
}

const textNote = "---\ntitle: Garden plan\ntags: [garden, plan]\n---\n# Garden plan\n\nSow tomatoes in March.\nSee [[other]].\n"

func TestReadNoteDropsBody(t *testing.T) {
	s := newSession(t)
	call(t, s, "write_note", map[string]any{"path": "projects/g/plan.md", "content": textNote}, nil)

	var note struct {
		Path    string   `json:"path"`
		Title   string   `json:"title"`
		Content string   `json:"content"`
		Tags    []string `json:"tags"`
		Links   []string `json:"links"`
		Hash    string   `json:"hash"`
		Size    int64    `json:"size"`
	}
	res := call(t, s, "read_note", map[string]any{"path": "projects/g/plan.md"}, &note)

	keys := structuredKeys(t, res)
	if _, ok := keys["body"]; ok {
		t.Error("read_note structuredContent still carries body")
	}
	for _, k := range []string{"path", "title", "content", "frontmatter", "tags", "hash", "size", "modTime"} {
		if _, ok := keys[k]; !ok {
			t.Errorf("read_note structuredContent lost %q (keys %v)", k, keys)
		}
	}
	if note.Content != textNote || note.Title != "Garden plan" || len(note.Tags) != 2 ||
		note.Size != int64(len(textNote)) || note.Hash == "" {
		t.Errorf("read_note = %+v", note)
	}

	// The text block is the plain rendering, not the JSON mirror: a short
	// header, a blank line, then the file exactly as on disk.
	text := toolText(t, res)
	want := "path: projects/g/plan.md\nhash: " + note.Hash + "\ntags: garden, plan\nlines: 8\n\n" + textNote
	if text != want {
		t.Errorf("read_note text =\n%q\nwant\n%q", text, want)
	}
	if strings.Contains(text, `"body"`) || strings.HasPrefix(text, "{") {
		t.Errorf("read_note text still looks like the JSON mirror: %q", text)
	}
}

// The output schema advertised for read_note matches what it sends.
func TestReadNoteOutputSchemaHasNoBody(t *testing.T) {
	s := newSession(t)
	res, err := s.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range res.Tools {
		if tool.Name != "read_note" {
			continue
		}
		raw, _ := json.Marshal(tool.OutputSchema)
		var schema struct {
			Properties map[string]any `json:"properties"`
		}
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatal(err)
		}
		if _, ok := schema.Properties["body"]; ok {
			t.Error("read_note output schema still declares body")
		}
		if _, ok := schema.Properties["content"]; !ok {
			t.Error("read_note output schema lost content")
		}
		return
	}
	t.Fatal("read_note not registered")
}

func TestNoteTextHeader(t *testing.T) {
	got := noteText(readNoteOut{Path: "inbox/a.md", Hash: "abc", Content: ptr("# A\n\ntext")})
	if want := "path: inbox/a.md\nhash: abc\nlines: 3\n\n# A\n\ntext"; got != want {
		t.Errorf("noteText = %q, want %q", got, want)
	}
}

func TestLineCount(t *testing.T) {
	for in, want := range map[string]int{
		"": 0, "a": 1, "a\n": 1, "a\nb": 2, "a\nb\n": 2, "\n": 1, "a\n\n": 2, "a\r\nb\r\n": 2,
	} {
		if got := lineCount(in); got != want {
			t.Errorf("lineCount(%q) = %d, want %d", in, got, want)
		}
	}
}

// A read sends the note about twice (structured + text), not four times.
// This guards the long note of the payload fixture, where the duplication
// was largest.
func TestReadNotePayloadSendsContentOncePerChannel(t *testing.T) {
	fx := writePayloadVault(t, t.TempDir())
	v, err := vault.New(fx.root)
	if err != nil {
		t.Fatal(err)
	}
	ix := vault.NewIndex(v)
	if err := ix.Build(); err != nil {
		t.Fatal(err)
	}
	server := New(Deps{Vault: v, Index: ix, Searcher: vault.NewScanSearcher(v, ix), Structure: vault.DefaultStructure()})
	st, ct := mcp.NewInMemoryTransports()
	ctx := context.Background()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ss.Close() }()
	rec := &payloadRecorder{}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "0"}, nil).
		Connect(ctx, recordingTransport{Transport: ct, rec: rec}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()

	run := &payloadRun{t: t, session: session, rec: rec, fx: fx, byTool: map[string]*payloadStats{}}
	run.call("read_note", map[string]any{"path": fx.longNote}, nil)
	note, err := v.Read(fx.longNote)
	if err != nil {
		t.Fatal(err)
	}
	size := len(note.Content)
	// JSON escaping (newlines, quotes) and the metadata cost a little; a
	// second copy of the body would cost about another size.
	if st := run.stats; st.Structured > size*6/5 || st.Text > size+512 {
		t.Errorf("read_note of a %d byte note sent structured %d B, text %d B; want about one copy each",
			size, st.Structured, st.Text)
	}
}

// searchFixture is a note whose search hits lines below the frontmatter,
// so the rendered line numbers have to count it.
const searchFixture = "---\ntags: [x]\n---\n# Pantry\n\nalpha line\nfiller one\nalpha again\nfiller two\nfiller three\nfiller four\nfiller five\nfiller six\nalpha last\nend\n"

func TestSearchNotesText(t *testing.T) {
	s := newSession(t)
	call(t, s, "write_note", map[string]any{"path": "home/pantry.md", "content": searchFixture}, nil)
	call(t, s, "write_note", map[string]any{"path": "home/cellar.md", "content": "# Cellar\n\nno match here\nalpha beta\n"}, nil)

	var out searchNotesOut
	res := call(t, s, "search_notes", map[string]any{"query": "alpha"}, &out)
	if len(out.Results) != 2 || len(out.Results[0].Snippets) == 0 || len(out.Results[1].Snippets) == 0 {
		t.Fatalf("search_notes = %+v", out)
	}
	text := toolText(t, res)
	if strings.HasPrefix(text, "{") || strings.Contains(text, `"snippets"`) {
		t.Fatalf("search_notes text still looks like the JSON mirror: %q", text)
	}

	// One header line, then per note (in ranked order) its path and title
	// and its best snippet's match line, numbered like the file.
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if want := "2 notes (best matching line of each; read_note for the full text)"; lines[0] != want {
		t.Errorf("header = %q, want %q", lines[0], want)
	}
	if len(lines) != 1+2*len(out.Results) {
		t.Fatalf("search_notes text has %d lines, want %d:\n%s", len(lines), 1+2*len(out.Results), text)
	}
	files := map[string]string{"home/pantry.md": searchFixture, "home/cellar.md": "# Cellar\n\nno match here\nalpha beta\n"}
	for i, r := range out.Results {
		if want := r.Path + " (" + r.Title + ")"; lines[1+2*i] != want {
			t.Errorf("note %d = %q, want %q", i, lines[1+2*i], want)
		}
		best := r.Snippets[0]
		l := lines[2+2*i]
		n, rest, ok := strings.Cut(l, ": ")
		if !ok || n != strconv.Itoa(best.Line) {
			t.Errorf("note %d match line %q, want the best snippet's line %d", i, l, best.Line)
			continue
		}
		// The number addresses the file line shown (frontmatter counted).
		if fileLine := strings.Split(files[r.Path], "\n")[best.Line-1]; rest != fileLine {
			t.Errorf("line %d rendered as %q, file has %q", best.Line, rest, fileLine)
		}
	}
	// The context windows stay in structuredContent only.
	if strings.Contains(text, "filler") {
		t.Errorf("search_notes text carries context lines:\n%s", text)
	}
}

func TestSearchTextEdgeCases(t *testing.T) {
	if got := searchText(nil, 2); got != "no matching notes" {
		t.Errorf("empty = %q", got)
	}

	// A pure tag filter has no snippets: just the list of notes.
	got := searchText([]vault.Result{{Path: "a.md", Title: "A"}, {Path: "b.md"}}, 2)
	if want := "2 notes\na.md (A)\nb.md\n"; got != want {
		t.Errorf("tag filter = %q, want %q", got, want)
	}

	// Only the best (first) snippet is shown; its match line is found in
	// the window even when the window is clipped at the top of the file.
	got = searchText([]vault.Result{{Path: "a.md", Snippets: []vault.Snippet{
		{Line: 1, Match: "a", Context: "a\nx"},
		{Line: 3, Match: "b", Context: "x\nb\ny"},
	}}}, 1)
	if want := "1 note (best matching line of each; read_note for the full text)\na.md\n1: a\n"; got != want {
		t.Errorf("best snippet = %q, want %q", got, want)
	}
	got = searchText([]vault.Result{{Path: "a.md", Snippets: []vault.Snippet{
		{Line: 3, Match: "b", Context: "x\nb\ny"},
	}}}, 1)
	if want := "1 note (best matching line of each; read_note for the full text)\na.md\n3: b\n"; got != want {
		t.Errorf("windowed snippet = %q, want %q", got, want)
	}

	// A window that does not fit the context size falls back to the line
	// holding the match instead of picking a wrong one.
	got = searchText([]vault.Result{{Path: "a.md", Snippets: []vault.Snippet{
		{Line: 10, Match: "needle", Context: "needle here"},
	}}}, 2)
	if want := "1 note (best matching line of each; read_note for the full text)\na.md\n10: needle here\n"; got != want {
		t.Errorf("fallback = %q, want %q", got, want)
	}
}

func TestClipLine(t *testing.T) {
	if got := clipLine("short line", "line"); got != "short line" {
		t.Errorf("short = %q", got)
	}

	long := strings.Repeat("á", 300) + " needle " + strings.Repeat("b", 300)
	got := clipLine(long, "needle")
	body := strings.TrimSuffix(strings.TrimPrefix(got, "…"), "…")
	if utf8.RuneCountInString(body) != maxTextLine || !strings.HasPrefix(got, "…") || !strings.HasSuffix(got, "…") {
		t.Errorf("clipped %q: want %d runes between two cut marks", got, maxTextLine)
	}
	if !strings.Contains(got, "needle") || !utf8.ValidString(got) {
		t.Errorf("clipped line lost the match or split a rune: %q", got)
	}

	// A match near the start keeps the start; one at the very end keeps
	// the end.
	if got := clipLine("needle "+strings.Repeat("x", 400), "needle"); !strings.HasPrefix(got, "needle") || !strings.HasSuffix(got, "…") {
		t.Errorf("match at start = %q", got)
	}
	if got := clipLine(strings.Repeat("x", 400)+" needle", "needle"); !strings.HasPrefix(got, "…") || !strings.HasSuffix(got, "needle") {
		t.Errorf("match at end = %q", got)
	}
	// No match found: keep the start.
	if got := clipLine(strings.Repeat("x", 400), "zzz"); !strings.HasPrefix(got, "xxx") || !strings.HasSuffix(got, "…") {
		t.Errorf("no match = %q", got)
	}
}

// findAndReadBaseline is the find-and-read scenario on v1.12.1, recorded in
// docs/payload-baseline.md. NSO-570 requires at least 40 % less.
const findAndReadBaseline = 89948

func TestFindAndReadPayloadBudget(t *testing.T) {
	report := runPayload(t)
	for _, r := range report.Scenarios {
		if r.Name != "find-and-read" {
			continue
		}
		if limit := findAndReadBaseline * 60 / 100; r.Result() > limit {
			t.Errorf("find-and-read returns %d B (structured %d, text %d), want at most %d (60 %% of the v1.12.1 %d B)",
				r.Result(), r.Structured, r.Text, limit, findAndReadBaseline)
		}
		return
	}
	t.Fatal("find-and-read scenario missing")
}

func TestCuratorTextBlocks(t *testing.T) {
	s := newSession(t, func(d *Deps) { d.Curator = true })
	call(t, s, "write_note", map[string]any{"path": "projects/g/plan.md", "content": textNote}, nil)
	call(t, s, "write_note", map[string]any{"path": "projects/g/notes.md",
		"content": "---\ntags: [garden]\n---\n# Other\n\nLinks to [[plan]].\n"}, nil)

	var tc vault.TagsContext
	res := call(t, s, "suggest_tags", map[string]any{"path": "projects/g/plan.md"}, &tc)
	if tc.Path != "projects/g/plan.md" || len(tc.VaultTags) == 0 || tc.Excerpt == "" {
		t.Fatalf("suggest_tags structured = %+v", tc)
	}
	text := toolText(t, res)
	for _, want := range []string{
		"path: projects/g/plan.md\n",
		"current tags: garden, plan\n",
		"vault tags: garden (2), plan (1)\n",
		"\nexcerpt:\n# Garden plan\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("suggest_tags text misses %q:\n%s", want, text)
		}
	}
	if strings.HasPrefix(text, "{") {
		t.Errorf("suggest_tags text is the JSON mirror: %q", text)
	}

	var links suggestLinksOut
	res = call(t, s, "suggest_links", map[string]any{"path": "projects/g/plan.md"}, &links)
	if len(links.Candidates) != 1 || links.Candidates[0].Path != "projects/g/notes.md" {
		t.Fatalf("suggest_links structured = %+v", links)
	}
	if text, want := toolText(t, res), "projects/g/notes.md (Other): "+links.Candidates[0].Reason+"\n"; text != want {
		t.Errorf("suggest_links text = %q, want %q", text, want)
	}

	if got := linksText(nil); got != "no link candidates" {
		t.Errorf("empty links = %q", got)
	}
	if got := tagsContextText(&vault.TagsContext{Path: "a.md", Excerpt: "x"}); got != "path: a.md\ncurrent tags: none\nneighbor tags: none\nvault tags: none\n\nexcerpt:\nx" {
		t.Errorf("empty tags context = %q", got)
	}
}

// Tools outside the lean set keep the SDK's JSON mirror in the text block.
func TestOtherToolsKeepJSONMirror(t *testing.T) {
	s := newSession(t)
	call(t, s, "write_note", map[string]any{"path": "projects/g/plan.md", "content": textNote}, nil)
	res := call(t, s, "list_notes", map[string]any{"recursive": true}, nil)
	raw, _ := json.Marshal(res.StructuredContent)
	if text := toolText(t, res); text != string(raw) {
		t.Errorf("list_notes text = %q, want the JSON mirror %q", text, raw)
	}
}
