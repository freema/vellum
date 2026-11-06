package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/freema/vellum/internal/vault"
)

// Folder descriptions over MCP (NSO-573).

// contextNotes is a small vault with two described folders (one nested in
// the other), one undescribed folder and notes matching "needle" in each.
var contextNotes = map[string]string{
	"projects/x/_index.md":          "---\ndescription: Project X specs and decisions.\n---\n",
	"projects/x/a.md":               "# A\n\nneedle in x\n",
	"projects/x/deep/b.md":          "# B\n\nneedle deep in x\n",
	"projects/x/deep/sub/_index.md": "Deep subfolder of X.\n",
	"projects/x/deep/sub/c.md":      "# C\n\nneedle in sub\n",
	"projects/y/d.md":               "# D\n\nneedle in y\n",
}

// Acceptance: without a single description note, search_notes, list_notes,
// read_note and suggest_location answer as before: no contexts key, no
// description keys, no folder lines in the text.
func TestNoContextsWithoutDescriptionNotes(t *testing.T) {
	notes := map[string]string{}
	for p, c := range contextNotes {
		if !strings.HasSuffix(p, "/_index.md") {
			notes[p] = c
		}
	}
	s := newReadSession(t, notes)
	for _, res := range []wireResult{
		s.call("search_notes", map[string]any{"query": "needle"}),
		s.call("search_notes", map[string]any{"query": "needle", "response_format": "concise"}),
		s.call("list_notes", map[string]any{"dir": "projects"}),
		s.call("read_note", map[string]any{"path": "projects/x/deep/sub/c.md", "outline": true}),
	} {
		if res.IsError {
			t.Fatal(res.Text)
		}
		if strings.Contains(string(res.Structured), `"contexts"`) || strings.Contains(string(res.Structured), `"description"`) {
			t.Errorf("structured result mentions descriptions: %s", res.Structured)
		}
		if strings.Contains(res.Text, "folder ") {
			t.Errorf("text block has folder lines:\n%s", res.Text)
		}
	}
}

// Acceptance: results from projects/x/** carry the description of
// projects/x, nested folders too, once per response; the text block
// carries the same as "folder dir: description" lines.
func TestSearchNotesContexts(t *testing.T) {
	s := newReadSession(t, contextNotes)
	want := map[string]string{
		"projects/x":          "Project X specs and decisions.",
		"projects/x/deep/sub": "Deep subfolder of X.",
	}
	for _, format := range []string{"detailed", "concise"} {
		res := s.call("search_notes", map[string]any{"query": "needle", "response_format": format})
		out := decode[searchNotesOut](t, res)
		if len(out.Results) != 4 {
			t.Fatalf("%s: %d results", format, len(out.Results))
		}
		if !reflect.DeepEqual(out.Contexts, want) {
			t.Errorf("%s: contexts = %v, want %v", format, out.Contexts, want)
		}
		if n := strings.Count(string(res.Structured), "Project X specs"); n != 1 {
			t.Errorf("%s: description of projects/x appears %d times, want once", format, n)
		}
		wantText := "folder projects/x: Project X specs and decisions.\nfolder projects/x/deep/sub: Deep subfolder of X.\n"
		if !strings.Contains(res.Text, wantText) {
			t.Errorf("%s: text block lacks the folder lines:\n%s", format, res.Text)
		}
	}

	// Only the folders of this page's hits: a search within projects/y
	// carries no description.
	out := decode[searchNotesOut](t, s.call("search_notes", map[string]any{"query": "needle", "dir": "projects/y"}))
	if out.Contexts != nil {
		t.Errorf("contexts for projects/y = %v", out.Contexts)
	}
	// A hit in projects/x/deep (undescribed) still gets projects/x above it.
	out = decode[searchNotesOut](t, s.call("search_notes", map[string]any{"query": "deep", "dir": "projects/x/deep"}))
	if !reflect.DeepEqual(out.Contexts, want) && !reflect.DeepEqual(out.Contexts, map[string]string{"projects/x": want["projects/x"]}) {
		t.Errorf("contexts under projects/x/deep = %v", out.Contexts)
	}
}

