package mcpserver

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/freema/vellum/internal/vault"
)

// Partial reads of read_note (NSO-569).
//
// A plain read_note returns the whole file. For a long note an agent can
// instead ask for its outline (headings with line numbers), one section, or
// a line range, and optionally have every line prefixed with its number.
// Lines are numbered from 1 over the whole file, frontmatter included,
// exactly as search_notes numbers its snippets, so a snippet's line works
// as start_line. The hash is always that of the whole file, so an edit
// after a partial read can still pass expected_hash. Without the new
// parameters nothing changes: the plain read is byte for byte what it was.

// partial reports whether in asks for anything but the plain whole-note
// read.
func (in readNoteIn) partial() bool {
	return in.Outline || in.Section != "" || in.StartLine != 0 || in.MaxLines != 0 || in.LineNumbers
}

// ranged reports whether in narrows the content to a line range or a
// section, which adds startLine/endLine/totalLines/truncated to the result.
func (in readNoteIn) ranged() bool {
	return in.Section != "" || in.StartLine != 0 || in.MaxLines != 0
}

// validate rejects option combinations that have no single meaning. The
// outline overrides every other option, so it is never rejected.
func (in readNoteIn) validate() error {
	if in.Outline {
		return nil
	}
	if in.StartLine < 0 || in.MaxLines < 0 {
		return errors.New("start_line and max_lines must not be negative")
	}
	if in.Section != "" && in.StartLine != 0 {
		return errors.New("section and start_line cannot be combined: section selects its own lines; " +
			"read the section alone (max_lines may cap it), or use the startLine/endLine it reports to read a range")
	}
	return nil
}

// readPart serves every read_note call that asks for more than the plain
// read: the outline, a section, a line range, or numbered lines. It returns
// the structured result and its text block. With an index the outline also
// carries the descriptions of the note's folders (NSO-573).
func readPart(note *vault.Note, in readNoteIn, ix *vault.Index) (readNoteOut, string, error) {
	lines := vault.SplitLines(note.Content)
	total := len(lines)
	out := newReadNoteOut(note)
	out.TotalLines = ptr(total)

	if in.Outline {
		// Only what an agent needs to pick a section or range: no content,
		// no links, no modTime, no title (the frontmatter and the first
		// heading carry it). Headings are compact "N: ## Text" strings,
		// the shape of the text block: as objects they cost about 30 bytes
		// more per heading, which put a 600-line note's outline over 2 kB.
		out.Content = nil
		out.Links = nil
		out.ModTime = time.Time{}
		out.Title = ""
		for _, h := range vault.Outline(note.Content) {
			out.Headings = append(out.Headings, headingLine(h))
		}
		if ix != nil { // read_notes' outline_only leaves the folders out
			out.Contexts = ix.FolderContexts(note.Path)
		}
		return out, outlineText(note, out), nil
	}

	if !in.ranged() {
		// Only line_numbers: the whole note, numbered.
		out.TotalLines = nil
		out.Content = ptr(numberLines(lines, 1))
		return out, noteText(out), nil
	}

	first, last := max(in.StartLine, 1), total
	if in.Section != "" {
		var err error
		if first, last, err = vault.SectionLines(note.Content, in.Section); err != nil {
			return readNoteOut{}, "", err
		}
	}
	if in.MaxLines > 0 && last-first+1 > in.MaxLines {
		last = first + in.MaxLines - 1
		out.Truncated = true
	}
	out.StartLine = first
	content := ""
	if first <= last {
		out.EndLine = last
		if in.LineNumbers {
			content = numberLines(lines[first-1:last], first)
		} else {
			content = strings.Join(lines[first-1:last], "")
		}
	}
	out.Content = &content
	return out, partText(out, in.Section), nil
}

// numberLines prefixes each line with its file line number as "N: ", the
// format search_notes' text block uses. from is the number of lines[0].
func numberLines(lines []string, from int) string {
	var b strings.Builder
	for i, l := range lines {
		b.WriteString(strconv.Itoa(from + i))
		b.WriteString(": ")
		b.WriteString(l)
	}
	return b.String()
}

// partText renders a line range or section read: the read_note header with
// a lines line that says which part this is and where to continue, a blank
// line, then the lines.
func partText(n readNoteOut, section string) string {
	var b strings.Builder
	b.WriteString("path: " + n.Path + "\n")
	b.WriteString("hash: " + n.Hash + "\n")
	if len(n.Tags) > 0 {
		b.WriteString("tags: " + strings.Join(n.Tags, ", ") + "\n")
	}
	if section != "" {
		b.WriteString("section: " + section + "\n")
	}
	total := strconv.Itoa(*n.TotalLines)
	switch {
	case n.EndLine == 0:
		b.WriteString("lines: none (start_line " + strconv.Itoa(n.StartLine) + " is past the end, the note has " + total + ")\n")
	case n.Truncated:
		b.WriteString("lines: " + strconv.Itoa(n.StartLine) + "-" + strconv.Itoa(n.EndLine) + " of " + total +
			" (truncated, continue with start_line " + strconv.Itoa(n.EndLine+1) + ")\n")
	default:
		b.WriteString("lines: " + strconv.Itoa(n.StartLine) + "-" + strconv.Itoa(n.EndLine) + " of " + total + "\n")
	}
	b.WriteString("\n")
	if n.Content != nil {
		b.WriteString(*n.Content)
	}
	return b.String()
}

// outlineText renders the outline: the read_note header (with a
// "folder dir: description" line per described folder above the note), a
// blank line, the frontmatter block exactly as in the file, then one
// "N: ## Heading" line per heading (headingLine), N being its file line.
func outlineText(note *vault.Note, n readNoteOut) string {
	var b strings.Builder
	b.WriteString("path: " + n.Path + "\n")
	b.WriteString("hash: " + n.Hash + "\n")
	if len(n.Tags) > 0 {
		b.WriteString("tags: " + strings.Join(n.Tags, ", ") + "\n")
	}
	b.WriteString("lines: " + strconv.Itoa(*n.TotalLines) + "\n")
	b.WriteString(contextsText(n.Contexts))
	b.WriteString("\n")
	if fm := note.Content[:len(note.Content)-len(note.Body)]; fm != "" {
		b.WriteString(fm)
		if !strings.HasSuffix(fm, "\n") {
			b.WriteString("\n")
		}
	}
	if len(n.Headings) == 0 {
		b.WriteString("(no headings)\n")
	}
	for _, h := range n.Headings {
		b.WriteString(h + "\n")
	}
	return b.String()
}

// headingLine renders one outline entry as "N: ## Text": the file line, the
// level as that many "#", the heading text.
func headingLine(h vault.Heading) string {
	return strconv.Itoa(h.Line) + ": " + strings.Repeat("#", h.Level) + " " + h.Text
}

func ptr[T any](v T) *T { return &v }
