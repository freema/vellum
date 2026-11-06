package vault

import (
	"fmt"
	"os"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"unicode"
	"unicode/utf8"
	"unsafe"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// SearchOpts narrow and shape a search.
type SearchOpts struct {
	// Tags that must all be present on a note (AND). Applied via the
	// metadata index before any file is opened.
	Tags []string
	// Dir limits results to a vault subtree (path prefix).
	Dir string
	// Regex treats the query as a regular expression (case-insensitive).
	Regex bool
	// MaxResults caps the number of returned notes (default 50).
	MaxResults int
	// Offset skips the first N notes of the ranking (paging, see
	// SearchPage). Negative counts as 0.
	Offset int
	// ContextLines is the number of lines around a match in the snippet
	// (0 = default 2; NoContext or any negative value = the match line
	// alone).
	ContextLines int
	// MaxSnippets caps snippets per note (default 3).
	MaxSnippets int
	// MinScore drops ranked results whose Score is below it (0 to 1,
	// default 0 = keep all). It applies before Offset and MaxResults, so
	// Total counts only the notes that pass. Ignored where there is no
	// score: regex mode, an empty query, the legacy ranker.
	MinScore float64
}

// NoContext asks for snippets without surrounding lines: ContextLines is
// zero-means-default, so a real zero needs its own value.
const NoContext = -1

func (o SearchOpts) withDefaults() SearchOpts {
	if o.MaxResults <= 0 {
		o.MaxResults = 50
	}
	switch {
	case o.ContextLines == 0:
		o.ContextLines = 2
	case o.ContextLines < 0:
		o.ContextLines = 0
	}
	if o.Offset < 0 {
		o.Offset = 0
	}
	if o.MaxSnippets <= 0 {
		o.MaxSnippets = 3
	}
	return o
}

// Snippet is one match with surrounding context.
type Snippet struct {
	Line    int    `json:"line"`    // 1-based line number of the match
	Match   string `json:"match"`   // exact matched text (for highlighting)
	Context string `json:"context"` // match line ± ContextLines
}

// Result is one matching note.
type Result struct {
	Path  string `json:"path"`
	Title string `json:"title"`
	// Score is the BM25F relevance of a ranked text search, 0 to 1 and not
	// relative to the best hit (see search_bm25f.go). Left out (0) where
	// nothing is ranked: regex mode and an empty query (pure tag filter).
	Score    float64   `json:"score,omitempty"`
	Snippets []Snippet `json:"snippets,omitempty"`
}

// Searcher is the search abstraction. The default implementation is a
// zero-dependency ranked scan over an in-memory content cache; a bleve-backed
// index can slot in behind the same interface later if that ever hurts.
type Searcher interface {
	Search(query string, opts SearchOpts) ([]Result, error)
}

// ---------------------------------------------------------------- folding

// foldPool hands out diacritics-stripping transformers (NFD-decompose, drop
// combining marks, recompose). Chained transformers carry state and are NOT
// safe for concurrent use, and search folds from parallel workers — hence a
// pool instead of one shared instance. transform.String resets before use.
var foldPool = sync.Pool{
	New: func() any {
		return transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	},
}

// fold lowercases and strips diacritics for matching — "ukol" finds "úkol",
// "poznamka" finds "poznámka".
func fold(s string) string {
	if isLowerASCII(s) {
		return s // fast path: nothing to fold
	}
	tr := foldPool.Get().(transform.Transformer)
	out, _, err := transform.String(tr, s)
	foldPool.Put(tr)
	if err != nil {
		return strings.ToLower(s)
	}
	return strings.ToLower(out)
}

func isLowerASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf || (s[i] >= 'A' && s[i] <= 'Z') {
			return false
		}
	}
	return true
}

// foldLine folds one line and returns a byte-offset map from the folded
// string back into the raw one, so a match found in folded text can be
// sliced out of the original (diacritics change byte widths).
func foldLine(raw string) (string, []int) {
	if isLowerASCII(raw) {
		return raw, nil // identity: offsets map 1:1
	}
	var b strings.Builder
	b.Grow(len(raw))
	offsets := make([]int, 0, len(raw))
	for i, r := range raw {
		f := foldRune(r)
		for range len(f) {
			offsets = append(offsets, i)
		}
		b.WriteString(f)
	}
	return b.String(), offsets
}