// Acceptance: write_note on projects/x/_index.md shows in contexts at once,
// and a bare _index.md lands in the vault root, not the inbox.
func TestWriteNoteDescriptionTakesEffect(t *testing.T) {
	s := newSession(t)
	call(t, s, "write_note", map[string]any{"path": "projects/x/a.md", "content": "# A\n\nneedle\n"}, nil)

	var found searchNotesOut
	call(t, s, "search_notes", map[string]any{"query": "needle"}, &found)
	if found.Contexts != nil {
		t.Fatalf("contexts before any description: %v", found.Contexts)
	}

	var w writeNoteOut
	call(t, s, "write_note", map[string]any{"path": "projects/x/_index.md", "content": "---\ndescription: First text\n---\n"}, &w)
	call(t, s, "search_notes", map[string]any{"query": "needle"}, &found)
	if got := found.Contexts["projects/x"]; got != "First text" {
		t.Fatalf("contexts after write_note = %v", found.Contexts)
	}
	// Update the description the way the instructions say: read, then
	// write with expected_hash.
	call(t, s, "write_note", map[string]any{
		"path": "projects/x/_index.md", "content": "---\ndescription: Second text\n---\n",
		"overwrite": true, "expected_hash": w.Hash,
	}, nil)
	call(t, s, "search_notes", map[string]any{"query": "needle"}, &found)
	if got := found.Contexts["projects/x"]; got != "Second text" {
		t.Fatalf("contexts after the update = %v", found.Contexts)
	}

	var root writeNoteOut
	call(t, s, "write_note", map[string]any{"path": "_index.md", "content": "---\ndescription: The whole vault\n---\n"}, &root)
	if root.Path != "_index.md" {
		t.Errorf("bare _index.md written to %q, want the vault root", root.Path)
	}
	var other writeNoteOut
	call(t, s, "write_note", map[string]any{"path": "loose.md", "content": "# Loose\n"}, &other)
	if other.Path != "inbox/loose.md" {
		t.Errorf("bare loose.md written to %q, want the inbox", other.Path)
	}
}

// Acceptance: the instructions carry the one sentence about _index.md (with
// the configured file name) and, when the vault root is described, that
// description; no tool for writing descriptions exists.
func TestInstructionsFolderDescriptions(t *testing.T) {
	sentence := "Folders are described by <folder>/_index.md with a `description:` frontmatter field. " +
		"Create or update it with write_note (read it first if it exists)."
	if got := instructions(Deps{}); !strings.Contains(got, sentence) || strings.Contains(got, "This vault:") {
		t.Errorf("instructions without a vault:\n%s", got)
	}

	v, err := vault.New(t.TempDir(), vault.WithContextFile("README.md"))
	if err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("Osobní poznámky. ", 40) // 680 characters
	if err := v.Write("README.md", "---\ndescription: "+long+"\n---\n", vault.WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	ix := vault.NewIndex(v)
	if err := ix.Build(); err != nil {
		t.Fatal(err)
	}
	got := instructions(Deps{Vault: v, Index: ix})
	if !strings.Contains(got, "Folders are described by <folder>/README.md with a `description:` frontmatter field.") {
		t.Errorf("instructions do not name the configured file:\n%s", got)
	}
	_, desc, ok := strings.Cut(got, "\n\nThis vault: ")
	if !ok || !strings.HasPrefix(desc, "Osobní poznámky.") || len([]rune(desc)) > 500 {
		t.Errorf("vault description in the instructions: %d characters, %q", len([]rune(desc)), desc)
	}

	for _, curator := range []bool{false, true} {
		sess := newSession(t, func(d *Deps) { d.Curator = curator })
		res, err := sess.ListTools(context.Background(), &mcp.ListToolsParams{})
		if err != nil {
			t.Fatal(err)
		}
		for _, tool := range res.Tools {
			if tool.Name == "describe_folder" || (strings.Contains(tool.Name, "describ") && !tool.Annotations.ReadOnlyHint) {
				t.Errorf("a tool for writing descriptions exists: %s", tool.Name)
			}
		}
	}
}

func TestListNotesDirDescriptions(t *testing.T) {
	s := newReadSession(t, contextNotes)
	out := decode[listNotesOut](t, s.call("list_notes", map[string]any{"dir": "projects"}))
	want := []vault.DirCount{
		{Path: "projects/x", Notes: 5, Description: "Project X specs and decisions."},
		{Path: "projects/y", Notes: 1},
	}
	if !reflect.DeepEqual(out.Dirs, want) {
		t.Errorf("dirs = %+v, want %+v", out.Dirs, want)
	}
}

// The outline of read_note carries the chain of descriptions from the top
// folder down to the note's; a plain read does not change.
func TestReadNoteOutlineContexts(t *testing.T) {
	s := newReadSession(t, contextNotes)
	out, res := s.read(map[string]any{"path": "projects/x/deep/sub/c.md", "outline": true})
	var got struct {
		Contexts map[string]string `json:"contexts"`
	}
	if err := json.Unmarshal(res.Structured, &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"projects/x": "Project X specs and decisions.", "projects/x/deep/sub": "Deep subfolder of X."}
	if !reflect.DeepEqual(got.Contexts, want) {
		t.Errorf("outline contexts = %v, want %v", got.Contexts, want)
	}
	if i, j := strings.Index(string(res.Structured), `"projects/x"`), strings.Index(string(res.Structured), `"projects/x/deep/sub"`); i < 0 || j < i {
		t.Errorf("contexts are not top folder first: %s", res.Structured)
	}
	wantText := "lines: 3\nfolder projects/x: Project X specs and decisions.\nfolder projects/x/deep/sub: Deep subfolder of X.\n\n"
	if !strings.Contains(res.Text, wantText) {
		t.Errorf("outline text:\n%s", res.Text)
	}
	if len(out.Headings) != 1 {
		t.Errorf("headings = %v", out.Headings)
	}

	_, plain := s.read(map[string]any{"path": "projects/x/deep/sub/c.md"})
	if strings.Contains(string(plain.Structured), "contexts") || strings.Contains(plain.Text, "folder ") {
		t.Errorf("plain read changed:\n%s\n%s", plain.Structured, plain.Text)
	}
}

func curatorReadSession(t *testing.T, notes map[string]string) *readSession {
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
	server := New(Deps{Vault: v, Index: ix, Searcher: vault.NewScanSearcher(v, ix), Structure: vault.DefaultStructure(), Version: "test", Curator: true})
	st, ct := mcp.NewInMemoryTransports()
	ctx := context.Background()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	rec := &payloadRecorder{}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "contexts-test", Version: "0"}, nil).
		Connect(ctx, recordingTransport{Transport: ct, rec: rec}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return &readSession{t: t, session: cs, rec: rec, vault: v}
}

