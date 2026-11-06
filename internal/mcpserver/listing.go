package mcpserver

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/freema/vellum/internal/vault"
)

// Limits, paging and the concise mode of search_notes and list_notes
// (NSO-572).
//
// Both tools cap what one call returns and say so when they cut: total is
// the size of the whole result, truncated is set when more follows the
// page, and hint tells the agent how to narrow the call or where the next
// page starts. Without the new parameters a call returns the same notes in
// the same order as before, only fewer of them.

const (
	// searchDefaultMax caps search_notes. The vault default (50) stays for
	// the REST API and the web UI; only agents get the smaller page.
	searchDefaultMax = 20
	// listDefaultLimit caps list_notes, so a recursive listing of a large
	// vault cannot outgrow what a client passes on to the model.
	listDefaultLimit = 200

	formatDetailed = "detailed"
	formatConcise  = "concise"

	// list_notes sort orders (NSO-576).
	sortPath     = "path"
	sortModified = "modified"
)

// searchHit is one search_notes result. In the detailed format it is
// exactly vault.Result (path, title, score, snippets); in the concise
// format the snippets are left out and line points at the first match
// instead.
// vault.Result is embedded so its fields keep their names, order and
// meaning.
//
// A concise hit carries no text of the matching line: a page of 20 has to
// stay under 3 kB with long paths too (the payload fixture's paths are 60
// bytes and more), and a few dozen characters around the match mostly
// repeat the query. The line number is where read_note's start_line opens
// the note.
type searchHit struct {
	vault.Result
	Line int `json:"line,omitempty"` // concise: file line of the first match (frontmatter counted)
}

type searchNotesOut struct {
	Results   []searchHit `json:"results"`
	Total     int         `json:"total"`     // notes the query matched in all
	Truncated bool        `json:"truncated"` // more matches follow this page
	Hint      string      `json:"hint,omitempty"`
	// Relaxed is set when no note holds every query word and the results
	// are notes that miss one of them (NSO-578).
	Relaxed bool `json:"relaxed,omitempty"`
	// Contexts maps folder -> description (NSO-573) for the folders the
	// page's notes are in and their parent folders: once per response, not
	// per hit, and left out when none of them is described.
	Contexts map[string]string `json:"contexts,omitempty"`
}

// searchNotes runs search_notes: argument checks, the paged search, and
// both renderings of the page (structured and text).
func searchNotes(d Deps, in searchNotesIn) (searchNotesOut, string, error) {
	concise := false
	switch in.ResponseFormat {
	case "", formatDetailed:
	case formatConcise:
		concise = true
	default:
		return searchNotesOut{}, "", fmt.Errorf("response_format must be %q or %q, got %q", formatDetailed, formatConcise, in.ResponseFormat)
	}
	if in.Offset < 0 || in.MaxSnippets < 0 || (in.ContextLines != nil && *in.ContextLines < 0) {
		return searchNotesOut{}, "", errors.New("offset, context_lines and max_snippets must not be negative")
	}
	if in.MinScore < 0 || in.MinScore > 1 {
		return searchNotesOut{}, "", fmt.Errorf("min_score must be between 0 and 1, got %g", in.MinScore)
	}

	limit := in.MaxResults
	if limit <= 0 {
		limit = searchDefaultMax
	}
	contextLines := searchContextLines
	if in.ContextLines != nil {
		contextLines = *in.ContextLines
	}
	opts := vault.SearchOpts{
		Tags:        in.Tags,
		Dir:         in.Dir,
		Regex:       in.Regex,
		MaxResults:  limit,
		Offset:      in.Offset,
		MaxSnippets: in.MaxSnippets,
		MinScore:    in.MinScore,
	}
	if concise {
		// One line per note: the best snippet without its window.
		contextLines, opts.MaxSnippets = 0, 1
	}
	opts.ContextLines = contextLines
	if contextLines == 0 {
		opts.ContextLines = vault.NoContext // 0 means "default" in the vault
	}

	page, err := vault.SearchPage(d.Searcher, in.Query, opts)
	if err != nil {
		return searchNotesOut{}, "", err
	}

	out := searchNotesOut{
		Results:   make([]searchHit, len(page.Results)),
		Total:     page.Total,
		Truncated: in.Offset+len(page.Results) < page.Total,
		Hint:      pageHint(page.Total, in.Offset, len(page.Results), "results", "narrow with tags or dir"),
		Relaxed:   page.Relaxed,
	}
	paths := make([]string, len(page.Results))
	for i, r := range page.Results {
		paths[i] = r.Path
	}
	out.Contexts = d.Index.FolderContexts(paths...)
	for i, r := range page.Results {
		if !concise {
			out.Results[i] = searchHit{Result: r}
			continue
		}
		hit := searchHit{Result: vault.Result{Path: r.Path, Title: r.Title, Score: r.Score}}
		if len(r.Snippets) > 0 {
			hit.Line = r.Snippets[0].Line
		}
		out.Results[i] = hit
	}
	text := searchListText(page.Results, contextLines, listPage{
		total: page.Total, offset: in.Offset, hint: out.Hint, concise: concise,
		contexts: out.Contexts, relaxed: page.Relaxed,
	})
	return out, text, nil
}