// foldRune is one rune of a line folded the way foldLine folds it. The
// fold of a rune below U+0800 (Latin with all its accents, Greek, Cyrillic,
// Hebrew, Arabic) is computed once and kept in a table, so folding a note
// does not allocate per accented letter.
func foldRune(r rune) string {
	if r < utf8.RuneSelf {
		return string(unicode.ToLower(r))
	}
	if r >= rune(len(foldedRunes)) {
		return fold(string(r))
	}
	if f := foldedRunes[r].Load(); f != nil {
		return *f
	}
	f := fold(string(r))
	foldedRunes[r].Store(&f)
	return f
}

var foldedRunes [0x800]atomic.Pointer[string]

// rawSpanOf maps a [start,end) span of the folded line back to the raw
// line, as rawSpan does with foldLine's offset map, without building the
// map.
func rawSpanOf(raw string, start, end int) (int, int) {
	if isLowerASCII(raw) {
		return start, end // identity, as foldLine's nil map
	}
	rs, pos := -1, 0
	for i, r := range raw {
		n := len(foldRune(r))
		if rs < 0 && start < pos+n {
			rs = i
		}
		if end < pos+n {
			return rs, i
		}
		pos += n
	}
	if rs < 0 {
		return 0, 0 // start past the folded line
	}
	return rs, len(raw)
}

// foldLines folds every line exactly as foldLine does and joins them with
// newlines into one string; each folded line is a substring of it. The
// search cache keeps the folded text once instead of once per line plus
// once joined, and keeps no offset maps (snippets rebuild the map of the
// few lines they show, see termSnippets).
func foldLines(lines []string) (all string, folded []string) {
	n := len(lines)
	for _, l := range lines {
		n += len(l)
	}
	var b strings.Builder
	b.Grow(n)
	bounds := make([]int, len(lines)+1)
	for i, l := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		bounds[i] = b.Len()
		if isLowerASCII(l) {
			b.WriteString(l)
			continue
		}
		for _, r := range l {
			b.WriteString(foldRune(r))
		}
	}
	all = b.String()
	folded = make([]string, len(lines))
	for i := range lines {
		end := len(all)
		if i+1 < len(lines) {
			end = bounds[i+1] - 1 // the newline before the next line
		}
		folded[i] = all[bounds[i]:end]
	}
	return all, folded
}

// rawSpan maps a [start,end) span in the folded line back to the raw line.
func rawSpan(raw string, offsets []int, start, end int) (int, int) {
	if offsets == nil { // identity fast path
		return start, end
	}
	if start >= len(offsets) {
		return 0, 0
	}
	rs := offsets[start]
	re := len(raw)
	if end < len(offsets) {
		re = offsets[end]
	}
	return rs, re
}

// ---------------------------------------------------------------- fuzzy

// fuzzyMaxDist is the typo budget for a term: short terms must match
// exactly, medium ones tolerate one edit, long ones two.
func fuzzyMaxDist(term string) int {
	switch n := utf8.RuneCountInString(term); {
	case n < 4:
		return 0
	case n < 8:
		return 1
	default:
		return 2
	}
}

// levenshteinAtMost computes the edit distance between a and b, giving up
// (returning k+1) as soon as it provably exceeds k. Words are short, so the
// DP rows live on the stack.
func levenshteinAtMost(a, b string, k int) int {
	ra, rb := []rune(a), []rune(b)
	la, lb := len(ra), len(rb)
	if la-lb > k || lb-la > k {
		return k + 1
	}
	var prevA, currA [48]int
	var prev, curr []int
	if lb+1 <= len(prevA) {
		prev, curr = prevA[:lb+1], currA[:lb+1]
	} else {
		prev, curr = make([]int, lb+1), make([]int, lb+1)
	}
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		curr[0] = i
		rowMin := curr[0]
		for j := 1; j <= lb; j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			curr[j] = min(min(curr[j-1]+1, prev[j]+1), prev[j-1]+cost)
			rowMin = min(rowMin, curr[j])
		}
		if rowMin > k {
			return k + 1 // the whole row is over budget — no way back
		}
		prev, curr = curr, prev
	}
	return prev[lb]
}

