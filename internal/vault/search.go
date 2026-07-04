package vault

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
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

// maxCachedNotes bounds the content cache. When exceeded the cache is reset
// (it is only a cache — the next search repopulates it from disk).
const maxCachedNotes = 8192

// cachedNote is one note's content held in RAM for searching. modTime+size
// come from the metadata index, so a stale entry is detected without a stat.
type cachedNote struct {
	modTime int64
	size    int64
	lines   []string // raw lines, for snippets
	lower   []string // lowercased lines, for case-insensitive matching
}

// ScanSearcher matches candidate notes against the query, narrowing first
// through the metadata index (tags, directory) and reading file content
// through an in-memory cache keyed by the index's modTime+size — so repeated
// searches are pure RAM, and any write (which updates the index) invalidates
// exactly the notes it touched. Results are ranked by relevance: title
// matches beat tag matches beat path matches beat body matches.
type ScanSearcher struct {
	vault *Vault
	index *Index

	mu    sync.Mutex
	cache map[string]*cachedNote
}

// NewScanSearcher builds the default searcher.
func NewScanSearcher(v *Vault, ix *Index) *ScanSearcher {
	return &ScanSearcher{vault: v, index: ix, cache: map[string]*cachedNote{}}
}

// content returns the note's lines, served from the cache when the index
// says the file has not changed since it was cached.
func (s *ScanSearcher) content(e Entry) (*cachedNote, error) {
	s.mu.Lock()
	if c, ok := s.cache[e.Path]; ok && c.modTime == e.ModTime && c.size == e.Size {
		s.mu.Unlock()
		return c, nil
	}
	s.mu.Unlock()

	raw, err := s.vault.ReadRaw(e.Path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(raw, "\n")
	lower := make([]string, len(lines))
	for i, l := range lines {
		lower[i] = strings.ToLower(l)
	}
	c := &cachedNote{modTime: e.ModTime, size: e.Size, lines: lines, lower: lower}

	s.mu.Lock()
	if len(s.cache) >= maxCachedNotes {
		s.cache = map[string]*cachedNote{} // reset, it's only a cache
	}
	s.cache[e.Path] = c
	s.mu.Unlock()
	return c, nil
}

// scored pairs a result with its rank inputs so sorting happens once at the end.
type scored struct {
	res     Result
	score   int
	modTime int64
}

// Search runs a ranked, case-insensitive search. Multi-word queries AND their
// terms: every term must appear in the note's title, tags, path or body.
// An empty query returns the metadata-filtered notes without snippets, which
// is how a pure tag filter is expressed. Regex mode matches line-by-line like
// grep.
func (s *ScanSearcher) Search(query string, opts SearchOpts) ([]Result, error) {
	opts = opts.withDefaults()
	candidates := s.candidates(opts)

	query = strings.TrimSpace(query)
	if query == "" {
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
		terms = strings.Fields(strings.ToLower(query))
	}

	jobs := make(chan Entry)
	resCh := make(chan scored)
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for e := range jobs {
				var r scored
				var ok bool
				if re != nil {
					r, ok = s.matchRegex(e, re, opts)
				} else {
					r, ok = s.matchTerms(e, terms, opts)
				}
				if ok {
					resCh <- r
				}
			}
		}()
	}
	go func() {
		for _, e := range candidates {
			jobs <- e
		}
		close(jobs)
		wg.Wait()
		close(resCh)
	}()

	var results []scored
	for r := range resCh {
		results = append(results, r)
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].score != results[j].score {
			return results[i].score > results[j].score
		}
		if results[i].modTime != results[j].modTime {
			return results[i].modTime > results[j].modTime
		}
		return results[i].res.Path < results[j].res.Path
	})
	if len(results) > opts.MaxResults {
		results = results[:opts.MaxResults]
	}
	out := make([]Result, len(results))
	for i, r := range results {
		out[i] = r.res
	}
	return out, nil
}

// candidates narrows via the metadata index without touching the disk.
func (s *ScanSearcher) candidates(opts SearchOpts) []Entry {
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
	var out []Entry
	for _, e := range s.index.All() {
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

// matchTerms scores a note against AND-ed terms. Every term must appear in
// the title, a tag, the path or the body; where it appears decides the rank.
func (s *ScanSearcher) matchTerms(e Entry, terms []string, opts SearchOpts) (scored, bool) {
	c, err := s.content(e)
	if err != nil {
		return scored{}, false // unreadable candidates are skipped, not fatal
	}
	lowTitle := strings.ToLower(e.Title)
	lowPath := strings.ToLower(e.Path)

	score := 0
	for _, term := range terms {
		matched := false
		switch {
		case strings.HasPrefix(lowTitle, term):
			score += 60
			matched = true
		case strings.Contains(lowTitle, term):
			score += 40
			matched = true
		}
		for _, tag := range e.Tags {
			lt := strings.ToLower(tag)
			if lt == term {
				score += 30
				matched = true
				break
			}
			if strings.Contains(lt, term) {
				score += 15
				matched = true
				break
			}
		}
		if strings.Contains(lowPath, term) {
			score += 10
			matched = true
		}
		hits := 0
		for _, line := range c.lower {
			if strings.Contains(line, term) {
				hits++
				if hits >= 5 {
					break
				}
			}
		}
		if hits > 0 {
			score += hits * 4
			matched = true
		}
		if !matched {
			return scored{}, false
		}
	}
	// Phrase bonus: the words in order beat the words scattered around.
	phrase := strings.Join(terms, " ")
	if len(terms) > 1 && strings.Contains(lowTitle, phrase) {
		score += 30
	}

	// Snippets: best lines first — a line with the full phrase beats a line
	// with more terms beats a line with fewer.
	type lineHit struct {
		line       int // 0-based
		quality    int
		start, end int
	}
	var hits []lineHit
	for i, line := range c.lower {
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
		match := raw
		// Indexes come from the lowered line; for the rare rune whose case
		// change shifts byte offsets, fall back to the whole line.
		if h.start <= len(raw) && h.end <= len(raw) && h.start <= h.end {
			match = raw[h.start:h.end]
		}
		lo := max(0, h.line-opts.ContextLines)
		hi := min(len(c.lines), h.line+opts.ContextLines+1)
		snippets = append(snippets, Snippet{
			Line:    h.line + 1,
			Match:   match,
			Context: strings.Join(c.lines[lo:hi], "\n"),
		})
	}
	return scored{
		res:     Result{Path: e.Path, Title: e.Title, Snippets: snippets},
		score:   score,
		modTime: e.ModTime,
	}, true
}

// matchRegex matches line-by-line like grep and scores by hit count.
func (s *ScanSearcher) matchRegex(e Entry, re *regexp.Regexp, opts SearchOpts) (scored, bool) {
	c, err := s.content(e)
	if err != nil {
		return scored{}, false
	}
	var snippets []Snippet
	hits := 0
	for i, line := range c.lines {
		loc := re.FindStringIndex(line)
		if loc == nil {
			continue
		}
		hits++
		if len(snippets) < opts.MaxSnippets {
			lo := max(0, i-opts.ContextLines)
			hi := min(len(c.lines), i+opts.ContextLines+1)
			snippets = append(snippets, Snippet{
				Line:    i + 1,
				Match:   line[loc[0]:loc[1]],
				Context: strings.Join(c.lines[lo:hi], "\n"),
			})
		}
	}
	if hits == 0 {
		return scored{}, false
	}
	return scored{
		res:     Result{Path: e.Path, Title: e.Title, Snippets: snippets},
		score:   hits,
		modTime: e.ModTime,
	}, true
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