// Acceptance: suggest_location offers, for content without tags, the folder
// whose description holds words of the content; curator reports leave
// description notes out; find_undescribed_folders lists the folders that
// need one.
func TestCuratorFolderDescriptions(t *testing.T) {
	notes := map[string]string{
		"projects/garden/_index.md": "---\ndescription: Zahrada, výsadba rajčat a sklizeň.\n---\n",
		"projects/garden/log.md":    "# Log\n\nplain\n",
		"projects/go/tips.md":       "---\ntags: [go]\n---\n# Go Tips\n",
		"inbox/_index.md":           "Unfiled notes, sorted weekly.\n",
	}
	for i := range 5 {
		notes[fmt.Sprintf("projects/big/n%d.md", i)] = fmt.Sprintf("---\ntags: [big]\n---\n# Big %d\n", i)
	}
	s := curatorReadSession(t, notes)

	sugg := decode[suggestLocationOut](t, s.call("suggest_location", map[string]any{
		"content": "# Odrůdy rajčat\n\nCo zasadit na zahradu, výsadba v dubnu.\n",
	}))
	if len(sugg.Suggestions) != 2 {
		t.Fatalf("suggestions = %+v", sugg.Suggestions)
	}
	top, inbox := sugg.Suggestions[0], sugg.Suggestions[1]
	if top.Dir != "projects/garden" || top.Description != "Zahrada, výsadba rajčat a sklizeň." ||
		top.Reason != "the folder description mentions: rajčat, výsadba" {
		t.Errorf("top suggestion = %+v", top)
	}
	if inbox.Dir != "inbox" || inbox.Description != "Unfiled notes, sorted weekly." {
		t.Errorf("inbox fallback = %+v", inbox)
	}

	for _, tool := range []string{"find_untagged", "find_orphans"} {
		res := s.call(tool, nil)
		if strings.Contains(string(res.Structured), "_index.md") {
			t.Errorf("%s reports a description note: %s", tool, res.Structured)
		}
	}

	res := s.call("find_undescribed_folders", nil)
	out := decode[undescribedOut](t, res)
	if out.Total != 1 || out.Truncated || len(out.Folders) != 1 {
		t.Fatalf("find_undescribed_folders = %s", res.Structured)
	}
	if f := out.Folders[0]; f.Dir != "projects/big" || f.Path != "projects/big/_index.md" || f.Notes != 5 ||
		len(f.Titles) != 5 || !reflect.DeepEqual(f.Tags, []string{"big"}) {
		t.Errorf("undescribed folder = %+v", f)
	}
	if out := decode[undescribedOut](t, s.call("find_undescribed_folders", map[string]any{"min_notes": 50})); out.Folders == nil || len(out.Folders) != 0 {
		t.Errorf("min_notes 50 = %+v, want an empty list", out)
	}
}

