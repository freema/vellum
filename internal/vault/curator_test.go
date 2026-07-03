package vault

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func curatorFixture(t *testing.T) (*Vault, *Index) {
	t.Helper()
	v := newTestVault(t)
	mustWrite(t, v, "projects/go/notes.md", "---\ntags: [go, dev]\n---\n# Go Notes\n")
	mustWrite(t, v, "projects/go/tips.md", "---\ntags: [go]\n---\n# Go Tips\n")
	mustWrite(t, v, "projects/cooking/pasta.md", "---\ntags: [recipes]\n---\n# Pasta\n")
	mustWrite(t, v, "inbox/untagged.md", "# Untagged note, no links\n")
	mustWrite(t, v, "inbox/linked.md", "---\ntags: [go]\n---\nMentions Go Notes in text and links [[pasta]].\n")
	ix := NewIndex(v)
	if err := ix.Build(); err != nil {
		t.Fatal(err)
	}
	return v, ix
}

func TestSuggestLocation(t *testing.T) {
	_, ix := curatorFixture(t)
	sugg := ix.SuggestLocation("---\ntags: [go, dev]\n---\nnew go content\n", DefaultStructure())
	if len(sugg) < 2 {
		t.Fatalf("suggestions = %+v", sugg)
	}
	if sugg[0].Dir != "projects/go" || len(sugg[0].SharedTags) != 2 {
		t.Errorf("top suggestion = %+v, want projects/go via [dev go]", sugg[0])
	}
	if last := sugg[len(sugg)-1]; last.Dir != "inbox" {
		t.Errorf("last suggestion = %+v, want inbox fallback", last)
	}
	// No tag overlap: only the inbox fallback.
	none := ix.SuggestLocation("plain text\n", DefaultStructure())
	if len(none) != 1 || none[0].Dir != "inbox" {
		t.Errorf("no-overlap suggestions = %+v", none)
	}
}

func TestSuggestTags(t *testing.T) {
	v, ix := curatorFixture(t)
	tc, err := ix.SuggestTags(v, "inbox/linked.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(tc.CurrentTags) != 1 || tc.CurrentTags[0] != "go" {
		t.Errorf("current tags = %v", tc.CurrentTags)
	}
	if len(tc.VaultTags) == 0 {
		t.Error("vault tag vocabulary missing")
	}
	// linked.md links to pasta.md (tag recipes) -> neighbor tag.
	if len(tc.NeighborTags) != 1 || tc.NeighborTags[0] != "recipes" {
		t.Errorf("neighbor tags = %v, want [recipes]", tc.NeighborTags)
	}
	if tc.Excerpt == "" {
		t.Error("excerpt missing")
	}
}

func TestSuggestLinks(t *testing.T) {
	v, ix := curatorFixture(t)

	// pasta.md is linked from linked.md but doesn't link back.
	cands, err := ix.SuggestLinks(v, "projects/cooking/pasta.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) == 0 || cands[0].Path != "inbox/linked.md" {
		t.Fatalf("candidates = %+v, want unreciprocated backlink first", cands)
	}

	// linked.md mentions "Go Notes" in its body and shares the go tag.
	cands, err = ix.SuggestLinks(v, "inbox/linked.md")
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]string{}
	for _, c := range cands {
		byPath[c.Path] = c.Reason
	}
	if r, ok := byPath["projects/go/notes.md"]; !ok || r != "its title appears in this note's text" {
		t.Errorf("title mention candidate = %q, all: %v", r, byPath)
	}
	if _, ok := byPath["projects/go/tips.md"]; !ok {
		t.Errorf("shared-tag candidate missing, all: %v", byPath)
	}
	// Already-linked pasta.md must not be suggested.
	if _, ok := byPath["projects/cooking/pasta.md"]; ok {
		t.Error("already linked note suggested")
	}
}

func TestFindUntaggedAndOrphans(t *testing.T) {
	_, ix := curatorFixture(t)

	untagged := ix.FindUntagged()
	if len(untagged) != 1 || untagged[0].Path != "inbox/untagged.md" {
		t.Errorf("untagged = %v", entryPaths(untagged))
	}

	orphans := ix.FindOrphans()
	want := map[string]bool{"inbox/untagged.md": true, "projects/go/notes.md": true, "projects/go/tips.md": true}
	if len(orphans) != len(want) {
		t.Errorf("orphans = %v", entryPaths(orphans))
	}
	for _, e := range orphans {
		if !want[e.Path] {
			t.Errorf("unexpected orphan %s", e.Path)
		}
	}
}

func TestFindInboxStale(t *testing.T) {
	v, ix := curatorFixture(t)

	// Age one inbox note artificially.
	old := time.Now().Add(-30 * 24 * time.Hour)
	if err := os.Chtimes(filepath.Join(v.Root(), "inbox/untagged.md"), old, old); err != nil {
		t.Fatal(err)
	}
	if err := ix.Update("inbox/untagged.md"); err != nil {
		t.Fatal(err)
	}

	stale := ix.FindInboxStale("inbox", 14*24*time.Hour)
	if len(stale) != 1 || stale[0].Path != "inbox/untagged.md" {
		t.Errorf("stale = %v", entryPaths(stale))
	}
	if fresh := ix.FindInboxStale("inbox", 60*24*time.Hour); len(fresh) != 0 {
		t.Errorf("nothing should be 60d stale, got %v", entryPaths(fresh))
	}
}
