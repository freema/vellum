package vault

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func buildTestIndex(t *testing.T) (*Vault, *Index) {
	t.Helper()
	v := newTestVault(t)
	mustWrite(t, v, "inbox/alpha.md", "---\ntitle: Alpha\ntags: [go, mcp]\n---\nLinks to [[beta]] and [gamma](../projects/p/gamma.md).\n")
	mustWrite(t, v, "notes/beta.md", "---\ntags: [go]\ntype: task\nstatus: in-progress\n---\n# Beta\n\nInline #extra tag.\n\n```\n#codetag should not count\n[[code-link]] neither\n```\n")
	mustWrite(t, v, "projects/p/gamma.md", "# Gamma\n\nBack to [[alpha]].\n")
	ix := NewIndex(v)
	if err := ix.Build(); err != nil {
		t.Fatalf("Build: %v", err)
	}
	return v, ix
}

func TestIndexBuild(t *testing.T) {
	_, ix := buildTestIndex(t)

	if ix.Len() != 3 {
		t.Fatalf("Len = %d, want 3", ix.Len())
	}
	e, ok := ix.Get("notes/beta.md")
	if !ok {
		t.Fatal("beta not indexed")
	}
	if e.Title != "Beta" || e.Type != "task" || e.Status != "in-progress" {
		t.Errorf("beta entry = %+v", e)
	}
	if want := []string{"extra", "go"}; !reflect.DeepEqual(e.Tags, want) {
		t.Errorf("beta tags = %v, want %v (code block must be ignored)", e.Tags, want)
	}
}

func TestIndexTags(t *testing.T) {
	_, ix := buildTestIndex(t)

	tags := ix.Tags()
	want := []TagCount{{"extra", 1}, {"go", 2}, {"mcp", 1}}
	if !reflect.DeepEqual(tags, want) {
		t.Errorf("Tags() = %v, want %v", tags, want)
	}
	if got := ix.PathsByTag("go"); !reflect.DeepEqual(got, []string{"inbox/alpha.md", "notes/beta.md"}) {
		t.Errorf("PathsByTag(go) = %v", got)
	}
	if got := ix.PathsByTag("codetag"); got != nil {
		t.Errorf("code-block tag must not be indexed, got %v", got)
	}
}

func TestIndexLinksAndBacklinks(t *testing.T) {
	_, ix := buildTestIndex(t)

	// alpha links to beta (bare wikilink by stem) and gamma (relative md link).
	if got := ix.Links("inbox/alpha.md"); !reflect.DeepEqual(got, []string{"notes/beta.md", "projects/p/gamma.md"}) {
		t.Errorf("Links(alpha) = %v", got)
	}
	if got := ix.Backlinks("notes/beta.md"); !reflect.DeepEqual(got, []string{"inbox/alpha.md"}) {
		t.Errorf("Backlinks(beta) = %v", got)
	}
	if got := ix.Backlinks("inbox/alpha.md"); !reflect.DeepEqual(got, []string{"projects/p/gamma.md"}) {
		t.Errorf("Backlinks(alpha) = %v", got)
	}
	// The [[code-link]] inside the fence must not create an edge.
	if got := ix.Backlinks("code-link.md"); got != nil {
		t.Errorf("fenced wikilink must not resolve, got %v", got)
	}
}

func TestIndexIncrementalUpdate(t *testing.T) {
	v, ix := buildTestIndex(t)

	mustWrite(t, v, "inbox/delta.md", "---\ntags: [fresh]\n---\nSee [[beta]].\n")
	if err := ix.Update("inbox/delta.md"); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got := ix.PathsByTag("fresh"); !reflect.DeepEqual(got, []string{"inbox/delta.md"}) {
		t.Errorf("new tag not indexed: %v", got)
	}
	if got := ix.Backlinks("notes/beta.md"); !reflect.DeepEqual(got, []string{"inbox/alpha.md", "inbox/delta.md"}) {
		t.Errorf("Backlinks(beta) after update = %v", got)
	}

	// Modify: tags change, old tag disappears.
	mustWrite(t, v, "inbox/delta.md", "---\ntags: [stale]\n---\nno links\n")
	if err := ix.Update("inbox/delta.md"); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got := ix.PathsByTag("fresh"); got != nil {
		t.Errorf("old tag should be gone, got %v", got)
	}
	if got := ix.Backlinks("notes/beta.md"); !reflect.DeepEqual(got, []string{"inbox/alpha.md"}) {
		t.Errorf("stale backlink kept: %v", got)
	}

	// Delete: Update on a missing file removes the entry.
	if err := v.Delete("inbox/delta.md"); err != nil {
		t.Fatal(err)
	}
	if err := ix.Update("inbox/delta.md"); err != nil {
		t.Fatalf("Update after delete: %v", err)
	}
	if _, ok := ix.Get("inbox/delta.md"); ok {
		t.Error("deleted note still indexed")
	}
	if got := ix.PathsByTag("stale"); got != nil {
		t.Errorf("tags of deleted note kept: %v", got)
	}
}