// fuzzyWord returns the closest word of the note within the term's edit
// budget, or "" when nothing is close enough. Words of incompatible length
// are skipped without computing a distance.
func fuzzyWord(c *cachedNote, term string) (string, int) {
	k := fuzzyMaxDist(term)
	if k == 0 {
		return "", 0
	}
	tl := utf8.RuneCountInString(term)
	best, bestDist := "", k+1
	for _, w := range c.words {
		if int(w.rlen) < tl-k || int(w.rlen) > tl+k {
			continue
		}
		if d := levenshteinAtMost(term, w.s, k); d < bestDist {
			best, bestDist = w.s, d
			if d <= 1 {
				break // an exact hit is impossible here (substring ran first)
			}
		}
	}
	if bestDist > k {
		return "", 0
	}
	return best, bestDist
}

// ---------------------------------------------------------------- cache

// The content cache takes a note while it holds fewer than maxCachedNotes
// notes or fewer than maxCacheBytes (estimated, cachedNote.cost) bytes: a
// vault of short notes fills the byte budget, one of long notes stops at
// the note count, so neither caches fewer notes, nor holds more memory,
// than the count bound alone allowed. A full cache keeps what it holds and
// serves the notes beyond it from disk: a search walks every note in the
// same order each time, so evicting (or resetting the whole cache) would
// make a vault larger than the bound miss on every note of every search.
// A note already cached is still refreshed when it changes, and a full
// cache drops the notes gone from the index (pruneCache).
const (
	maxCachedNotes = 8192
	maxCacheBytes  = 256 << 20
)

// cachedNote is one note's content held in RAM for searching. modTime
// (nanoseconds) and size come from the metadata index, so a stale entry is
// detected without a stat.
// Everything needed to match is pre-folded once per file version.
type cachedNote struct {
	modTime int64
	size    int64
	lines   []string // raw lines, for snippets
	folded  []string // lowercased, diacritics-stripped lines for matching, substrings of all
	all     string   // folded lines joined — one Contains beats a line loop
	title   string   // folded title
	path    string   // folded path
	tags    []string // folded tags
	words   []word   // unique folded words (title + body), for typo matching

	// BM25F field data (search_bm25f.go): where the body starts in all
	// (after the frontmatter), the folded heading texts, the word count per
	// field, and whether body terms are counted over words (bodyTermCount).
	bodyOff      int
	headings     string
	fieldLen     [numFields]int32
	countByWords bool
}

// cost estimates the bytes a cached note holds: the raw text (the lines
// share it), the folded text, the string and word headers, and the struct.
func (c *cachedNote) cost() int64 {
	n := c.size + int64(len(c.all)+len(c.title)+len(c.path)+len(c.headings))
	n += int64(len(c.lines)+len(c.folded)) * int64(unsafe.Sizeof(""))
	n += int64(len(c.words)) * int64(unsafe.Sizeof(word{}))
	for _, t := range c.tags {
		n += int64(len(t)) + int64(unsafe.Sizeof(""))
	}
	return n + int64(unsafe.Sizeof(cachedNote{}))
}

// word is one dictionary entry with its rune length pre-computed, so the
// fuzzy pass can skip length-incompatible words without counting runes,
// and with how often it occurs in the body (BM25F, see bodyTermCount).
type word struct {
	s    string
	rlen int32
	body int32
}

// collectWords extracts the unique folded words of a note (title + lines).
func collectWords(title string, lines []string) []word {
	seen := map[string]bool{}
	var out []word
	add := func(s string) {
		start := -1
		for i, r := range s {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				if start < 0 {
					start = i
				}
				continue
			}
			if start >= 0 {
				if w := s[start:i]; !seen[w] {
					seen[w] = true
					out = append(out, word{s: w, rlen: int32(utf8.RuneCountInString(w))})
				}
				start = -1
			}
		}
		if start >= 0 {
			if w := s[start:]; !seen[w] {
				seen[w] = true
				out = append(out, word{s: w, rlen: int32(utf8.RuneCountInString(w))})
			}
		}
	}
	add(title)
	for _, l := range lines {
		add(l)
	}
	// The cache keeps this slice for the note's lifetime: drop the spare
	// capacity append left behind (up to half of it).
	return slices.Clone(out)
}