// payloadDescriptions are realistic description notes for the payload
// fixture: the vault root, the projects folder, three projects and one area.
var payloadDescriptions = map[string]string{
	"_index.md":          "---\ndescription: Osobní vault, domácí projekty, oblasti života a archiv.\n---\n",
	"projects/_index.md": "---\ndescription: Aktivní projekty s jasným cílem a koncem.\n---\n",
	"projects/rekonstrukce-koupelny/_index.md": "---\ndescription: Rekonstrukce koupelny, nabídky dodavatelů, schůzky, rozpočet a harmonogram prací.\n---\n",
	"projects/zahrada/_index.md":               "---\ndescription: Zahrada, výsadba zeleniny, deník sklizně a plány na další sezónu.\n---\n",
	"projects/homelab/_index.md":               "---\ndescription: Domácí server, runbooky, incidenty, nasazení a zálohování.\n---\n",
	"areas/finance/_index.md":                  "---\ndescription: Faktury, daně a pojištění domácnosti.\n---\n",
}

// descriptionOf is the description: field of a payloadDescriptions note.
func descriptionOf(content string) string {
	_, rest, _ := strings.Cut(content, "description: ")
	d, _, _ := strings.Cut(rest, "\n")
	return d
}

// TestFolderContextPayload replays the payload scenarios on the fixture
// with and without description notes. Acceptance: where-does-it-go grows by
// no more than the descriptions it returns (plus their JSON key), and no
// scenario grows by more than every description once (per channel), which
// rules out a description repeated per hit. With -v it prints the table.
func TestFolderContextPayload(t *testing.T) {
	before := runPayload(t)
	after := runPayloadWith(t, payloadDescriptions)

	allOnce := 0 // every described folder once, as "dir": "description" or a folder line
	for p, c := range payloadDescriptions {
		if p != "_index.md" { // the root goes into the instructions, never into results
			allOnce += len(p) + len(descriptionOf(c)) + 32
		}
	}
	for i, b := range before.Scenarios {
		a := after.Scenarios[i]
		if a.Calls != b.Calls {
			t.Errorf("%s: %d calls with descriptions, %d without", a.Name, a.Calls, b.Calls)
		}
		if a.Structured-b.Structured > allOnce || a.Text-b.Text > allOnce {
			t.Errorf("%s grew by %d B structured, %d B text; bound %d B", a.Name,
				a.Structured-b.Structured, a.Text-b.Text, allOnce)
		}
		if a.Name != "where-does-it-go" {
			continue
		}
		// Only projects/zahrada among the suggestions is described.
		bound := len(descriptionOf(payloadDescriptions["projects/zahrada/_index.md"])) + len(`,"description":""`)
		if a.Structured-b.Structured > bound || a.Text-b.Text > bound {
			t.Errorf("where-does-it-go grew by %d B structured, %d B text; the description is %d B with its key",
				a.Structured-b.Structured, a.Text-b.Text, bound)
		}
	}
	if !testing.Verbose() {
		return
	}
	fmt.Print("\nScenarios with folder descriptions (NSO-573), Result B and structuredContent B:\n\n")
	fmt.Print("| Scenario | Result B before | Result B after | structuredContent B before | structuredContent B after |\n")
	fmt.Print("| --- | ---: | ---: | ---: | ---: |\n")
	var tb, ta, sb, sa int
	for i, b := range before.Scenarios {
		a := after.Scenarios[i]
		tb, ta, sb, sa = tb+b.Result(), ta+a.Result(), sb+b.Structured, sa+a.Structured
		fmt.Printf("| %s | %d | %d | %d | %d |\n", b.Name, b.Result(), a.Result(), b.Structured, a.Structured)
	}
	fmt.Printf("| **total** | %d | %d | %d | %d |\n\n", tb, ta, sb, sa)
	fmt.Print(markdownTable("Scenario (with descriptions)", after.Scenarios))
	fmt.Println()
}
