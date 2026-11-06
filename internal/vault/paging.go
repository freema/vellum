package vault

import "math"

// Page is one window of a ranked or sorted list plus the length of the
// whole list, so a caller can tell an agent how much it did not see.
type Page struct {
	Results []Result
	// Total is the number of notes the query matched before Offset and
	// MaxResults cut the window out of it.
	Total int
	// Relaxed is set when no note matched every query term and the results
	// are the notes that miss one of them (NSO-578).
	Relaxed bool
}

// PagedSearcher is a Searcher that can report the size of the full
// ranking along with one window of it. ScanSearcher implements it.
type PagedSearcher interface {
	Searcher
	SearchPage(query string, opts SearchOpts) (Page, error)
}

// SearchPage runs a paged search on any Searcher. A PagedSearcher answers
// directly; any other one ranks everything and the window is cut here,
// which gives the same results at the cost of snippets for every match.
func SearchPage(s Searcher, query string, opts SearchOpts) (Page, error) {
	if p, ok := s.(PagedSearcher); ok {
		return p.SearchPage(query, opts)
	}
	all := opts
	all.Offset = 0
	all.MaxResults = math.MaxInt32
	results, err := s.Search(query, all)
	if err != nil {
		return Page{}, err
	}
	opts = opts.withDefaults()
	return Page{Results: window(results, opts.Offset, opts.MaxResults), Total: len(results)}, nil
}

// window returns at most limit items starting at offset; an offset past
// the end gives an empty (non-nil when items is) slice. limit <= 0 means
// no cap.
func window[T any](items []T, offset, limit int) []T {
	offset = min(max(offset, 0), len(items))
	items = items[offset:]
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items
}
