package vault

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
)

// Search quality harness (NSO-568). A fixed corpus (testdata/search/vault)
// and a golden query set with graded answers (testdata/search/golden.json)
// score any Searcher on MRR, nDCG@10 and recall@5, plus how many results
// come back for queries that have no answer. Stored floors
// (testdata/search/baseline.json) guard against regressions.
//
//	go test -run TestSearchQuality -v ./internal/vault
//
// prints the per-query table, the per-kind summary and latency/memory.
// docs/search-quality.md records the numbers and the rules for changing
// the golden set.

const (
	searchCorpusDir    = "../../testdata/search/vault"
	searchGoldenFile   = "../../testdata/search/golden.json"
	searchBaselineFile = "../../testdata/search/baseline.json"

	// Graded relevance from the golden set.
	gradeExact   = 2 // exactly the note the query is after
	gradeRelated = 1 // related, a fine result but not the answer

	ndcgCutoff   = 10
	recallCutoff = 5

	// latencyRounds is how often each golden query is timed after warm-up.
	latencyRounds = 5
)

// qualityRankers are the Searcher implementations the harness scores. A
// candidate ranker is registered here next to the current one so both run
// over the same corpus and golden set. "scan" is the default ScanSearcher
// (BM25F since NSO-577), "legacy" the hand-tuned points it replaced, kept
// for comparison until the legacy ranker is removed.
var qualityRankers = []struct {
	name  string
	build func(*Vault, *Index) Searcher
}{
	{"scan", func(v *Vault, ix *Index) Searcher { return NewScanSearcher(v, ix) }},
	{"legacy", buildLegacyRanker},
}

func buildLegacyRanker(v *Vault, ix *Index) Searcher {
	s := NewScanSearcher(v, ix)
	if err := s.SetRanker(RankerLegacy); err != nil {
		panic(err)
	}
	return s
}

// requiredQueryKinds is the coverage NSO-568 asks of the golden set.
var requiredQueryKinds = []string{
	"single-word", "multi-word", "sentence", "inflection", "no-diacritics",
	"typo", "common-rare", "filename-only", "heading-only", "tag-only",
	"long-vs-short", "no-answer",
}

type goldenSet struct {
	Version     int           `json:"version"`
	Description string        `json:"description"`
	Queries     []goldenQuery `json:"queries"`
}

type goldenQuery struct {
	ID    string   `json:"id"`
	Query string   `json:"query"`
	Lang  string   `json:"lang"`
	Kinds []string `json:"kinds"`
	Dir   string   `json:"dir,omitempty"`
	Tags  []string `json:"tags,omitempty"`
	// Relevant maps a vault path to its grade (gradeExact or gradeRelated).
	// Empty means the vault holds no answer.
	Relevant map[string]int `json:"relevant"`
	Note     string         `json:"note,omitempty"`
}

func (q goldenQuery) answerable() bool { return len(q.Relevant) > 0 }

// qualityFloor is the stored guard for one ranker: summary metrics must not
// drop below the floors, and no-answer queries must not return more results
// on average than the ceiling.
type qualityFloor struct {
	MRR                 float64 `json:"mrr"`
	NDCG10              float64 `json:"ndcg10"`
	Recall5             float64 `json:"recall5"`
	NoAnswerMeanResults float64 `json:"noAnswerMeanResults"`
}

type qualityBaseline struct {
	Comment string                  `json:"comment"`
	Rankers map[string]qualityFloor `json:"rankers"`
}

func loadGoldenSet(tb testing.TB) goldenSet {
	tb.Helper()
	raw, err := os.ReadFile(searchGoldenFile)
	if err != nil {
		tb.Fatal(err)
	}
	var set goldenSet
	if err := json.Unmarshal(raw, &set); err != nil {
		tb.Fatalf("parse %s: %v", searchGoldenFile, err)
	}
	return set
}

func loadQualityBaseline(tb testing.TB) qualityBaseline {
	tb.Helper()
	raw, err := os.ReadFile(searchBaselineFile)
	if err != nil {
		tb.Fatal(err)
	}
	var b qualityBaseline
	if err := json.Unmarshal(raw, &b); err != nil {
		tb.Fatalf("parse %s: %v", searchBaselineFile, err)
	}
	return b
}

