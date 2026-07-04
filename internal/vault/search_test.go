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

func TestSearchRankingTitleFirst(t *testing.T) {
	v := newTestVault(t)
	// "roadmap" in the body only vs. in the title — the title match must rank first.
	mustWrite(t, v, "inbox/meeting.md", "# Weekly sync\n\nWe discussed the roadmap briefly.\n")
	mustWrite(t, v, "projects/roadmap.md", "# Roadmap\n\nThe plan for the next quarter.\n")
	ix := NewIndex(v)
	if err := ix.Build(); err != nil {
		t.Fatal(err)
	}
	s := NewScanSearcher(v, ix)

	results, err := s.Search("roadmap", SearchOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %+v, want 2", results)
	}
	if results[0].Path != "projects/roadmap.md" {
		t.Errorf("first = %s, want the title match projects/roadmap.md", results[0].Path)
	}
}

func TestSearchMultiTermAND(t *testing.T) {
	s := newSearchFixture(t)
	// "apples great": both terms in apples.md; go/notes.md has only "great".
	results, err := s.Search("apples great", SearchOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Path != "inbox/apples.md" {
		t.Errorf("AND search = %+v, want only inbox/apples.md", results)
	}
	// A term found nowhere kills the note.
	if r, _ := s.Search("apples nonexistentterm", SearchOpts{}); len(r) != 0 {
		t.Errorf("impossible AND should be empty, got %+v", r)
	}
}

func TestSearchCacheInvalidatedByWrite(t *testing.T) {
	v := newTestVault(t)
	mustWrite(t, v, "inbox/note.md", "# Note\n\nfirst version\n")
	ix := NewIndex(v)
	if err := ix.Build(); err != nil {
		t.Fatal(err)
	}
	s := NewScanSearcher(v, ix)

	if r, _ := s.Search("first", SearchOpts{}); len(r) != 1 {
		t.Fatalf("warm-up search = %+v", r)
	}
	// Rewrite the note; the index update changes modTime/size, which must
	// invalidate the searcher's content cache.
	if err := v.Write("inbox/note.md", "# Note\n\nsecond version entirely\n", WriteOptions{Overwrite: true}); err != nil {
		t.Fatal(err)
	}
	if err := ix.Update("inbox/note.md"); err != nil {
		t.Fatal(err)
	}
	if r, _ := s.Search("second version", SearchOpts{}); len(r) != 1 {
		t.Errorf("stale cache: new content not found, got %+v", r)
	}
	if r, _ := s.Search("first", SearchOpts{}); len(r) != 0 {
		t.Errorf("stale cache: old content still found, got %+v", r)
	}
}

func TestSearchDiacriticsInsensitive(t *testing.T) {
	v := newTestVault(t)
	mustWrite(t, v, "inbox/ukoly.md", "# Úkoly na týden\n\nDodělat přílohy a poznámky.\n")
	mustWrite(t, v, "inbox/plain.md", "# Plain\n\nnothing here\n")
	ix := NewIndex(v)
	if err := ix.Build(); err != nil {
		t.Fatal(err)
	}
	s := NewScanSearcher(v, ix)

	// ASCII query finds the accented note…
	results, err := s.Search("ukoly", SearchOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Path != "inbox/ukoly.md" {
		t.Fatalf("ascii→accented = %+v", results)
	}
	// …and the snippet keeps the original diacritics.
	if results[0].Snippets[0].Match != "Úkoly" {
		t.Errorf("match = %q, want Úkoly with original diacritics", results[0].Snippets[0].Match)
	}

	// The accented query works too (both sides are folded).
	if r, _ := s.Search("přílohy", SearchOpts{}); len(r) != 1 {
		t.Errorf("accented query = %+v, want 1", r)
	}
	if r, _ := s.Search("poznamky", SearchOpts{}); len(r) != 1 {
		t.Errorf("poznamky→poznámky = %+v, want 1", r)
	}
}

func TestSearchWordBoundaryBeatsInfix(t *testing.T) {
	v := newTestVault(t)
	// "note" at a word start vs. buried inside "keynotes".
	mustWrite(t, v, "a/infix.md", "# Keynotes summary\n\nkeynotes keynotes\n")
	mustWrite(t, v, "b/boundary.md", "# Note taking\n\nnote here\n")
	ix := NewIndex(v)
	if err := ix.Build(); err != nil {
		t.Fatal(err)
	}
	s := NewScanSearcher(v, ix)

	results, err := s.Search("note", SearchOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || results[0].Path != "b/boundary.md" {
		t.Errorf("word-boundary ranking = %+v, want b/boundary.md first", results)
	}
}

func TestSearchTypoTolerance(t *testing.T) {
	v := newTestVault(t)
	mustWrite(t, v, "inbox/preklepy.md", "# Překlepy v textu\n\nOpravit všechny překlepy do pátku.\n")
	mustWrite(t, v, "inbox/other.md", "# Other\n\nnothing relevant\n")
	ix := NewIndex(v)
	if err := ix.Build(); err != nil {
		t.Fatal(err)
	}
	s := NewScanSearcher(v, ix)

	// One typo ("překlApy") — still found, diacritics-insensitively too.
	results, err := s.Search("preklapy", SearchOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Path != "inbox/preklepy.md" {
		t.Fatalf("typo search = %+v, want inbox/preklepy.md", results)
	}
	// The snippet highlights the real word from the text.
	if len(results[0].Snippets) == 0 || !strings.Contains(strings.ToLower(fold(results[0].Snippets[0].Match)), "preklepy") {
		t.Errorf("typo snippet = %+v, want a highlighted 'překlepy'", results[0].Snippets)
	}

	// Short terms stay strict: "cat" must not fuzzy-match "car".
	mustWrite(t, v, "inbox/car.md", "# Car\n\ncar stuff\n")
	if err := ix.Update("inbox/car.md"); err != nil {
		t.Fatal(err)
	}
	if r, _ := s.Search("cat", SearchOpts{}); len(r) != 0 {
		t.Errorf("short-term fuzzy leak: %+v", r)
	}
}

func TestSearchExactBeatsFuzzy(t *testing.T) {
	v := newTestVault(t)
	mustWrite(t, v, "a/exact.md", "# Notes\n\nplanning the planning session\n")
	mustWrite(t, v, "b/fuzzy.md", "# Misc\n\nplanting a tree\n")
	ix := NewIndex(v)
	if err := ix.Build(); err != nil {
		t.Fatal(err)
	}
	s := NewScanSearcher(v, ix)

	results, err := s.Search("planning", SearchOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) < 1 || results[0].Path != "a/exact.md" {
		t.Fatalf("exact-vs-fuzzy = %+v, want a/exact.md first", results)
	}
	for _, r := range results[1:] {
		if r.Path == "b/fuzzy.md" {
			return // fuzzy result present but ranked below — perfect
		}
	}
	// fuzzy result may also be absent only if distance > budget; "planning"
	// vs "planting" is distance 2 within budget 2, so it must be there.
	t.Errorf("fuzzy result missing: %+v", results)
}
