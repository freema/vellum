package vault

import (
	"fmt"
	"os"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"unicode"
	"unicode/utf8"

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
	// ContextLines is the number of lines around a match in the snippet
	// (default 2).
	ContextLines int
	// MaxSnippets caps snippets per note (default 3).
	MaxSnippets int
}

func (o SearchOpts) withDefaults() SearchOpts {
	if o.MaxResults <= 0 {
		o.MaxResults = 50
	}
	if o.ContextLines <= 0 {
		o.ContextLines = 2
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
	Path     string    `json:"path"`
	Title    string    `json:"title"`
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
		var f string
		if r < utf8.RuneSelf {
			f = string(unicode.ToLower(r))
		} else {
			f = fold(string(r))
		}
		for range len(f) {
			offsets = append(offsets, i)
		}
		b.WriteString(f)
	}
	return b.String(), offsets
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
		if w.rlen < tl-k || w.rlen > tl+k {
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

// maxCachedNotes bounds the content cache. When exceeded the cache is reset
// (it is only a cache — the next search repopulates it from disk).
const maxCachedNotes = 8192

// cachedNote is one note's content held in RAM for searching. modTime+size
// come from the metadata index, so a stale entry is detected without a stat.
// Everything needed to match is pre-folded once per file version.
type cachedNote struct {
	modTime int64
	size    int64
	lines   []string // raw lines, for snippets
	folded  []string // lowercased, diacritics-stripped lines for matching
	offsets [][]int  // folded→raw byte maps (nil for pure-ASCII lines)
	all     string   // folded lines joined — one Contains beats a line loop
	title   string   // folded title
	path    string   // folded path
	tags    []string // folded tags
	words   []word   // unique folded words (title + body), for typo matching
}

// word is one dictionary entry with its rune length pre-computed, so the
// fuzzy pass can skip length-incompatible words without counting runes.
type word struct {
	s    string
	rlen int
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
					out = append(out, word{s: w, rlen: utf8.RuneCountInString(w)})
				}
				start = -1
			}
		}
		if start >= 0 {
			if w := s[start:]; !seen[w] {
				seen[w] = true
				out = append(out, word{s: w, rlen: utf8.RuneCountInString(w)})
			}
		}
	}
	add(title)
	for _, l := range lines {
		add(l)
	}
	return out
}

// ScanSearcher matches candidate notes against the query, narrowing first
// through the metadata index (tags, directory) and reading file content
// through an in-memory cache keyed by the index's modTime+size — so repeated
// searches are pure RAM, and any write (which updates the index) invalidates
// exactly the notes it touched. Results are ranked by relevance: title
// matches beat tag matches beat path matches beat body hits; a typo-tolerant
// fallback keeps misspelled queries working at a rank below any exact hit.
type ScanSearcher struct {
	vault *Vault
	index *Index

	mu    sync.RWMutex
	cache map[string]*cachedNote
}

// NewScanSearcher builds the default searcher.
func NewScanSearcher(v *Vault, ix *Index) *ScanSearcher {
	return &ScanSearcher{vault: v, index: ix, cache: map[string]*cachedNote{}}
}

