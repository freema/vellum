package vault

import (
	"fmt"
	"strings"
	"testing"
)

// cachedPaths returns the cached notes by path.
func cachedPaths(s *ScanSearcher) map[string]*cachedNote {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]*cachedNote, len(s.cache))
	for p, c := range s.cache {
		out[p] = c
	}
	return out
}

func searchCount(t *testing.T, s *ScanSearcher, q string) int {
	t.Helper()
	res, err := s.Search(q, SearchOpts{})
	if err != nil {
		t.Fatal(err)
	}
	return len(res)
}

// A vault larger than the cache bound: the cache fills once and keeps what
// it holds, the notes beyond it come from disk, and every search still
// sees every note.
func TestSearchCacheFullKeepsWhatItHolds(t *testing.T) {
	v := newTestVault(t)
	for i := range 5 {
		mustWrite(t, v, fmt.Sprintf("n%d.md", i), fmt.Sprintf("# Note %d\n\nalpha text %d\n", i, i))
	}
	ix := NewIndex(v)
	if err := ix.Build(); err != nil {
		t.Fatal(err)
	}
	s := NewScanSearcher(v, ix)
	s.maxCached, s.maxBytes = 3, 0 // the note count alone bounds the cache

	if n := searchCount(t, s, "alpha"); n != 5 {
		t.Fatalf("first search: %d results, want 5", n)
	}
	first := cachedPaths(s)
	if len(first) != 3 {
		t.Fatalf("cache holds %d notes, want the bound 3", len(first))
	}
	for range 3 {
		if n := searchCount(t, s, "alpha"); n != 5 {
			t.Fatalf("repeated search: %d results, want 5", n)
		}
	}
	again := cachedPaths(s)
	if len(again) != 3 {
		t.Fatalf("cache holds %d notes after more searches, want 3", len(again))
	}
	for p, c := range first {
		if again[p] != c {
			t.Errorf("%s was evicted or re-read from disk although it did not change", p)
		}
	}
}

