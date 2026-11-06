package vault

import (
	"fmt"
	"strings"
)

// Heading is one markdown heading of a note, as read_note's outline
// reports it.
type Heading struct {
	Level int    `json:"level"` // 1 for "#", up to 6
	Text  string `json:"text"`
	Line  int    `json:"line"` // 1-based file line, frontmatter counted
}

// bodyHeading is a heading found in a slice of lines: idx is its index in
// that slice.
type bodyHeading struct {
	idx, level int
	text       string
}

// scanHeadings lists the ATX headings of lines (as split by
// strings.SplitAfter(body, "\n")), skipping fenced code blocks. It is the
// one place that decides what counts as a heading for sections, so reading
// a section and patching it cannot disagree.
func scanHeadings(lines []string) []bodyHeading {
	var out []bodyHeading
	inFence := false
	for i, line := range lines {
		if fenceRe.MatchString(line) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		m := headingRe.FindStringSubmatch(strings.TrimRight(line, "\r\n"))
		if m == nil {
			continue
		}
		out = append(out, bodyHeading{
			idx:   i,
			level: len(line) - len(strings.TrimLeft(line, "#")),
			text:  m[1],
		})
	}
	return out
}

// findSection locates the section named by section in lines (a note body
// as split by strings.SplitAfter(body, "\n")). section is a heading's text
// (case-insensitive, surrounding space ignored) or, when no heading has
// that text, a path of heading texts separated by ">", outermost first,
// such as "Project A > Notes": the last part names the heading, the parts
// before it name some of its enclosing headings in order (levels in
// between may be skipped). A heading whose text itself contains ">" is
// found by its whole text first, and as one step of a path.
//
// start is the index of the heading line in lines, end the index one past
// the section's last line: the next heading of the same or a higher level,
// or len(lines). A section no heading matches fails with
// ErrSectionNotFound naming the headings the note has; one that matches
// more than one heading fails with ErrSectionAmbiguous naming every
// candidate's full path and file line, so nothing is read or written by
// guess. offset is the number of file lines before lines (the
// frontmatter), for the line numbers in the errors.
func findSection(lines []string, offset int, section string) (start, end int, err error) {
	hs := scanHeadings(lines)
	parents := headingParents(hs)
	matches := matchSection(hs, parents, section)
	switch len(matches) {
	case 0:
		return -1, -1, sectionNotFound(section, toHeadings(hs, offset))
	case 1:
	default:
		return -1, -1, sectionAmbiguous(section, hs, parents, matches, offset)
	}
	i := matches[0]
	start, end = hs[i].idx, len(lines)
	for _, h := range hs[i+1:] {
		if h.level <= hs[i].level {
			end = h.idx
			break
		}
	}
	return start, end, nil
}

// headingParents returns, per heading of hs, the indexes in hs of its
// enclosing headings (each of a lower level than the next), outermost
// first.
func headingParents(hs []bodyHeading) [][]int {
	out := make([][]int, len(hs))
	var stack []int
	for i, h := range hs {
		for len(stack) > 0 && hs[stack[len(stack)-1]].level >= h.level {
			stack = stack[:len(stack)-1]
		}
		out[i] = append([]int(nil), stack...)
		stack = append(stack, i)
	}
	return out
}

// matchSection returns the indexes in hs of the headings section names:
// those whose whole text equals it, or, when there are none and section
// contains ">", those its heading path matches.
func matchSection(hs []bodyHeading, parents [][]int, section string) []int {
	want := strings.TrimSpace(section)
	var out []int
	for i, h := range hs {
		if strings.EqualFold(strings.TrimSpace(h.text), want) {
			out = append(out, i)
		}
	}
	if len(out) > 0 || !strings.Contains(want, ">") {
		return out
	}
	path := pathParts(want)
	for _, p := range path {
		if p == "" { // "A >", "> B" or "A >> B" name no heading
			return nil
		}
	}
	for i, h := range hs {
		rest, ok := trimPartsSuffix(path, pathParts(h.text))
		if !ok {
			continue
		}
		// The remaining parts must name enclosing headings, innermost
		// last; levels in between may be skipped.
		for j := len(parents[i]) - 1; j >= 0 && len(rest) > 0; j-- {
			if r, ok := trimPartsSuffix(rest, pathParts(hs[parents[i][j]].text)); ok {
				rest = r
			}
		}
		if len(rest) == 0 {
			out = append(out, i)
		}
	}
	return out
}

// pathParts splits a heading path (or a heading text) at ">" and trims
// each part.
func pathParts(s string) []string {
	parts := strings.Split(s, ">")
	for i, p := range parts {
		parts[i] = strings.TrimSpace(p)
	}
	return parts
}