// content returns the note's pre-folded content, served from the cache when
// the index says the file has not changed since it was cached.
func (s *ScanSearcher) content(e *Entry) (*cachedNote, error) {
	s.mu.RLock()
	c, ok := s.cache[e.Path]
	s.mu.RUnlock()
	if ok && c.modTime == e.ModTime && c.size == e.Size {
		return c, nil
	}

	raw, err := s.vault.ReadRaw(e.Path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(raw, "\n")
	folded := make([]string, len(lines))
	offsets := make([][]int, len(lines))
	for i, l := range lines {
		folded[i], offsets[i] = foldLine(l)
	}
	tags := make([]string, len(e.Tags))
	for i, t := range e.Tags {
		tags[i] = fold(t)
	}
	foldedTitle := fold(e.Title)
	c = &cachedNote{
		modTime: e.ModTime, size: e.Size,
		lines: lines, folded: folded, offsets: offsets,
		all:   strings.Join(folded, "\n"),
		title: foldedTitle, path: fold(e.Path), tags: tags,
		words: collectWords(foldedTitle, folded),
	}

	s.mu.Lock()
	if len(s.cache) >= maxCachedNotes {
		s.cache = map[string]*cachedNote{} // reset, it's only a cache
	}
	s.cache[e.Path] = c
	s.mu.Unlock()
	return c, nil
}

// ---------------------------------------------------------------- search

// scored is a phase-1 hit: rank inputs plus everything phase 2 needs to
// build snippets for the winners only.
type scored struct {
	entry        *Entry
	note         *cachedNote
	score        int
	modTime      int64
	snippetTerms []string // terms + typo-resolved words (nil in regex mode)
}

// Search runs a ranked, case- and diacritics-insensitive search. Multi-word
// queries AND their terms: every term must appear in the note's title, tags,
// path or body — or be within its typo budget of some word. An empty query
// returns the metadata-filtered notes without snippets, which is how a pure
// tag filter is expressed. Regex mode matches line-by-line like grep.
//
// Two phases keep it fast: a parallel scoring pass over the candidates that
// does no snippet work, then snippet extraction for the top MaxResults only.
func (s *ScanSearcher) Search(query string, opts SearchOpts) ([]Result, error) {
	opts = opts.withDefaults()
	candidates := s.candidates(opts)

	query = strings.TrimSpace(query)
	if query == "" {
		sort.Slice(candidates, func(i, j int) bool { return candidates[i].Path < candidates[j].Path })
		if len(candidates) > opts.MaxResults {
			candidates = candidates[:opts.MaxResults]
		}
		out := make([]Result, len(candidates))
		for i, e := range candidates {
			out[i] = Result{Path: e.Path, Title: e.Title}
		}
		return out, nil
	}

	var re *regexp.Regexp
	var terms []string
	if opts.Regex {
		var err error
		re, err = regexp.Compile("(?i)" + query)
		if err != nil {
			return nil, fmt.Errorf("invalid search regex: %w", err)
		}
	} else {
		terms = strings.Fields(fold(query))
	}

	// Phase 1: parallel scoring, one shard per worker, no locks in the loop.
	workers := min(max(runtime.GOMAXPROCS(0), 1), 16)
	shards := make([][]scored, workers)
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
				if re != nil {
					r, ok = s.scoreRegex(candidates[i], re)
				} else {
					r, ok = s.scoreTerms(candidates[i], terms)
				}
				if ok {
					shards[w] = append(shards[w], r)
				}
			}
		}(w)
	}
	wg.Wait()

	var results []scored
	for _, sh := range shards {
		results = append(results, sh...)
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
	if len(results) > opts.MaxResults {
		results = results[:opts.MaxResults]
	}

	// Phase 2: snippets for the winners only.
	out := make([]Result, len(results))
	for i, r := range results {
		res := Result{Path: r.entry.Path, Title: r.entry.Title}
		if re != nil {
			res.Snippets = regexSnippets(r.note, re, opts)
		} else {
			res.Snippets = termSnippets(r.note, r.snippetTerms, opts)
		}
		out[i] = res
	}
	return out, nil
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

// scoreTerms scores a note against AND-ed terms. Every term must appear in
// the title, a tag, the path or the body (or typo-match a word); where it
// appears decides the rank, and a hit at a word start beats one buried
// inside a word.
func (s *ScanSearcher) scoreTerms(e *Entry, terms []string) (scored, bool) {
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
	return scored{entry: e, note: c, score: score, modTime: e.ModTime, snippetTerms: snippetTerms}, true
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
	return scored{entry: e, note: c, score: hits, modTime: e.ModTime}, true
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
		rs, re := rawSpan(raw, c.offsets[h.line], h.start, h.end)
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