// corpusModTime is stamped on every corpus file. A git checkout leaves
// arbitrary modification times, and rankers break ties by them, so the
// copy gets one fixed time and ties fall through to the path.
var corpusModTime = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

// openSearchCorpus copies the corpus into a temp dir with fixed modification
// times and builds its metadata index.
func openSearchCorpus(tb testing.TB) (*Vault, *Index) {
	tb.Helper()
	dir := tb.TempDir()
	if err := os.CopyFS(dir, os.DirFS(searchCorpusDir)); err != nil {
		tb.Fatalf("copy corpus: %v", err)
	}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		return os.Chtimes(path, corpusModTime, corpusModTime)
	})
	if err != nil {
		tb.Fatalf("stamp corpus: %v", err)
	}
	v, err := New(dir)
	if err != nil {
		tb.Fatal(err)
	}
	ix := NewIndex(v)
	if err := ix.Build(); err != nil {
		tb.Fatal(err)
	}
	return v, ix
}

// ---------------------------------------------------------------- metrics

// reciprocalRank is 1/rank of the first exact answer (grade 2), 0 when no
// exact answer is in the ranking.
func reciprocalRank(ranked []string, rel map[string]int) float64 {
	for i, p := range ranked {
		if rel[p] >= gradeExact {
			return 1 / float64(i+1)
		}
	}
	return 0
}

// ndcgAt is the normalised discounted cumulative gain of the top k with
// exponential gain (2^grade - 1) and a log2(rank+1) discount.
func ndcgAt(ranked []string, rel map[string]int, k int) float64 {
	gain := func(g int) float64 { return math.Exp2(float64(g)) - 1 }
	var dcg float64
	for i, p := range ranked[:min(k, len(ranked))] {
		dcg += gain(rel[p]) / math.Log2(float64(i+2))
	}
	grades := make([]int, 0, len(rel))
	for _, g := range rel {
		grades = append(grades, g)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(grades)))
	var idcg float64
	for i, g := range grades[:min(k, len(grades))] {
		idcg += gain(g) / math.Log2(float64(i+2))
	}
	if idcg == 0 {
		return 0
	}
	return dcg / idcg
}

// recallAt is the share of all graded notes (grade 1 or 2) in the top k.
func recallAt(ranked []string, rel map[string]int, k int) float64 {
	total := 0
	for _, g := range rel {
		if g >= gradeRelated {
			total++
		}
	}
	if total == 0 {
		return 0
	}
	found := 0
	for _, p := range ranked[:min(k, len(ranked))] {
		if rel[p] >= gradeRelated {
			found++
		}
	}
	return float64(found) / float64(total)
}

// exactRank is the 1-based rank of the first exact answer, 0 when missing.
func exactRank(ranked []string, rel map[string]int) int {
	for i, p := range ranked {
		if rel[p] >= gradeExact {
			return i + 1
		}
	}
	return 0
}

// percentile returns the nearest-rank percentile (0 < p <= 100) of sorted.
func latencyPercentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	i := int(math.Ceil(p/100*float64(len(sorted)))) - 1
	return sorted[min(max(i, 0), len(sorted)-1)]
}

// ---------------------------------------------------------------- eval

type queryEval struct {
	q       goldenQuery
	ranked  []string
	scores  []float64 // Result.Score per ranked note (0 for a ranker without scores)
	rank    int       // first exact answer, 0 = missing
	rr      float64
	ndcg    float64
	recall  float64
	latency time.Duration // median of latencyRounds
}

type kindSummary struct {
	kind                string
	queries             int
	mrr, ndcg10, recall float64
}

type qualityReport struct {
	ranker     string
	queries    []queryEval
	answerable int
	mrr        float64
	ndcg10     float64
	recall5    float64

	noAnswer            int
	noAnswerEmpty       int // no-answer queries that returned nothing
	noAnswerMeanResults float64

	p50, p95   time.Duration
	indexHeap  int64 // retained heap of the metadata index
	searchHeap int64 // retained heap of the warm searcher (its caches)
}

func retainedHeap() int64 {
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return int64(m.HeapAlloc)
}

