package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/freema/vellum/internal/vault"
)

// read_notes (NSO-571): several notes in one call.
//
// After a search an agent often wants three to five notes. read_notes reads
// them in one call instead of one read_note each. The answer is per note:
// one bad path is an error on its own entry and the rest still come back.
// A byte budget keeps the result small. A note over max_bytes_per_note is
// skipped with its size, line count and how to read it in parts, never cut
// in the middle; once max_total_bytes is spent, every remaining note is
// skipped the same way. Paths go through Vault.ReadVisible: read_note's path
// rules plus the hidden-path guard, so a bulk read reaches exactly the notes
// list_notes and search_notes show.

const (
	// readNotesMaxNotes caps paths per call, and the glob matches read.
	readNotesMaxNotes = 20
	// readNotesPerNote is the default max_bytes_per_note (the value qmd's
	// multi_get uses).
	readNotesPerNote = 10 << 10
	// readNotesTotal is the default max_total_bytes.
	readNotesTotal = 60 << 10
)

type readNotesIn struct {
	Paths           []string `json:"paths,omitempty" jsonschema:"vault-relative note paths, read in this order; at most 20. Give paths or pattern, not both"`
	Pattern         string   `json:"pattern,omitempty" jsonschema:"glob over note paths instead of paths, e.g. projects/x/*.md; * and ? stay within one folder (no **); matches are read in alphabetical order, at most 20"`
	MaxBytesPerNote int      `json:"max_bytes_per_note,omitempty" jsonschema:"a note larger than this is skipped, not cut (default 10240)"`
	MaxTotalBytes   int      `json:"max_total_bytes,omitempty" jsonschema:"byte budget of the whole call; once spent, the remaining notes are skipped (default 61440)"`
	OutlineOnly     bool     `json:"outline_only,omitempty" jsonschema:"return each note's outline (headings with line numbers, frontmatter, tags, hash, size and line count, as read_note's outline) instead of its content"`
}

// readNotesItem is one note of the answer: its content, its outline, an
// error, or skipped with the facts needed to read it another way. Only the
// keys of that case are present.
type readNotesItem struct {
	Path        string         `json:"path"`
	Title       string         `json:"title,omitempty"`
	Content     *string        `json:"content,omitempty"`     // raw file content incl. frontmatter
	Frontmatter map[string]any `json:"frontmatter,omitempty"` // outline only, as read_note's outline
	Tags        []string       `json:"tags,omitempty"`
	Hash        string         `json:"hash,omitempty"`       // sha256 hex of the file, for expected_hash
	Size        int64          `json:"size,omitempty"`       // file size in bytes; outline and skipped
	TotalLines  int            `json:"totalLines,omitempty"` // lines in the file; outline and skipped
	Headings    []string       `json:"headings,omitempty"`   // outline only: "N: ## Heading text"
	Skipped     bool           `json:"skipped,omitempty"`
	Reason      string         `json:"reason,omitempty"` // why it was skipped and how to read it instead
	Error       string         `json:"error,omitempty"`
}

// readNotesSummary closes the answer. Bytes is what the read notes count
// against max_total_bytes: their content, or for outline_only their outline
// text block.
type readNotesSummary struct {
	Read    int `json:"read"`
	Skipped int `json:"skipped"`
	Errors  int `json:"errors"`
	Bytes   int `json:"bytes"`
	// Matched is the number of notes the pattern matched; only the first
	// 20 of them are read. Omitted for a list of paths.
	Matched int `json:"matched,omitempty"`
}

type readNotesOut struct {
	Notes   []readNotesItem  `json:"notes"`
	Summary readNotesSummary `json:"summary"`
}

