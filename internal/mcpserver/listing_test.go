package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/freema/vellum/internal/vault"
)

// dirSession serves an existing vault directory through a recording client,
// for fixtures too big to write note by note through the vault layer.
func dirSession(t *testing.T, root string) (*readSession, *vault.ScanSearcher) {
	t.Helper()
	s, searcher, _ := dirSessionIndex(t, root)
	return s, searcher
}

// dirSessionIndex is dirSession that also hands out the index, for tests
// that rebuild it the way a restart or a file watcher would.
func dirSessionIndex(t *testing.T, root string) (*readSession, *vault.ScanSearcher, *vault.Index) {
	t.Helper()
	v, err := vault.New(root)
	if err != nil {
		t.Fatal(err)
	}
	ix := vault.NewIndex(v)
	if err := ix.Build(); err != nil {
		t.Fatal(err)
	}
	searcher := vault.NewScanSearcher(v, ix)
	server := New(Deps{Vault: v, Index: ix, Searcher: searcher, Structure: vault.DefaultStructure(), Version: "test"})
	st, ct := mcp.NewInMemoryTransports()
	ctx := context.Background()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	rec := &payloadRecorder{}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "listing-test", Version: "0"}, nil).
		Connect(ctx, recordingTransport{Transport: ct, rec: rec}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return &readSession{t: t, session: cs, rec: rec, vault: v}, searcher, ix
}