// ScanSearcher matches candidate notes against the query, narrowing first
// through the metadata index (tags, directory) and reading file content
// through an in-memory cache keyed by the index's modTime+size — so repeated
// searches are pure RAM, and any write (which updates the index) invalidates
// exactly the notes it touched. Results are ranked by BM25F relevance over
// the title, path, headings, tags and body (search_bm25f.go), with a 0 to 1 score;
// a typo-tolerant fallback keeps misspelled queries working at a reduced
// weight.
type ScanSearcher struct {
	vault  *Vault
	index  *Index
	ranker string // RankerBM25F (default) or RankerLegacy, see SetRanker

	mu         sync.RWMutex
	cache      map[string]*cachedNote
	cacheBytes int64 // sum of the cached notes' cost
	maxCached  int   // maxCachedNotes; tests lower it
	maxBytes   int64 // maxCacheBytes; tests lower it
	turnedAway bool  // the cache refused a note since the last pruneCache
}

// NewScanSearcher builds the default searcher.
func NewScanSearcher(v *Vault, ix *Index) *ScanSearcher {
	return &ScanSearcher{vault: v, index: ix, ranker: RankerBM25F, cache: map[string]*cachedNote{}, maxCached: maxCachedNotes, maxBytes: maxCacheBytes}
}