// trimPartsSuffix reports whether parts ends with suffix (case-insensitive)
// and returns what precedes it.
func trimPartsSuffix(parts, suffix []string) ([]string, bool) {
	if len(suffix) > len(parts) {
		return nil, false
	}
	head := len(parts) - len(suffix)
	for i, s := range suffix {
		if !strings.EqualFold(parts[head+i], s) {
			return nil, false
		}
	}
	return parts[:head], true
}

// headingPath is the full heading path of hs[i], as section accepts it.
func headingPath(hs []bodyHeading, parents [][]int, i int) string {
	names := make([]string, 0, len(parents[i])+1)
	for _, p := range parents[i] {
		names = append(names, strings.TrimSpace(hs[p].text))
	}
	return strings.Join(append(names, strings.TrimSpace(hs[i].text)), " > ")
}

func toHeadings(hs []bodyHeading, offset int) []Heading {
	out := make([]Heading, len(hs))
	for i, h := range hs {
		out[i] = Heading{Level: h.level, Text: h.text, Line: offset + h.idx + 1}
	}
	return out
}

// bodyOffset is the number of file lines before the body: the frontmatter
// block including its fences.
func bodyOffset(content string) (body string, offset int) {
	_, body = splitFrontmatter(content)
	return body, strings.Count(content[:len(content)-len(body)], "\n")
}

// Outline lists the headings of a note's body with their file line numbers
// (1-based, frontmatter counted, the numbering search snippets use).
// Headings inside fenced code blocks are skipped.
func Outline(content string) []Heading {
	body, offset := bodyOffset(content)
	return toHeadings(scanHeadings(strings.SplitAfter(body, "\n")), offset)
}

// SectionLines returns the file lines (1-based, inclusive, frontmatter
// counted) of the section patch_note would edit: its heading line and
// everything under it up to the next heading of the same or a higher level.
// section may be a heading path ("Parent > Child"). A missing section
// fails with ErrSectionNotFound, naming the headings the note does have,
// one that names several headings with ErrSectionAmbiguous, naming them.
func SectionLines(content, section string) (first, last int, err error) {
	body, offset := bodyOffset(content)
	lines := strings.SplitAfter(body, "\n")
	start, end, err := findSection(lines, offset, section)
	if err != nil {
		return 0, 0, err
	}
	// SplitAfter leaves an empty element after a trailing newline; it is
	// not a line of the file.
	if end == len(lines) && lines[end-1] == "" {
		end--
	}
	return offset + start + 1, offset + end, nil
}

// maxListedHeadings caps the headings named in a section-not-found error.
const maxListedHeadings = 50

func sectionNotFound(section string, hs []Heading) error {
	if len(hs) == 0 {
		return fmt.Errorf("%w: %q (the note has no headings)", ErrSectionNotFound, section)
	}
	names := make([]string, 0, min(len(hs), maxListedHeadings))
	for _, h := range hs[:min(len(hs), maxListedHeadings)] {
		names = append(names, fmt.Sprintf("%q", h.Text))
	}
	more := ""
	if len(hs) > maxListedHeadings {
		more = fmt.Sprintf(" and %d more", len(hs)-maxListedHeadings)
	}
	return fmt.Errorf("%w: %q; headings: %s%s", ErrSectionNotFound, section, strings.Join(names, ", "), more)
}

// sectionAmbiguous names every heading section matched, with its full
// heading path and file line. The paths are what section accepts to tell
// the headings apart, unless two of them are the same.
func sectionAmbiguous(section string, hs []bodyHeading, parents [][]int, matches []int, offset int) error {
	shown := matches[:min(len(matches), maxListedHeadings)]
	names := make([]string, 0, len(shown))
	seen := make(map[string]bool, len(matches))
	unique := true
	for _, i := range matches {
		p := strings.ToLower(headingPath(hs, parents, i))
		unique = unique && !seen[p]
		seen[p] = true
	}
	for _, i := range shown {
		names = append(names, fmt.Sprintf("%q (line %d)", headingPath(hs, parents, i), offset+hs[i].idx+1))
	}
	more := ""
	if len(matches) > len(shown) {
		more = fmt.Sprintf(" and %d more", len(matches)-len(shown))
	}
	hint := "pass one of these heading paths as section"
	if !unique {
		hint = "some of them share the same heading path, so section cannot tell those apart"
	}
	return fmt.Errorf("%w: %q matches %d headings: %s%s; %s",
		ErrSectionAmbiguous, section, len(matches), strings.Join(names, ", "), more, hint)
}

// SplitLines splits content into file lines the way search numbers them
// (strings.Split on "\n"), but keeps each line's terminator so a slice of
// lines joins back into the exact bytes of the file. A trailing newline
// ends the last line rather than starting an empty one.
func SplitLines(content string) []string {
	if content == "" {
		return nil
	}
	lines := strings.SplitAfter(content, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}