// writeFiles writes notes straight to disk under root.
func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for p, c := range files {
		abs := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// decode unmarshals a tool's structured result, failing on a tool error.
func decode[T any](t *testing.T, res wireResult) T {
	t.Helper()
	if res.IsError {
		t.Fatalf("tool error: %s", res.Text)
	}
	var out T
	if err := json.Unmarshal(res.Structured, &out); err != nil {
		t.Fatalf("decode %s: %v", res.Structured, err)
	}
	return out
}

// needleVault has 30 notes matching "needle" with different strengths, so
// the ranking has a definite order, plus one note that does not match.
func needleVault(t *testing.T) string {
	root := t.TempDir()
	files := map[string]string{"other/plain.md": "# Plain\n\nnothing to find\n"}
	for i := range 30 {
		body := strings.Repeat("filler line\n", 3) + strings.Repeat("a needle here\n", 1+i%4) + "end\n"
		title := fmt.Sprintf("Note %02d", i)
		if i%7 == 0 {
			title = fmt.Sprintf("Needle %02d", i) // title hits rank first
		}
		files[fmt.Sprintf("n/%02d/note-%02d.md", i%3, i)] = "---\ntags: [hay]\n---\n# " + title + "\n\n" + body
	}
	writeFiles(t, root, files)
	return root
}

// Acceptance: a call with today's parameters returns the same results in
// the same order, only at most 20 instead of 50, with total and truncated
// on top.
func TestSearchNotesDefaultPageKeepsRanking(t *testing.T) {
	s, searcher := dirSession(t, needleVault(t))
	want, err := searcher.Search("needle", vault.SearchOpts{ContextLines: searchContextLines}) // the old MCP call
	if err != nil || len(want) != 30 {
		t.Fatalf("vault search = %d results, %v", len(want), err)
	}

	res := s.call("search_notes", map[string]any{"query": "needle"})
	out := decode[searchNotesOut](t, res)
	if len(out.Results) != searchDefaultMax || out.Total != 30 || !out.Truncated {
		t.Fatalf("default page: %d results, total %d, truncated %v", len(out.Results), out.Total, out.Truncated)
	}
	var raw struct {
		Results []json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal(res.Structured, &raw); err != nil {
		t.Fatal(err)
	}
	for i, r := range raw.Results {
		// Same fields with the same values as the old vault.Result (the
		// wire form sorts keys, so compare decoded).
		old, _ := json.Marshal(want[i])
		var got, exp any
		_ = json.Unmarshal(r, &got)
		_ = json.Unmarshal(old, &exp)
		if !reflect.DeepEqual(got, exp) {
			t.Errorf("result %d = %s, want the old result %s", i, r, old)
		}
	}
	if out.Hint != "10 more results: narrow with tags or dir, or continue with offset: 20" {
		t.Errorf("hint = %q", out.Hint)
	}
	if !strings.HasPrefix(res.Text, "notes 1-20 of 30 (best matching line") || !strings.HasSuffix(res.Text, out.Hint+"\n") {
		t.Errorf("text header or hint missing:\n%s", res.Text)
	}

	// Asking for the old cap gets everything, exactly as before, and no hint.
	res = s.call("search_notes", map[string]any{"query": "needle", "max_results": 50})
	all := decode[searchNotesOut](t, res)
	if len(all.Results) != 30 || all.Total != 30 || all.Truncated || all.Hint != "" {
		t.Fatalf("max_results 50: %d results, total %d, truncated %v, hint %q", len(all.Results), all.Total, all.Truncated, all.Hint)
	}
	keys := structuredKeys(t, &mcp.CallToolResult{StructuredContent: json.RawMessage(res.Structured)})
	if _, ok := keys["hint"]; ok {
		t.Error("hint present on a complete page")
	}
	for _, k := range []string{"results", "total", "truncated"} {
		if _, ok := keys[k]; !ok {
			t.Errorf("structured result lacks %q", k)
		}
	}
	if !strings.HasPrefix(res.Text, "30 notes (") {
		t.Errorf("complete page header = %q", strings.SplitN(res.Text, "\n", 2)[0])
	}
}

func TestSearchNotesOffset(t *testing.T) {
	s, searcher := dirSession(t, needleVault(t))
	want, _ := searcher.Search("needle", vault.SearchOpts{})

	out := decode[searchNotesOut](t, s.call("search_notes", map[string]any{"query": "needle", "offset": 20}))
	if len(out.Results) != 10 || out.Total != 30 || out.Truncated || out.Hint != "" {
		t.Fatalf("offset 20: %d results, total %d, truncated %v, hint %q", len(out.Results), out.Total, out.Truncated, out.Hint)
	}
	for i, r := range out.Results {
		if r.Path != want[20+i].Path {
			t.Errorf("offset result %d = %s, want %s", i, r.Path, want[20+i].Path)
		}
	}

	out = decode[searchNotesOut](t, s.call("search_notes", map[string]any{"query": "needle", "offset": 5, "max_results": 5}))
	if len(out.Results) != 5 || out.Results[0].Path != want[5].Path || out.Hint != "20 more results: narrow with tags or dir, or continue with offset: 10" {
		t.Errorf("window 5..10 = %+v, hint %q", out.Results, out.Hint)
	}

	res := s.call("search_notes", map[string]any{"query": "needle", "offset": 40})
	out = decode[searchNotesOut](t, res)
	if len(out.Results) != 0 || out.Total != 30 || out.Truncated || out.Hint != "offset 40 is past the end: 30 results in all" {
		t.Errorf("past the end: %+v", out)
	}
	if !strings.HasPrefix(res.Text, "no notes on this page (30 matching in all)") {
		t.Errorf("past-the-end text = %q", res.Text)
	}

	// A pure tag filter pages in path order the same way.
	out = decode[searchNotesOut](t, s.call("search_notes", map[string]any{"tags": []string{"hay"}, "offset": 28}))
	if len(out.Results) != 2 || out.Total != 30 || out.Results[1].Path != "n/02/note-29.md" {
		t.Errorf("tag filter page = %+v", out)
	}

	// A single remaining note is spelled in the singular.
	out = decode[searchNotesOut](t, s.call("search_notes", map[string]any{"query": "needle", "max_results": 29}))
	if out.Hint != "1 more result: narrow with tags or dir, or continue with offset: 29" {
		t.Errorf("singular hint = %q", out.Hint)
	}
}

func TestSearchNotesSnippetOptions(t *testing.T) {
	note := "# Pantry\n\nb1\nb2\nalpha one\na1\na2\nfiller\nfiller\nalpha two\nfiller\nfiller\nalpha three\nend\n"
	s := newReadSession(t, map[string]string{"pantry.md": note})

	// Default: three snippets, two lines of context on each side.
	out := decode[searchNotesOut](t, s.call("search_notes", map[string]any{"query": "alpha"}))
	if sn := out.Results[0].Snippets; len(sn) != 3 || sn[0].Context != "b1\nb2\nalpha one\na1\na2" {
		t.Fatalf("default snippets = %+v", sn)
	}

	// context_lines 0 is a real zero: the matching line alone.
	res := s.call("search_notes", map[string]any{"query": "alpha", "context_lines": 0, "max_snippets": 2})
	out = decode[searchNotesOut](t, res)
	sn := out.Results[0].Snippets
	if len(sn) != 2 || sn[0].Context != "alpha one" || sn[1].Context != "alpha two" || sn[0].Line != 5 {
		t.Fatalf("context_lines 0, max_snippets 2 = %+v", sn)
	}
	if !strings.Contains(res.Text, "\n5: alpha one\n") {
		t.Errorf("text with context_lines 0 = %q", res.Text)
	}

	out = decode[searchNotesOut](t, s.call("search_notes", map[string]any{"query": "alpha", "context_lines": 1, "max_snippets": 1}))
	if sn := out.Results[0].Snippets; len(sn) != 1 || sn[0].Context != "b2\nalpha one\na1" {
		t.Errorf("context_lines 1 = %+v", sn)
	}
}

func TestSearchNotesConcise(t *testing.T) {
	note := "---\ntags: [x]\n---\n# Pantry\n\nfiller\n" + strings.Repeat("ž", 150) + " alpha " + strings.Repeat("y", 150) + "\nalpha again\n"
	s := newReadSession(t, map[string]string{"home/pantry.md": note, "home/cellar.md": "# Cellar\n\nalpha beta\n"})

	detailed := decode[searchNotesOut](t, s.call("search_notes", map[string]any{"query": "alpha"}))
	res := s.call("search_notes", map[string]any{"query": "alpha", "response_format": "concise"})
	concise := decode[searchNotesOut](t, res)
	if len(concise.Results) != len(detailed.Results) || concise.Total != 2 {
		t.Fatalf("concise = %+v", concise)
	}
	var raw struct {
		Results []map[string]json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal(res.Structured, &raw); err != nil {
		t.Fatal(err)
	}
	for i, hit := range concise.Results {
		d := detailed.Results[i]
		if hit.Path != d.Path || hit.Title != d.Title {
			t.Errorf("concise hit %d = %s, detailed %s: the ranking differs", i, hit.Path, d.Path)
		}
		if len(raw.Results[i]) != 4 || raw.Results[i]["snippets"] != nil || raw.Results[i]["line"] == nil || raw.Results[i]["score"] == nil {
			t.Errorf("concise hit %d carries %v, want path, title, score and line only", i, raw.Results[i])
		}
		if hit.Score != d.Score || hit.Score <= 0 {
			t.Errorf("concise hit %d score %v, detailed %v", i, hit.Score, d.Score)
		}
		if hit.Line != d.Snippets[0].Line {
			t.Errorf("concise line %d, want the best snippet's line %d", hit.Line, d.Snippets[0].Line)
		}
	}
	// The text block lists the same hits as path:line (title), without the
	// matching line itself.
	if !strings.HasPrefix(res.Text, "2 notes (path:line") || !strings.Contains(res.Text, "\nhome/pantry.md:7 (Pantry)\n") ||
		!strings.Contains(res.Text, "\nhome/cellar.md:3 (Cellar)\n") || strings.Contains(res.Text, "alpha") || strings.Contains(res.Text, "filler") {
		t.Errorf("concise text block:\n%s", res.Text)
	}

	// A pure tag filter has no match to point at: path and title only.
	res = s.call("search_notes", map[string]any{"tags": []string{"x"}, "response_format": "concise"})
	if out := decode[searchNotesOut](t, res); len(out.Results) != 1 || out.Results[0].Line != 0 || out.Results[0].Path != "home/pantry.md" ||
		res.Text != "1 note\nhome/pantry.md (Pantry)\n" {
		t.Errorf("concise tag filter = %+v, text %q", out, res.Text)
	}

	// Explicit detailed is the default.
	if d := decode[searchNotesOut](t, s.call("search_notes", map[string]any{"query": "alpha", "response_format": "detailed"})); !reflect.DeepEqual(d, detailed) {
		t.Errorf("response_format detailed differs from the default")
	}
}

// conciseFullPage runs a concise search that must fill a page of 20 and
// returns its byte counts per channel, checking every hit has its line.
func conciseFullPage(t *testing.T, root, query string) (structured, text, total int) {
	t.Helper()
	s, _ := dirSession(t, root)
	res := s.call("search_notes", map[string]any{"query": query, "response_format": "concise"})
	out := decode[searchNotesOut](t, res)
	if len(out.Results) != 20 || out.Total <= 20 {
		t.Fatalf("want a full page of 20 out of more: %d of %d", len(out.Results), out.Total)
	}
	for _, r := range out.Results {
		if r.Line == 0 || r.Path == "" || r.Title == "" || len(r.Snippets) != 0 {
			t.Errorf("concise hit %+v: want path, title and line, no snippets", r)
		}
		if !strings.Contains(res.Text, "\n"+r.Path+":"+fmt.Sprint(r.Line)+" ("+r.Title+")\n") {
			t.Errorf("text block misses %s:%d", r.Path, r.Line)
		}
	}
	return len(res.Structured), len(res.Text), out.Total
}

// Acceptance: response_format concise on a query with 20 results stays
// under 3 kB, on the NSO-567 measurement fixture (paths of 60 bytes and
// more, Czech diacritics) and on its find-and-read query. Each channel is
// what one client hands to the model (docs/payload-baseline.md), so each
// has to fit on its own.
func TestSearchNotesConciseUnder3KB(t *testing.T) {
	fx := writePayloadVault(t, t.TempDir())
	structured, text, total := conciseFullPage(t, fx.root, "dlažba rozpočet")
	if structured >= 3000 || text >= 3000 {
		t.Errorf("concise page of 20: structuredContent %d B, text %d B; want both under 3000", structured, text)
	}
	s, _ := dirSession(t, fx.root)
	detailed := s.call("search_notes", map[string]any{"query": "dlažba rozpočet"})
	if sum, dsum := structured+text, len(detailed.Structured)+len(detailed.Text); sum > dsum/4 {
		t.Errorf("concise page sends %d B over both channels, detailed %d B; want at most a quarter", sum, dsum)
	}
	t.Logf("payload fixture, 20 of %d: concise structuredContent %d B, text %d B (both %d B); detailed %d B, %d B",
		total, structured, text, structured+text, len(detailed.Structured), len(detailed.Text))
}

// The same page on a vault with everyday paths. Each channel on its own
// stays under 3 kB, as in the acceptance above (the BM25F score of NSO-577
// adds about 14 bytes per hit to structuredContent).
func TestSearchNotesConciseEverydayPaths(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{}
	for i := range 30 {
		files[fmt.Sprintf("projects/garden/planting-log-%02d.md", i)] = fmt.Sprintf("---\ntags: [garden]\n---\n# Planting log %d\n\n"+
			"We spent most of the weekend in the greenhouse, and moved the tomato seedlings %d into bigger pots before the cold "+
			"nights came back; the peppers stay inside for another two weeks.\n", i, i)
	}
	writeFiles(t, root, files)
	structured, text, total := conciseFullPage(t, root, "tomato seedlings")
	if structured >= 3000 || text >= 3000 {
		t.Errorf("concise page of 20: structuredContent %d B, text %d B; want both under 3000", structured, text)
	}
	t.Logf("everyday paths, 20 of %d: structuredContent %d B, text %d B", total, structured, text)
}

func TestSearchNotesArgumentErrors(t *testing.T) {
	s := newReadSession(t, map[string]string{"a.md": "# A\n\nalpha\n"})
	for _, args := range []map[string]any{
		{"query": "alpha", "response_format": "brief"},
		{"query": "alpha", "offset": -1},
		{"query": "alpha", "context_lines": -1},
		{"query": "alpha", "max_snippets": -2},
	} {
		if res := s.call("search_notes", args); !res.IsError {
			t.Errorf("%v: want a tool error, got %s", args, res.Structured)
		}
	}
	// A negative max_results falls back to the default, as before.
	if out := decode[searchNotesOut](t, s.call("search_notes", map[string]any{"query": "alpha", "max_results": -5})); len(out.Results) != 1 {
		t.Errorf("max_results -5 = %+v", out)
	}
}

// Acceptance: list_notes on the vault root without recursive returns the
// subfolders with their note counts.
func TestListNotesDirs(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"top.md":                  "# Top\n",
		"projects/a/one.md":       "# One\n",
		"projects/a/deep/two.md":  "# Two\n",
		"projects/b.md":           "# B\n",
		"inbox/idea.md":           "# Idea\n",
		".hidden/secret.md":       "# Secret\n",
		"projects/.trash/gone.md": "# Gone\n",
	})
	if err := os.MkdirAll(filepath.Join(root, "empty", "inner"), 0o755); err != nil {
		t.Fatal(err)
	}
	s, _ := dirSession(t, root)

	res := s.call("list_notes", map[string]any{})
	out := decode[listNotesOut](t, res)
	if len(out.Notes) != 1 || out.Notes[0].Path != "top.md" || out.Notes[0].Title != "Top" || out.Total != 1 || out.Truncated || out.Hint != "" {
		t.Errorf("root notes = %+v", out)
	}
	wantDirs := []vault.DirCount{{Path: "empty", Notes: 0}, {Path: "inbox", Notes: 1}, {Path: "projects", Notes: 3}}
	if !reflect.DeepEqual(out.Dirs, wantDirs) {
		t.Errorf("root dirs = %+v, want %+v", out.Dirs, wantDirs)
	}

	out = decode[listNotesOut](t, s.call("list_notes", map[string]any{"dir": "projects"}))
	if wantDirs := []vault.DirCount{{Path: "projects/a", Notes: 2}}; !reflect.DeepEqual(out.Dirs, wantDirs) {
		t.Errorf("projects dirs = %+v, want %+v", out.Dirs, wantDirs)
	}
	if len(out.Notes) != 1 || out.Notes[0].Path != "projects/b.md" {
		t.Errorf("projects notes = %+v", out.Notes)
	}

	// Recursive lists every note below and still reports the direct
	// subfolders only.
	out = decode[listNotesOut](t, s.call("list_notes", map[string]any{"dir": "projects/", "recursive": true}))
	if out.Total != 3 || len(out.Notes) != 3 || len(out.Dirs) != 1 || out.Dirs[0].Path != "projects/a" {
		t.Errorf("recursive projects = %+v", out)
	}

	// A leaf folder has an empty dirs list, not a missing one.
	res = s.call("list_notes", map[string]any{"dir": "empty/inner"})
	if !strings.Contains(string(res.Structured), `"dirs":[]`) {
		t.Errorf("leaf folder = %s", res.Structured)
	}
	if res := s.call("list_notes", map[string]any{"dir": "missing"}); !res.IsError {
		t.Errorf("missing folder = %s", res.Structured)
	}
}

// Without the new parameters list_notes returns the notes it always did,
// as long as they fit the default limit.
func TestListNotesUnchangedBelowLimit(t *testing.T) {
	fx := writePayloadVault(t, t.TempDir())
	s, _ := dirSession(t, fx.root)
	want, err := s.vault.List("projects/zahrada", true)
	if err != nil {
		t.Fatal(err)
	}
	out := decode[listNotesOut](t, s.call("list_notes", map[string]any{"dir": "projects/zahrada", "recursive": true}))
	if !sameNotes(out.Notes, want) || out.Total != len(want) || out.Truncated || len(want) == 0 {
		t.Errorf("list_notes = %d notes, vault.List %d", len(out.Notes), len(want))
	}
}

// Acceptance: a recursive listing of a vault with 5,000 notes returns 200
// entries and a hint, not a megabyte of JSON.
func TestListNotesLargeVaultIsCapped(t *testing.T) {
	root := t.TempDir()
	files := make(map[string]string, 5000)
	for i := range 5000 {
		files[fmt.Sprintf("area-%02d/sub-%d/note-%04d.md", i%25, i%4, i)] = fmt.Sprintf("# Note %d\n\nbody\n", i)
	}
	writeFiles(t, root, files)
	s, _ := dirSession(t, root)

	res := s.call("list_notes", map[string]any{"recursive": true})
	out := decode[listNotesOut](t, res)
	if len(out.Notes) != listDefaultLimit || out.Total != 5000 || !out.Truncated {
		t.Fatalf("recursive listing: %d notes, total %d, truncated %v", len(out.Notes), out.Total, out.Truncated)
	}
	want := "4800 more notes: narrow with dir (dirs lists the subfolders and their note counts) or list without recursive, or continue with offset: 200"
	if out.Hint != want {
		t.Errorf("hint = %q, want %q", out.Hint, want)
	}
	if len(out.Dirs) != 25 || out.Dirs[0] != (vault.DirCount{Path: "area-00", Notes: 200}) {
		t.Errorf("dirs = %+v", out.Dirs)
	}
	if len(res.Structured) > 40_000 {
		t.Errorf("capped listing is %d B of structuredContent", len(res.Structured))
	}
	t.Logf("5,000 notes, recursive: structuredContent %d B, text %d B", len(res.Structured), len(res.Text))

	// Paging walks the whole listing in path order without gaps.
	all, err := s.vault.List("", true)
	if err != nil {
		t.Fatal(err)
	}
	page := decode[listNotesOut](t, s.call("list_notes", map[string]any{"recursive": true, "offset": 4900, "limit": 150}))
	if len(page.Notes) != 100 || page.Truncated || page.Hint != "" || !sameNotes(page.Notes, all[4900:]) {
		t.Errorf("last page: %d notes, truncated %v, hint %q", len(page.Notes), page.Truncated, page.Hint)
	}
	page = decode[listNotesOut](t, s.call("list_notes", map[string]any{"recursive": true, "offset": 200, "limit": 10}))
	if !sameNotes(page.Notes, all[200:210]) {
		t.Errorf("page at 200 = %+v", page.Notes)
	}
	if res := s.call("list_notes", map[string]any{"offset": -1}); !res.IsError {
		t.Errorf("negative offset accepted: %s", res.Structured)
	}
}

// sameNotes compares listings across the wire, where time stamps lose
// their monotonic reading.
func sameNotes(a, b []vault.NoteInfo) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Path != b[i].Path || a[i].Title != b[i].Title || a[i].Size != b[i].Size || !a[i].ModTime.Equal(b[i].ModTime) {
			return false
		}
	}
	return true
}

