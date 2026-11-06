package vault

import (
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// BM25F ranking (NSO-577).

// searcherOver builds a searcher over notes written into a fresh vault.
func searcherOver(t *testing.T, notes map[string]string) *ScanSearcher {
	t.Helper()
	v := newTestVault(t)
	for p, c := range notes {
		mustWrite(t, v, p, c)
	}
	ix := NewIndex(v)
	if err := ix.Build(); err != nil {
		t.Fatal(err)
	}
	return NewScanSearcher(v, ix)
}

func searchPaths(t *testing.T, s Searcher, query string, opts SearchOpts) ([]string, []Result) {
	t.Helper()
	res, err := s.Search(query, opts)
	if err != nil {
		t.Fatal(err)
	}
	paths := make([]string, len(res))
	for i, r := range res {
		paths[i] = r.Path
	}
	return paths, res
}

func TestBM25FFieldData(t *testing.T) {
	s := searcherOver(t, map[string]string{"a.md": "---\ntitle: Garden Plan\ntags: [home/garden, spring]\nstatus: done\n---\n" +
		"# Garden Plan\n\nPlant the tomatoes early.\n\n```\n# not a heading\n```\n\n## Watering\nWater the tomatoes daily, tomatoes!\n"})
	e := s.index.snapshot()[0]
	c, err := s.content(e)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(c.all[c.bodyOff:], "# garden plan\n") {
		t.Errorf("body starts with %q, want the first line after the frontmatter", c.all[c.bodyOff:min(len(c.all), c.bodyOff+20)])
	}
	if c.headings != "garden plan\nwatering" {
		t.Errorf("headings = %q, want the two real headings, not the one in fenced code", c.headings)
	}
	want := [numFields]int32{
		fieldTitle:    2,  // garden plan
		fieldPath:     2,  // a md
		fieldHeadings: 3,  // garden plan watering
		fieldTags:     3,  // home garden spring
		fieldBody:     12, // 9 words of text, 3 in the fenced code
	}
	if c.fieldLen != want {
		t.Errorf("field lengths = %v, want %v", c.fieldLen, want)
	}
	for _, w := range c.words {
		switch w.s {
		case "tomatoes":
			if w.body != 3 {
				t.Errorf("tomatoes body count = %d, want 3", w.body)
			}
		case "status", "watering":
			if w.body != 0 {
				t.Errorf("%s body count = %d, want 0 (frontmatter, heading)", w.s, w.body)
			}
		}
	}

	tf := make([]float64, numFields)
	c.termFields("tomatoes", true, tf)
	if tf[fieldBody] != 3 || tf[fieldTitle] != 0 {
		t.Errorf("tomatoes = %v", tf)
	}
	c.termFields("garden", true, tf)
	if tf[fieldTitle] != 1 || tf[fieldHeadings] != 1 || tf[fieldTags] != 1 || tf[fieldBody] != 0 {
		t.Errorf("garden = %v, want title, heading and tag once, body none", tf)
	}
	// A term only in another frontmatter key still counts, as body text.
	c.termFields("done", true, tf)
	if tf[fieldBody] != 1 {
		t.Errorf("frontmatter-only term = %v, want one body hit", tf)
	}
}

func TestTermCountWordStart(t *testing.T) {
	cases := []struct {
		s, term string
		want    float64
	}{
		{"fakturu nefakturovano faktur", "faktur", 2 + midWordWeight},
		{"", "a", 0},
		{"aaaa", "aa", 1 + midWordWeight}, // non-overlapping
		{"k3s-cluster k3s", "k3s", 2},
	}
	for _, c := range cases {
		if got := termCount(c.s, c.term); got != c.want {
			t.Errorf("termCount(%q, %q) = %v, want %v", c.s, c.term, got, c.want)
		}
	}
}

// Counting over the unique words and scanning the text give the same body
// counts, on every note of the golden corpus.
func TestBodyTermCountWordsMatchScan(t *testing.T) {
	v, ix := openSearchCorpus(t)
	s := NewScanSearcher(v, ix)
	rng := rand.New(rand.NewPCG(1, 577))
	checked := 0
	for _, e := range ix.snapshot() {
		c, err := s.content(e)
		if err != nil {
			t.Fatal(err)
		}
		var terms []string
		for range 6 {
			w := c.words[rng.IntN(len(c.words))].s
			terms = append(terms, w)
			if r := []rune(w); len(r) > 3 {
				from := rng.IntN(len(r) - 2)
				terms = append(terms, string(r[from:from+2+rng.IntN(len(r)-from-1)]))
			}
		}
		for _, term := range terms {
			if !isWordTerm(term) || repeatsInsideAWord(c, term) {
				continue
			}
			heads := termCount(c.headings, term)
			saved := c.countByWords
			c.countByWords = true
			byWords := c.bodyTermCount(term, true, heads)
			c.countByWords = false
			byScan := c.bodyTermCount(term, true, heads)
			c.countByWords = saved
			if byWords != byScan {
				t.Errorf("%s %q: %v over words, %v by scanning", e.Path, term, byWords, byScan)
			}
			checked++
		}
	}
	if checked < 1000 {
		t.Errorf("only %d terms checked", checked)
	}
}

// repeatsInsideAWord reports whether term occurs twice in one word of the
// note, the one case where the two counts differ (by design).
func repeatsInsideAWord(c *cachedNote, term string) bool {
	for _, w := range c.words {
		if strings.Count(w.s, term) > 1 {
			return true
		}
	}
	return false
}

// A rare term counts more than a common one: B repeats the rare word, A
// the common one, and B wins.
func TestBM25FRareTermWins(t *testing.T) {
	notes := map[string]string{
		"a.md": "# A\n\nalpha alpha alpha beta\n",
		"b.md": "# B\n\nalpha beta beta beta\n",
	}
	for i := range 10 {
		notes[fmt.Sprintf("filler/f%d.md", i)] = "# Filler\n\nalpha and other words\n"
	}
	paths, res := searchPaths(t, searcherOver(t, notes), "alpha beta", SearchOpts{})
	if !slices.Equal(paths, []string{"b.md", "a.md"}) || res[0].Score <= res[1].Score {
		t.Errorf("ranking = %v %v, want b.md (the rare word three times) first", paths, res)
	}
}

// A short note about the topic beats a long one that mentions it in
// passing, though the long one has more hits.
func TestBM25FShortNoteBeatsLong(t *testing.T) {
	long := "# Diary\n\n" + strings.Repeat("Another day with the usual chores, the weather and some reading.\n", 60) +
		"Mulching again.\n" + strings.Repeat("Evening walk, nothing much happened today at all.\n", 60) + "More mulching.\nMulching done.\n"
	s := searcherOver(t, map[string]string{
		"diary.md": long,
		"beds.md":  "# Beds\n\nMulching keeps the weeds down. Mulching twice a year.\n",
	})
	if paths, _ := searchPaths(t, s, "mulching", SearchOpts{}); !slices.Equal(paths, []string{"beds.md", "diary.md"}) {
		t.Errorf("ranking = %v, want the short note first", paths)
	}
}

func TestBM25FTitleBeatsBody(t *testing.T) {
	s := searcherOver(t, map[string]string{
		"x/body.md":  "# Weekly notes\n\nWe talked about the roadmap twice, roadmap.\n",
		"x/title.md": "# Roadmap\n\nPlans for next year.\n",
	})
	if paths, _ := searchPaths(t, s, "roadmap", SearchOpts{}); !slices.Equal(paths, []string{"x/title.md", "x/body.md"}) {
		t.Errorf("ranking = %v, want the title match first", paths)
	}
}

// A typo match counts less than an exact one in the same position.
// ("wireguard" has no Czech case ending, so only the typo rescue applies.)
func TestBM25FTypoBelowExact(t *testing.T) {
	s := searcherOver(t, map[string]string{
		"exact.md": "# One\n\nThe wireguard tunnel.\n",
		"typo.md":  "# Two\n\nThe wiregard tunnel.\n",
	})
	paths, res := searchPaths(t, s, "wireguard", SearchOpts{})
	if !slices.Equal(paths, []string{"exact.md", "typo.md"}) {
		t.Fatalf("ranking = %v", paths)
	}
	if got, want := res[1].Score, res[0].Score*typoWeight; got < want-0.002 || got > want+0.002 {
		t.Errorf("typo score %v, want about %v (exact %v × %v)", got, want, res[0].Score, typoWeight)
	}
	if res[1].Snippets[0].Match != "wiregard" {
		t.Errorf("typo snippet = %+v, want the note's word highlighted", res[1].Snippets)
	}
}

// Equal scores tie-break by modification time (newest first), then path.
func TestBM25FTieBreak(t *testing.T) {
	v := newTestVault(t)
	for _, p := range []string{"a/x.md", "b/x.md", "c/x.md"} {
		mustWrite(t, v, p, "# Note\n\nsame words here\n")
	}
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for p, mt := range map[string]time.Time{"a/x.md": old, "b/x.md": old, "c/x.md": old.Add(time.Hour)} {
		if err := os.Chtimes(filepath.Join(v.Root(), p), mt, mt); err != nil {
			t.Fatal(err)
		}
	}
	ix := NewIndex(v)
	if err := ix.Build(); err != nil {
		t.Fatal(err)
	}
	paths, res := searchPaths(t, NewScanSearcher(v, ix), "words", SearchOpts{})
	if !slices.Equal(paths, []string{"c/x.md", "a/x.md", "b/x.md"}) {
		t.Errorf("ranking = %v, want newest first, then by path", paths)
	}
	if res[0].Score != res[1].Score || res[1].Score != res[2].Score {
		t.Errorf("scores = %v %v %v, want equal", res[0].Score, res[1].Score, res[2].Score)
	}
}

// Document frequencies and mean field lengths come from the candidates
// after the dir and tag filters: a filtered search scores exactly like the
// same search over a vault holding only those notes.
func TestBM25FStatsOverCandidates(t *testing.T) {
	inDir := map[string]string{
		"d/one.md":   "# One\n\nalpha beta gamma\n",
		"d/two.md":   "# Two\n\nalpha alpha beta\n",
		"d/three.md": "# Three\n\nbeta gamma and a few more words\n",
	}
	all := map[string]string{}
	for p, c := range inDir {
		all[p] = c
	}
	for i := range 8 {
		all[fmt.Sprintf("other/n%d.md", i)] = "# Other\n\nbeta beta beta, a much longer note about other things entirely\n"
	}
	_, filtered := searchPaths(t, searcherOver(t, all), "alpha beta", SearchOpts{Dir: "d"})
	_, alone := searchPaths(t, searcherOver(t, inDir), "alpha beta", SearchOpts{})
	if len(filtered) != 2 || len(alone) != 2 {
		t.Fatalf("filtered %v, alone %v", filtered, alone)
	}
	for i := range filtered {
		if filtered[i].Path != alone[i].Path || filtered[i].Score != alone[i].Score {
			t.Errorf("hit %d: filtered %s %v, alone %s %v", i, filtered[i].Path, filtered[i].Score, alone[i].Path, alone[i].Score)
		}
	}
}

func TestBM25FMinScore(t *testing.T) {
	s := searcherOver(t, map[string]string{
		"strong.md": "# Compost\n\nCompost basics.\n",
		"weak.md":   "# Diary\n\n" + strings.Repeat("Nothing to report today, a quiet day.\n", 30) + "Some compost.\n",
		"mid.md":    "# Beds\n\nA layer of compost.\n",
	})
	all, err := s.SearchPage("compost", SearchOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if all.Total != 3 || all.Results[2].Path != "weak.md" {
		t.Fatalf("all = %+v", all)
	}
	cut := all.Results[2].Score + 0.001
	p, err := s.SearchPage("compost", SearchOpts{MinScore: cut, MaxResults: 1, Offset: 1})
	if err != nil {
		t.Fatal(err)
	}
	if p.Total != 2 || len(p.Results) != 1 || p.Results[0].Path != all.Results[1].Path {
		t.Errorf("min_score %v, offset 1, max 1 = %+v, want the second of two", cut, p)
	}
	// A score exactly at the threshold passes.
	if p, _ := s.SearchPage("compost", SearchOpts{MinScore: all.Results[2].Score}); p.Total != 3 {
		t.Errorf("min_score at the weakest score keeps %d, want 3", p.Total)
	}
}

// Regex mode and an empty query rank nothing: no score, MinScore ignored.
func TestBM25FUnrankedHaveNoScore(t *testing.T) {
	s := newSearchFixture(t)
	for _, c := range []struct {
		query string
		opts  SearchOpts
	}{
		{"gr.at", SearchOpts{Regex: true, MinScore: 0.9}},
		{"", SearchOpts{Tags: []string{"fruit"}, MinScore: 0.9}},
	} {
		_, res := searchPaths(t, s, c.query, c.opts)
		if len(res) == 0 {
			t.Errorf("%q: MinScore emptied an unranked search", c.query)
		}
		for _, r := range res {
			if r.Score != 0 {
				t.Errorf("%q: %s has score %v", c.query, r.Path, r.Score)
			}
		}
	}
}

func TestSetRanker(t *testing.T) {
	s := newSearchFixture(t)
	if err := s.SetRanker("bm42"); err == nil {
		t.Error("unknown ranker accepted")
	}
	if _, res := searchPaths(t, s, "great", SearchOpts{}); res[0].Score == 0 {
		t.Error("an unknown ranker name changed the default")
	}
	if err := s.SetRanker(RankerLegacy); err != nil {
		t.Fatal(err)
	}
	paths, res := searchPaths(t, s, "great", SearchOpts{MinScore: 0.99})
	if len(res) != 2 || res[0].Score != 0 {
		t.Errorf("legacy = %v %+v, want both hits without a score", paths, res)
	}
	if err := s.SetRanker(""); err != nil || s.ranker != RankerBM25F {
		t.Errorf("empty name = %v, ranker %q; want the default", err, s.ranker)
	}
}

// randomQueries mixes corpus words, word fragments, typos, accents and
// case into 1 to 4 term queries, some filtered by dir: a seeded stand-in
// for whatever agents type.
func randomQueries(t *testing.T, ix *Index, s *ScanSearcher, n int) []goldenQuery {
	t.Helper()
	rng := rand.New(rand.NewPCG(5, 77))
	var words []string
	entries := ix.snapshot()
	for _, e := range entries {
		c, err := s.content(e)
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range c.words {
			words = append(words, w.s)
		}
	}
	mutate := func(w string) string {
		r := []rune(w)
		switch rng.IntN(6) {
		case 0: // fragment
			if len(r) > 3 {
				from := rng.IntN(len(r) - 2)
				return string(r[from:])
			}
		case 1: // typo: drop a letter
			if len(r) > 4 {
				i := rng.IntN(len(r))
				return string(append(r[:i:i], r[i+1:]...))
			}
		case 2:
			return strings.ToUpper(w)
		case 3:
			return strings.NewReplacer("a", "á", "e", "ě", "u", "ů").Replace(w)
		case 4:
			return fmt.Sprintf("x%dq", rng.IntN(1000)) // matches nothing
		}
		return w
	}
	var out []goldenQuery
	for i := range n {
		terms := make([]string, 1+rng.IntN(4))
		for j := range terms {
			terms[j] = mutate(words[rng.IntN(len(words))])
		}
		q := goldenQuery{ID: fmt.Sprintf("random-%d", i), Query: strings.Join(terms, " ")}
		if rng.IntN(5) == 0 {
			q.Dir = strings.SplitN(entries[rng.IntN(len(entries))].Path, "/", 2)[0]
		}
		out = append(out, q)
	}
	return out
}

// Acceptance: score is always within 0 to 1, on random queries.
func TestBM25FScoreInRange(t *testing.T) {
	v, ix := openSearchCorpus(t)
	s := NewScanSearcher(v, ix)
	withResults := 0
	for _, q := range randomQueries(t, ix, s, 600) {
		_, res := searchPaths(t, s, q.Query, SearchOpts{Dir: q.Dir, MaxResults: 1000})
		if len(res) > 0 {
			withResults++
		}
		for i, r := range res {
			if r.Score < 1.0/scoreDecimals || r.Score > 1 {
				t.Errorf("%q: %s score %v, want within [0.001, 1]", q.Query, r.Path, r.Score)
			}
			if i > 0 && r.Score > res[i-1].Score {
				t.Errorf("%q: scores not descending at %d: %v after %v", q.Query, i, r.Score, res[i-1].Score)
			}
		}
	}
	if withResults < 150 {
		t.Errorf("only %d of 600 random queries returned something", withResults)
	}
}

// Acceptance (NSO-577): a query that returns results under the legacy
// ranker returns the same notes under BM25F (in another order), on the
// golden set and on random queries. Since NSO-578 BM25F may return more:
// a note whose only match of a term is a Czech word form of it, or, when
// no note holds every term, the relaxed results. Nothing the legacy ranker
// returns may go missing.
func TestBM25FSameResultSetAsLegacy(t *testing.T) {
	v, ix := openSearchCorpus(t)
	bm := NewScanSearcher(v, ix)
	legacy := NewScanSearcher(v, ix)
	if err := legacy.SetRanker(RankerLegacy); err != nil {
		t.Fatal(err)
	}
	entries := map[string]*Entry{}
	for _, e := range ix.snapshot() {
		entries[e.Path] = e
	}
	// wordFormOnly reports whether some term matches the note only through
	// its word form: no substring, no typo.
	wordFormOnly := func(path, query string) bool {
		c, err := bm.content(entries[path])
		if err != nil {
			t.Fatal(err)
		}
		for _, term := range strings.Fields(fold(query)) {
			if c.exactMatch(term) {
				continue
			}
			if w, _ := fuzzyWord(c, term); w != "" {
				continue
			}
			if cut := cutCzechEnding(term); cut != "" {
				if w, _ := c.prefixMatch(cut); w > 0 {
					return true
				}
			}
		}
		return false
	}
	queries := append(loadGoldenSet(t).Queries, randomQueries(t, ix, bm, 600)...)
	wordForms, relaxed := 0, 0
	for _, q := range queries {
		opts := SearchOpts{Dir: q.Dir, Tags: q.Tags, MaxResults: 1000}
		a, _ := searchPaths(t, legacy, q.Query, opts)
		page, err := bm.SearchPage(q.Query, opts)
		if err != nil {
			t.Fatal(err)
		}
		b := make(map[string]bool, len(page.Results))
		for _, r := range page.Results {
			b[r.Path] = true
		}
		for _, p := range a {
			if !b[p] {
				t.Errorf("%s %q: legacy returns %s, bm25f does not", q.ID, q.Query, p)
			}
			delete(b, p)
		}
		switch {
		case len(b) == 0:
		case page.Relaxed:
			relaxed++
			if len(a) > 0 {
				t.Errorf("%s %q: relaxed although legacy matched every term in %v", q.ID, q.Query, a)
			}
		default:
			for p := range b {
				if !wordFormOnly(p, q.Query) {
					t.Errorf("%s %q: bm25f adds %s, which no term matches through a word form", q.ID, q.Query, p)
				}
				wordForms++
			}
		}
	}
	if wordForms == 0 || relaxed == 0 {
		t.Errorf("word form extras %d, relaxed queries %d: want both exercised", wordForms, relaxed)
	}
}

// lostRankOne explains every golden query whose exact answer the legacy
// ranker put first and BM25F does not (NSO-577 acceptance).
var lostRankOne = map[string]string{
	"single-passport": "both notes mention passport once in the body; legacy ranked them equal and the path broke the tie, BM25F puts the shorter itinerary (99 words) above the checklist (135 words)",
}

// Acceptance: no golden query loses its first-place answer without an
// explanation in lostRankOne.
func TestBM25FKeepsFirstPlaceAnswers(t *testing.T) {
	set := loadGoldenSet(t)
	bm := evaluateRanker(t, "scan", func(v *Vault, ix *Index) Searcher { return NewScanSearcher(v, ix) }, set)
	legacy := evaluateRanker(t, "legacy", buildLegacyRanker, set)
	for i, l := range legacy.queries {
		b := bm.queries[i]
		_, explained := lostRankOne[b.q.ID]
		switch {
		case l.rank == 1 && b.rank != 1 && !explained:
			t.Errorf("%s: legacy answered at rank 1, BM25F at %s: %v", b.q.ID, rankLabel(b), b.ranked)
		case explained && (l.rank != 1 || b.rank == 1):
			t.Errorf("%s: listed in lostRankOne but legacy rank %d, BM25F %s", b.q.ID, l.rank, rankLabel(b))
		}
	}
	if bm.mrr <= legacy.mrr || bm.ndcg10 <= legacy.ndcg10 {
		t.Errorf("BM25F MRR %.4f nDCG@10 %.4f, legacy %.4f %.4f: want both higher", bm.mrr, bm.ndcg10, legacy.mrr, legacy.ndcg10)
	}
}

// Acceptance: queries without an answer have a clearly lower best score
// than queries with one (the basis for min_score).
func TestBM25FNoAnswerScoresLower(t *testing.T) {
	rep := evaluateRanker(t, "scan", func(v *Vault, ix *Index) Searcher { return NewScanSearcher(v, ix) }, loadGoldenSet(t))
	sp := rep.scoreSpread()
	if len(sp.noAnswerTop) == 0 || len(sp.answerTop) == 0 {
		t.Fatal("golden set without answerable or no-answer queries")
	}
	median := sp.answerTop[len(sp.answerTop)/2]
	if worst := sp.noAnswerTop[len(sp.noAnswerTop)-1]; worst >= median/2 {
		t.Errorf("best no-answer score %.3f, median answer score %.3f: want under half", worst, median)
	}
}
