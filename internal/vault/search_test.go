package vault

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func newSearchFixture(t *testing.T) *ScanSearcher {
	t.Helper()
	v := newTestVault(t)
	mustWrite(t, v, "inbox/apples.md", "---\ntags: [fruit]\n---\n# Apples\n\nline one\nApples are GREAT fruit.\nline three\nline four\n")
	mustWrite(t, v, "inbox/pears.md", "---\ntags: [fruit]\n---\n# Pears\n\nPears are fine.\n")
	mustWrite(t, v, "projects/go/notes.md", "---\ntags: [dev]\n---\n# Go Notes\n\nGoroutines are great.\n")
	mustWrite(t, v, "archive/old.md", "# Old\n\nnothing to see\n")
	ix := NewIndex(v)
	if err := ix.Build(); err != nil {
		t.Fatal(err)
	}
	return NewScanSearcher(v, ix)
}

func TestSearchCaseInsensitiveSubstring(t *testing.T) {
	s := newSearchFixture(t)
	results, err := s.Search("great", SearchOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %v, want 2 files", results)
	}
	if results[0].Path != "inbox/apples.md" || results[1].Path != "projects/go/notes.md" {
		t.Errorf("paths = %s, %s", results[0].Path, results[1].Path)
	}
	if results[0].Snippets[0].Match != "GREAT" {
		t.Errorf("match = %q, want original casing GREAT", results[0].Snippets[0].Match)
	}
}

func TestSearchSnippetContext(t *testing.T) {
	s := newSearchFixture(t)
	results, err := s.Search("GREAT fruit", SearchOpts{ContextLines: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %+v", results)
	}
	sn := results[0].Snippets[0]
	if sn.Line != 7 {
		t.Errorf("line = %d, want 7 (raw file line incl. frontmatter)", sn.Line)
	}
	want := "line one\nApples are GREAT fruit.\nline three"
	if sn.Context != want {
		t.Errorf("context = %q, want %q", sn.Context, want)
	}
}

func TestSearchTagAndFulltextCombination(t *testing.T) {
	s := newSearchFixture(t)
	// "great" appears in a fruit note and a dev note; the tag narrows it.
	results, err := s.Search("great", SearchOpts{Tags: []string{"fruit"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Path != "inbox/apples.md" {
		t.Errorf("tag+fulltext = %+v, want only inbox/apples.md", results)
	}
	// Multiple tags AND together.
	if r, _ := s.Search("great", SearchOpts{Tags: []string{"fruit", "dev"}}); len(r) != 0 {
		t.Errorf("AND of disjoint tags should be empty, got %+v", r)
	}
}

func TestSearchTagOnlyNoQuery(t *testing.T) {
	s := newSearchFixture(t)
	results, err := s.Search("", SearchOpts{Tags: []string{"fruit"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("tag filter = %+v, want 2", results)
	}
	if results[0].Snippets != nil {
		t.Error("tag-only search should have no snippets")
	}
}

func TestSearchDirFilter(t *testing.T) {
	s := newSearchFixture(t)
	results, err := s.Search("great", SearchOpts{Dir: "projects"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Path != "projects/go/notes.md" {
		t.Errorf("dir filter = %+v", results)
	}
}

func TestSearchRegex(t *testing.T) {
	s := newSearchFixture(t)
	results, err := s.Search(`gor\w+ines`, SearchOpts{Regex: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Snippets[0].Match != "Goroutines" {
		t.Errorf("regex search = %+v", results)
	}
	if _, err := s.Search("([", SearchOpts{Regex: true}); err == nil {
		t.Error("invalid regex must error")
	}
}

func TestSearchMaxResultsAndSnippets(t *testing.T) {
	v := newTestVault(t)
	for i := 0; i < 10; i++ {
		mustWrite(t, v, fmt.Sprintf("n%02d.md", i), "hit\nhit\nhit\nhit\nhit\n")
	}
	ix := NewIndex(v)
	if err := ix.Build(); err != nil {
		t.Fatal(err)
	}
	s := NewScanSearcher(v, ix)

	results, err := s.Search("hit", SearchOpts{MaxResults: 3, MaxSnippets: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 {
		t.Errorf("MaxResults not applied: %d", len(results))
	}
	if len(results[0].Snippets) != 2 {
		t.Errorf("MaxSnippets not applied: %d", len(results[0].Snippets))
	}
	// Deterministic order.
	if results[0].Path != "n00.md" || results[2].Path != "n02.md" {
		t.Errorf("order = %s..%s", results[0].Path, results[2].Path)
	}
}

func TestSearchSpeed1000Files(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping bulk search in -short")
	}
	v := newTestVault(t)
	for i := 0; i < 1200; i++ {
		body := strings.Repeat("filler line with words\n", 30)
		if i%100 == 0 {
			body += "the needle sentence\n"
		}
		mustWrite(t, v, fmt.Sprintf("bulk/d%d/n%d.md", i%20, i), "---\ntags: [t"+fmt.Sprint(i%5)+"]\n---\n"+body)
	}
	ix := NewIndex(v)
	if err := ix.Build(); err != nil {
		t.Fatal(err)
	}
	s := NewScanSearcher(v, ix)

	start := time.Now()
	results, err := s.Search("needle", SearchOpts{})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("scan of 1200 notes in %s, %d hits", elapsed, len(results))
	if len(results) != 12 {
		t.Errorf("hits = %d, want 12", len(results))
	}
	if elapsed > time.Second {
		t.Errorf("search took %s, want well under 1s (~100ms target)", elapsed)
	}
}
