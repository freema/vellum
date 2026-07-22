package vault

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func newTestVault(t testing.TB, opts ...Option) *Vault {
	t.Helper()
	v, err := New(t.TempDir(), opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return v
}

func mustWrite(t testing.TB, v *Vault, path, content string) {
	t.Helper()
	if err := v.Write(path, content, WriteOptions{Overwrite: true}); err != nil {
		t.Fatalf("Write(%s): %v", path, err)
	}
}

func sha(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestPathTraversalRejected(t *testing.T) {
	v := newTestVault(t)
	bad := []string{
		"../escape.md",
		"a/../../escape.md",
		"a/b/../../../escape.md",
		"/etc/passwd.md",
		"..",
		"notes/evil\x00.md",
		"",
	}
	for _, p := range bad {
		t.Run(p, func(t *testing.T) {
			if _, err := v.Read(p); !errors.Is(err, ErrInvalidPath) && !errors.Is(err, ErrNotMarkdown) {
				t.Errorf("Read(%q) error = %v, want ErrInvalidPath/ErrNotMarkdown", p, err)
			}
			if err := v.Write(p, "x", WriteOptions{}); !errors.Is(err, ErrInvalidPath) && !errors.Is(err, ErrNotMarkdown) {
				t.Errorf("Write(%q) error = %v, want ErrInvalidPath/ErrNotMarkdown", p, err)
			}
			if err := v.Delete(p); !errors.Is(err, ErrInvalidPath) && !errors.Is(err, ErrNotMarkdown) {
				t.Errorf("Delete(%q) error = %v, want ErrInvalidPath/ErrNotMarkdown", p, err)
			}
		})
	}
	// Interior ../ that stays inside the vault is fine.
	mustWrite(t, v, "a/note.md", "ok")
	if _, err := v.Read("a/b/../note.md"); err != nil {
		t.Errorf("interior ../ inside vault should resolve, got %v", err)
	}
}

func TestNonMarkdownRejected(t *testing.T) {
	v := newTestVault(t)
	for _, p := range []string{"note.txt", "script.sh", "note.md.bak", "noext"} {
		if err := v.Write(p, "x", WriteOptions{}); !errors.Is(err, ErrNotMarkdown) {
			t.Errorf("Write(%q) error = %v, want ErrNotMarkdown", p, err)
		}
	}
	if err := v.Write("note.markdown", "x", WriteOptions{}); err != nil {
		t.Errorf(".markdown should be accepted, got %v", err)
	}
	if err := v.Write("NOTE.MD", "x", WriteOptions{}); err != nil {
		t.Errorf("case-insensitive extension should be accepted, got %v", err)
	}
}

func TestSymlinkEscapeRejected(t *testing.T) {
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.md"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	v := newTestVault(t)

	// Directory symlink pointing outside the vault.
	if err := os.Symlink(outside, filepath.Join(v.Root(), "sub")); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Read("sub/secret.md"); !errors.Is(err, ErrInvalidPath) {
		t.Errorf("Read through dir symlink = %v, want ErrInvalidPath", err)
	}
	if err := v.Write("sub/new.md", "x", WriteOptions{}); !errors.Is(err, ErrInvalidPath) {
		t.Errorf("Write through dir symlink = %v, want ErrInvalidPath", err)
	}

	// File symlink pointing outside the vault.
	if err := os.Symlink(filepath.Join(outside, "secret.md"), filepath.Join(v.Root(), "link.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Read("link.md"); !errors.Is(err, ErrInvalidPath) {
		t.Errorf("Read of file symlink = %v, want ErrInvalidPath", err)
	}
}

func TestWriteReadRoundtrip(t *testing.T) {
	v := newTestVault(t)
	content := "---\ntitle: My Note\ntags: [alpha, zeta]\ntype: task\nstatus: backlog\n---\n" +
		"# Ignored Heading\n\nBody with #inline tag and [[linked-note]].\n"
	if err := v.Write("projects/x/note.md", content, WriteOptions{}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	note, err := v.Read("projects/x/note.md")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if note.Title != "My Note" {
		t.Errorf("Title = %q, want My Note", note.Title)
	}
	if note.Hash != sha(content) {
		t.Errorf("Hash = %s, want sha256 of content", note.Hash)
	}
	if want := []string{"alpha", "inline", "zeta"}; strings.Join(note.Tags, ",") != strings.Join(want, ",") {
		t.Errorf("Tags = %v, want %v", note.Tags, want)
	}
	if len(note.Links) != 1 || note.Links[0] != "linked-note" {
		t.Errorf("Links = %v, want [linked-note]", note.Links)
	}
	if note.Frontmatter["type"] != "task" || note.Frontmatter["status"] != "backlog" {
		t.Errorf("Frontmatter task fields missing: %v", note.Frontmatter)
	}
	if !strings.HasPrefix(note.Body, "# Ignored Heading") {
		t.Errorf("Body should start after frontmatter, got %q", note.Body)
	}
	if note.Path != "projects/x/note.md" {
		t.Errorf("Path = %q, want canonical forward-slash relative", note.Path)
	}
}

func TestWriteOverwriteSemantics(t *testing.T) {
	v := newTestVault(t)
	mustWrite(t, v, "n.md", "v1")

	if err := v.Write("n.md", "v2", WriteOptions{}); !errors.Is(err, ErrExists) {
		t.Errorf("Write without overwrite = %v, want ErrExists", err)
	}
	if err := v.Write("n.md", "v2", WriteOptions{Overwrite: true}); err != nil {
		t.Errorf("Write with overwrite = %v", err)
	}
	note, _ := v.Read("n.md")
	if note.Content != "v2" {
		t.Errorf("content = %q, want v2", note.Content)
	}
}

func TestWriteExpectedHash(t *testing.T) {
	v := newTestVault(t)
	mustWrite(t, v, "n.md", "v1")

	// Matching hash writes even without Overwrite.
	if err := v.Write("n.md", "v2", WriteOptions{ExpectedHash: sha("v1")}); err != nil {
		t.Fatalf("Write with matching hash = %v", err)
	}
	// Stale hash conflicts and leaves content untouched.
	err := v.Write("n.md", "v3", WriteOptions{ExpectedHash: sha("v1")})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("Write with stale hash = %v, want ErrConflict", err)
	}
	note, _ := v.Read("n.md")
	if note.Content != "v2" {
		t.Errorf("conflicting write must not modify content, got %q", note.Content)
	}
	// ExpectedHash against a missing note.
	if err := v.Write("missing.md", "x", WriteOptions{ExpectedHash: sha("v1")}); !errors.Is(err, ErrNotFound) {
		t.Errorf("Write missing with hash = %v, want ErrNotFound", err)
	}
}

func TestMaxFileSize(t *testing.T) {
	v := newTestVault(t, WithMaxFileSize(16))
	if err := v.Write("big.md", strings.Repeat("x", 17), WriteOptions{}); !errors.Is(err, ErrTooLarge) {
		t.Errorf("oversized Write = %v, want ErrTooLarge", err)
	}
	// Oversized file created behind the vault's back is refused on Read.
	if err := os.WriteFile(filepath.Join(v.Root(), "ext.md"), []byte(strings.Repeat("y", 32)), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Read("ext.md"); !errors.Is(err, ErrTooLarge) {
		t.Errorf("oversized Read = %v, want ErrTooLarge", err)
	}
}

const patchDoc = `---
title: Patched
---
intro line

## Alpha

alpha content

## Beta

beta content

### Beta child

child content

## Gamma

gamma content
`

func TestPatchSection(t *testing.T) {
	v := newTestVault(t)
	mustWrite(t, v, "p.md", patchDoc)

	if err := v.Patch("p.md", "Beta", "NEW BETA", ""); err != nil {
		t.Fatalf("Patch: %v", err)
	}
	note, _ := v.Read("p.md")
	want := `---
title: Patched
---
intro line

## Alpha

alpha content

## Beta
NEW BETA
## Gamma

gamma content
`
	if note.Content != want {
		t.Errorf("patched content mismatch:\ngot:\n%s\nwant:\n%s", note.Content, want)
	}
}

func TestPatchErrors(t *testing.T) {
	v := newTestVault(t)
	mustWrite(t, v, "p.md", patchDoc)

	if err := v.Patch("p.md", "Nope", "x", ""); !errors.Is(err, ErrSectionNotFound) {
		t.Errorf("Patch missing section = %v, want ErrSectionNotFound", err)
	}
	if err := v.Patch("p.md", "Beta", "x", sha("wrong")); !errors.Is(err, ErrConflict) {
		t.Errorf("Patch stale hash = %v, want ErrConflict", err)
	}
	if err := v.Patch("missing.md", "Beta", "x", ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("Patch missing note = %v, want ErrNotFound", err)
	}
}

func TestPatchIgnoresHeadingsInCode(t *testing.T) {
	v := newTestVault(t)
	doc := "```\n## Fake\n```\n\n## Fake\n\nreal section\n"
	mustWrite(t, v, "c.md", doc)
	if err := v.Patch("c.md", "Fake", "REPLACED", ""); err != nil {
		t.Fatalf("Patch: %v", err)
	}
	note, _ := v.Read("c.md")
	if !strings.Contains(note.Content, "```\n## Fake\n```") {
		t.Errorf("fenced heading must stay intact, got:\n%s", note.Content)
	}
	if !strings.Contains(note.Content, "## Fake\nREPLACED\n") {
		t.Errorf("real section not replaced:\n%s", note.Content)
	}
}

func TestAppendPrepend(t *testing.T) {
	v := newTestVault(t)
	mustWrite(t, v, "n.md", "---\ntitle: T\n---\nbody line\n")

	if err := v.Append("n.md", "appended", ""); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := v.Prepend("n.md", "prepended", ""); err != nil {
		t.Fatalf("Prepend: %v", err)
	}
	note, _ := v.Read("n.md")
	want := "---\ntitle: T\n---\nprepended\nbody line\nappended\n"
	if note.Content != want {
		t.Errorf("content:\n%q\nwant:\n%q", note.Content, want)
	}

	if err := v.Append("missing.md", "x", ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("Append missing = %v, want ErrNotFound", err)
	}
	if err := v.Prepend("n.md", "x", sha("stale")); !errors.Is(err, ErrConflict) {
		t.Errorf("Prepend stale hash = %v, want ErrConflict", err)
	}
}

func TestAppendWithoutTrailingNewline(t *testing.T) {
	v := newTestVault(t)
	mustWrite(t, v, "n.md", "no newline at end")
	if err := v.Append("n.md", "tail", ""); err != nil {
		t.Fatal(err)
	}
	note, _ := v.Read("n.md")
	if note.Content != "no newline at end\ntail\n" {
		t.Errorf("content = %q", note.Content)
	}
}

func TestDelete(t *testing.T) {
	v := newTestVault(t)
	mustWrite(t, v, "n.md", "x")
	if err := v.Delete("n.md"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := v.Delete("n.md"); !errors.Is(err, ErrNotFound) {
		t.Errorf("second Delete = %v, want ErrNotFound", err)
	}
}

func TestMove(t *testing.T) {
	v := newTestVault(t)
	mustWrite(t, v, "inbox/n.md", "content")

	if err := v.Move("inbox/n.md", "projects/p/n.md"); err != nil {
		t.Fatalf("Move: %v", err)
	}
	if _, err := v.Read("inbox/n.md"); !errors.Is(err, ErrNotFound) {
		t.Errorf("source should be gone, got %v", err)
	}
	note, err := v.Read("projects/p/n.md")
	if err != nil || note.Content != "content" {
		t.Errorf("target read = %v, %v", note, err)
	}

	mustWrite(t, v, "other.md", "y")
	if err := v.Move("other.md", "projects/p/n.md"); !errors.Is(err, ErrExists) {
		t.Errorf("Move onto existing = %v, want ErrExists", err)
	}
	if err := v.Move("missing.md", "x.md"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Move missing = %v, want ErrNotFound", err)
	}
	if err := v.Move("projects/p/n.md", "../out.md"); !errors.Is(err, ErrInvalidPath) {
		t.Errorf("Move escaping target = %v, want ErrInvalidPath", err)
	}
	// A dot-path would be skipped by List and the index — the note would sit
	// on disk and disappear from vellum on the next build.
	if err := v.Move("projects/p/n.md", "inbox/.secret.md"); !errors.Is(err, ErrInvalidPath) {
		t.Errorf("Move to a hidden path = %v, want ErrInvalidPath", err)
	}
	if err := v.Write("inbox/.secret.md", "x", WriteOptions{Overwrite: true}); !errors.Is(err, ErrInvalidPath) {
		t.Errorf("Write to a hidden path = %v, want ErrInvalidPath", err)
	}
	if err := v.Write(".hidden/n.md", "x", WriteOptions{Overwrite: true}); !errors.Is(err, ErrInvalidPath) {
		t.Errorf("Write into a hidden dir = %v, want ErrInvalidPath", err)
	}
	if err := v.CreateDir(".private"); !errors.Is(err, ErrInvalidPath) {
		t.Errorf("CreateDir(.private) = %v, want ErrInvalidPath", err)
	}
}

// A symlink to a hidden directory must not be a way in: the requested path
// looks clean but the note physically lands under a dot segment the vault
// scan skips, so it would vanish from the index on the next build.
func TestWriteThroughSymlinkToHiddenDir(t *testing.T) {
	v := newTestVault(t)
	if err := os.MkdirAll(filepath.Join(v.Root(), ".private"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(v.Root(), ".private"), filepath.Join(v.Root(), "visible")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	err := v.Write("visible/n.md", "x", WriteOptions{Overwrite: true})
	if !errors.Is(err, ErrInvalidPath) {
		t.Errorf("Write through a symlink to a hidden dir = %v, want ErrInvalidPath", err)
	}
	if _, err := os.Stat(filepath.Join(v.Root(), ".private", "n.md")); !os.IsNotExist(err) {
		t.Errorf("note was written into the hidden dir anyway: %v", err)
	}
}

// Move gets the same symlink-to-hidden-dir guard as Write: renaming a note
// onto a path that resolves through a symlink into a hidden directory must be
// refused, or the note would vanish from the index on the next build.
func TestMoveThroughSymlinkToHiddenDir(t *testing.T) {
	v := newTestVault(t)
	mustWrite(t, v, "inbox/n.md", "content")
	if err := os.MkdirAll(filepath.Join(v.Root(), ".private"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(v.Root(), ".private"), filepath.Join(v.Root(), "visible")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := v.Move("inbox/n.md", "visible/n.md"); !errors.Is(err, ErrInvalidPath) {
		t.Errorf("Move through a symlink to a hidden dir = %v, want ErrInvalidPath", err)
	}
	if _, err := os.Stat(filepath.Join(v.Root(), ".private", "n.md")); !os.IsNotExist(err) {
		t.Errorf("note was moved into the hidden dir anyway: %v", err)
	}
	if _, err := v.Read("inbox/n.md"); err != nil {
		t.Errorf("source note should be untouched after a refused move: %v", err)
	}
}

// A hard link is a second note under a different name — renaming onto it
// would drop that note from the vault, so it must stay an ErrExists.
func TestMoveOntoHardLink(t *testing.T) {
	v := newTestVault(t)
	mustWrite(t, v, "inbox/a.md", "content")
	if err := os.Link(filepath.Join(v.Root(), "inbox/a.md"), filepath.Join(v.Root(), "inbox/b.md")); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	if err := v.Move("inbox/a.md", "inbox/b.md"); !errors.Is(err, ErrExists) {
		t.Errorf("Move onto a hard link = %v, want ErrExists", err)
	}
}

// A case-only rename is a rename, not a collision — on a case-insensitive
// volume the "existing target" Move sees is the source file itself.
func TestMoveCaseOnlyRename(t *testing.T) {
	v := newTestVault(t)
	mustWrite(t, v, "inbox/notes.md", "content")

	if err := v.Move("inbox/notes.md", "inbox/Notes.md"); err != nil {
		t.Fatalf("case-only rename: %v", err)
	}
	note, err := v.Read("inbox/Notes.md")
	if err != nil || note.Content != "content" {
		t.Fatalf("read after case-only rename = %+v, %v", note, err)
	}
	entries, err := v.List("inbox", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Path != "inbox/Notes.md" {
		t.Errorf("listing after rename = %+v, want a single Notes.md", entries)
	}
}

func TestList(t *testing.T) {
	v := newTestVault(t)
	mustWrite(t, v, "b.md", "---\ntitle: B Note\n---\n")
	mustWrite(t, v, "a.md", "# A Heading\n")
	mustWrite(t, v, "sub/deep.md", "deep")
	// Write refuses dot-paths, so plant this one the way an editor would.
	if err := os.WriteFile(filepath.Join(v.Root(), ".hidden.md"), []byte("hidden"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(v.Root(), ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(v.Root(), ".git", "x.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(v.Root(), "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	flat, err := v.List("", false)
	if err != nil {
		t.Fatalf("List flat: %v", err)
	}
	if got := paths(flat); strings.Join(got, ",") != "a.md,b.md" {
		t.Errorf("flat list = %v, want [a.md b.md]", got)
	}
	if flat[0].Title != "A Heading" || flat[1].Title != "B Note" {
		t.Errorf("titles = %q, %q", flat[0].Title, flat[1].Title)
	}

	rec, err := v.List("", true)
	if err != nil {
		t.Fatalf("List recursive: %v", err)
	}
	if got := paths(rec); strings.Join(got, ",") != "a.md,b.md,sub/deep.md" {
		t.Errorf("recursive list = %v", got)
	}

	sub, err := v.List("sub", false)
	if err != nil || len(sub) != 1 || sub[0].Path != "sub/deep.md" {
		t.Errorf("List(sub) = %v, %v", sub, err)
	}

	if _, err := v.List("missing-dir", false); !errors.Is(err, ErrNotFound) {
		t.Errorf("List missing dir = %v, want ErrNotFound", err)
	}
}

func paths(infos []NoteInfo) []string {
	out := make([]string, len(infos))
	for i, in := range infos {
		out[i] = in.Path
	}
	return out
}

func TestConcurrentAppendsSerialize(t *testing.T) {
	v := newTestVault(t)
	mustWrite(t, v, "n.md", "start\n")
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := v.Append("n.md", "line", ""); err != nil {
				t.Errorf("Append: %v", err)
			}
		}()
	}
	wg.Wait()
	note, _ := v.Read("n.md")
	if got := strings.Count(note.Content, "line"); got != 20 {
		t.Errorf("appended %d lines, want 20", got)
	}
}

func TestConflictSequence(t *testing.T) {
	v := newTestVault(t)
	mustWrite(t, v, "n.md", "base")
	h := sha("base")

	if err := v.Write("n.md", "first", WriteOptions{ExpectedHash: h}); err != nil {
		t.Fatalf("first writer: %v", err)
	}
	if err := v.Write("n.md", "second", WriteOptions{ExpectedHash: h}); !errors.Is(err, ErrConflict) {
		t.Fatalf("second writer = %v, want ErrConflict", err)
	}
}
