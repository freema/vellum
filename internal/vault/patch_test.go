package vault

import (
	"errors"
	"strings"
	"testing"
)

// legacyPatch is patch_note's section replace as it was before NSO-575:
// the first heading whose text matches (outside fenced code), its body up
// to the next heading of the same or a higher level swapped for the new
// content. TestPatchReplaceUnchanged holds the new code to its bytes.
func legacyPatch(content, section, newContent string) (string, bool) {
	_, body := splitFrontmatter(content)
	prefix := content[:len(content)-len(body)]
	lines := strings.SplitAfter(body, "\n")
	start, end, level := -1, len(lines), 0
	for _, h := range scanHeadings(lines) {
		if start == -1 {
			if strings.EqualFold(strings.TrimSpace(h.text), strings.TrimSpace(section)) {
				start, level = h.idx, h.level
			}
		} else if h.level <= level {
			end = h.idx
			break
		}
	}
	if start == -1 {
		return "", false
	}
	var b strings.Builder
	for _, l := range lines[:start+1] {
		b.WriteString(l)
	}
	if !strings.HasSuffix(b.String(), "\n") {
		b.WriteString("\n")
	}
	b.WriteString(strings.TrimRight(newContent, "\n") + "\n")
	for _, l := range lines[end:] {
		b.WriteString(l)
	}
	return prefix + b.String(), true
}

// Acceptance: without the new parameters, a note with unique headings is
// patched byte for byte as before.
func TestPatchReplaceUnchanged(t *testing.T) {
	docs := map[string]struct {
		content  string
		sections []string
	}{"patchDoc": {patchDoc, []string{"Alpha", "beta", " Beta child ", "Gamma"}}}
	for name, doc := range sectionDocs {
		docs[name] = doc
	}
	for name, doc := range docs {
		for _, section := range doc.sections {
			for _, content := range []string{"NEW", "", "two\nlines\n\n\n", "\nleading blank"} {
				want, ok := legacyPatch(doc.content, section, content)
				if !ok {
					t.Fatalf("%s/%s: legacy patch found no section", name, section)
				}
				v := newTestVault(t)
				mustWrite(t, v, "p.md", doc.content)
				if err := v.Patch("p.md", section, content, ""); err != nil {
					t.Fatalf("%s/%s: %v", name, section, err)
				}
				v2 := newTestVault(t)
				mustWrite(t, v2, "p.md", doc.content)
				r, err := v2.PatchSection("p.md", section, "", content, sha(doc.content))
				if err != nil {
					t.Fatalf("%s/%s PatchSection: %v", name, section, err)
				}
				got, _ := v.Read("p.md")
				if got.Content != want || r.Content != want {
					t.Errorf("%s/%s %q:\n got %q\n     %q\nwant %q", name, section, content, got.Content, r.Content, want)
				}
				if r.Hash != sha(want) {
					t.Errorf("%s/%s: hash %s, want %s", name, section, r.Hash, sha(want))
				}
			}
		}
	}
}

// twoProjects has two "Poznámky" sections under different parents, and a
// third, fake one inside a code block.
const twoProjects = `---
title: Projekty
---
# Projekty

## Projekt A

a intro

### Poznámky

a notes

## Projekt B

### Detail

#### Poznámky

b notes

` + "```md\n### Poznámky\n```\n"

func patchErr(t *testing.T, content, section string, op PatchOp, newContent string) error {
	t.Helper()
	v := newTestVault(t)
	mustWrite(t, v, "p.md", content)
	_, err := v.PatchSection("p.md", section, op, newContent, "")
	if err == nil {
		t.Fatalf("PatchSection(%q, %s) succeeded, want an error", section, op)
	}
	if got, _ := v.Read("p.md"); got.Content != content {
		t.Fatalf("a failed patch wrote the note:\n%s", got.Content)
	}
	return err
}

// Acceptance: two sections of the same name without a path fail with both
// candidates and leave the file as it was; the fenced heading is not one.
func TestPatchAmbiguousSection(t *testing.T) {
	for _, op := range PatchOps {
		err := patchErr(t, twoProjects, "poznámky", op, "x")
		if !errors.Is(err, ErrSectionAmbiguous) {
			t.Fatalf("%s: err = %v, want ErrSectionAmbiguous", op, err)
		}
		msg := err.Error()
		for _, want := range []string{
			`matches 2 headings`,
			`"Projekty > Projekt A > Poznámky" (line 10)`,
			`"Projekty > Projekt B > Detail > Poznámky" (line 18)`,
			`pass one of these heading paths as section`,
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("%s: error %q misses %s", op, msg, want)
			}
		}
	}

	// Headings with the same full path cannot be told apart by section;
	// the error says so instead of suggesting a path.
	err := patchErr(t, "# Log\n## Day\none\n## Day\ntwo\n", "Log > Day", PatchAppend, "x")
	if !errors.Is(err, ErrSectionAmbiguous) || !strings.Contains(err.Error(), "same heading path") {
		t.Errorf("same-path candidates: %v", err)
	}

	// read_note's section read shares the lookup.
	if _, _, err := SectionLines(twoProjects, "Poznámky"); !errors.Is(err, ErrSectionAmbiguous) {
		t.Errorf("SectionLines = %v, want ErrSectionAmbiguous", err)
	}
}