func registerReadNotes(s *mcp.Server, d Deps) {
	mcp.AddTool(s, readTool("read_notes", "Read notes",
		"Read several notes in one call: a list of paths (at most 20) or a glob pattern such as projects/x/*.md. Each note comes back with its content, hash, tags and title, or with its own error, or skipped when it is over the byte budget (never cut; read it with read_note's outline, section or line range). outline_only returns each note's outline instead.",
	), func(ctx context.Context, req *mcp.CallToolRequest, in readNotesIn) (*mcp.CallToolResult, readNotesOut, error) {
		out, text, err := readNotes(d.Vault, in)
		if err != nil {
			return nil, readNotesOut{}, err
		}
		return textResult(text), out, nil
	})
}

// readNotes resolves the request to a list of paths and reads them within
// the budget. It returns the structured answer and its text block. Only a
// malformed request fails as a whole.
//
// The text block is for clients that read only the text: per note a
// "==> path <==" line (the head(1) convention, which cannot be mistaken
// for frontmatter), then the hash and tags lines, a blank line and the
// file as on disk (for outline_only read_note's outline text block), or
// an error or skipped line. A summary line closes it.
func readNotes(v *vault.Vault, in readNotesIn) (readNotesOut, string, error) {
	perNote, total, err := in.budget()
	if err != nil {
		return readNotesOut{}, "", err
	}
	out := readNotesOut{Notes: []readNotesItem{}}
	paths := in.Paths
	switch {
	case len(in.Paths) > 0 && in.Pattern != "":
		return readNotesOut{}, "", errors.New("give paths or pattern, not both")
	case len(in.Paths) > readNotesMaxNotes:
		return readNotesOut{}, "", fmt.Errorf("at most %d paths per call, got %d", readNotesMaxNotes, len(in.Paths))
	case in.Pattern != "":
		matched, err := globNotes(v, in.Pattern)
		if err != nil {
			return readNotesOut{}, "", err
		}
		out.Summary.Matched = len(matched)
		paths = matched[:min(len(matched), readNotesMaxNotes)]
	case len(in.Paths) == 0:
		return readNotesOut{}, "", errors.New("give paths (a list of note paths) or pattern (a glob such as projects/x/*.md)")
	}

	var b strings.Builder
	spent, exhausted := 0, false
	for _, p := range paths {
		b.WriteString("==> " + p + " <==\n")
		note, err := v.ReadVisible(p)
		if err != nil {
			out.Notes = append(out.Notes, readNotesItem{Path: p, Error: err.Error()})
			out.Summary.Errors++
			b.WriteString("error: " + err.Error() + "\n")
			continue
		}
		item, text := readNotesEntry(note, in.OutlineOnly)
		n := len(text)
		if !in.OutlineOnly {
			n = len(note.Content)
		}
		switch {
		case !exhausted && n > perNote:
			item = skippedNote(note, fmt.Sprintf("over max_bytes_per_note (%d B)", perNote))
		case !exhausted && spent+n > total:
			exhausted = true
			fallthrough
		case exhausted:
			item = skippedNote(note, fmt.Sprintf("max_total_bytes (%d B) already spent; call read_notes again for the rest", total))
		default:
			spent += n
			out.Summary.Read++
		}
		if item.Skipped {
			out.Summary.Skipped++
			fmt.Fprintf(&b, "skipped: %d B, %d lines, %s\n", item.Size, item.TotalLines, item.Reason)
		} else {
			// The "==> path <==" line already names the note: drop the
			// outline text block's own path line.
			b.WriteString(strings.TrimPrefix(text, "path: "+note.Path+"\n"))
			if !strings.HasSuffix(text, "\n") {
				b.WriteByte('\n')
			}
		}
		out.Notes = append(out.Notes, item)
	}
	out.Summary.Bytes = spent

	s := out.Summary
	switch {
	case len(paths) == 0:
		b.WriteString("no notes match " + in.Pattern + "\n")
	case s.Read == len(paths):
		fmt.Fprintf(&b, "%s read, %d B\n", plural(s.Read, "note"), s.Bytes)
	default:
		fmt.Fprintf(&b, "%s: %d read (%d B), %d skipped, %s\n",
			plural(len(paths), "note"), s.Read, s.Bytes, s.Skipped, plural(s.Errors, "error"))
	}
	if s.Matched > len(paths) {
		fmt.Fprintf(&b, "the pattern matched %d notes, only the first %d were read; narrow it for the rest\n", s.Matched, len(paths))
	}
	return out, b.String(), nil
}