// content returns the note's pre-folded content, served from the cache when
// the index says the file has not changed since it was cached.
func (s *ScanSearcher) content(e *Entry) (*cachedNote, error) {
	s.mu.RLock()
	c, ok := s.cache[e.Path]
	s.mu.RUnlock()
	if ok && c.modTime == e.modNano && c.size == e.Size {
		return c, nil
	}

	raw, err := s.vault.ReadRaw(e.Path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(raw, "\n")
	all, folded := foldLines(lines)
	tags := make([]string, len(e.Tags))
	for i, t := range e.Tags {
		tags[i] = fold(t)
	}
	foldedTitle := fold(e.Title)
	c = &cachedNote{
		modTime: e.modNano, size: e.Size,
		lines: lines, folded: folded, all: all,
		title: foldedTitle, path: fold(e.Path), tags: tags,
		words: collectWords(foldedTitle, folded),
	}
	c.fillFields(raw)

	cost := c.cost()
	s.mu.Lock()
	if old, cached := s.cache[e.Path]; cached {
		s.cacheBytes += cost - old.cost()
		s.cache[e.Path] = c
	} else if !s.cacheFull(cost) {
		s.cacheBytes += cost
		s.cache[e.Path] = c
	} else {
		s.turnedAway = true
	}
	s.mu.Unlock()
	return c, nil
}

// cacheFull reports whether the cache can not take a new note of the given
// cost. The caller holds s.mu.
func (s *ScanSearcher) cacheFull(cost int64) bool {
	return len(s.cache) >= s.maxCached && s.cacheBytes+cost > s.maxBytes
}

// pruneCache drops the cached notes the index no longer has (deleted or
// moved), so they do not hold places of a full cache. It runs once per
// search and only when the cache has turned a note away since the last
// run; below the bounds a stale entry costs nothing but memory and is gone
// at the next restart.
func (s *ScanSearcher) pruneCache() {
	s.mu.Lock()
	if !s.turnedAway {
		s.mu.Unlock()
		return
	}
	s.turnedAway = false
	paths := make([]string, 0, len(s.cache))
	for p := range s.cache {
		paths = append(paths, p)
	}
	s.mu.Unlock()

	var gone []string
	for _, p := range paths {
		if _, ok := s.index.Get(p); !ok {
			gone = append(gone, p)
		}
	}
	if len(gone) == 0 {
		return
	}
	s.mu.Lock()
	for _, p := range gone {
		if c, ok := s.cache[p]; ok {
			s.cacheBytes -= c.cost()
			delete(s.cache, p)
		}
	}
	s.mu.Unlock()
}

// ---------------------------------------------------------------- search

// scored is a phase-1 hit: rank inputs plus everything phase 2 needs to
// build snippets for the winners only.
type scored struct {
	entry        *Entry
	note         *cachedNote
	score        float64 // rank key: BM25F relevance, legacy points or regex hits
	modTime      int64
	snippetTerms []string // terms + typo-resolved words (nil in regex mode)

	// BM25F inputs from phase 1 (search_bm25f.go): where the per-term,
	// per-field occurrence counts and match weights sit (worker shard,
	// offset in its slab).
	shard, tfOff int32
}

// Search runs a ranked, case- and diacritics-insensitive search. Multi-word
// queries AND their terms: every term must appear in the note's title, tags,
// path or body — or match a word through its Czech word form, or be within
// its typo budget of some word (search_match.go). When no note holds every
// term, the BM25F ranker relaxes the AND to notes that miss one term
// (Page.Relaxed). An empty query
// returns the metadata-filtered notes without snippets, which is how a pure
// tag filter is expressed. Regex mode matches line-by-line like grep.
//
// Two phases keep it fast: a parallel scoring pass over the candidates that
// does no snippet work, then snippet extraction for the top MaxResults only.
func (s *ScanSearcher) Search(query string, opts SearchOpts) ([]Result, error) {
	p, err := s.SearchPage(query, opts)
	return p.Results, err
}

// SearchPage is Search plus paging: the window of MaxResults notes from
// Offset on, and the length of the whole ranking. Only the window gets
// snippets.
func (s *ScanSearcher) SearchPage(query string, opts SearchOpts) (Page, error) {
	opts = opts.withDefaults()
	candidates := s.candidates(opts)
	s.pruneCache()

	query = strings.TrimSpace(query)
	if query == "" {
		sort.Slice(candidates, func(i, j int) bool { return candidates[i].Path < candidates[j].Path })
		total := len(candidates)
		candidates = window(candidates, opts.Offset, opts.MaxResults)
		out := make([]Result, len(candidates))
		for i, e := range candidates {
			out[i] = Result{Path: e.Path, Title: e.Title}
		}
		return Page{Results: out, Total: total}, nil
	}

	var re *regexp.Regexp
	var terms []string
	if opts.Regex {
		var err error
		re, err = regexp.Compile("(?i)" + query)
		if err != nil {
			return Page{}, fmt.Errorf("invalid search regex: %w", err)
		}
	} else {
		terms = strings.Fields(fold(query))
	}

	// Phase 1: parallel scoring (matchAll). BM25F also gathers per-shard
	// corpus statistics (document frequency per term, field lengths),
	// merged before the scores are computed.
	bm25f := re == nil && s.ranker != RankerLegacy
	wordTerms := make([]bool, len(terms))
	for i, t := range terms {
		wordTerms[i] = isWordTerm(t)
	}
	results, stats := s.matchAll(candidates, re, terms, wordTerms, bm25f, false)
	relaxed := false
	if bm25f && len(results) == 0 && len(terms) > 1 {
		// No note holds every term: an agent's long query fails on one word
		// the right note does not use. Rather than nothing, return the
		// notes that miss one term, marked relaxed. The missing term adds
		// nothing to their score, and a note only counts when the terms it
		// holds carry most of the query (holdsMostOfQuery), so a query
		// word no note knows does not fall back to its common rest.
		results, stats = s.matchAll(candidates, re, terms, wordTerms, bm25f, true)
		relaxed = true
	}
	if bm25f {
		results = scoreBM25F(results, len(terms), len(candidates), stats, opts.MinScore, relaxed)
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].score != results[j].score {
			return results[i].score > results[j].score
		}
		if results[i].modTime != results[j].modTime {
			return results[i].modTime > results[j].modTime
		}
		return results[i].entry.Path < results[j].entry.Path
	})
	total := len(results)
	results = window(results, opts.Offset, opts.MaxResults)

	// Phase 2: snippets for the winners only.
	out := make([]Result, len(results))
	for i, r := range results {
		res := Result{Path: r.entry.Path, Title: r.entry.Title}
		if bm25f {
			res.Score = r.score
		}
		if re != nil {
			res.Snippets = regexSnippets(r.note, re, opts)
		} else {
			res.Snippets = termSnippets(r.note, r.snippetTerms, opts)
		}
		out[i] = res
	}
	return Page{Results: out, Total: total, Relaxed: relaxed && total > 0}, nil
}

