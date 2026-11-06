package mcpserver

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/freema/vellum/internal/vault"
)

// Text blocks of the read-heavy tools (NSO-570).
//
// Every tool returns structuredContent. When a handler leaves the result's
// Content empty, go-sdk mirrors the same JSON into a text block for clients
// that predate structured output. For the tools whose results are mostly
// note text (read_note, search_notes, suggest_tags, suggest_links) that
// mirror is the most expensive way to say it: every newline and quote is
// escaped and every key repeats per item. These tools instead put a plain
// rendering into the text block: a short header plus the markdown itself,
// unquoted, and for search_notes a compact list of the hits with their best
// matching line. A client that reads only the text still gets everything it
// needs to act (paths, the content hash, the content, line numbers);
// structuredContent is unchanged apart from the dropped read_note body. See docs/payload-baseline.md for the measurements and
// the clients this was checked against.

// textResult carries a plain-text rendering in place of the SDK's JSON
// mirror. structuredContent is still filled in by the SDK from the typed
// output.
func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

// readNoteOut is read_note's structured result: vault.Note without Body.
// Body is Content minus the frontmatter block, so sending both put the
// note on the wire twice. Content stays because the hash is computed over
// it and edits are built from it. The REST API keeps vault.Note (and its
// body) for the web UI. Field order follows vault.Note.
//
// The fields after ModTime belong to the partial reads of NSO-569 (line
// range, section, outline) and are omitted from a plain read, which stays
// byte for byte what it was. Content is a pointer so the outline can leave
// it out entirely rather than claim the note is empty. Title and ModTime
// are never empty on a read (a note without frontmatter title or heading
// is titled after its file name), so their omit tags only matter for the
// outline, which leaves them out to stay small.
type readNoteOut struct {
	Path        string         `json:"path"`
	Title       string         `json:"title,omitempty"`   // never empty on a read; left out of the outline
	Content     *string        `json:"content,omitempty"` // raw file content incl. frontmatter, or the lines asked for
	Frontmatter map[string]any `json:"frontmatter,omitempty"`
	Tags        []string       `json:"tags,omitempty"`
	Links       []string       `json:"links,omitempty"`
	Hash        string         `json:"hash"` // sha256 hex of the whole file, also on partial reads
	Size        int64          `json:"size"`
	ModTime     time.Time      `json:"modTime,omitzero"` // set on every read but the outline

	StartLine  int      `json:"startLine,omitempty"`  // first line returned (1-based, frontmatter counted)
	EndLine    int      `json:"endLine,omitempty"`    // last line returned; omitted when none is
	TotalLines *int     `json:"totalLines,omitempty"` // lines in the whole file
	Truncated  bool     `json:"truncated,omitempty"`  // max_lines cut the range or section short
	Headings   []string `json:"headings,omitempty"`   // outline only: "N: ## Heading text"
	// Contexts, outline only (NSO-573): the descriptions of the note's
	// folder and its parent folders, keyed by folder, top folder first.
	Contexts map[string]string `json:"contexts,omitempty"`
}

func newReadNoteOut(n *vault.Note) readNoteOut {
	return readNoteOut{
		Path:        n.Path,
		Title:       n.Title,
		Content:     &n.Content,
		Frontmatter: n.Frontmatter,
		Tags:        n.Tags,
		Links:       n.Links,
		Hash:        n.Hash,
		Size:        n.Size,
		ModTime:     n.ModTime,
	}
}

// lineCount is the number of lines an editor shows for s: a trailing
// newline ends the last line rather than starting an empty one.
func lineCount(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}

// noteText renders read_note's text block: a header with what an agent
// needs to edit the note (path, hash) and to place it (tags, line count),
// a blank line, then the raw file content exactly as on disk.
func noteText(n readNoteOut) string {
	var b strings.Builder
	content := ""
	if n.Content != nil {
		content = *n.Content
	}
	b.Grow(len(content) + len(n.Path) + 128)
	b.WriteString("path: " + n.Path + "\n")
	b.WriteString("hash: " + n.Hash + "\n")
	if len(n.Tags) > 0 {
		b.WriteString("tags: " + strings.Join(n.Tags, ", ") + "\n")
	}
	b.WriteString("lines: " + strconv.Itoa(lineCount(content)) + "\n\n")
	b.WriteString(content)
	return b.String()
}

// searchText renders search results as a compact list: per note a
// "path (title)" line and its best matching line as "N: text", N being the
// file line number (frontmatter counted, as read_note shows it). Notes keep
// the ranked order. The context windows and the further snippets stay in
// structuredContent only: in the text they would cost several times the
// list itself (see docs/payload-baseline.md), and a client that reads only
// the text can still pick a note and open it at the right line. This is
// also the shape of the concise format (NSO-572). contextLines must be the
// value the search ran with: it locates the match line inside its window.
func searchText(results []vault.Result, contextLines int) string {
	return searchListText(results, contextLines, listPage{total: len(results)})
}

// listPage is where a page of results sits in the whole ranking, for the
// header and hint of searchListText, and whether it renders the concise
// format.
type listPage struct {
	total, offset int
	hint          string
	concise       bool              // one "path:line (title)" line per note, no match text
	contexts      map[string]string // folder descriptions (NSO-573), after the list
	relaxed       bool              // the notes miss one query word each (NSO-578)
}

