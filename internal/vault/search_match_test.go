package vault

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"
)

// Match quality (NSO-578): Czech word forms and the relaxed AND.

func TestCutCzechEnding(t *testing.T) {
	for _, c := range []struct{ term, want string }{
		{"faktury", "faktur"},
		{"zahrady", "zahrad"},
		{"uctenky", "uctenk"},
		{"smlouvy", "smlouv"},
		{"chorvatsku", "chorvatsk"},
		{"dlouhym", "dlouh"},  // -ym
		{"kynutim", "kynut"},  // -im
		{"noveho", ""},        // -eho would leave 3 runes: too short
		{"systemu", "syst"},   // -emu, an adjective ending: a noun loses more, still a prefix of system
		{"ucetniho", "ucetn"}, // -iho
		{"zviratech", "zvir"}, // -atech
		{"certificates", "certificat"},
		{"psa", ""},       // too short to cut
		{"auto", ""},      // a cut would leave 3 runes
		{"wireguard", ""}, // no ending
		{"k3s-cluster", ""},
		{"", ""},
	} {
		if got := cutCzechEnding(c.term); got != c.want {
			t.Errorf("cutCzechEnding(%q) = %q, want %q", c.term, got, c.want)
		}
	}
}

func TestPrefixWeight(t *testing.T) {
	near := func(a, b float64) bool { return math.Abs(a-b) < 1e-9 }
	if w := prefixWeight(6, 0); !near(w, prefixWeightMax) {
		t.Errorf("no excess = %v, want %v", w, prefixWeightMax)
	}
	if w := prefixWeight(6, 1); !near(w, 0.6*6/6.3) {
		t.Errorf("faktur in fakturu = %v, want %v", w, 0.6*6/6.3)
	}
	if prefixWeight(4, 5) >= prefixWeight(4, 1) || prefixWeight(8, 2) <= prefixWeight(4, 2) {
		t.Error("prefix weight must fall with the excess and rise with the length")
	}
}

// A Czech word form finds the note even where neither a substring nor a
// typo does, scores below the exact form, and its snippet highlights the
// note's whole word.
func TestSearchCzechWordForm(t *testing.T) {
	s := searcherOver(t, map[string]string{
		"exact.md":  "# Platby\n\nPoslat faktury do konce měsíce.\n",
		"form.md":   "# Dodavatelé\n\nKaždou fakturu archivovat.\n",
		"plural.md": "# Archiv\n\nStarší faktura za nájem, faktur je víc.\n",
		"other.md":  "# Nákup\n\nKoupit mléko.\n",
	})
	paths, res := searchPaths(t, s, "faktury", SearchOpts{})
	if len(paths) != 3 || paths[0] != "exact.md" || slices.Contains(paths, "other.md") {
		t.Fatalf("faktury = %v, want the exact form first and both word forms", paths)
	}
	for _, r := range res[1:] {
		if r.Score >= res[0].Score {
			t.Errorf("%s: word form score %v not below the exact %v", r.Path, r.Score, res[0].Score)
		}
	}
	for _, r := range res {
		if r.Path == "form.md" && (len(r.Snippets) == 0 || r.Snippets[0].Match != "fakturu") {
			t.Errorf("form.md snippets = %+v, want the whole word fakturu highlighted", r.Snippets)
		}
	}

	// Beyond the typo budget: "smlouvami" is three edits from "smlouvu".
	s = searcherOver(t, map[string]string{"najem.md": "# Nájem\n\nPodepsat nájemní smlouvu.\n"})
	if paths, _ := searchPaths(t, s, "smlouvami", SearchOpts{}); !slices.Equal(paths, []string{"najem.md"}) {
		t.Errorf("smlouvami = %v, want the note saying smlouvu (no typo match: three edits)", paths)
	}
	// Too short to cut: "psa" does not find "pes".
	s = searcherOver(t, map[string]string{"pes.md": "# Pes\n\nVeterinář, pes a psovi očkování.\n"})
	if paths, _ := searchPaths(t, s, "psa", SearchOpts{}); len(paths) != 0 {
		t.Errorf("psa = %v, want nothing (no cut below %d runes)", paths, minPrefixRunes)
	}
}