// listNotes runs list_notes: one page of the listing plus the direct
// subfolders of dir with their note counts.
func listNotes(d Deps, in listNotesIn) (listNotesOut, error) {
	if in.Offset < 0 {
		return listNotesOut{}, errors.New("offset must not be negative")
	}
	limit := in.Limit
	if limit <= 0 {
		limit = listDefaultLimit
	}
	opts := vault.ListOptions{Offset: in.Offset, Limit: limit}
	switch in.Sort {
	case "", sortPath:
	case sortModified:
		opts.ByModified = true
	default:
		return listNotesOut{}, fmt.Errorf("sort must be %q or %q, got %q", sortPath, sortModified, in.Sort)
	}
	if in.Since != "" {
		since, err := parseSince(in.Since, time.Now())
		if err != nil {
			return listNotesOut{}, err
		}
		opts.Since = since
	}
	notes, total, err := d.Vault.ListWith(in.Dir, in.Recursive, opts)
	if err != nil {
		return listNotesOut{}, err
	}
	if opts.ByModified {
		// The excerpt is already in the index: what changed, without a
		// read_note per note. A note the index does not know yet (a file
		// created in another editor before the index caught up) goes
		// without one.
		for i := range notes {
			if e, ok := d.Index.Get(notes[i].Path); ok {
				notes[i].Excerpt = e.Excerpt
			}
		}
	}
	dirs, err := d.Index.Subdirs(in.Dir)
	if err != nil {
		return listNotesOut{}, err
	}
	narrow := "narrow with dir (dirs lists the subfolders and their note counts)"
	if in.Recursive {
		narrow += " or list without recursive"
	}
	switch {
	case in.Since != "":
		narrow += " or a shorter since"
	case opts.ByModified:
		narrow += " or since (e.g. 7d)"
	}
	return listNotesOut{
		Notes:     notes,
		Dirs:      dirs,
		Total:     total,
		Truncated: in.Offset+len(notes) < total,
		Hint:      pageHint(total, in.Offset, len(notes), "notes", narrow),
	}, nil
}

// parseSince reads list_notes' since: a whole number of hours, days or
// weeks back from now (24h, 7d, 2w), or a date (2026-11-01, midnight in
// the server's time zone) or an RFC 3339 time. Days and weeks are 24 and
// 168 hours. Natural language ("last week") is deliberately not parsed:
// the error lists the forms that work.
func parseSince(s string, now time.Time) (time.Time, error) {
	in := strings.ToLower(strings.TrimSpace(s))
	bad := fmt.Errorf("since %q not understood: use a duration back from now like 24h, 7d or 2w, or a date like 2026-11-01", s)
	if in == "" {
		return time.Time{}, bad
	}
	if unit, ok := sinceUnits[in[len(in)-1]]; ok {
		n, err := strconv.Atoi(in[:len(in)-1])
		if err != nil || n < 1 || n > sinceMaxCount {
			return time.Time{}, bad
		}
		return now.Add(-time.Duration(n) * unit), nil
	}
	if t, err := time.ParseInLocation(time.DateOnly, in, now.Location()); err == nil {
		return t, nil
	}
	if t, err := time.Parse(time.RFC3339, strings.ToUpper(in)); err == nil {
		return t, nil
	}
	return time.Time{}, bad
}

// sinceUnits are the duration suffixes parseSince takes.
var sinceUnits = map[byte]time.Duration{'h': time.Hour, 'd': 24 * time.Hour, 'w': 7 * 24 * time.Hour}

// sinceMaxCount bounds the number in a since duration: 10,000 weeks (some
// 190 years) still fits a time.Duration, and nothing in a vault is older.
const sinceMaxCount = 10_000

// pageHint tells an agent what a capped page left out and how to get at
// it: narrow the call, or continue at the next offset. Empty when the page
// holds everything from offset on.
func pageHint(total, offset, returned int, what, narrow string) string {
	switch rest := total - offset - returned; {
	case total > 0 && returned == 0 && offset >= total:
		return fmt.Sprintf("offset %d is past the end: %d %s in all", offset, total, what)
	case rest > 0:
		if rest == 1 {
			what = strings.TrimSuffix(what, "s")
		}
		return fmt.Sprintf("%d more %s: %s, or continue with offset: %d", rest, what, narrow, offset+returned)
	default:
		return ""
	}
}
