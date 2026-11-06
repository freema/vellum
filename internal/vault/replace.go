package vault

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Exact find-and-replace inside a note (NSO-574).
//
// Replace is the small-edit counterpart of Patch: instead of sending a
// whole section back, the caller names the exact text to change. The match
// is byte for byte (no regular expressions, no whitespace tolerance) over
// the whole file, frontmatter included, so the line numbers it reports are
// the ones read_note and search_notes use.

var (
	// ErrEmptyOldText rejects a replacement without text to find: an empty
	// string matches everywhere and nowhere in particular.
	ErrEmptyOldText = errors.New("old_text must not be empty")
	// ErrTextNotFound means the text to replace does not occur in the note.
	ErrTextNotFound = errors.New("text not found")
	// ErrTextNotUnique means the text occurs more than once and the caller
	// did not ask to replace every occurrence.
	ErrTextNotUnique = errors.New("text occurs more than once")
)

// LineSpan is a range of file lines, 1-based and inclusive, counted over
// the whole file (frontmatter included).
type LineSpan struct {
	First, Last int
}

// ReplaceResult describes a successful Replace.
type ReplaceResult struct {
	Content string     // the note as written
	Hash    string     // sha256 hex of Content, the next expected_hash
	Count   int        // occurrences replaced
	Spans   []LineSpan // per replacement, the lines its new text occupies, in file order
}

// Replace swaps oldText for newText in the note at path. Without all,
// oldText must occur exactly once; with all, every non-overlapping
// occurrence is replaced. An empty newText deletes the text. Nothing is
// written when the hash does not match (ErrConflict), the text is missing
// (ErrTextNotFound), ambiguous (ErrTextNotUnique, naming the lines of
// every occurrence) or the result would exceed the size limit
// (ErrTooLarge).
func (v *Vault) Replace(path, oldText, newText string, all bool, expectedHash string) (*ReplaceResult, error) {
	if oldText == "" {
		return nil, ErrEmptyOldText
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

	content := note.Content
	at := occurrences(content, oldText)
	switch {
	case len(at) == 0:
		hint := ""
		if strings.Contains(content, "\r\n") && strings.Contains(oldText, "\n") && !strings.Contains(oldText, "\r\n") {
			hint = " (the note uses CRLF line endings)"
		}
		return nil, fmt.Errorf("%w in %s%s", ErrTextNotFound, path, hint)
	case len(at) > 1 && !all:
		return nil, fmt.Errorf("%w in %s: %d occurrences, in lines %s",
			ErrTextNotUnique, path, len(at), joinLines(occurrenceLines(content, at)))
	}

	// Rebuild the content and remember where each new text landed.
	var b strings.Builder
	b.Grow(len(content) + len(at)*(len(newText)-len(oldText)))
	starts := make([]int, len(at))
	prev := 0
	for i, o := range at {
		b.WriteString(content[prev:o])
		starts[i] = b.Len()
		b.WriteString(newText)
		prev = o + len(oldText)
	}
	b.WriteString(content[prev:])
	data := b.String()

	if int64(len(data)) > v.maxSize {
		return nil, fmt.Errorf("%w: %s (%d bytes)", ErrTooLarge, path, len(data))
	}
	if err := v.writeAtomic(abs, []byte(data)); err != nil {
		return nil, err
	}

	spans := make([]LineSpan, len(starts))
	line, pos := 1, 0 // line number of byte offset pos in data
	lineAt := func(off int) int {
		line += strings.Count(data[pos:off], "\n")
		pos = off
		return line
	}
	for i, s := range starts {
		first := lineAt(s)
		last := first
		if end := s + len(newText); end > s {
			last = lineAt(end - 1) // the line of the new text's last byte
		}
		spans[i] = LineSpan{First: first, Last: last}
	}
	return &ReplaceResult{Content: data, Hash: hashOf([]byte(data)), Count: len(at), Spans: spans}, nil
}

// occurrences returns the byte offsets of the non-overlapping occurrences
// of sub in s, scanning left to right (what strings.Replace replaces).
func occurrences(s, sub string) []int {
	var at []int
	for off := 0; ; {
		i := strings.Index(s[off:], sub)
		if i < 0 {
			return at
		}
		at = append(at, off+i)
		off += i + len(sub)
	}
}

// occurrenceLines maps ascending byte offsets in s to the distinct 1-based
// lines they start on.
func occurrenceLines(s string, at []int) []int {
	var lines []int
	line, pos := 1, 0
	for _, o := range at {
		line += strings.Count(s[pos:o], "\n")
		pos = o
		if len(lines) == 0 || lines[len(lines)-1] != line {
			lines = append(lines, line)
		}
	}
	return lines
}

func joinLines(lines []int) string {
	s := make([]string, len(lines))
	for i, l := range lines {
		s[i] = strconv.Itoa(l)
	}
	return strings.Join(s, ", ")
}