// matchAll is phase 1 over all candidates: parallel, one shard per worker,
// no locks in the loop. allowMiss is the relaxed AND of BM25F (one term may
// be missing).
func (s *ScanSearcher) matchAll(candidates []*Entry, re *regexp.Regexp, terms []string, wordTerms []bool, bm25f, allowMiss bool) ([]scored, []bm25Stats) {
	workers := min(max(runtime.GOMAXPROCS(0), 1), 16)
	shards := make([][]scored, workers)
	stats := make([]bm25Stats, workers)
	var next atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for {
				i := int(next.Add(1)) - 1
				if i >= len(candidates) {
					return
				}
				var r scored
				var ok bool
				switch {
				case re != nil:
					r, ok = s.scoreRegex(candidates[i], re)
				case bm25f:
					r, ok = s.matchBM25F(candidates[i], terms, wordTerms, &stats[w], allowMiss)
					r.shard = int32(w)
				default:
					r, ok = s.scoreLegacy(candidates[i], terms)
				}
				if ok {
					shards[w] = append(shards[w], r)
				}
			}
		}(w)
	}
	wg.Wait()

	n := 0
	for _, sh := range shards {
		n += len(sh)
	}
	results := make([]scored, 0, n)
	for _, sh := range shards {
		results = append(results, sh...)
	}
	return results, stats
}

// candidates narrows via the metadata index without touching the disk.
// Entries are shared pointers — the index never mutates an entry in place.
func (s *ScanSearcher) candidates(opts SearchOpts) []*Entry {
	var paths map[string]bool
	if len(opts.Tags) > 0 {
		for i, tag := range opts.Tags {
			tagged := s.index.PathsByTag(tag)
			if len(tagged) == 0 {
				return nil
			}
			set := map[string]bool{}
			for _, p := range tagged {
				if i == 0 || paths[p] {
					set[p] = true
				}
			}
			paths = set
		}
	}

	dir := strings.Trim(opts.Dir, "/")
	var out []*Entry
	for _, e := range s.index.snapshot() {
		if paths != nil && !paths[e.Path] {
			continue
		}
		if dir != "" && e.Path != dir && !strings.HasPrefix(e.Path, dir+"/") {
			continue
		}
		out = append(out, e)
	}
	return out
}

// wordBoundaryAt reports whether position i in a folded string starts a word.
func wordBoundaryAt(s string, i int) bool {
	if i == 0 {
		return true
	}
	r, _ := utf8.DecodeLastRuneInString(s[:i])
	return !unicode.IsLetter(r) && !unicode.IsDigit(r)
}

// scoreLegacy is the hand-tuned ranker BM25F replaces (VELLUM_RANKER=legacy,
// kept only to compare the two over the same golden set; NSO-577 removes it
// once BM25F is accepted). It scores a note against AND-ed terms. Every term
// must appear in the title, a tag, the path or the body (or typo-match a
// word); where it appears decides the rank, and a hit at a word start beats
// one buried inside a word.
func (s *ScanSearcher) scoreLegacy(e *Entry, terms []string) (scored, bool) {
	c, err := s.content(e)
	if err != nil {
		return scored{}, false // unreadable candidates are skipped, not fatal
	}

	score := 0
	var fuzzyWords []string // typo-resolved words, for snippets/highlighting
	for _, term := range terms {
		matched := false
		if idx := strings.Index(c.title, term); idx >= 0 {
			switch {
			case idx == 0:
				score += 60
			case wordBoundaryAt(c.title, idx):
				score += 50
			default:
				score += 35
			}
			matched = true
		}
		for _, tag := range c.tags {
			if tag == term {
				score += 30
				matched = true
				break
			}
			if strings.Contains(tag, term) {
				score += 15
				matched = true
				break
			}
		}
		if strings.Contains(c.path, term) {
			score += 10
			matched = true
		}
		// One Contains over the joined content decides whether the per-line
		// hit count (for ranking) is worth computing at all.
		if strings.Contains(c.all, term) {
			hits := 0
			for _, line := range c.folded {
				if idx := strings.Index(line, term); idx >= 0 {
					hits++
					if wordBoundaryAt(line, idx) {
						hits++ // word-start hits weigh double
					}
					if hits >= 8 {
						break
					}
				}
			}
			score += hits * 3
			matched = true
		}
		if !matched {
			// Typo fallback: the term matched nothing exactly — look for the
			// closest word within the edit-distance budget. A typo hit keeps
			// the note in the results but scores well below any exact hit.
			w, dist := fuzzyWord(c, term)
			if w == "" {
				return scored{}, false
			}
			score += 5 - 2*dist // dist 1 → +3, dist 2 → +1
			fuzzyWords = append(fuzzyWords, w)
		}
	}
	// Phrase bonus: the words in order beat the words scattered around.
	if len(terms) > 1 && strings.Contains(c.title, strings.Join(terms, " ")) {
		score += 30
	}

	snippetTerms := terms
	if len(fuzzyWords) > 0 {
		snippetTerms = append(append(make([]string, 0, len(terms)+len(fuzzyWords)), terms...), fuzzyWords...)
	}
	return scored{entry: e, note: c, score: float64(score), modTime: e.ModTime, snippetTerms: snippetTerms}, true
}

