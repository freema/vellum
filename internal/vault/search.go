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
// zero-dependency parallel scan; a bleve-backed index can slot in behind
// the same interface later if scanning ever hurts.
type Searcher interface {
	Search(query string, opts SearchOpts) ([]Result, error)
}

// ScanSearcher scans candidate files in parallel, narrowing first through
// the metadata index (tags, directory) so only the remainder is read.
// No warm-up, no index to corrupt.
type ScanSearcher struct {
	vault *Vault
	index *Index
}

// NewScanSearcher builds the default searcher.
func NewScanSearcher(v *Vault, ix *Index) *ScanSearcher {
	return &ScanSearcher{vault: v, index: ix}
}

// Search runs a case-insensitive substring (or regex) search. An empty
// query returns the metadata-filtered notes without snippets, which is how
// a pure tag filter is expressed.
func (s *ScanSearcher) Search(query string, opts SearchOpts) ([]Result, error) {
	opts = opts.withDefaults()

	var matcher func(line string) (int, int)
	if query != "" {
		if opts.Regex {
			re, err := regexp.Compile("(?i)" + query)
			if err != nil {
				return nil, fmt.Errorf("invalid search regex: %w", err)
			}
			matcher = func(line string) (int, int) {
				if loc := re.FindStringIndex(line); loc != nil {
					return loc[0], loc[1]
				}
				return -1, -1
			}
		} else {
			lq := strings.ToLower(query)
			matcher = func(line string) (int, int) {
				if i := strings.Index(strings.ToLower(line), lq); i >= 0 {
					return i, i + len(lq)
				}
				return -1, -1
			}
		}
	}

	candidates := s.candidates(opts)
	if matcher == nil {
		if len(candidates) > opts.MaxResults {
			candidates = candidates[:opts.MaxResults]
		}
		out := make([]Result, len(candidates))
		for i, e := range candidates {
			out[i] = Result{Path: e.Path, Title: e.Title}
		}
		return out, nil
	}

	jobs := make(chan Entry)
	resCh := make(chan Result)
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for e := range jobs {
				if r, ok := s.scanFile(e, matcher, opts); ok {
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

	var results []Result
	for r := range resCh {
		results = append(results, r)
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Path < results[j].Path })
	if len(results) > opts.MaxResults {
		results = results[:opts.MaxResults]
	}
	return results, nil
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

// scanFile reads one note and collects snippets for matching lines.
func (s *ScanSearcher) scanFile(e Entry, matcher func(string) (int, int), opts SearchOpts) (Result, bool) {
	content, err := s.vault.ReadRaw(e.Path)
	if err != nil {
		return Result{}, false // unreadable candidates are skipped, not fatal
	}
	lines := strings.Split(content, "\n")
	var snippets []Snippet
	for i, line := range lines {
		start, end := matcher(line)
		if start < 0 {
			continue
		}
		lo := max(0, i-opts.ContextLines)
		hi := min(len(lines), i+opts.ContextLines+1)
		snippets = append(snippets, Snippet{
			Line:    i + 1,
			Match:   line[start:end],
			Context: strings.Join(lines[lo:hi], "\n"),
		})
		if len(snippets) >= opts.MaxSnippets {
			break
		}
	}
	if len(snippets) == 0 {
		return Result{}, false
	}
	return Result{Path: e.Path, Title: e.Title, Snippets: snippets}, true
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