// evaluateRanker runs the golden set through one ranker built over a fresh
// copy of the corpus.
func evaluateRanker(tb testing.TB, name string, build func(*Vault, *Index) Searcher, set goldenSet) qualityReport {
	tb.Helper()
	before := retainedHeap()
	v, ix := openSearchCorpus(tb)
	afterIndex := retainedHeap()
	s := build(v, ix)

	search := func(q goldenQuery) []Result {
		res, err := s.Search(q.Query, SearchOpts{Dir: q.Dir, Tags: q.Tags})
		if err != nil {
			tb.Fatalf("%s: search %q: %v", name, q.Query, err)
		}
		return res
	}

	rep := qualityReport{ranker: name}
	for _, q := range set.Queries {
		res := search(q) // first run also warms the searcher
		ranked := make([]string, len(res))
		scores := make([]float64, len(res))
		for i, r := range res {
			ranked[i] = r.Path
			scores[i] = r.Score
		}
		rep.queries = append(rep.queries, queryEval{
			q: q, ranked: ranked, scores: scores,
			rank:   exactRank(ranked, q.Relevant),
			rr:     reciprocalRank(ranked, q.Relevant),
			ndcg:   ndcgAt(ranked, q.Relevant, ndcgCutoff),
			recall: recallAt(ranked, q.Relevant, recallCutoff),
		})
	}
	rep.searchHeap = retainedHeap() - afterIndex
	rep.indexHeap = afterIndex - before

	var all []time.Duration
	for i := range rep.queries {
		lat := make([]time.Duration, latencyRounds)
		for r := range lat {
			t0 := time.Now()
			search(rep.queries[i].q)
			lat[r] = time.Since(t0)
		}
		all = append(all, lat...)
		slices.Sort(lat)
		rep.queries[i].latency = lat[len(lat)/2]
	}
	slices.Sort(all)
	rep.p50, rep.p95 = latencyPercentile(all, 50), latencyPercentile(all, 95)

	var noAnswerResults int
	for _, e := range rep.queries {
		if !e.q.answerable() {
			rep.noAnswer++
			noAnswerResults += len(e.ranked)
			if len(e.ranked) == 0 {
				rep.noAnswerEmpty++
			}
			continue
		}
		rep.answerable++
		rep.mrr += e.rr
		rep.ndcg10 += e.ndcg
		rep.recall5 += e.recall
	}
	if rep.answerable > 0 {
		n := float64(rep.answerable)
		rep.mrr, rep.ndcg10, rep.recall5 = rep.mrr/n, rep.ndcg10/n, rep.recall5/n
	}
	if rep.noAnswer > 0 {
		rep.noAnswerMeanResults = float64(noAnswerResults) / float64(rep.noAnswer)
	}
	return rep
}

// byKind summarises the answerable queries per kind (a query counts for
// every kind it lists).
func (r qualityReport) byKind() []kindSummary {
	m := map[string]*kindSummary{}
	for _, e := range r.queries {
		if !e.q.answerable() {
			continue
		}
		for _, k := range e.q.Kinds {
			ks := m[k]
			if ks == nil {
				ks = &kindSummary{kind: k}
				m[k] = ks
			}
			ks.queries++
			ks.mrr += e.rr
			ks.ndcg10 += e.ndcg
			ks.recall += e.recall
		}
	}
	out := make([]kindSummary, 0, len(m))
	for _, ks := range m {
		n := float64(ks.queries)
		out = append(out, kindSummary{ks.kind, ks.queries, ks.mrr / n, ks.ndcg10 / n, ks.recall / n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].kind < out[j].kind })
	return out
}

// score is the score of the i-th result, 0 when the ranker gave none.
func (e queryEval) score(i int) float64 {
	if i >= len(e.scores) {
		return 0
	}
	return e.scores[i]
}

// topScore is the score of the first result, 0 when there is none.
func (e queryEval) topScore() float64 { return e.score(0) }

// scoreSpread summarises the scores a ranker gives (NSO-577, the basis for
// min_score): the top score of answerable queries that return something
// and of no-answer queries (0 when they return nothing), the score of each
// exact answer, and the scores of returned notes the golden set does not
// judge relevant. Each sorted, for min / median / max.
type scoreSpread struct {
	answerTop, noAnswerTop, exact, unjudged []float64
	scored                                  bool // the ranker gives scores at all
}