// searchListText is searchText for one page of a longer ranking: the
// header says which part of how many notes this is, and the hint (how to
// narrow the search or get the next page) closes the text.
func searchListText(results []vault.Result, contextLines int, p listPage) string {
	var b strings.Builder
	switch {
	case len(results) == 0 && p.total == 0:
		return "no matching notes"
	case len(results) == 0:
		fmt.Fprintf(&b, "no notes on this page (%d matching in all)", p.total)
	case p.offset == 0 && len(results) == p.total && len(results) == 1:
		b.WriteString("1 note")
	case p.offset == 0 && len(results) == p.total:
		fmt.Fprintf(&b, "%d notes", len(results))
	default:
		fmt.Fprintf(&b, "notes %d-%d of %d", p.offset+1, p.offset+len(results), p.total)
	}
	switch {
	case p.relaxed && p.total == 1:
		b.WriteString(" that misses one query word (relaxed: no note has them all)")
	case p.relaxed:
		b.WriteString(" that miss one query word each (relaxed: no note has them all)")
	}
	switch {
	case !hasSnippets(results):
	case p.concise:
		b.WriteString(" (path:line of each first match; read_note start_line opens it there)")
	default:
		b.WriteString(" (best matching line of each; read_note for the full text)")
	}
	b.WriteByte('\n')
	for _, r := range results {
		b.WriteString(r.Path)
		if p.concise && len(r.Snippets) > 0 {
			b.WriteString(":" + strconv.Itoa(r.Snippets[0].Line))
		}
		if r.Title != "" {
			b.WriteString(" (" + r.Title + ")")
		}
		b.WriteByte('\n')
		if !p.concise && len(r.Snippets) > 0 {
			s := r.Snippets[0] // best first
			b.WriteString(strconv.Itoa(s.Line) + ": " + clipLine(snippetMatchLine(s, contextLines), s.Match) + "\n")
		}
	}
	b.WriteString(contextsText(p.contexts))
	if p.hint != "" {
		b.WriteString(p.hint + "\n")
	}
	return b.String()
}

// contextsText renders folder descriptions (NSO-573) as one
// "folder dir: description" line each, parent folders first. Empty for
// none.
func contextsText(contexts map[string]string) string {
	if len(contexts) == 0 {
		return ""
	}
	dirs := make([]string, 0, len(contexts))
	for d := range contexts {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs) // a parent sorts before its children
	var b strings.Builder
	for _, d := range dirs {
		b.WriteString("folder " + d + ": " + contexts[d] + "\n")
	}
	return b.String()
}

func hasSnippets(results []vault.Result) bool {
	for _, r := range results {
		if len(r.Snippets) > 0 {
			return true
		}
	}
	return false
}

// snippetMatchLine is the full text of a snippet's match line, taken from
// its context window. A window that does not fit contextLines (the search
// ran with another context size) falls back to the first window line that
// holds the match rather than guessing.
func snippetMatchLine(s vault.Snippet, contextLines int) string {
	window := strings.Split(s.Context, "\n")
	if i := s.Line - max(1, s.Line-contextLines); i >= 0 && i < len(window) {
		return window[i]
	}
	return matchLine(s)
}

// maxTextLine caps a rendered match line, in runes. Markdown paragraphs are
// often a single long line; the text list only needs enough of it to show
// why the note matched.
const maxTextLine = 200

// clipLine shortens line to maxTextLine runes, keeping the match in view
// and marking each cut with "…".
func clipLine(line, match string) string {
	const width = maxTextLine
	r := []rune(line)
	if len(r) <= width {
		return line
	}
	start := 0
	if i := strings.Index(line, match); match != "" && i >= 0 {
		// Start a little before the match so it reads in context.
		start = max(0, utf8.RuneCountInString(line[:i])-width/4)
	}
	start = min(start, len(r)-width)
	out := string(r[start : start+width])
	if start > 0 {
		out = "…" + out
	}
	if start+width < len(r) {
		out += "…"
	}
	return out
}

// matchLine is the fallback text for a snippet whose window cannot be
// placed: the context line holding the match, or the match itself.
func matchLine(s vault.Snippet) string {
	for _, l := range strings.Split(s.Context, "\n") {
		if s.Match != "" && strings.Contains(l, s.Match) {
			return l
		}
	}
	return s.Match
}

// tagsContextText renders suggest_tags' context: the tag lists as plain
// comma-separated lines (vault tags with their note counts), then the
// excerpt as markdown.
func tagsContextText(tc *vault.TagsContext) string {
	var b strings.Builder
	b.WriteString("path: " + tc.Path + "\n")
	b.WriteString("current tags: " + orNone(strings.Join(tc.CurrentTags, ", ")) + "\n")
	b.WriteString("neighbor tags: " + orNone(strings.Join(tc.NeighborTags, ", ")) + "\n")
	vocab := make([]string, len(tc.VaultTags))
	for i, t := range tc.VaultTags {
		vocab[i] = t.Tag + " (" + strconv.Itoa(t.Count) + ")"
	}
	b.WriteString("vault tags: " + orNone(strings.Join(vocab, ", ")) + "\n")
	b.WriteString("\nexcerpt:\n" + tc.Excerpt)
	return b.String()
}

// linksText renders suggest_links' candidates one per line.
func linksText(cands []vault.LinkCandidate) string {
	if len(cands) == 0 {
		return "no link candidates"
	}
	var b strings.Builder
	for _, c := range cands {
		b.WriteString(c.Path)
		if c.Title != "" {
			b.WriteString(" (" + c.Title + ")")
		}
		b.WriteString(": " + c.Reason + "\n")
	}
	return b.String()
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

// searchContextLines is the snippet context search_notes asks for (the
// vault default). searchText needs it to find the match line in a window.
const searchContextLines = 2