// scoreRegex counts matching lines like grep (capped — the count only ranks).
func (s *ScanSearcher) scoreRegex(e *Entry, re *regexp.Regexp) (scored, bool) {
	c, err := s.content(e)
	if err != nil {
		return scored{}, false
	}
	hits := 0
	for _, line := range c.lines {
		if re.MatchString(line) {
			hits++
			if hits >= 32 {
				break
			}
		}
	}
	if hits == 0 {
		return scored{}, false
	}
	return scored{entry: e, note: c, score: float64(hits), modTime: e.ModTime}, true
}

// termSnippets extracts the best matching lines: a line with the full phrase
// beats a line with more terms beats a line with fewer. Typo-resolved words
// are part of the hunt, so a fuzzy match still gets a highlighted snippet.
func termSnippets(c *cachedNote, terms []string, opts SearchOpts) []Snippet {
	phrase := strings.Join(terms, " ")
	type lineHit struct {
		line       int // 0-based
		quality    int
		start, end int // span in the folded line
	}
	var hits []lineHit
	for i, line := range c.folded {
		q, start, end := 0, -1, -1
		for _, term := range terms {
			if idx := strings.Index(line, term); idx >= 0 {
				q++
				if start < 0 {
					start, end = idx, idx+len(term)
				}
			}
		}
		if q == 0 {
			continue
		}
		if len(terms) > 1 {
			if idx := strings.Index(line, phrase); idx >= 0 {
				q += len(terms)
				start, end = idx, idx+len(phrase)
			}
		}
		hits = append(hits, lineHit{line: i, quality: q, start: start, end: end})
	}
	sort.SliceStable(hits, func(a, b int) bool { return hits[a].quality > hits[b].quality })
	if len(hits) > opts.MaxSnippets {
		hits = hits[:opts.MaxSnippets]
	}
	var snippets []Snippet
	for _, h := range hits {
		raw := c.lines[h.line]
		rs, re := rawSpanOf(raw, h.start, h.end)
		lo := max(0, h.line-opts.ContextLines)
		hi := min(len(c.lines), h.line+opts.ContextLines+1)
		snippets = append(snippets, Snippet{
			Line:    h.line + 1,
			Match:   raw[rs:re], // original casing and diacritics
			Context: strings.Join(c.lines[lo:hi], "\n"),
		})
	}
	return snippets
}

// regexSnippets collects the first matching lines with context.
func regexSnippets(c *cachedNote, re *regexp.Regexp, opts SearchOpts) []Snippet {
	var snippets []Snippet
	for i, line := range c.lines {
		loc := re.FindStringIndex(line)
		if loc == nil {
			continue
		}
		lo := max(0, i-opts.ContextLines)
		hi := min(len(c.lines), i+opts.ContextLines+1)
		snippets = append(snippets, Snippet{
			Line:    i + 1,
			Match:   line[loc[0]:loc[1]],
			Context: strings.Join(c.lines[lo:hi], "\n"),
		})
		if len(snippets) >= opts.MaxSnippets {
			break
		}
	}
	return snippets
}

// ReadRaw returns the raw content of a note without any markdown parsing.
// Same path validation and size limits as Read.
func (v *Vault) ReadRaw(path string) (string, error) {
	abs, err := v.resolveNote(path)
	if err != nil {
		return "", err
	}
	fi, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("%w: %s", ErrNotFound, path)
		}
		return "", err
	}
	if fi.Size() > v.maxSize {
		return "", fmt.Errorf("%w: %s (%d bytes)", ErrTooLarge, path, fi.Size())
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