// The word form weight replaces the typo weight where it is higher: the same
// note scores prefixWeight against the exact form, not typoWeight.
func TestSearchWordFormWeight(t *testing.T) {
	s := searcherOver(t, map[string]string{
		"exact.md": "# A\n\nfaktury\n",
		"form.md":  "# B\n\nfakturu\n", // one edit too: the better rescue wins
	})
	_, res := searchPaths(t, s, "faktury", SearchOpts{})
	if len(res) != 2 || res[0].Path != "exact.md" {
		t.Fatalf("ranking = %+v", res)
	}
	want := res[0].Score * prefixWeight(6, 1)
	if got := res[1].Score; math.Abs(got-want) > 0.002 {
		t.Errorf("word form score %v, want about %v (exact %v × %v)", got, want, res[0].Score, prefixWeight(6, 1))
	}
	if prefixWeight(6, 1) <= typoWeight {
		t.Errorf("test premise: prefix weight %v should beat the typo weight %v", prefixWeight(6, 1), typoWeight)
	}
}

// relaxedVault holds a note that has three of four query words, notes that
// share only the common words, and filler, so the statistics look like a
// small vault.
func relaxedVault(t *testing.T) *ScanSearcher {
	t.Helper()
	notes := map[string]string{
		"homelab/certs.md": "# Certificates\n\nRotate the TLS certificates on the reverse proxy every quarter.\n",
		"homelab/proxy.md": "# Proxy\n\nThe reverse proxy runs in a container.\n",
	}
	for i := range 8 {
		notes[fmt.Sprintf("misc/n%d.md", i)] = "# Note\n\nThe weather on the weekend, a walk in the park.\n"
	}
	return searcherOver(t, notes)
}

