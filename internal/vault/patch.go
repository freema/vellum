package vault

import (
	"errors"
	"fmt"
	"strings"
)

// Section edits of patch_note (NSO-575).
//
// PatchSection finds one section (by heading text or by a heading path
// such as "Project A > Notes", see findSection) and replaces its body,
// adds to it, or inserts a new block before or after it. A section name
// that matches several headings is an error that names them all, and
// nothing is written.
//
// Blank lines at the seams: inserted content is separated from a
// neighbouring heading by exactly one blank line, and blank lines already
// there are not doubled. append and prepend continue the section's text,
// so where the content meets ordinary text nothing is added: it follows
// the section's last line (or precedes its first) directly, as
// append_to_note does, and a blank line at that edge of the content is
// kept, to start a new paragraph. insert_before and insert_after add a
// block of their own, always set off by one blank line. replace writes
// exactly what patch_note always has.

// PatchOp says what PatchSection does with the section.
type PatchOp string

const (
	// PatchReplace swaps the section's body; its heading line stays.
	PatchReplace PatchOp = "replace"
	// PatchAppend adds content at the end of the section, after its
	// subsections if it has any, before the next heading of the same or
	// a higher level.
	PatchAppend PatchOp = "append"
	// PatchPrepend adds content at the start of the section's body,
	// right under its heading.
	PatchPrepend PatchOp = "prepend"
	// PatchInsertBefore inserts content (typically a new section with
	// its heading) before the section's heading.
	PatchInsertBefore PatchOp = "insert_before"
	// PatchInsertAfter inserts content after the end of the section,
	// before the next heading of the same or a higher level (or at the
	// end of the note).
	PatchInsertAfter PatchOp = "insert_after"
)

// PatchOps lists the operations in the order the tool describes them.
var PatchOps = []PatchOp{PatchReplace, PatchAppend, PatchPrepend, PatchInsertBefore, PatchInsertAfter}

var (
	// ErrPatchOp rejects an operation PatchSection does not know.
	ErrPatchOp = errors.New("unknown operation")
	// ErrEmptyContent rejects an insert of nothing: only replace may
	// empty a section.
	ErrEmptyContent = errors.New("content must not be empty")
)

// ParsePatchOp maps an operation name to its PatchOp; empty means
// PatchReplace.
func ParsePatchOp(s string) (PatchOp, error) {
	if s == "" {
		return PatchReplace, nil
	}
	for _, op := range PatchOps {
		if string(op) == s {
			return op, nil
		}
	}
	names := make([]string, len(PatchOps))
	for i, op := range PatchOps {
		names[i] = string(op)
	}
	return "", fmt.Errorf("%w %q: want one of %s", ErrPatchOp, s, strings.Join(names, ", "))
}

// PatchResult describes a successful PatchSection.
type PatchResult struct {
	Content string   // the note as written
	Hash    string   // sha256 hex of Content, the next expected_hash
	Lines   LineSpan // the file lines the new content occupies, frontmatter counted
}

// PatchSection edits the section named by section (a heading text or a
// heading path, see findSection) of the note at path as op says. Nothing
// is written when the hash does not match (ErrConflict), the section is
// missing (ErrSectionNotFound) or ambiguous (ErrSectionAmbiguous, naming
// the candidates), content is empty for an insert (ErrEmptyContent) or
// the result would exceed the size limit (ErrTooLarge).
func (v *Vault) PatchSection(path, section string, op PatchOp, content, expectedHash string) (*PatchResult, error) {
	if op == "" {
		op = PatchReplace
	}
	if _, err := ParsePatchOp(string(op)); err != nil {
		return nil, err
	}
	if op != PatchReplace && strings.TrimSpace(content) == "" {
		return nil, fmt.Errorf("%w for %s", ErrEmptyContent, op)
	}
	abs, err := v.resolveNote(path)
	if err != nil {
		return nil, err
	}

	v.mu.Lock()
	defer v.mu.Unlock()

	note, err := v.readAbs(abs)
	if err != nil {
		return nil, err
	}
	if expectedHash != "" && note.Hash != expectedHash {
		return nil, fmt.Errorf("%w: %s", ErrConflict, path)
	}

	prefix := note.Content[:len(note.Content)-len(note.Body)]
	offset := strings.Count(prefix, "\n")
	lines := strings.SplitAfter(note.Body, "\n")
	start, end, err := findSection(lines, offset, section)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var body string
	var first, n int
	if op == PatchReplace {
		body, first, n = replaceSection(lines, start, end, content)
	} else {
		body, first, n = insertIntoSection(lines, start, end, op, content)
	}
	data := prefix + body
	if int64(len(data)) > v.maxSize {
		return nil, fmt.Errorf("%w: %s (%d bytes)", ErrTooLarge, path, len(data))
	}
	if err := v.writeAtomic(abs, []byte(data)); err != nil {
		return nil, err
	}
	return &PatchResult{
		Content: data,
		Hash:    hashOf([]byte(data)),
		Lines:   LineSpan{First: offset + first + 1, Last: offset + first + n},
	}, nil
}

