package vault

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

// Folder descriptions (NSO-573).

func buildIndex(t *testing.T, v *Vault) *Index {
	t.Helper()
	ix := NewIndex(v)
	if err := ix.Build(); err != nil {
		t.Fatal(err)
	}
	return ix
}

func TestValidateContextFile(t *testing.T) {
	for _, ok := range []string{"_index.md", "README.md", "index.markdown"} {
		if err := ValidateContextFile(ok); err != nil {
			t.Errorf("ValidateContextFile(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", "a/_index.md", `a\_index.md`, ".index.md", "_index.txt", "_index"} {
		if err := ValidateContextFile(bad); err == nil {
			t.Errorf("ValidateContextFile(%q) accepted", bad)
		}
	}
	if _, err := New(t.TempDir(), WithContextFile("dir/x.md")); !errors.Is(err, ErrInvalidPath) {
		t.Errorf("New with a bad context file = %v, want ErrInvalidPath", err)
	}
	v, err := New(t.TempDir())
	if err != nil || v.ContextFile() != DefaultContextFile {
		t.Fatalf("default context file = %q, %v", v.ContextFile(), err)
	}
	if !v.IsContextNote("_index.md") || !v.IsContextNote("a/b/_index.md") ||
		v.IsContextNote("a/index.md") || v.IsContextNote("a/_Index.md") {
		t.Error("IsContextNote matches the wrong files")
	}
}

func TestFolderDescriptionSources(t *testing.T) {
	note := func(content string) *Note {
		raw, body := splitFrontmatter(content)
		return &Note{Body: body, Frontmatter: parseFrontmatter(raw)}
	}
	tests := []struct{ name, content, want string }{
		{"frontmatter", "---\ndescription: Vývoj vellumu. Specifikace, rozhodnutí.\n---\n# X\n\nBody text.\n", "Vývoj vellumu. Specifikace, rozhodnutí."},
		{"frontmatter wins over body", "---\ndescription: From frontmatter\n---\nFrom body\n", "From frontmatter"},
		{"multi-line frontmatter collapsed", "---\ndescription: >\n  Garden notes,\n  planting logs\n---\n", "Garden notes, planting logs"},
		{"first paragraph", "# Garden\n\nPlanting logs and\nharvest notes.\n\nSecond paragraph.\n", "Planting logs and harvest notes."},
		{"rule and headings skipped", "---\ntitle: T\n---\n---\n## Sub\n\nText here.\n", "Text here."},
		{"paragraph ends at a heading", "Line one\n## Next\nLine two\n", "Line one"},
		{"empty description falls back", "---\ndescription: \"  \"\n---\nBody paragraph.\n", "Body paragraph."},
		{"nothing", "---\ntags: [a]\n---\n# Only a heading\n", ""},
	}
	for _, tt := range tests {
		if got := folderDescription(note(tt.content), maxFolderDescription); got != tt.want {
			t.Errorf("%s: description = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestFolderDescriptionCap(t *testing.T) {
	long := strings.Repeat("žluťoučký kůň ", 60) // 840 characters
	n := &Note{Frontmatter: map[string]any{"description": long}}
	got := folderDescription(n, maxFolderDescription)
	if c := utf8.RuneCountInString(got); c > maxFolderDescription || !strings.HasSuffix(got, "…") || !utf8.ValidString(got) {
		t.Errorf("capped description: %d characters, valid %v: %q", c, utf8.ValidString(got), got)
	}
	if got := folderDescription(n, maxVaultDescription); utf8.RuneCountInString(got) != maxVaultDescription {
		t.Errorf("vault description: %d characters, want %d", utf8.RuneCountInString(got), maxVaultDescription)
	}
	if got := capRunes("short", 300); got != "short" {
		t.Errorf("capRunes changed a short text: %q", got)
	}
}

// Acceptance: a vault without a single description note answers exactly as
// before, and the index holds nothing for descriptions.
func TestNoDescriptionNotes(t *testing.T) {
	_, ix := curatorFixture(t)
	if ix.contexts != nil || ix.contextNotes != nil {
		t.Errorf("index holds description state without description notes: %v %v", ix.contexts, ix.contextNotes)
	}
	if got := ix.FolderContexts("projects/go/notes.md", "inbox/linked.md"); got != nil {
		t.Errorf("FolderContexts = %#v, want nil", got)
	}
	if got := ix.FolderDescription(""); got != "" {
		t.Errorf("vault description = %q", got)
	}
}

// Acceptance: after projects/x/_index.md is created, results from
// projects/x/** carry its description, nested folders included; each level
// comes separately and the vault root is left out.
func TestFolderContextsChain(t *testing.T) {
	v := newTestVault(t)
	mustWrite(t, v, "_index.md", "---\ndescription: The whole vault\n---\n")
	mustWrite(t, v, "projects/x/_index.md", "---\ndescription: Project X\n---\n")
	mustWrite(t, v, "projects/x/a/b/_index.md", "Deep folder B.\n")
	mustWrite(t, v, "projects/x/note.md", "# Note\n")
	mustWrite(t, v, "projects/x/a/b/c/deep.md", "# Deep\n")
	mustWrite(t, v, "projects/y/other.md", "# Other\n")
	mustWrite(t, v, "top.md", "# Top\n")
	ix := buildIndex(t, v)

	if got := ix.FolderContexts("projects/x/note.md"); !reflect.DeepEqual(got, map[string]string{"projects/x": "Project X"}) {
		t.Errorf("contexts of projects/x/note.md = %v", got)
	}
	want := map[string]string{"projects/x": "Project X", "projects/x/a/b": "Deep folder B."}
	if got := ix.FolderContexts("projects/x/a/b/c/deep.md"); !reflect.DeepEqual(got, want) {
		t.Errorf("contexts of the nested note = %v, want %v", got, want)
	}
	if got := ix.FolderContexts("projects/y/other.md", "top.md"); got != nil {
		t.Errorf("contexts of undescribed folders = %v, want nil", got)
	}
	// Many hits from the same folders: one description per folder.
	if got := ix.FolderContexts("projects/x/note.md", "projects/x/a/b/c/deep.md", "projects/y/other.md", "projects/x/_index.md"); !reflect.DeepEqual(got, want) {
		t.Errorf("contexts of several notes = %v, want %v", got, want)
	}
	if got := ix.FolderDescription(""); got != "The whole vault" {
		t.Errorf("vault description = %q", got)
	}
	if got := ix.FolderDescription("projects/x/"); got != "Project X" {
		t.Errorf("FolderDescription(projects/x/) = %q", got)
	}
}

// Writes through vellum (Update) take effect at once, even when the new
// text has the same size and lands in the same second; removing or moving
// the description note drops it.
func TestFolderContextsFollowWrites(t *testing.T) {
	v := newTestVault(t)
	mustWrite(t, v, "projects/x/note.md", "# Note\n")
	ix := buildIndex(t, v)

	write := func(p, content string) {
		t.Helper()
		if err := v.Write(p, content, WriteOptions{Overwrite: true}); err != nil {
			t.Fatal(err)
		}
		if err := ix.Update(p); err != nil {
			t.Fatal(err)
		}
	}
	write("projects/x/_index.md", "---\ndescription: aaaa\n---\n")
	if got := ix.FolderDescription("projects/x"); got != "aaaa" {
		t.Fatalf("after create: %q", got)
	}
	write("projects/x/_index.md", "---\ndescription: bbbb\n---\n") // same size, same second
	if got := ix.FolderDescription("projects/x"); got != "bbbb" {
		t.Fatalf("after rewrite: %q", got)
	}
	write("projects/x/_index.md", "# Only a heading\n")
	if got := ix.FolderContexts("projects/x/note.md"); got != nil {
		t.Fatalf("empty description still served: %v", got)
	}
	write("projects/x/_index.md", "---\ndescription: back\n---\n")

	if err := v.Move("projects/x/_index.md", "projects/z/_index.md"); err != nil {
		t.Fatal(err)
	}
	if err := ix.Rename("projects/x/_index.md", "projects/z/_index.md"); err != nil {
		t.Fatal(err)
	}
	if ix.FolderDescription("projects/x") != "" || ix.FolderDescription("projects/z") != "back" {
		t.Errorf("after move: x=%q z=%q", ix.FolderDescription("projects/x"), ix.FolderDescription("projects/z"))
	}
	if err := v.Delete("projects/z/_index.md"); err != nil {
		t.Fatal(err)
	}
	ix.Remove("projects/z/_index.md")
	if ix.contexts != nil || ix.contextNotes != nil {
		t.Errorf("after delete the index still holds %v %v", ix.contexts, ix.contextNotes)
	}
}

// Acceptance: a description edited in another editor shows once the index
// is rebuilt from disk.
func TestFolderContextsExternalEdit(t *testing.T) {
	v := newTestVault(t)
	mustWrite(t, v, "projects/x/_index.md", "---\ndescription: Old text\n---\n")
	ix := buildIndex(t, v)

	abs := filepath.Join(v.Root(), "projects", "x", "_index.md")
	if err := os.WriteFile(abs, []byte("---\ndescription: New text\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ix.FolderDescription("projects/x"); got != "Old text" {
		t.Fatalf("before the rebuild: %q", got)
	}
	if err := ix.Build(); err != nil {
		t.Fatal(err)
	}
	if got := ix.FolderDescription("projects/x"); got != "New text" {
		t.Errorf("after the rebuild: %q, want New text", got)
	}
}

// VELLUM_CONTEXT_FILE: another file name takes over, _index.md becomes an
// ordinary note.
func TestCustomContextFile(t *testing.T) {
	v, err := New(t.TempDir(), WithContextFile("README.md"))
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, v, "docs/README.md", "Project documentation.\n")
	mustWrite(t, v, "docs/_index.md", "---\ndescription: Not the description\n---\n")
	ix := buildIndex(t, v)
	if got := ix.FolderDescription("docs"); got != "Project documentation." {
		t.Errorf("description with README.md = %q", got)
	}
	got, err := v.ResolveWritePath("README.md", "", DefaultStructure())
	if err != nil || got != "README.md" {
		t.Errorf("bare README.md resolves to %q, %v; want the vault root", got, err)
	}
	if got, _ := v.ResolveWritePath("_index.md", "", DefaultStructure()); got != "inbox/_index.md" {
		t.Errorf("bare _index.md with README.md configured resolves to %q, want the inbox", got)
	}
}

// Acceptance: a bare _index.md is the vault's own description and stays in
// the root; every other bare file name still goes to the inbox.
func TestResolveWritePathContextFile(t *testing.T) {
	v := newTestVault(t)
	s := DefaultStructure()
	for requested, want := range map[string]string{
		"_index.md":            "_index.md",
		"./_index.md":          "_index.md",
		"projects/x/_index.md": "projects/x/_index.md",
		"index.md":             "inbox/index.md",
		"note.md":              "inbox/note.md",
	} {
		got, err := v.ResolveWritePath(requested, "", s)
		if err != nil || got != want {
			t.Errorf("ResolveWritePath(%q) = %q, %v; want %q", requested, got, err, want)
		}
	}
}

// Acceptance: content without tags is offered the folder whose description
// shares its words, with the description on the suggestion.
func TestSuggestLocationByDescription(t *testing.T) {
	v := newTestVault(t)
	mustWrite(t, v, "projects/garden/_index.md", "---\ndescription: Zahrada, výsadba rajčat a sklizeň.\n---\n")
	mustWrite(t, v, "projects/garden/log.md", "# Log\n\nplain text\n")
	mustWrite(t, v, "projects/books/_index.md", "---\ndescription: Notes about the books I read, with quotes.\n---\n")
	mustWrite(t, v, "projects/books/dune.md", "---\ntags: [scifi]\n---\n# Dune\n")
	mustWrite(t, v, "projects/go/tips.md", "---\ntags: [go]\n---\n# Go Tips\n")
	ix := buildIndex(t, v)

	sugg := ix.SuggestLocation("# Odrůdy rajčat\n\nCo zasadit na ZAHRADA příští rok.\n", DefaultStructure())
	if len(sugg) != 2 || sugg[0].Dir != "projects/garden" {
		t.Fatalf("suggestions = %+v, want projects/garden then the inbox", sugg)
	}
	top := sugg[0]
	if top.Description != "Zahrada, výsadba rajčat a sklizeň." || len(top.SharedTags) != 0 ||
		top.NoteCount != 1 || top.Reason != "the folder description mentions: Zahrada, rajčat" {
		t.Errorf("top suggestion = %+v", top)
	}

	// Function words ("the", "with", "notes") alone do not tie content to
	// the books folder.
	if s := ix.SuggestLocation("Some notes with the usual words.\n", DefaultStructure()); len(s) != 1 {
		t.Errorf("stop words matched a description: %+v", s)
	}

	// Tags and description words add up; tags still work as before.
	both := ix.SuggestLocation("---\ntags: [scifi]\n---\nA book with quotes\n", DefaultStructure())
	if both[0].Dir != "projects/books" || both[0].Reason !=
		"notes in this folder share tags: scifi; the folder description mentions: quotes" {
		t.Errorf("tags + words = %+v", both[0])
	}
	if tags := ix.SuggestLocation("---\ntags: [go]\n---\nx\n", DefaultStructure()); tags[0].Dir != "projects/go" ||
		tags[0].Description != "" || tags[0].Reason != "notes in this folder share tags: go" {
		t.Errorf("tag-only suggestion changed: %+v", tags[0])
	}
}

// Acceptance: the curator reports leave description notes out.
func TestCuratorSkipsContextNotes(t *testing.T) {
	v := newTestVault(t)
	mustWrite(t, v, "inbox/_index.md", "Unfiled notes.\n")
	mustWrite(t, v, "projects/x/_index.md", "Project X.\n")
	mustWrite(t, v, "projects/x/plain.md", "# Plain\n")
	ix := buildIndex(t, v)
	old := ix.entries["inbox/_index.md"]
	cp := *old
	cp.ModTime = 1 // ancient: would be stale
	ix.entries["inbox/_index.md"] = &cp

	for name, got := range map[string][]Entry{
		"FindUntagged":   ix.FindUntagged(),
		"FindOrphans":    ix.FindOrphans(),
		"FindInboxStale": ix.FindInboxStale("inbox", 0),
	} {
		for _, e := range got {
			if v.IsContextNote(e.Path) {
				t.Errorf("%s reports the description note %s", name, e.Path)
			}
		}
	}
	if got := ix.FindUntagged(); len(got) != 1 || got[0].Path != "projects/x/plain.md" {
		t.Errorf("FindUntagged = %+v", got)
	}
}

func TestFindUndescribedFolders(t *testing.T) {
	v := newTestVault(t)
	for i := range 6 {
		mustWrite(t, v, fmt.Sprintf("projects/big/n%d.md", i), fmt.Sprintf("---\ntags: [alpha%s]\n---\n# Big %d\n", strings.Repeat(", beta", i%2), i))
		mustWrite(t, v, fmt.Sprintf("projects/described/n%d.md", i), "# D\n")
		mustWrite(t, v, fmt.Sprintf("root%d.md", i), "# R\n")
	}
	for i := range 4 {
		mustWrite(t, v, fmt.Sprintf("projects/small/n%d.md", i), "# S\n")
	}
	mustWrite(t, v, "projects/described/_index.md", "Described.\n")
	mustWrite(t, v, "projects/big/sub/n.md", "# Sub\n") // subfolders do not count
	ix := buildIndex(t, v)

	got := ix.FindUndescribedFolders(5)
	want := []UndescribedFolder{
		{Dir: "", Path: "_index.md", Notes: 6, Titles: []string{"R", "R", "R", "R", "R"}},
		{Dir: "projects/big", Path: "projects/big/_index.md", Notes: 6,
			Titles: []string{"Big 0", "Big 1", "Big 2", "Big 3", "Big 4"}, Tags: []string{"alpha", "beta"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FindUndescribedFolders(5) =\n%+v\nwant\n%+v", got, want)
	}
	if got := ix.FindUndescribedFolders(4); len(got) != 3 || got[2].Dir != "projects/small" {
		t.Errorf("FindUndescribedFolders(4) = %+v", got)
	}
}

func TestSubdirsCarryDescriptions(t *testing.T) {
	v := newTestVault(t)
	mustWrite(t, v, "projects/x/_index.md", "---\ndescription: Project X\n---\n")
	mustWrite(t, v, "projects/x/a.md", "# A\n")
	mustWrite(t, v, "projects/y/b.md", "# B\n")
	ix := buildIndex(t, v)
	got, err := ix.Subdirs("projects")
	if err != nil {
		t.Fatal(err)
	}
	want := []DirCount{{Path: "projects/x", Notes: 2, Description: "Project X"}, {Path: "projects/y", Notes: 1}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Subdirs = %+v, want %+v", got, want)
	}
}

// Acceptance: the memory for descriptions grows with the described folders,
// not with the notes. 2,000 notes in 20 folders, 3 of them described: the
// index holds three descriptions and nothing per note.
func TestFolderContextsMemoryPerFolder(t *testing.T) {
	v := newTestVault(t)
	root := v.Root()
	for i := range 2000 {
		p := filepath.Join(root, fmt.Sprintf("area%02d", i%20), fmt.Sprintf("n%04d.md", i))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("# N\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, d := range []string{"area00", "area01", "area02"} {
		mustWrite(t, v, d+"/_index.md", "Folder "+d+".\n")
	}
	ix := buildIndex(t, v)
	if len(ix.contexts) != 3 || len(ix.contextNotes) != 3 {
		t.Errorf("description state: %d folders, %d notes; want 3 and 3", len(ix.contexts), len(ix.contextNotes))
	}
}
