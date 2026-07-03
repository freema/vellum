package vault

import (
	"errors"
	"reflect"
	"testing"
)

func TestAddTagsFlowList(t *testing.T) {
	v := newTestVault(t)
	mustWrite(t, v, "n.md", "---\ntitle: X\ntags: [go]\ncustom: keep\n---\nbody\n")

	tags, err := v.AddTags("n.md", []string{"mcp", "#hash-stripped", "go"}, "")
	if err != nil {
		t.Fatalf("AddTags: %v", err)
	}
	if want := []string{"go", "mcp", "hash-stripped"}; !reflect.DeepEqual(tags, want) {
		t.Errorf("tags = %v, want %v", tags, want)
	}
	note, _ := v.Read("n.md")
	want := "---\ntitle: X\ntags: [go, mcp, hash-stripped]\ncustom: keep\n---\nbody\n"
	if note.Content != want {
		t.Errorf("content = %q, want %q", note.Content, want)
	}
}

func TestAddTagsBlockListCollapsed(t *testing.T) {
	v := newTestVault(t)
	mustWrite(t, v, "n.md", "---\ntags:\n  - go\n  - old\ntitle: After\n---\nbody\n")

	if _, err := v.AddTags("n.md", []string{"new"}, ""); err != nil {
		t.Fatal(err)
	}
	note, _ := v.Read("n.md")
	want := "---\ntags: [go, old, new]\ntitle: After\n---\nbody\n"
	if note.Content != want {
		t.Errorf("content = %q, want %q", note.Content, want)
	}
}

func TestAddTagsNoFrontmatter(t *testing.T) {
	v := newTestVault(t)
	mustWrite(t, v, "n.md", "# Plain\n")
	if _, err := v.AddTags("n.md", []string{"solo"}, ""); err != nil {
		t.Fatal(err)
	}
	note, _ := v.Read("n.md")
	if note.Content != "---\ntags: [solo]\n---\n# Plain\n" {
		t.Errorf("content = %q", note.Content)
	}
}

func TestAddTagsMissingTagsLine(t *testing.T) {
	v := newTestVault(t)
	mustWrite(t, v, "n.md", "---\ntitle: X\n---\nbody\n")
	if _, err := v.AddTags("n.md", []string{"fresh"}, ""); err != nil {
		t.Fatal(err)
	}
	note, _ := v.Read("n.md")
	if note.Content != "---\ntags: [fresh]\ntitle: X\n---\nbody\n" {
		t.Errorf("content = %q", note.Content)
	}
}

func TestRemoveTags(t *testing.T) {
	v := newTestVault(t)
	mustWrite(t, v, "n.md", "---\ntags: [a, b, c]\n---\nbody with #inline\n")

	tags, err := v.RemoveTags("n.md", []string{"b"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(tags, []string{"a", "c"}) {
		t.Errorf("tags = %v", tags)
	}
	// Removing the rest drops the whole tags line.
	if _, err := v.RemoveTags("n.md", []string{"a", "c"}, ""); err != nil {
		t.Fatal(err)
	}
	note, _ := v.Read("n.md")
	if note.Content != "---\n---\nbody with #inline\n" {
		t.Errorf("content = %q", note.Content)
	}
	// Inline body tag untouched and still indexed via parsing.
	if !reflect.DeepEqual(note.Tags, []string{"inline"}) {
		t.Errorf("inline tag lost: %v", note.Tags)
	}
}

func TestRemoveTagsNoFrontmatterNoop(t *testing.T) {
	v := newTestVault(t)
	mustWrite(t, v, "n.md", "# Plain #inline\n")
	tags, err := v.RemoveTags("n.md", []string{"inline"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if tags != nil {
		t.Errorf("tags = %v, want nil", tags)
	}
	note, _ := v.Read("n.md")
	if note.Content != "# Plain #inline\n" {
		t.Errorf("content changed: %q", note.Content)
	}
}

func TestTagsConflictAndErrors(t *testing.T) {
	v := newTestVault(t)
	mustWrite(t, v, "n.md", "---\ntags: [a]\n---\n")
	if _, err := v.AddTags("n.md", []string{"b"}, sha("stale")); !errors.Is(err, ErrConflict) {
		t.Errorf("AddTags stale hash = %v, want ErrConflict", err)
	}
	if _, err := v.AddTags("missing.md", []string{"x"}, ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("AddTags missing = %v, want ErrNotFound", err)
	}
	// No-change add does not rewrite the file.
	before, _ := v.Read("n.md")
	if _, err := v.AddTags("n.md", []string{"a"}, ""); err != nil {
		t.Fatal(err)
	}
	after, _ := v.Read("n.md")
	if before.Hash != after.Hash {
		t.Error("no-op AddTags must not rewrite the file")
	}
}