func TestSearchRelaxedAND(t *testing.T) {
	s := relaxedVault(t)

	// Strict AND finds nothing ("xylofon" is nowhere): the note with the
	// other three words comes back, flagged, scored lower than a strict hit.
	p, err := s.SearchPage("rotate tls certificates xylofon", SearchOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !p.Relaxed || p.Total != 1 || p.Results[0].Path != "homelab/certs.md" {
		t.Fatalf("relaxed page = %+v, want certs.md flagged relaxed", p)
	}
	strict, err := s.SearchPage("rotate tls certificates", SearchOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if strict.Relaxed || strict.Total != 1 {
		t.Fatalf("strict page = %+v", strict)
	}
	if got := p.Results[0].Score; got <= 0 || got >= strict.Results[0].Score {
		t.Errorf("relaxed score %v, want above 0 and below the strict %v", got, strict.Results[0].Score)
	}
	if len(p.Results[0].Snippets) == 0 {
		t.Error("relaxed hit without snippets")
	}

	// A strict hit exists: no relaxing, even though other notes miss one word.
	if p, _ := s.SearchPage("reverse proxy", SearchOpts{}); p.Relaxed || p.Total != 2 {
		t.Errorf("reverse proxy = %+v, want the two strict hits, not relaxed", p)
	}

	// Two words missing: nothing.
	if p, _ := s.SearchPage("rotate certificates xylofon marimba", SearchOpts{}); p.Total != 0 || p.Relaxed {
		t.Errorf("two missing words = %+v, want nothing", p)
	}
	// One word: nothing to relax.
	if p, _ := s.SearchPage("xylofon", SearchOpts{}); p.Total != 0 || p.Relaxed {
		t.Errorf("single unknown word = %+v, want nothing", p)
	}
	// The held words must carry most of the query: "the" and "weekend" are
	// common, the unknown word is the rarest of all, so the filler notes
	// that hold only the common rest are not returned.
	if p, _ := s.SearchPage("weekend xylofon", SearchOpts{}); p.Total != 0 || p.Relaxed {
		t.Errorf("common word plus an unknown one = %+v, want nothing", p)
	}
	// min_score applies to relaxed hits too; a page it empties is not
	// flagged.
	if p, _ := s.SearchPage("rotate tls certificates xylofon", SearchOpts{MinScore: 0.99}); p.Total != 0 || p.Relaxed {
		t.Errorf("relaxed with min_score 0.99 = %+v, want nothing and no flag", p)
	}

	// Regex mode and the legacy ranker never relax.
	if p, _ := s.SearchPage("rotate.*xylofon", SearchOpts{Regex: true}); p.Total != 0 || p.Relaxed {
		t.Errorf("regex = %+v", p)
	}
	if err := s.SetRanker(RankerLegacy); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.SearchPage("rotate tls certificates xylofon", SearchOpts{}); p.Total != 0 || p.Relaxed {
		t.Errorf("legacy = %+v, want strict AND", p)
	}
}

// The relaxed rule as a unit: a match counts only when the terms it holds
// carry more than half the summed IDF.
func TestHoldsMostOfQuery(t *testing.T) {
	tfs := func(weights ...float64) []float64 {
		out := make([]float64, len(weights)*tfStride)
		for i, w := range weights {
			out[i*tfStride+numFields] = w
		}
		return out
	}
	idf := []float64{3, 1, 1}
	for _, c := range []struct {
		weights []float64
		want    bool
	}{
		{[]float64{1, 1, 0}, true},   // misses a common term: 4 of 5
		{[]float64{0, 1, 1}, false},  // misses the rare term: 2 of 5
		{[]float64{0.5, 0, 1}, true}, // a rescued term holds its IDF
		{[]float64{0, 0, 0}, false},  // nothing held
		{[]float64{1, 0.5, 1}, true}, // everything held
		{[]float64{0, 0.5, 0.5}, false},
	} {
		if got := holdsMostOfQuery(tfs(c.weights...), idf, 5); got != c.want {
			t.Errorf("weights %v: %v, want %v", c.weights, got, c.want)
		}
	}
}

// inflectionRecallBefore is the recall@5 of the golden set's inflection
// queries before NSO-578 (BM25F of NSO-577): 5 of 9 queries.
const inflectionRecallBefore = 0.5556

// Acceptance (NSO-578): the inflection queries of the golden set find more
// of their answers in the top 5 than before, the summary metrics do not
// drop below the NSO-577 values, and the no-answer queries still return
// nothing.
func TestMatchQualityGoldenSet(t *testing.T) {
	rep := evaluateRanker(t, "scan", func(v *Vault, ix *Index) Searcher { return NewScanSearcher(v, ix) }, loadGoldenSet(t))
	recall := -1.0
	for _, k := range rep.byKind() {
		if k.kind == "inflection" {
			recall = k.recall
		}
	}
	if recall <= inflectionRecallBefore {
		t.Errorf("inflection recall@5 = %.4f, want above %.4f", recall, inflectionRecallBefore)
	}
	// NSO-577 BM25F on the same set.
	if rep.mrr < 0.8333 || rep.ndcg10 < 0.8433 || rep.recall5 < 0.8452 {
		t.Errorf("summary MRR %.4f nDCG@10 %.4f recall@5 %.4f, below BM25F before NSO-578 (0.8333 / 0.8433 / 0.8452)",
			rep.mrr, rep.ndcg10, rep.recall5)
	}
	if rep.noAnswerEmpty != rep.noAnswer {
		t.Errorf("%d of %d no-answer queries return nothing, want all", rep.noAnswerEmpty, rep.noAnswer)
	}
	// Every exact answer of an inflection query that is found scores above
	// the min_score advice of 0.3 (the typo rescue left two below it).
	for _, e := range rep.queries {
		if !slices.Contains(e.q.Kinds, "inflection") {
			continue
		}
		for i, p := range e.ranked {
			if e.q.Relevant[p] == gradeExact && e.score(i) < 0.3 {
				t.Errorf("%s: exact answer %s scores %.3f, below 0.3", e.q.ID, p, e.score(i))
			}
		}
	}
}

// Relaxed results keep the score within 0 to 1 too, on random queries
// (many of which hold a word no note has).
func TestRelaxedScoreInRange(t *testing.T) {
	v, ix := openSearchCorpus(t)
	s := NewScanSearcher(v, ix)
	relaxed := 0
	for _, q := range randomQueries(t, ix, s, 600) {
		if len(strings.Fields(q.Query)) < 2 {
			continue
		}
		p, err := s.SearchPage(q.Query, SearchOpts{Dir: q.Dir, MaxResults: 1000})
		if err != nil {
			t.Fatal(err)
		}
		if !p.Relaxed {
			continue
		}
		relaxed++
		for _, r := range p.Results {
			if r.Score < 1.0/scoreDecimals || r.Score > 1 {
				t.Errorf("%q: relaxed %s score %v, want within [0.001, 1]", q.Query, r.Path, r.Score)
			}
		}
	}
	if relaxed < 20 {
		t.Errorf("only %d random queries were relaxed", relaxed)
	}
}