func (r qualityReport) scoreSpread() scoreSpread {
	var sp scoreSpread
	for _, e := range r.queries {
		if e.topScore() > 0 {
			sp.scored = true
		}
		if !e.q.answerable() {
			sp.noAnswerTop = append(sp.noAnswerTop, e.topScore())
			continue
		}
		if len(e.ranked) > 0 {
			sp.answerTop = append(sp.answerTop, e.topScore())
		}
		for i, p := range e.ranked {
			switch g := e.q.Relevant[p]; {
			case g >= gradeExact:
				sp.exact = append(sp.exact, e.score(i))
			case g == 0:
				sp.unjudged = append(sp.unjudged, e.score(i))
			}
		}
	}
	for _, v := range [][]float64{sp.answerTop, sp.noAnswerTop, sp.exact, sp.unjudged} {
		slices.Sort(v)
	}
	return sp
}

// minMedMax renders sorted values as "min / median / max (n)".
func minMedMax(sorted []float64) string {
	if len(sorted) == 0 {
		return "none"
	}
	return fmt.Sprintf("%.3f / %.3f / %.3f (%d)", sorted[0], sorted[len(sorted)/2], sorted[len(sorted)-1], len(sorted))
}

func bytesMiB(b int64) float64 { return float64(b) / (1 << 20) }

func durMs(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

// String renders the report as Markdown tables, so the same text works in a
// terminal and in docs/search-quality.md.
func (r qualityReport) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n### Ranker %q\n\n", r.ranker)
	fmt.Fprintf(&b, "| Metric | Value |\n|---|---|\n")
	fmt.Fprintf(&b, "| MRR (first exact answer) | %.4f |\n", r.mrr)
	fmt.Fprintf(&b, "| nDCG@%d | %.4f |\n", ndcgCutoff, r.ndcg10)
	fmt.Fprintf(&b, "| recall@%d | %.4f |\n", recallCutoff, r.recall5)
	fmt.Fprintf(&b, "| answerable queries | %d |\n", r.answerable)
	fmt.Fprintf(&b, "| no-answer queries returning nothing | %d of %d |\n", r.noAnswerEmpty, r.noAnswer)
	fmt.Fprintf(&b, "| no-answer queries, mean results | %.2f |\n", r.noAnswerMeanResults)
	fmt.Fprintf(&b, "| latency p50 / p95 (warm, %d rounds) | %.3f ms / %.3f ms |\n", latencyRounds, durMs(r.p50), durMs(r.p95))
	fmt.Fprintf(&b, "| heap: index / warm searcher | %.2f MiB / %.2f MiB |\n", bytesMiB(r.indexHeap), bytesMiB(r.searchHeap))
	if sp := r.scoreSpread(); sp.scored {
		fmt.Fprintf(&b, "| score, top hit of answerable queries with results (min / median / max) | %s |\n", minMedMax(sp.answerTop))
		fmt.Fprintf(&b, "| score, top hit of no-answer queries (0 = no results) | %s |\n", minMedMax(sp.noAnswerTop))
		fmt.Fprintf(&b, "| score, exact answers | %s |\n", minMedMax(sp.exact))
		fmt.Fprintf(&b, "| score, results not judged relevant | %s |\n", minMedMax(sp.unjudged))
	}

	fmt.Fprintf(&b, "\n| Kind | Queries | MRR | nDCG@%d | recall@%d |\n|---|---|---|---|---|\n", ndcgCutoff, recallCutoff)
	for _, k := range r.byKind() {
		fmt.Fprintf(&b, "| %s | %d | %.3f | %.3f | %.3f |\n", k.kind, k.queries, k.mrr, k.ndcg10, k.recall)
	}

	fmt.Fprintf(&b, "\n| Query | Kinds | Rank | nDCG@%d | R@%d | Results | Top score | ms |\n|---|---|---|---|---|---|---|---|\n", ndcgCutoff, recallCutoff)
	for _, e := range r.queries {
		rank := "-"
		switch {
		case !e.q.answerable():
			rank = "n/a"
		case e.rank > 0:
			rank = fmt.Sprint(e.rank)
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %.3f | %.3f | %d | %.3f | %.3f |\n",
			e.q.ID, strings.Join(e.q.Kinds, ", "), rank, e.ndcg, e.recall, len(e.ranked), e.topScore(), durMs(e.latency))
	}

	fmt.Fprintf(&b, "\nNot first (exact answer missing or below rank 1), and no-answer queries with results:\n\n")
	for _, e := range r.queries {
		if (e.q.answerable() && e.rank == 1) || (!e.q.answerable() && len(e.ranked) == 0) {
			continue
		}
		top := e.ranked[:min(3, len(e.ranked))]
		fmt.Fprintf(&b, "- `%s` %q: rank %s, top %v\n", e.q.ID, e.q.Query, rankLabel(e), top)
	}
	return b.String()
}