// A note the full cache holds is refreshed when it changes, without
// growing the cache.
func TestSearchCacheFullRefreshesChangedNote(t *testing.T) {
	v := newTestVault(t)
	for i := range 5 {
		mustWrite(t, v, fmt.Sprintf("n%d.md", i), fmt.Sprintf("# Note %d\n\nalpha text %d\n", i, i))
	}
	ix := NewIndex(v)
	if err := ix.Build(); err != nil {
		t.Fatal(err)
	}
	s := NewScanSearcher(v, ix)
	s.maxCached, s.maxBytes = 3, 0 // the note count alone bounds the cache
	searchCount(t, s, "alpha")

	var changed string
	for p := range cachedPaths(s) {
		changed = p
		break
	}
	mustWrite(t, v, changed, "# Changed\n\nalpha and a brand new word zebra\n")
	if err := ix.Update(changed); err != nil {
		t.Fatal(err)
	}
	res, err := s.Search("zebra", SearchOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Path != changed {
		t.Fatalf("search for the new word = %+v, want %s", res, changed)
	}
	if c := cachedPaths(s); len(c) != 3 || !strings.Contains(c[changed].all, "zebra") {
		t.Errorf("cache: %d notes, %s refreshed = %v; want 3 and true", len(c), changed, strings.Contains(c[changed].all, "zebra"))
	}
}

// A full cache drops the notes the index no longer has, and the freed place
// goes to a note that was served from disk.
func TestSearchCacheFullPrunesDeletedNotes(t *testing.T) {
	v := newTestVault(t)
	for i := range 5 {
		mustWrite(t, v, fmt.Sprintf("n%d.md", i), fmt.Sprintf("# Note %d\n\nalpha text %d\n", i, i))
	}
	ix := NewIndex(v)
	if err := ix.Build(); err != nil {
		t.Fatal(err)
	}
	s := NewScanSearcher(v, ix)
	s.maxCached, s.maxBytes = 3, 0 // the note count alone bounds the cache
	searchCount(t, s, "alpha")

	var gone string
	for p := range cachedPaths(s) {
		gone = p
		break
	}
	removeOutside(t, v, gone)
	ix.Remove(gone)

	if n := searchCount(t, s, "alpha"); n != 4 {
		t.Fatalf("search after the delete: %d results, want 4", n)
	}
	c := cachedPaths(s)
	if _, ok := c[gone]; ok {
		t.Errorf("deleted %s still cached", gone)
	}
	if len(c) != 3 {
		t.Errorf("cache holds %d notes, want the freed place reused (3)", len(c))
	}
}

// foldLines folds each line exactly as foldLine does, and its lines are the
// joined text cut at the newlines.
func TestFoldLinesMatchesFoldLine(t *testing.T) {
	raw := "# Příliš ŽLUŤOUČKÝ kůň\n\nplain ascii line\nMixed CASE Ascii\r\n\nÚkoly: ÚČET, Straße, İstanbul, ﬁ ligature\ninvalid \xff\xfe bytes ř\n\n"
	lines := strings.Split(raw, "\n")
	all, folded := foldLines(lines)
	if len(folded) != len(lines) {
		t.Fatalf("%d folded lines, want %d", len(folded), len(lines))
	}
	want := make([]string, len(lines))
	for i, l := range lines {
		want[i], _ = foldLine(l)
		if folded[i] != want[i] {
			t.Errorf("line %d: foldLines %q, foldLine %q", i, folded[i], want[i])
		}
	}
	if j := strings.Join(want, "\n"); all != j {
		t.Errorf("joined text %q, want %q", all, j)
	}
	if a, f := foldLines(nil); a != "" || len(f) != 0 {
		t.Errorf("no lines: %q %v", a, f)
	}
}

// checkCacheBytes fails when the running byte count drifts from the sum of
// the cached notes' cost.
func checkCacheBytes(t *testing.T, s *ScanSearcher) {
	t.Helper()
	s.mu.RLock()
	defer s.mu.RUnlock()
	var sum int64
	for _, c := range s.cache {
		sum += c.cost()
	}
	if sum != s.cacheBytes {
		t.Errorf("cacheBytes %d, the cached notes cost %d", s.cacheBytes, sum)
	}
}

// The byte budget bounds a cache of notes past the note count, and the
// count keeps a cache of notes past the budget: a note is taken while
// either bound has room.
func TestSearchCacheByteBudget(t *testing.T) {
	v := newTestVault(t)
	for i := range 6 {
		mustWrite(t, v, fmt.Sprintf("n%d.md", i), fmt.Sprintf("# Note %d\n\nalpha %s\n", i, strings.Repeat("word ", 50)))
	}
	ix := NewIndex(v)
	if err := ix.Build(); err != nil {
		t.Fatal(err)
	}

	// Count bound 1, budget for everything: every note is cached.
	s := NewScanSearcher(v, ix)
	s.maxCached = 1
	if n := searchCount(t, s, "alpha"); n != 6 {
		t.Fatalf("%d results, want 6", n)
	}
	if c := cachedPaths(s); len(c) != 6 {
		t.Errorf("roomy budget: %d notes cached, want 6", len(c))
	}
	checkCacheBytes(t, s)

	// No count bound, a budget for about two notes: the cache stops at the
	// budget and keeps serving every note.
	one := cachedPaths(s)["n0.md"].cost()
	s = NewScanSearcher(v, ix)
	s.maxCached, s.maxBytes = 0, 2*one+one/2
	for range 2 {
		if n := searchCount(t, s, "alpha"); n != 6 {
			t.Fatalf("%d results, want 6", n)
		}
	}
	if c := cachedPaths(s); len(c) != 2 {
		t.Errorf("tight budget: %d notes cached, want 2", len(c))
	}
	if s.cacheBytes > s.maxBytes {
		t.Errorf("cache holds %d bytes over the budget %d", s.cacheBytes, s.maxBytes)
	}
	checkCacheBytes(t, s)

	// A refresh and a prune keep the byte count exact.
	var p string
	for p = range cachedPaths(s) {
		break
	}
	mustWrite(t, v, p, "# Changed\n\nalpha "+strings.Repeat("longer text ", 80)+"\n")
	if err := ix.Update(p); err != nil {
		t.Fatal(err)
	}
	searchCount(t, s, "alpha")
	checkCacheBytes(t, s)
	removeOutside(t, v, p)
	ix.Remove(p)
	searchCount(t, s, "alpha")
	if _, ok := cachedPaths(s)[p]; ok {
		t.Errorf("deleted %s still cached", p)
	}
	checkCacheBytes(t, s)
}

// rawSpanOf maps every folded span back to the raw line exactly as rawSpan
// does with foldLine's offset map.
func TestRawSpanOfMatchesOffsetMap(t *testing.T) {
	for _, raw := range []string{
		"plain ascii",
		"Mixed CASE",
		"Příliš žluťoučký kůň úpěl ďábelské ódy",
		"ﬁ ligature, Straße, İstanbul, é combining",
		"invalid \xff\xfe bytes ř",
		"",
	} {
		folded, offsets := foldLine(raw)
		for start := 0; start <= len(folded)+1; start++ {
			for end := start; end <= len(folded)+1; end++ {
				ws, we := rawSpan(raw, offsets, start, end)
				gs, ge := rawSpanOf(raw, start, end)
				if gs != ws || ge != we {
					t.Fatalf("%q [%d,%d): rawSpanOf (%d,%d), rawSpan (%d,%d)", raw, start, end, gs, ge, ws, we)
				}
			}
		}
	}
}