func TestPageHint(t *testing.T) {
	for _, c := range []struct {
		total, offset, returned int
		want                    string
	}{
		{0, 0, 0, ""},
		{5, 0, 5, ""},
		{5, 3, 2, ""},
		{7, 0, 5, "2 more notes: narrow, or continue with offset: 5"},
		{6, 0, 5, "1 more note: narrow, or continue with offset: 5"},
		{5, 9, 0, "offset 9 is past the end: 5 notes in all"},
	} {
		if got := pageHint(c.total, c.offset, c.returned, "notes", "narrow"); got != c.want {
			t.Errorf("pageHint(%d, %d, %d) = %q, want %q", c.total, c.offset, c.returned, got, c.want)
		}
	}
}

func TestSearchListTextPaging(t *testing.T) {
	results := []vault.Result{{Path: "a.md", Title: "A"}, {Path: "b.md"}}
	got := searchListText(results, 2, listPage{total: 9, offset: 4, hint: "3 more results: x"})
	if want := "notes 5-6 of 9\na.md (A)\nb.md\n3 more results: x\n"; got != want {
		t.Errorf("page text = %q, want %q", got, want)
	}
	if got := searchListText(nil, 2, listPage{total: 3, offset: 3, hint: "h"}); got != "no notes on this page (3 matching in all)\nh\n" {
		t.Errorf("empty page = %q", got)
	}
}