func rankLabel(e queryEval) string {
	switch {
	case !e.q.answerable():
		return "n/a"
	case e.rank == 0:
		return "missing"
	default:
		return fmt.Sprint(e.rank)
	}
}

// ---------------------------------------------------------------- tests

// TestSearchQuality scores every registered ranker on the golden set and
// fails when a summary metric drops below its stored floor.
func TestSearchQuality(t *testing.T) {
	set := loadGoldenSet(t)
	baseline := loadQualityBaseline(t)
	for _, rk := range qualityRankers {
		t.Run(rk.name, func(t *testing.T) {
			rep := evaluateRanker(t, rk.name, rk.build, set)
			t.Log(rep.String())

			floor, ok := baseline.Rankers[rk.name]
			if !ok {
				t.Logf("no stored floor for ranker %q, metrics are informational", rk.name)
				return
			}
			for _, v := range floor.violations(rep) {
				t.Error(v)
			}
			if rep.mrr > floor.MRR+0.005 || rep.ndcg10 > floor.NDCG10+0.005 || rep.recall5 > floor.Recall5+0.005 {
				t.Logf("metrics beat the stored floors, consider raising them in %s: mrr %.4f, ndcg10 %.4f, recall5 %.4f",
					searchBaselineFile, rep.mrr, rep.ndcg10, rep.recall5)
			}
		})
	}
}

// violations lists every summary metric of rep that is worse than the floor.
func (f qualityFloor) violations(rep qualityReport) []string {
	const eps = 1e-9
	var out []string
	below := func(metric string, got, want float64) {
		if got < want-eps {
			out = append(out, fmt.Sprintf("%s = %.4f, below the stored floor %.4f", metric, got, want))
		}
	}
	below("MRR", rep.mrr, f.MRR)
	below("nDCG@10", rep.ndcg10, f.NDCG10)
	below("recall@5", rep.recall5, f.Recall5)
	if rep.noAnswerMeanResults > f.NoAnswerMeanResults+eps {
		out = append(out, fmt.Sprintf("no-answer mean results = %.2f, above the stored ceiling %.2f",
			rep.noAnswerMeanResults, f.NoAnswerMeanResults))
	}
	return out
}

// reversedSearcher wraps a ranker and reverses its ranking: a deliberately
// worse ranker for checking that the quality gate notices regressions.
type reversedSearcher struct{ Searcher }

func (r reversedSearcher) Search(query string, opts SearchOpts) ([]Result, error) {
	res, err := r.Searcher.Search(query, opts)
	slices.Reverse(res)
	return res, err
}

// noisySearcher returns every note for any query: the worst case for the
// no-answer ceiling.
type noisySearcher struct{ ix *Index }

