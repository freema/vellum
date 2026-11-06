package vault

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// sectionDocs exercise the section boundaries: frontmatter offsets, nested
// levels, headings in fenced code, the last section, a heading as the last
// line, CRLF line ends and a file without a trailing newline.
var sectionDocs = map[string]struct {
	content  string
	sections []string
}{
	"frontmatter": {patchDoc, []string{"Alpha", "beta", "Beta child", "Gamma"}},
	"fenced": {
		"# Top\n\n```md\n## Fake\n```\n\n## Fake\n\nreal\n~~~\n# Not a heading\n~~~\nstill fake section\n\n## Next\nx\n",
		[]string{"Top", "Fake", "Next"},
	},
	"heading-at-eof":         {"---\ntags: [a]\n---\n# A\ntext\n## Last", []string{"A", "Last"}},
	"heading-at-eof-newline": {"# A\ntext\n## Last\n", []string{"A", "Last"}},
	"crlf": {
		"---\r\ntitle: C\r\n---\r\n# Crlf\r\n\r\nintro\r\n\r\n## One\r\none\r\n\r\n## Two\r\ntwo\r\n",
		[]string{"Crlf", "One", "Two"},
	},
	"no-trailing-newline": {"# H\n\n## Mid\nmiddle\n## End\nlast line", []string{"H", "Mid", "End"}},
	"deep": {
		"# T\n## A\n### A1\n#### A1x\ntext\n### A2\n## B\n",
		[]string{"T", "A", "A1", "A1x", "A2", "B"},
	},
}

// TestSectionLinesMatchPatch is the shared contract of read_note's section
// read and patch_note: the lines SectionLines reports are exactly what
// Patch replaces (the heading line stays, everything after it up to the
// section end is swapped), and nothing before or after them changes.
func TestSectionLinesMatchPatch(t *testing.T) {
	const sentinel = "SENTINEL-REPLACEMENT"
	for name, doc := range sectionDocs {
		for _, section := range doc.sections {
			t.Run(name+"/"+section, func(t *testing.T) {
				v := newTestVault(t)
				mustWrite(t, v, "s.md", doc.content)
				first, last, err := SectionLines(doc.content, section)
				if err != nil {
					t.Fatalf("SectionLines: %v", err)
				}
				lines := SplitLines(doc.content)
				if first < 1 || last < first || last > len(lines) {
					t.Fatalf("range %d-%d out of 1-%d", first, last, len(lines))
				}
				heading := strings.TrimRight(lines[first-1], "\r\n")
				if !strings.EqualFold(strings.TrimSpace(strings.TrimLeft(heading, "#")), section) {
					t.Fatalf("line %d = %q, want the %q heading", first, heading, section)
				}

				if err := v.Patch("s.md", section, sentinel, ""); err != nil {
					t.Fatalf("Patch: %v", err)
				}
				note, err := v.Read("s.md")
				if err != nil {
					t.Fatal(err)
				}
				before := strings.Join(lines[:first], "")
				after := strings.Join(lines[last:], "")
				got := note.Content
				if !strings.HasPrefix(got, before) || !strings.HasSuffix(got, after) || len(got) < len(before)+len(after) {
					t.Fatalf("patch touched lines outside %d-%d:\nbefore %q\nafter  %q\ngot    %q", first, last, before, after, got)
				}
				middle := got[len(before) : len(got)-len(after)]
				if strings.TrimLeft(middle, "\n") != sentinel+"\n" {
					t.Fatalf("patched lines %d-%d = %q, want only the replacement", first+1, last, middle)
				}
			})
		}
	}
}

func TestOutline(t *testing.T) {
	doc := sectionDocs["fenced"].content
	got := Outline("---\ntitle: X\ntags: [a, b]\n---\n" + doc)
	want := []Heading{
		{Level: 1, Text: "Top", Line: 5},
		{Level: 2, Text: "Fake", Line: 11},
		{Level: 2, Text: "Next", Line: 19},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Outline = %+v, want %+v", got, want)
	}
	if got := Outline("no headings\n"); len(got) != 0 {
		t.Errorf("Outline without headings = %+v", got)
	}
	crlf := Outline(sectionDocs["crlf"].content)
	if len(crlf) != 3 || crlf[1] != (Heading{Level: 2, Text: "One", Line: 8}) {
		t.Errorf("CRLF outline = %+v", crlf)
	}
	// The outline's line numbers point at the heading lines of the file.
	lines := SplitLines(patchDoc)
	for _, h := range Outline(patchDoc) {
		if l := lines[h.Line-1]; !strings.HasSuffix(strings.TrimSpace(l), h.Text) || !strings.HasPrefix(l, "#") {
			t.Errorf("heading %+v points at %q", h, l)
		}
	}
}

func TestSectionNotFoundListsHeadings(t *testing.T) {
	_, _, err := SectionLines(patchDoc, "Delta")
	if !errors.Is(err, ErrSectionNotFound) {
		t.Fatalf("err = %v, want ErrSectionNotFound", err)
	}
	for _, want := range []string{`"Delta"`, `"Alpha"`, `"Beta"`, `"Beta child"`, `"Gamma"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q misses %s", err, want)
		}
	}
	if _, _, err := SectionLines("plain text\n", "x"); !errors.Is(err, ErrSectionNotFound) || !strings.Contains(err.Error(), "no headings") {
		t.Errorf("no-heading note: %v", err)
	}
	var many strings.Builder
	for i := range maxListedHeadings + 5 {
		many.WriteString("## H" + strings.Repeat("x", i%3) + "\n")
	}
	if _, _, err := SectionLines(many.String(), "missing"); err == nil || !strings.Contains(err.Error(), "and 5 more") {
		t.Errorf("long heading list not capped: %v", err)
	}
}

func TestSplitLines(t *testing.T) {
	for in, want := range map[string][]string{
		"":         nil,
		"a":        {"a"},
		"a\n":      {"a\n"},
		"a\nb":     {"a\n", "b"},
		"a\r\nb\n": {"a\r\n", "b\n"},
		"\n\n":     {"\n", "\n"},
	} {
		if got := SplitLines(in); !reflect.DeepEqual(got, want) {
			t.Errorf("SplitLines(%q) = %q, want %q", in, got, want)
		}
		// Search numbers lines with strings.Split(raw, "\n"); line i of
		// SplitLines must be line i there, minus the terminator.
		split := strings.Split(in, "\n")
		for i, l := range SplitLines(in) {
			if strings.TrimSuffix(l, "\n") != split[i] {
				t.Errorf("SplitLines(%q)[%d] = %q, search line %q", in, i, l, split[i])
			}
		}
	}
}
