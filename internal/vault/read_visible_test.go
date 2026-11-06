package vault

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// ReadVisible reads what Read reads, minus the notes the vault scan never
// shows: hidden paths, directly or through a symlinked folder.
func TestReadVisible(t *testing.T) {
	v := newTestVault(t)
	mustWrite(t, v, "projects/p/n.md", "# N\n")
	for _, p := range []string{".obsidian/w.md", "projects/.drafts/d.md", "inbox/.secret.md"} {
		abs := filepath.Join(v.Root(), filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte("hidden\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	note, err := v.ReadVisible("projects/p/n.md")
	if err != nil || note.Content != "# N\n" || note.Path != "projects/p/n.md" {
		t.Fatalf("ReadVisible = %+v, %v", note, err)
	}
	want, _ := v.Read("projects/p/n.md")
	if note.Hash != want.Hash || note.Title != want.Title {
		t.Errorf("ReadVisible differs from Read: %+v vs %+v", note, want)
	}

	for _, p := range []string{".obsidian/w.md", "projects/.drafts/d.md", "inbox/.secret.md"} {
		if _, err := v.ReadVisible(p); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("ReadVisible(%s) = %v, want ErrInvalidPath", p, err)
		}
		// Read itself keeps serving them, so nothing already there is trapped.
		if _, err := v.Read(p); err != nil {
			t.Errorf("Read(%s) = %v, want it readable", p, err)
		}
	}
	for p, wantErr := range map[string]error{
		"../out.md":        ErrInvalidPath,
		"/etc/passwd.md":   ErrInvalidPath,
		"projects/n.txt":   ErrNotMarkdown,
		"projects/none.md": ErrNotFound,
	} {
		if _, err := v.ReadVisible(p); !errors.Is(err, wantErr) {
			t.Errorf("ReadVisible(%s) = %v, want %v", p, err, wantErr)
		}
	}

	if err := os.Symlink(filepath.Join(v.Root(), ".obsidian"), filepath.Join(v.Root(), "visible")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := v.ReadVisible("visible/w.md"); !errors.Is(err, ErrInvalidPath) {
		t.Errorf("ReadVisible through a symlink to a hidden dir = %v, want ErrInvalidPath", err)
	}
}