func (n noisySearcher) Search(string, SearchOpts) ([]Result, error) {
	var out []Result
	for _, e := range n.ix.snapshot() {
		out = append(out, Result{Path: e.Path, Title: e.Title})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out[:min(len(out), 50)], nil
}

// TestSearchQualityGateCatchesRegressions runs worse rankers through the
// harness and requires the stored floors of the current ranker to flag them.
func TestSearchQualityGateCatchesRegressions(t *testing.T) {
	set := loadGoldenSet(t)
	floor, ok := loadQualityBaseline(t).Rankers["scan"]
	if !ok {
		t.Fatal("no stored floor for the scan ranker")
	}

	reversed := evaluateRanker(t, "reversed", func(v *Vault, ix *Index) Searcher {
		return reversedSearcher{NewScanSearcher(v, ix)}
	}, set)
	if v := floor.violations(reversed); len(v) == 0 {
		t.Errorf("reversed ranking passed the gate: mrr %.4f, ndcg10 %.4f", reversed.mrr, reversed.ndcg10)
	}

	noisy := evaluateRanker(t, "noisy", func(_ *Vault, ix *Index) Searcher { return noisySearcher{ix} }, set)
	v := floor.violations(noisy)
	if !slices.ContainsFunc(v, func(s string) bool { return strings.HasPrefix(s, "no-answer") }) {
		t.Errorf("a ranker answering every query passed the no-answer ceiling: %v", v)
	}
}

// TestSearchQualityDeterministic runs the golden set twice over two fresh
// corpus copies and requires identical rankings: the quality gate must not
// depend on scheduling, GOMAXPROCS or checkout times.
func TestSearchQualityDeterministic(t *testing.T) {
	set := loadGoldenSet(t)
	for _, rk := range qualityRankers {
		t.Run(rk.name, func(t *testing.T) {
			a := evaluateRanker(t, rk.name, rk.build, set)
			b := evaluateRanker(t, rk.name, rk.build, set)
			for i := range a.queries {
				if !reflect.DeepEqual(a.queries[i].ranked, b.queries[i].ranked) {
					t.Errorf("%s: rankings differ between runs:\n%v\n%v",
						a.queries[i].q.ID, a.queries[i].ranked, b.queries[i].ranked)
				}
			}
			if a.mrr != b.mrr || a.ndcg10 != b.ndcg10 || a.recall5 != b.recall5 {
				t.Errorf("summary differs: %v/%v/%v vs %v/%v/%v", a.mrr, a.ndcg10, a.recall5, b.mrr, b.ndcg10, b.recall5)
			}
		})
	}
}

// TestGoldenSetValid keeps the golden set well-formed and in sync with the
// corpus: sizes, unique ids, valid grades, existing paths, kind coverage.
func TestGoldenSetValid(t *testing.T) {
	set := loadGoldenSet(t)
	if n := len(set.Queries); n < 30 || n > 50 {
		t.Errorf("golden set has %d queries, want 30 to 50", n)
	}

	corpus := map[string]bool{}
	err := filepath.WalkDir(searchCorpusDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(searchCorpusDir, path)
		if err != nil {
			return err
		}
		corpus[filepath.ToSlash(rel)] = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(corpus) < 300 {
		t.Errorf("corpus has %d notes, want several hundred", len(corpus))
	}

	ids := map[string]bool{}
	kinds := map[string]int{}
	langs := map[string]int{}
	for _, q := range set.Queries {
		if q.ID == "" || ids[q.ID] {
			t.Errorf("missing or duplicate id %q", q.ID)
		}
		ids[q.ID] = true
		if strings.TrimSpace(q.Query) == "" {
			t.Errorf("%s: empty query", q.ID)
		}
		if q.Lang != "cs" && q.Lang != "en" {
			t.Errorf("%s: lang %q, want cs or en", q.ID, q.Lang)
		}
		langs[q.Lang]++
		if len(q.Kinds) == 0 {
			t.Errorf("%s: no kinds", q.ID)
		}
		noAnswer := slices.Contains(q.Kinds, "no-answer")
		for _, k := range q.Kinds {
			kinds[k]++
		}
		if noAnswer != !q.answerable() {
			t.Errorf("%s: no-answer kind and an empty relevant list must go together", q.ID)
		}
		exact := 0
		for p, g := range q.Relevant {
			if g != gradeExact && g != gradeRelated {
				t.Errorf("%s: %s has grade %d, want 1 or 2", q.ID, p, g)
			}
			if g == gradeExact {
				exact++
			}
			if !corpus[p] {
				t.Errorf("%s: %s is not in the corpus", q.ID, p)
			}
			if q.Dir != "" && !strings.HasPrefix(p, strings.Trim(q.Dir, "/")+"/") {
				t.Errorf("%s: %s is outside dir %q", q.ID, p, q.Dir)
			}
		}
		if q.answerable() && exact == 0 {
			t.Errorf("%s: no exact (grade 2) answer", q.ID)
		}
	}
	for _, k := range requiredQueryKinds {
		if kinds[k] == 0 {
			t.Errorf("no query of kind %q", k)
		}
	}
	if langs["cs"] == 0 || langs["en"] == 0 {
		t.Errorf("languages = %v, want Czech and English queries", langs)
	}
}

func TestSearchQualityMetrics(t *testing.T) {
	rel := map[string]int{"a": 2, "b": 1, "c": 1}
	near := func(got, want float64) bool { return math.Abs(got-want) < 1e-9 }

	cases := []struct {
		name                string
		ranked              []string
		rr, ndcg, recall    float64
		rank                int
		ndcgK, recallCutoff int
	}{
		{"ideal", []string{"a", "b", "c"}, 1, 1, 1, 1, 10, 5},
		{"empty", nil, 0, 0, 0, 0, 10, 5},
		{"exact answer second", []string{"b", "a", "x"}, 0.5,
			(1 + 3/math.Log2(3)) / (3 + 1/math.Log2(3) + 1/math.Log2(4)), 2.0 / 3, 2, 10, 5},
		{"only related", []string{"x", "c"}, 0,
			(1 / math.Log2(3)) / (3 + 1/math.Log2(3) + 1/math.Log2(4)), 1.0 / 3, 0, 10, 5},
		{"cutoff hides the answer", []string{"x", "y", "a"}, 1.0 / 3, 0, 0, 3, 2, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := reciprocalRank(c.ranked, rel); !near(got, c.rr) {
				t.Errorf("RR = %v, want %v", got, c.rr)
			}
			if got := ndcgAt(c.ranked, rel, c.ndcgK); !near(got, c.ndcg) {
				t.Errorf("nDCG = %v, want %v", got, c.ndcg)
			}
			if got := recallAt(c.ranked, rel, c.recallCutoff); !near(got, c.recall) {
				t.Errorf("recall = %v, want %v", got, c.recall)
			}
			if got := exactRank(c.ranked, rel); got != c.rank {
				t.Errorf("rank = %d, want %d", got, c.rank)
			}
		})
	}

	if got := ndcgAt([]string{"a"}, map[string]int{}, 10); got != 0 {
		t.Errorf("nDCG without judgments = %v, want 0", got)
	}
	if got := recallAt([]string{"a"}, map[string]int{}, 5); got != 0 {
		t.Errorf("recall without judgments = %v, want 0", got)
	}

	lat := []time.Duration{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	if p := latencyPercentile(lat, 50); p != 5 {
		t.Errorf("p50 = %v, want 5", p)
	}
	if p := latencyPercentile(lat, 95); p != 10 {
		t.Errorf("p95 = %v, want 10", p)
	}
	if p := latencyPercentile(nil, 95); p != 0 {
		t.Errorf("p95 of nothing = %v, want 0", p)
	}
}

// TestSearchQualityReportFlagsFailures checks the report lists the queries a
// ranker gets wrong, which is the input for tuning the next ranker.
func TestSearchQualityReportFlagsFailures(t *testing.T) {
	rep := qualityReport{ranker: "fake", answerable: 2, noAnswer: 1}
	rep.queries = []queryEval{
		{q: goldenQuery{ID: "hit", Query: "hit", Kinds: []string{"single-word"}, Relevant: map[string]int{"a.md": 2}}, ranked: []string{"a.md"}, rank: 1, rr: 1},
		{q: goldenQuery{ID: "miss", Query: "miss", Kinds: []string{"typo"}, Relevant: map[string]int{"a.md": 2}}, ranked: []string{"b.md"}},
		{q: goldenQuery{ID: "none", Query: "none", Kinds: []string{"no-answer"}}, ranked: []string{"c.md"}},
	}
	out := rep.String()
	for _, want := range []string{"`miss` \"miss\": rank missing, top [b.md]", "`none` \"none\": rank n/a, top [c.md]", "| typo | 1 |"} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "`hit`") {
		t.Errorf("report flags a query answered at rank 1:\n%s", out)
	}
}