// Acceptance: a heading path picks the right one of the same-named
// sections; parts are case-insensitive and levels in between may be
// skipped.
func TestPatchSectionPath(t *testing.T) {
	// The heading line of each section and the line after its end
	// (1-based); 0 means the path names no heading.
	lines := SplitLines(twoProjects)
	type span struct{ heading, next int }
	a, b := span{10, 14}, span{18, len(lines) + 1}
	for section, want := range map[string]span{
		"Projekt A > Poznámky":                        a,
		"projekt b>poznámky":                          b,
		"Projekt B > Detail > Poznámky":               b,
		"Projekty > Projekt A > Poznámky":             a,
		"  Projekty >  Projekt B > Poznámky  ":        b,
		"Projekty > Projekt B > Detail > Poznámky":    b,
		"Projekty > Projekt A > Projekt A > Poznámky": {},
	} {
		v := newTestVault(t)
		mustWrite(t, v, "p.md", twoProjects)
		res, err := v.PatchSection("p.md", section, PatchReplace, "REPLACED", "")
		if want.heading == 0 {
			if !errors.Is(err, ErrSectionNotFound) {
				t.Errorf("%q: err = %v, want ErrSectionNotFound", section, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%q: %v", section, err)
		}
		wantContent := strings.Join(lines[:want.heading], "") + "REPLACED\n" + strings.Join(lines[want.next-1:], "")
		if res.Content != wantContent {
			t.Errorf("%q:\n got %q\nwant %q", section, res.Content, wantContent)
		}
		if res.Lines != (LineSpan{want.heading + 1, want.heading + 1}) {
			t.Errorf("%q: lines %+v, want line %d", section, res.Lines, want.heading+1)
		}
	}

	for _, section := range []string{"Projekt A >", "> Poznámky", "Projekt A >> Poznámky", "Projekt C > Poznámky", "Projekt B > Projekt A > Poznámky"} {
		if err := patchErr(t, twoProjects, section, PatchReplace, "x"); !errors.Is(err, ErrSectionNotFound) {
			t.Errorf("%q: err = %v, want ErrSectionNotFound", section, err)
		}
	}
}

// A heading that itself contains ">" is found by its whole text first, and
// it can be one step of a path.
func TestPatchHeadingWithSeparator(t *testing.T) {
	doc := "# Inputs > Outputs\n\nio\n\n## in > out\n\nab\n\n# A\n\n## B\n\nb\n"
	for section, wantBody := range map[string]string{
		"Inputs > Outputs":              "io",
		"inputs>outputs":                "io",
		"in > out":                      "ab",
		"Inputs > Outputs > in > out":   "ab",
		"A > B":                         "b",
		"Inputs > Outputs > in>out":     "ab",
		"Inputs>Outputs > in > out    ": "ab",
	} {
		v := newTestVault(t)
		mustWrite(t, v, "p.md", doc)
		first, last, err := SectionLines(doc, section)
		if err != nil {
			t.Fatalf("%q: %v", section, err)
		}
		lines := SplitLines(doc)
		if body := strings.TrimSpace(strings.Join(lines[first:last], "")); !strings.HasPrefix(body, wantBody) {
			t.Errorf("%q: section body %q, want it to start with %q", section, body, wantBody)
		}
		if _, err := v.PatchSection("p.md", section, PatchAppend, "more", ""); err != nil {
			t.Errorf("%q: PatchSection: %v", section, err)
		}
	}
}

// patchCase runs one PatchSection and checks the note it writes and the
// line range it reports: those lines hold the content as sent, without
// its blank edge lines.
func patchCase(t *testing.T, content, section string, op PatchOp, add, want string) {
	t.Helper()
	v := newTestVault(t)
	mustWrite(t, v, "p.md", content)
	res, err := v.PatchSection("p.md", section, op, add, sha(content))
	if err != nil {
		t.Fatalf("%s %q: %v", op, section, err)
	}
	if res.Content != want {
		t.Fatalf("%s %q:\n got %q\nwant %q", op, section, res.Content, want)
	}
	if got, _ := v.Read("p.md"); got.Content != want || got.Hash != res.Hash {
		t.Fatalf("%s: disk %q (hash %s), result hash %s", op, got.Content, got.Hash, res.Hash)
	}
	lines := SplitLines(want)
	if res.Lines.First < 1 || res.Lines.Last < res.Lines.First || res.Lines.Last > len(lines) {
		t.Fatalf("%s: lines %+v out of 1-%d", op, res.Lines, len(lines))
	}
	got := strings.Join(lines[res.Lines.First-1:res.Lines.Last], "")
	core := strings.Trim(strings.ReplaceAll(add, "\r\n", "\n"), "\n")
	if op == PatchReplace {
		core = strings.TrimRight(add, "\n")
	}
	if strings.TrimRight(strings.ReplaceAll(got, "\r\n", "\n"), "\n") != core {
		t.Errorf("%s: lines %d-%d = %q, want the content %q", op, res.Lines.First, res.Lines.Last, got, core)
	}
}

const opsDoc = "---\ntitle: T\n---\n# Top\n\nintro\n\n## A\n\na1\na2\n\n## B\nb1\n"

func TestPatchAppend(t *testing.T) {
	// Acceptance: append adds at the end of the section and leaves the
	// next heading in place, one blank line above it.
	patchCase(t, opsDoc, "A", PatchAppend, "- new",
		"---\ntitle: T\n---\n# Top\n\nintro\n\n## A\n\na1\na2\n- new\n\n## B\nb1\n")
	// A blank line the content starts with opens a new paragraph; extra
	// blank lines before the next heading are not doubled.
	patchCase(t, "## A\na1\n\n\n\n## B\n", "A", PatchAppend, "\npara\n\n\n",
		"## A\na1\n\npara\n\n## B\n")
	// The last section: content goes to the end of the note.
	patchCase(t, opsDoc, "B", PatchAppend, "b2", opsDoc+"b2\n")
	// No trailing newline in the note.
	patchCase(t, "## B\nb1", "B", PatchAppend, "b2", "## B\nb1\nb2\n")
	// An empty section: one blank line under its heading and above the
	// next.
	patchCase(t, "## A\n## B\n", "A", PatchAppend, "x", "## A\n\nx\n\n## B\n")
	patchCase(t, "## A", "A", PatchAppend, "x", "## A\n\nx\n")
	// Content that is a heading is set off from text above.
	patchCase(t, "## A\na1\n## B\n", "A", PatchAppend, "### A2\nmore", "## A\na1\n\n### A2\nmore\n\n## B\n")
	// The section's subsections are part of it.
	patchCase(t, "## A\na\n### A1\na1\n## B\n", "A", PatchAppend, "x", "## A\na\n### A1\na1\nx\n\n## B\n")
	// The top-level section ends at the end of the note.
	patchCase(t, opsDoc, "Top", PatchAppend, "end", opsDoc+"end\n")
}

func TestPatchPrepend(t *testing.T) {
	patchCase(t, opsDoc, "A", PatchPrepend, "first",
		"---\ntitle: T\n---\n# Top\n\nintro\n\n## A\n\nfirst\na1\na2\n\n## B\nb1\n")
	// Compact style: still one blank line under the heading.
	patchCase(t, opsDoc, "B", PatchPrepend, "b0\n\n", opsDoc[:len(opsDoc)-len("b1\n")]+"\nb0\n\nb1\n")
	// Before a subsection heading and in an empty section.
	patchCase(t, "## A\n\n\n### A1\nx\n", "A", PatchPrepend, "\n\nlead\n", "## A\n\nlead\n\n### A1\nx\n")
	patchCase(t, "## A\n\n## B\n", "A", PatchPrepend, "x", "## A\n\nx\n\n## B\n")
	patchCase(t, "## A\n", "A", PatchPrepend, "x", "## A\n\nx\n")
}

func TestPatchInsertBefore(t *testing.T) {
	patchCase(t, opsDoc, "B", PatchInsertBefore, "## New\nnew",
		"---\ntitle: T\n---\n# Top\n\nintro\n\n## A\n\na1\na2\n\n## New\nnew\n\n## B\nb1\n")
	// The first heading of the body, right under the frontmatter.
	patchCase(t, opsDoc, "Top", PatchInsertBefore, "\n\nPreface.\n\n",
		"---\ntitle: T\n---\nPreface.\n\n# Top\n\nintro\n\n## A\n\na1\na2\n\n## B\nb1\n")
	// Text directly above the heading gets one blank line.
	patchCase(t, "text\n## A\n", "A", PatchInsertBefore, "## Z", "text\n\n## Z\n\n## A\n")
}

func TestPatchInsertAfter(t *testing.T) {
	// Acceptance: after the last section the content lands at the end of
	// the note.
	patchCase(t, opsDoc, "B", PatchInsertAfter, "## C\nc1", opsDoc+"\n## C\nc1\n")
	patchCase(t, "## B\nb1\n\n\n", "B", PatchInsertAfter, "## C", "## B\nb1\n\n## C\n")
	patchCase(t, "## B\nb1", "B", PatchInsertAfter, "## C", "## B\nb1\n\n## C\n")
	// Between two sections, after the section's subsections.
	patchCase(t, opsDoc, "A", PatchInsertAfter, "## A2\nx",
		"---\ntitle: T\n---\n# Top\n\nintro\n\n## A\n\na1\na2\n\n## A2\nx\n\n## B\nb1\n")
	patchCase(t, "## A\na\n### A1\na1\n## B\n", "A", PatchInsertAfter, "## A2", "## A\na\n### A1\na1\n\n## A2\n\n## B\n")
}

// Acceptance: headings inside fenced code are still ignored, both when
// finding the section and when deciding where it ends.
func TestPatchOpsIgnoreFencedHeadings(t *testing.T) {
	doc := "## Notes\nreal\n```\n## Notes\n## Other\n```\n## Next\n"
	patchCase(t, doc, "Notes", PatchAppend, "x", "## Notes\nreal\n```\n## Notes\n## Other\n```\nx\n\n## Next\n")
	patchCase(t, doc, "Notes", PatchInsertAfter, "## Mid", "## Notes\nreal\n```\n## Notes\n## Other\n```\n\n## Mid\n\n## Next\n")
	if err := patchErr(t, doc, "Other", PatchAppend, "x"); !errors.Is(err, ErrSectionNotFound) {
		t.Errorf("fenced heading as section: %v", err)
	}
}

// A CRLF note gets CRLF separator lines.
func TestPatchCRLF(t *testing.T) {
	doc := "---\r\ntitle: C\r\n---\r\n## One\r\none\r\n\r\n## Two\r\ntwo\r\n"
	patchCase(t, doc, "One", PatchAppend, "more", "---\r\ntitle: C\r\n---\r\n## One\r\none\r\nmore\r\n\r\n## Two\r\ntwo\r\n")
	patchCase(t, doc, "Two", PatchInsertBefore, "## Mid\r\n", "---\r\ntitle: C\r\n---\r\n## One\r\none\r\n\r\n## Mid\r\n\r\n## Two\r\ntwo\r\n")
}

func TestPatchSectionErrors(t *testing.T) {
	for _, op := range []PatchOp{PatchAppend, PatchPrepend, PatchInsertBefore, PatchInsertAfter} {
		if err := patchErr(t, opsDoc, "A", op, " \n\n"); !errors.Is(err, ErrEmptyContent) {
			t.Errorf("%s with empty content: %v", op, err)
		}
	}
	if err := patchErr(t, opsDoc, "A", "upsert", "x"); !errors.Is(err, ErrPatchOp) {
		t.Errorf("unknown op: %v", err)
	}
	v := newTestVault(t)
	mustWrite(t, v, "p.md", opsDoc)
	if _, err := v.PatchSection("p.md", "A", PatchAppend, "x", sha("stale")); !errors.Is(err, ErrConflict) {
		t.Errorf("stale hash: %v", err)
	}
	if _, err := v.PatchSection("p.md", "A", PatchAppend, strings.Repeat("x", DefaultMaxFileSize), ""); !errors.Is(err, ErrTooLarge) {
		t.Errorf("oversized: %v", err)
	}
	if err := patchErr(t, opsDoc, "Missing", PatchInsertAfter, "x"); !errors.Is(err, ErrSectionNotFound) || !strings.Contains(err.Error(), `"Top"`) {
		t.Errorf("missing section: %v", err)
	}
	// Replacing with nothing still empties the section, as before.
	patchCase(t, "## A\na\n## B\n", "A", PatchReplace, "", "## A\n\n## B\n")
}

func TestParsePatchOp(t *testing.T) {
	for in, want := range map[string]PatchOp{"": PatchReplace, "replace": PatchReplace, "append": PatchAppend, "insert_after": PatchInsertAfter} {
		if got, err := ParsePatchOp(in); err != nil || got != want {
			t.Errorf("ParsePatchOp(%q) = %q, %v", in, got, err)
		}
	}
	_, err := ParsePatchOp("Append")
	if !errors.Is(err, ErrPatchOp) || !strings.Contains(err.Error(), "replace, append, prepend, insert_before, insert_after") {
		t.Errorf("ParsePatchOp(Append) = %v", err)
	}
}
