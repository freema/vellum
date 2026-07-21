package vault

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestInitStructureOnEmptyRoot(t *testing.T) {
	v := newTestVault(t)
	created, err := v.InitStructure(DefaultStructure())
	if err != nil {
		t.Fatalf("InitStructure: %v", err)
	}
	if !created {
		t.Fatal("expected structure to be created on empty root")
	}
	for _, dir := range []string{"inbox", "projects", "archive"} {
		if fi, err := os.Stat(filepath.Join(v.Root(), dir)); err != nil || !fi.IsDir() {
			t.Errorf("%s not created: %v", dir, err)
		}
	}

	// Idempotent: second call is a no-op and does not fail.
	created, err = v.InitStructure(DefaultStructure())
	if err != nil {
		t.Fatalf("second InitStructure: %v", err)
	}
	if created {
		t.Error("second call must not report created")
	}
}

func TestInitStructureLeavesPopulatedVaultAlone(t *testing.T) {
	v := newTestVault(t)
	mustWrite(t, v, "existing.md", "content")

	created, err := v.InitStructure(DefaultStructure())
	if err != nil {
		t.Fatalf("InitStructure: %v", err)
	}
	if created {
		t.Error("populated vault must not be initialized")
	}
	if _, err := os.Stat(filepath.Join(v.Root(), "inbox")); !os.IsNotExist(err) {
		t.Error("inbox must not be created in a populated vault")
	}
}

func TestInitStructureCustomDirs(t *testing.T) {
	v := newTestVault(t)
	s := Structure{Inbox: "in", Projects: "work", Archive: "old"}
	if _, err := v.InitStructure(s); err != nil {
		t.Fatalf("InitStructure: %v", err)
	}
	for _, dir := range []string{"in", "work", "old"} {
		if fi, err := os.Stat(filepath.Join(v.Root(), dir)); err != nil || !fi.IsDir() {
			t.Errorf("%s not created: %v", dir, err)
		}
	}
}

func TestResolveWritePath(t *testing.T) {
	v := newTestVault(t)
	s := DefaultStructure()

	tests := []struct {
		name, requested, content, want string
	}{
		{"explicit dir honored", "projects/x/note.md", "", "projects/x/note.md"},
		{"bare filename to inbox", "note.md", "", "inbox/note.md"},
		{"no path, title slug", "", "---\ntitle: My Great Note\n---\n", "inbox/my-great-note.md"},
		{"no path, heading slug", "", "# Heading Title\n", "inbox/heading-title.md"},
		{"no path, no title", "", "just text\n", "inbox/untitled.md"},
		{"diacritics kept", "", "# Poznámka č. 1\n", "inbox/poznámka-č-1.md"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := v.ResolveWritePath(tt.requested, tt.content, s)
			if err != nil {
				t.Fatalf("ResolveWritePath: %v", err)
			}
			if got != tt.want {
				t.Errorf("ResolveWritePath(%q) = %q, want %q", tt.requested, got, tt.want)
			}
		})
	}
}

func TestResolveWritePathUniquifies(t *testing.T) {
	v := newTestVault(t)
	s := DefaultStructure()
	mustWrite(t, v, "inbox/my-note.md", "x")
	mustWrite(t, v, "inbox/my-note-2.md", "x")

	got, err := v.ResolveWritePath("", "# My Note\n", s)
	if err != nil {
		t.Fatalf("ResolveWritePath: %v", err)
	}
	if got != "inbox/my-note-3.md" {
		t.Errorf("ResolveWritePath = %q, want inbox/my-note-3.md", got)
	}
}

func TestSlugify(t *testing.T) {
	tests := []struct{ in, want string }{
		{"My Great Note", "my-great-note"},
		{"  Spaces   everywhere  ", "spaces-everywhere"},
		{"Symbols!@#$%^&*()", "symbols"},
		{"", "untitled"},
		{"---", "untitled"},
		{"Čeština žije", "čeština-žije"},
	}
	for _, tt := range tests {
		if got := slugify(tt.in); got != tt.want {
			t.Errorf("slugify(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// A pasted paragraph as a title must still produce a filename the kernel
// accepts, cut on a rune boundary so the name stays valid UTF-8.
func TestSlugifyLongTitle(t *testing.T) {
	for _, in := range []string{strings.Repeat("a", 400), strings.Repeat("ěščř ", 100)} {
		got := slugify(in)
		if len(got) > maxSlugBytes {
			t.Errorf("slugify(%d chars) = %d bytes, want <= %d", len(in), len(got), maxSlugBytes)
		}
		if !utf8.ValidString(got) {
			t.Errorf("slugify(%d chars) cut mid-rune: %q", len(in), got)
		}
		if strings.HasSuffix(got, "-") {
			t.Errorf("slugify(%d chars) ends in a dash: %q", len(in), got)
		}
	}
}