// The new parameters are optional and typed in the published schema.
func TestSearchAndListSchemas(t *testing.T) {
	s := newSession(t)
	tools, err := s.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	props := map[string][]string{
		"search_notes": {"query", "max_results", "offset", "response_format", "context_lines", "max_snippets", "min_score"},
		"list_notes":   {"dir", "recursive", "limit", "offset", "sort", "since"},
	}
	for _, tool := range tools.Tools {
		want, ok := props[tool.Name]
		if !ok {
			continue
		}
		raw, _ := json.Marshal(tool.InputSchema)
		var schema struct {
			Properties map[string]struct {
				Type any `json:"type"`
			} `json:"properties"`
			Required []string `json:"required"`
		}
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatal(err)
		}
		if len(schema.Required) != 0 {
			t.Errorf("%s requires %v", tool.Name, schema.Required)
		}
		for _, p := range want {
			if _, ok := schema.Properties[p]; !ok {
				t.Errorf("%s schema lacks %q: %s", tool.Name, p, raw)
			}
		}
		if tool.Name == "search_notes" {
			if typ := fmt.Sprint(schema.Properties["context_lines"].Type); !strings.Contains(typ, "integer") {
				t.Errorf("context_lines type = %v", typ)
			}
		}
	}
}

// orientBaseline is the orient scenario on v1.12.1 (one recursive
// list_notes of every note), recorded in docs/payload-baseline.md.
const orientBaseline = 79816

// The folder counts make learning the structure cheap: the orient scenario
// has to stay under 5 % of what listing every note cost.
func TestOrientPayloadBudget(t *testing.T) {
	report := runPayload(t)
	for _, r := range report.Scenarios {
		if r.Name != "orient" {
			continue
		}
		if limit := orientBaseline * 5 / 100; r.Result() > limit {
			t.Errorf("orient returns %d B (structured %d, text %d), want at most %d (5 %% of the v1.12.1 %d B)",
				r.Result(), r.Structured, r.Text, limit, orientBaseline)
		}
		return
	}
	t.Fatal("orient scenario missing")
}