func TestIndexOnChange(t *testing.T) {
	v, ix := buildTestIndex(t) // Build must not have emitted anything

	var got []Change
	ix.OnChange(func(c Change) { got = append(got, c) })

	mustWrite(t, v, "inbox/fresh.md", "# Fresh\n")
	if err := ix.Update("inbox/fresh.md"); err != nil {
		t.Fatal(err)
	}
	if err := v.Move("inbox/fresh.md", "notes/fresh.md"); err != nil {
		t.Fatal(err)
	}
	if err := ix.Rename("inbox/fresh.md", "notes/fresh.md"); err != nil {
		t.Fatal(err)
	}
	if err := v.Delete("notes/fresh.md"); err != nil {
		t.Fatal(err)
	}
	ix.Remove("notes/fresh.md")
	// Update of a path that vanished from disk degrades to a removal.
	if err := ix.Update("notes/fresh.md"); err != nil {
		t.Fatal(err)
	}

	want := []Change{
		{Path: "inbox/fresh.md"},                // Update
		{Path: "inbox/fresh.md", Deleted: true}, // Rename: old path
		{Path: "notes/fresh.md"},                // Rename: new path
		{Path: "notes/fresh.md", Deleted: true}, // Remove
		{Path: "notes/fresh.md", Deleted: true}, // Update -> ErrNotFound -> Remove
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("changes = %+v\nwant %+v", got, want)
	}
}

func TestIndexRenameReresolvesWikilinks(t *testing.T) {
	v, ix := buildTestIndex(t)

	// gamma links to [[alpha]]; move alpha and the edge must follow.
	if err := v.Move("inbox/alpha.md", "archive/alpha.md"); err != nil {
		t.Fatal(err)
	}
	if err := ix.Rename("inbox/alpha.md", "archive/alpha.md"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if got := ix.Backlinks("archive/alpha.md"); !reflect.DeepEqual(got, []string{"projects/p/gamma.md"}) {
		t.Errorf("Backlinks after rename = %v", got)
	}
	if got := ix.Backlinks("inbox/alpha.md"); got != nil {
		t.Errorf("old path still has backlinks: %v", got)
	}
}

func TestIndexWikilinkAmbiguityDeterministic(t *testing.T) {
	v := newTestVault(t)
	mustWrite(t, v, "b/note.md", "# B\n")
	mustWrite(t, v, "a/note.md", "# A\n")
	mustWrite(t, v, "linker.md", "See [[note]].\n")
	ix := NewIndex(v)
	if err := ix.Build(); err != nil {
		t.Fatal(err)
	}
	// Lexicographically smallest path wins.
	if got := ix.Links("linker.md"); !reflect.DeepEqual(got, []string{"a/note.md"}) {
		t.Errorf("ambiguous wikilink resolved to %v, want [a/note.md]", got)
	}
}

func TestIndexPathWikilink(t *testing.T) {
	v := newTestVault(t)
	mustWrite(t, v, "projects/x/deep.md", "# Deep\n")
	mustWrite(t, v, "linker.md", "See [[projects/x/deep]] and [[projects/x/deep.md]].\n")
	ix := NewIndex(v)
	if err := ix.Build(); err != nil {
		t.Fatal(err)
	}
	if got := ix.Links("linker.md"); !reflect.DeepEqual(got, []string{"projects/x/deep.md"}) {
		t.Errorf("path wikilink resolved to %v", got)
	}
}

func TestExcerptRuneSafeTruncation(t *testing.T) {
	// A long Czech line whose 160th byte lands inside a multi-byte rune — the
	// excerpt must cut on a rune boundary, never producing U+FFFD in the UI.
	long := strings.Repeat("cache a session anti-cheat běží přes middleware ", 8)
	got := excerptOf(long)
	if !utf8.ValidString(got) {
		t.Fatalf("excerpt is not valid UTF-8: %q", got)
	}
	if strings.ContainsRune(got, utf8.RuneError) {
		t.Fatalf("excerpt contains U+FFFD: %q", got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("long excerpt should end with an ellipsis: %q", got)
	}
	if len(got) > 160+len("…") {
		t.Errorf("excerpt too long: %d bytes", len(got))
	}
	// Sweep the cut point across every offset of a multi-byte run for good
	// measure — padding with i ASCII bytes shifts which byte position 160 hits.
	for i := 0; i < 4; i++ {
		s := strings.Repeat("x", 150+i) + " řěščřžýáíé"
		if e := excerptOf(s); !utf8.ValidString(e) || strings.ContainsRune(e, utf8.RuneError) {
			t.Errorf("pad %d: invalid excerpt %q", i, e)
		}
	}
}

func TestIndexBuildSpeed(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping bulk build in -short")
	}
	v := newTestVault(t)
	for i := 0; i < 1200; i++ {
		content := fmt.Sprintf("---\ntitle: Note %d\ntags: [t%d, common]\n---\n# Note %d\n\nBody with #inline%d and [[note-%d]].\n",
			i, i%50, i, i%20, (i+1)%1200)
		mustWrite(t, v, fmt.Sprintf("bulk/dir%d/note-%d.md", i%30, i), content)
	}
	ix := NewIndex(v)
	start := time.Now()
	if err := ix.Build(); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	t.Logf("built index of %d notes in %s", ix.Len(), elapsed)
	if ix.Len() != 1200 {
		t.Errorf("Len = %d, want 1200", ix.Len())
	}
	if elapsed > 3*time.Second {
		t.Errorf("index build took %s, want well under 3s", elapsed)
	}
}
