package vault

import (
	"path/filepath"
	"reflect"
	"testing"
)

// TestFixtureVault pins the deterministic testdata/vault fixture shared by
// unit, integration and e2e tests (PHY-129). If this fails, the fixture
// changed — update docs/e2e.md expectations too.
func TestFixtureVault(t *testing.T) {
	root, err := filepath.Abs("../../testdata/vault")
	if err != nil {
		t.Fatal(err)
	}
	v, err := New(root)
	if err != nil {
		t.Fatalf("open fixture vault: %v", err)
	}
	ix := NewIndex(v)
	if err := ix.Build(); err != nil {
		t.Fatal(err)
	}

	if ix.Len() != 7 {
		t.Errorf("fixture notes = %d, want 7", ix.Len())
	}

	// Tasks in all three states.
	for _, status := range []string{StatusBacklog, StatusInProgress, StatusDone} {
		if got := ix.ListTasks(status, "demo"); len(got) != 1 {
			t.Errorf("tasks(%s) = %d, want 1", status, len(got))
		}
	}

	// Wikilinks resolve into backlinks.
	if got := ix.Backlinks("projects/demo/deep-dive.md"); !reflect.DeepEqual(got,
		[]string{"inbox/welcome.md", "projects/demo/task-backlog.md"}) {
		t.Errorf("backlinks(deep-dive) = %v", got)
	}

	// The untagged orphan for the curator tools.
	untagged := ix.FindUntagged()
	if len(untagged) != 1 || untagged[0].Path != "inbox/untagged.md" {
		t.Errorf("untagged = %v", entryPaths(untagged))
	}

	// Inline tag from the welcome note is indexed.
	if got := ix.PathsByTag("fixture"); !reflect.DeepEqual(got, []string{"inbox/welcome.md"}) {
		t.Errorf("PathsByTag(fixture) = %v", got)
	}

	// The long note reads fine and stays under the size cap.
	long, err := v.Read("projects/demo/deep-dive.md")
	if err != nil {
		t.Fatalf("read long note: %v", err)
	}
	if long.Size < 10_000 {
		t.Errorf("long note size = %d, want >10KB", long.Size)
	}
}