// insertIntoSection adds content to the section lines[start:end] (start
// its heading line, as findSection returns them) for every op but
// PatchReplace, and returns the new body with the index of the content's
// first line in it and the content's line count. The blank lines at the
// insertion point are replaced by the seams the package comment
// describes.
func insertIntoSection(lines []string, start, end int, op PatchOp, content string) (body string, first, n int) {
	isHeading := make(map[int]bool)
	for _, h := range scanHeadings(lines) {
		isHeading[h.idx] = true
	}
	// SplitAfter leaves an empty element after a trailing newline; it is
	// not a line of the note.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	end = min(end, len(lines))
	eol := "\n"
	if strings.HasSuffix(lines[start], "\r\n") {
		eol = "\r\n"
	}
	core, lead, trail := contentLines(content, eol)

	// The insertion point is a run of blank lines lines[gapFrom:gapTo],
	// possibly empty, between line above (-1 at the start of the body)
	// and line below (len(lines) at the end of the note).
	var above, below int
	switch op {
	case PatchPrepend:
		above, below = start, start+1
		for below < end && isBlank(lines[below]) {
			below++
		}
	case PatchInsertBefore:
		above, below = start-1, start
		for above >= 0 && isBlank(lines[above]) {
			above--
		}
	default: // PatchAppend, PatchInsertAfter
		above, below = end-1, end
		for above > start && isBlank(lines[above]) {
			above--
		}
	}

	// Blank lines between the line above and the content, and between
	// the content and the line below.
	sepAbove, sepBelow := 0, 0
	switch {
	case above < 0:
	case op == PatchInsertBefore || op == PatchInsertAfter,
		isHeading[above], isHeadingLine(core[0]):
		sepAbove = 1
	default: // append after ordinary text: the content's own blank lines
		sepAbove = lead
	}
	switch {
	case below >= len(lines):
	case op == PatchInsertBefore || op == PatchInsertAfter,
		isHeading[below], isHeadingLine(core[len(core)-1]):
		sepBelow = 1
	default: // prepend before ordinary text: the content's own blank lines
		sepBelow = trail
	}

	var b strings.Builder
	for _, l := range lines[:above+1] {
		b.WriteString(l)
	}
	if above >= 0 && !strings.HasSuffix(lines[above], "\n") {
		b.WriteString(eol) // the note's last line had no newline
	}
	b.WriteString(strings.Repeat(eol, sepAbove))
	for _, l := range core {
		b.WriteString(l)
	}
	b.WriteString(strings.Repeat(eol, sepBelow))
	for _, l := range lines[below:] {
		b.WriteString(l)
	}
	return b.String(), above + 1 + sepAbove, len(core)
}

// contentLines splits content into its lines, each ending in a newline
// (eol for the last one when content has none), without the blank lines
// it starts and ends with; lead and trail count those.
func contentLines(content, eol string) (core []string, lead, trail int) {
	lines := strings.SplitAfter(content, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for lead < len(lines) && isBlank(lines[lead]) {
		lead++
	}
	last := len(lines)
	for last > lead && isBlank(lines[last-1]) {
		last--
	}
	trail = len(lines) - last
	core = lines[lead:last]
	if l := core[len(core)-1]; !strings.HasSuffix(l, "\n") {
		core[len(core)-1] = l + eol
	}
	return core, lead, trail
}

func isBlank(line string) bool { return strings.TrimSpace(line) == "" }

// isHeadingLine reports whether line, a line of inserted content, is an
// ATX heading.
func isHeadingLine(line string) bool {
	return headingRe.MatchString(strings.TrimRight(line, "\r\n"))
}
