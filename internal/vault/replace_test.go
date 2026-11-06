package vault

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// readRaw returns a note's bytes straight from disk, bypassing the vault.
func readRaw(t *testing.T, v *Vault, path string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(v.Root(), filepath.FromSlash(path)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestReplaceOnce(t *testing.T) {
	v := newTestVault(t)
	orig := "# Plan\n\nline one\nThe date is 14 March.\nline three\n"
	mustWrite(t, v, "a.md", orig)

	res, err := v.Replace("a.md", "14 March", "21 March", false, sha(orig))
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(orig, "14 March", "21 March", 1)
	if got := readRaw(t, v, "a.md"); got != want {
		t.Fatalf("file = %q, want %q", got, want)
	}
	if res.Content != want || res.Hash != sha(want) || res.Count != 1 {
		t.Errorf("result = %+v", res)
	}
	if !reflect.DeepEqual(res.Spans, []LineSpan{{4, 4}}) {
		t.Errorf("spans = %v, want line 4", res.Spans)
	}
	if n, _ := v.Read("a.md"); n.Hash != res.Hash {
		t.Errorf("reported hash %s, read_note would say %s", res.Hash, n.Hash)
	}
}

// Exact match only: no regular expressions, no whitespace tolerance.
func TestReplaceIsExact(t *testing.T) {
	v := newTestVault(t)
	orig := "a.b  c\nprice (x)\n"
	mustWrite(t, v, "a.md", orig)

	for _, old := range []string{"a.b c", "a?b", "A.b", "price \\(x\\)", "a.b  c\n\n"} {
		if _, err := v.Replace("a.md", old, "Z", false, ""); !errors.Is(err, ErrTextNotFound) {
			t.Errorf("Replace(%q) = %v, want ErrTextNotFound", old, err)
		}
	}
	if _, err := v.Replace("a.md", "price (x)", "price [y]", false, ""); err != nil {
		t.Fatal(err)
	}
	if got := readRaw(t, v, "a.md"); got != "a.b  c\nprice [y]\n" {
		t.Errorf("file = %q", got)
	}
}

func TestReplaceAmbiguousNamesEveryLine(t *testing.T) {
	v := newTestVault(t)
	orig := "---\ntags: [todo]\n---\n# todo list\n\nnothing\ntodo: call\nnothing\ntodo todo\n"
	mustWrite(t, v, "a.md", orig)

	_, err := v.Replace("a.md", "todo", "done", false, "")
	if !errors.Is(err, ErrTextNotUnique) {
		t.Fatalf("err = %v, want ErrTextNotUnique", err)
	}
	// Lines over the whole file, frontmatter counted; line 9 holds two
	// occurrences and is named once.
	if msg := err.Error(); !strings.Contains(msg, "5 occurrences, in lines 2, 4, 7, 9") {
		t.Errorf("err = %q, want the lines of every occurrence", msg)
	}
	if got := readRaw(t, v, "a.md"); got != orig {
		t.Error("an ambiguous replace wrote the note")
	}
}

func TestReplaceAll(t *testing.T) {
	v := newTestVault(t)
	orig := "x\nfoo\ny\nfoo foo\nz\n"
	mustWrite(t, v, "a.md", orig)

	res, err := v.Replace("a.md", "foo", "bar\nbaz", true, "")
	if err != nil {
		t.Fatal(err)
	}
	want := "x\nbar\nbaz\ny\nbar\nbaz bar\nbaz\nz\n"
	if got := readRaw(t, v, "a.md"); got != want {
		t.Fatalf("file = %q, want %q", got, want)
	}
	if res.Count != 3 {
		t.Errorf("count = %d, want 3", res.Count)
	}
	if want := []LineSpan{{2, 3}, {5, 6}, {6, 7}}; !reflect.DeepEqual(res.Spans, want) {
		t.Errorf("spans = %v, want %v", res.Spans, want)
	}

	// Non-overlapping, left to right, like strings.ReplaceAll.
	mustWrite(t, v, "b.md", "aaaa\n")
	if res, err := v.Replace("b.md", "aa", "b", true, ""); err != nil || res.Count != 2 {
		t.Fatalf("Replace(aa) = %+v, %v", res, err)
	}
	if got := readRaw(t, v, "b.md"); got != "bb\n" {
		t.Errorf("file = %q", got)
	}
}

func TestReplaceDeletes(t *testing.T) {
	v := newTestVault(t)
	mustWrite(t, v, "a.md", "one\ntwo\nthree\n")

	res, err := v.Replace("a.md", "two\n", "", false, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := readRaw(t, v, "a.md"); got != "one\nthree\n" {
		t.Errorf("file = %q", got)
	}
	// The deleted text leaves an empty span at the line that now follows.
	if !reflect.DeepEqual(res.Spans, []LineSpan{{2, 2}}) {
		t.Errorf("spans = %v", res.Spans)
	}
}

func TestReplaceRejectsAndWritesNothing(t *testing.T) {
	v := newTestVault(t, WithMaxFileSize(32))
	orig := "# Note\n\nsmall text\n"
	mustWrite(t, v, "a.md", orig)

	cases := []struct {
		name, old, new, hash string
		want                 error
	}{
		{"empty old_text", "", "x", "", ErrEmptyOldText},
		{"not found", "missing", "x", "", ErrTextNotFound},
		{"stale hash", "small", "big", sha("something else"), ErrConflict},
		{"too large", "small", strings.Repeat("big ", 10), "", ErrTooLarge},
	}
	for _, c := range cases {
		if _, err := v.Replace("a.md", c.old, c.new, false, c.hash); !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.want)
		}
		if got := readRaw(t, v, "a.md"); got != orig {
			t.Fatalf("%s: note written: %q", c.name, got)
		}
	}

	if _, err := v.Replace("missing.md", "a", "b", false, ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing note: err = %v, want ErrNotFound", err)
	}
	if _, err := v.Replace("../x.md", "a", "b", false, ""); !errors.Is(err, ErrInvalidPath) {
		t.Errorf("escaping path: err = %v, want ErrInvalidPath", err)
	}
}

// The search covers the frontmatter, and the line endings are the file's.
func TestReplaceFrontmatterAndCRLF(t *testing.T) {
	v := newTestVault(t)
	mustWrite(t, v, "a.md", "---\ntags: [draft]\n---\n# A\n")
	if _, err := v.Replace("a.md", "tags: [draft]", "tags: [final]", false, ""); err != nil {
		t.Fatal(err)
	}
	if n, _ := v.Read("a.md"); !reflect.DeepEqual(n.Tags, []string{"final"}) {
		t.Errorf("tags = %v, want [final]", n.Tags)
	}

	mustWrite(t, v, "w.md", "one\r\ntwo\r\n")
	_, err := v.Replace("w.md", "one\ntwo", "x", false, "")
	if !errors.Is(err, ErrTextNotFound) || !strings.Contains(err.Error(), "CRLF") {
		t.Errorf("LF old_text in a CRLF note: err = %v, want ErrTextNotFound naming CRLF", err)
	}
	if _, err := v.Replace("w.md", "one\r\ntwo", "x", false, ""); err != nil {
		t.Fatal(err)
	}
	if got := readRaw(t, v, "w.md"); got != "x\r\n" {
		t.Errorf("file = %q", got)
	}
}