// budget returns the per-note and total byte limits with their defaults.
func (in readNotesIn) budget() (perNote, total int, err error) {
	if in.MaxBytesPerNote < 0 || in.MaxTotalBytes < 0 {
		return 0, 0, errors.New("max_bytes_per_note and max_total_bytes must not be negative")
	}
	perNote, total = in.MaxBytesPerNote, in.MaxTotalBytes
	if perNote == 0 {
		perNote = readNotesPerNote
	}
	if total == 0 {
		total = readNotesTotal
	}
	return perNote, total, nil
}

// readNotesEntry builds a note's entry and its text. The outline is
// read_note's outline (readPart), so both tools describe a note the same
// way; for outline_only its text block is what counts against the budget.
func readNotesEntry(note *vault.Note, outline bool) (readNotesItem, string) {
	if !outline {
		// read_note's header minus lines: the whole file follows, and the
		// structured entry has no line count either.
		var b strings.Builder
		b.Grow(len(note.Content) + 96)
		b.WriteString("hash: " + note.Hash + "\n")
		if len(note.Tags) > 0 {
			b.WriteString("tags: " + strings.Join(note.Tags, ", ") + "\n")
		}
		b.WriteString("\n" + note.Content)
		return readNotesItem{
			Path:    note.Path,
			Title:   note.Title,
			Content: &note.Content,
			Tags:    note.Tags,
			Hash:    note.Hash,
		}, b.String()
	}
	o, text, _ := readPart(note, readNoteIn{Outline: true}, nil) // an outline read cannot fail
	return readNotesItem{
		Path:        o.Path,
		Frontmatter: o.Frontmatter,
		Tags:        o.Tags,
		Hash:        o.Hash,
		Size:        o.Size,
		TotalLines:  *o.TotalLines,
		Headings:    o.Headings,
	}, text
}

// skippedNote is the entry of a note left out of the answer: its size and
// line count, and in reason why and how to read it instead.
func skippedNote(note *vault.Note, why string) readNotesItem {
	return readNotesItem{
		Path:       note.Path,
		Size:       note.Size,
		TotalLines: len(vault.SplitLines(note.Content)),
		Skipped:    true,
		Reason:     why + "; use read_note with outline, section or start_line/max_lines",
	}
}

// globNotes returns the notes whose vault path matches pattern
// (path.Match), in alphabetical order. Only the folder named by the
// pattern's literal leading segments is listed, recursively only when the
// pattern has wildcard folders below it. Like list_notes it never returns
// hidden notes, and a pattern that names a hidden folder is refused.
func globNotes(v *vault.Vault, pattern string) ([]string, error) {
	if strings.Contains(pattern, "**") {
		return nil, errors.New("pattern: ** is not supported, * matches within one folder (projects/x/*.md, projects/*/plan.md)")
	}
	pattern = path.Clean(pattern)
	if _, err := path.Match(pattern, ""); err != nil {
		return nil, fmt.Errorf("pattern %q: %w", pattern, err)
	}
	segs := strings.Split(pattern, "/")
	for _, s := range segs {
		if strings.HasPrefix(s, ".") && s != "." && s != ".." {
			return nil, fmt.Errorf("%w: pattern %s reaches into a hidden path", vault.ErrInvalidPath, pattern)
		}
	}
	fixed := 0 // literal folder segments at the start
	for fixed < len(segs)-1 && !strings.ContainsAny(segs[fixed], `*?[\`) {
		fixed++
	}
	notes, err := v.List(strings.Join(segs[:fixed], "/"), len(segs)-fixed > 1)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, n := range notes {
		if ok, _ := path.Match(pattern, n.Path); ok {
			out = append(out, n.Path)
		}
	}
	return out, nil
}

// plural renders "1 note", "2 notes".
func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return strconv.Itoa(n) + " " + word + "s"
}
