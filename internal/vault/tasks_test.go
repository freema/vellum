package vault

import (
	"errors"
	"reflect"
	"testing"
)

func TestSetStatusReplacesOnlyStatusLine(t *testing.T) {
	v := newTestVault(t)
	original := "---\ntitle: Rich Note   \ntags: [a, b]\ntype: task\nstatus: backlog\ncustom_field: keep me\n---\n# Body\n\nuntouched #tag\n"
	mustWrite(t, v, "t.md", original)

	if err := v.SetStatus("t.md", StatusDone, ""); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	note, _ := v.Read("t.md")
	want := "---\ntitle: Rich Note   \ntags: [a, b]\ntype: task\nstatus: done\ncustom_field: keep me\n---\n# Body\n\nuntouched #tag\n"
	if note.Content != want {
		t.Errorf("content:\n%q\nwant:\n%q", note.Content, want)
	}
}

func TestSetStatusInsertsAfterType(t *testing.T) {
	v := newTestVault(t)
	mustWrite(t, v, "t.md", "---\ntype: task\ntitle: X\n---\nbody\n")
	if err := v.SetStatus("t.md", StatusInProgress, ""); err != nil {
		t.Fatal(err)
	}
	note, _ := v.Read("t.md")
	want := "---\ntype: task\nstatus: in-progress\ntitle: X\n---\nbody\n"
	if note.Content != want {
		t.Errorf("content = %q, want %q", note.Content, want)
	}
}

func TestSetStatusAppendsWhenNoTypeOrStatus(t *testing.T) {
	v := newTestVault(t)
	mustWrite(t, v, "t.md", "---\ntitle: X\n---\nbody\n")
	if err := v.SetStatus("t.md", StatusBacklog, ""); err != nil {
		t.Fatal(err)
	}
	note, _ := v.Read("t.md")
	want := "---\ntitle: X\nstatus: backlog\ntype: task\n---\nbody\n"
	if note.Content != want {
		t.Errorf("content = %q, want %q", note.Content, want)
	}
}

func TestSetStatusCreatesFrontmatter(t *testing.T) {
	v := newTestVault(t)
	mustWrite(t, v, "plain.md", "# Just Body\n")
	if err := v.SetStatus("plain.md", StatusBacklog, ""); err != nil {
		t.Fatal(err)
	}
	note, _ := v.Read("plain.md")
	want := "---\ntype: task\nstatus: backlog\n---\n# Just Body\n"
	if note.Content != want {
		t.Errorf("content = %q, want %q", note.Content, want)
	}
}

func TestSetStatusConvertsKnowledgeToTask(t *testing.T) {
	v := newTestVault(t)
	mustWrite(t, v, "k.md", "---\ntype: knowledge\n---\nbody\n")
	if err := v.SetStatus("k.md", StatusDone, ""); err != nil {
		t.Fatal(err)
	}
	note, _ := v.Read("k.md")
	if note.Frontmatter["type"] != "task" || note.Frontmatter["status"] != "done" {
		t.Errorf("frontmatter = %v", note.Frontmatter)
	}
}

func TestSetStatusValidation(t *testing.T) {
	v := newTestVault(t)
	mustWrite(t, v, "t.md", "x")
	if err := v.SetStatus("t.md", "doing", ""); err == nil {
		t.Error("invalid status must be rejected")
	}
	if err := v.SetStatus("missing.md", StatusDone, ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing note = %v, want ErrNotFound", err)
	}
	if err := v.SetStatus("t.md", StatusDone, sha("stale")); !errors.Is(err, ErrConflict) {
		t.Errorf("stale hash = %v, want ErrConflict", err)
	}
}

func TestListTasks(t *testing.T) {
	v := newTestVault(t)
	mustWrite(t, v, "projects/vellum/a.md", "---\ntype: task\nstatus: backlog\n---\n")
	mustWrite(t, v, "projects/vellum/b.md", "---\ntype: task\nstatus: done\n---\n")
	mustWrite(t, v, "projects/other/c.md", "---\ntype: task\nstatus: backlog\n---\n")
	mustWrite(t, v, "inbox/k.md", "---\ntype: knowledge\n---\n")
	mustWrite(t, v, "inbox/untyped.md", "# No type\n")
	mustWrite(t, v, "inbox/loose-task.md", "---\ntype: task\nstatus: in-progress\n---\n")
	ix := NewIndex(v)
	if err := ix.Build(); err != nil {
		t.Fatal(err)
	}

	all := ix.ListTasks("", "")
	if got := entryPaths(all); !reflect.DeepEqual(got, []string{
		"inbox/loose-task.md", "projects/other/c.md", "projects/vellum/a.md", "projects/vellum/b.md",
	}) {
		t.Errorf("all tasks = %v", got)
	}

	backlog := ix.ListTasks(StatusBacklog, "")
	if got := entryPaths(backlog); !reflect.DeepEqual(got, []string{"projects/other/c.md", "projects/vellum/a.md"}) {
		t.Errorf("backlog tasks = %v", got)
	}

	proj := ix.ListTasks("", "vellum")
	if got := entryPaths(proj); !reflect.DeepEqual(got, []string{"projects/vellum/a.md", "projects/vellum/b.md"}) {
		t.Errorf("project tasks = %v", got)
	}

	both := ix.ListTasks(StatusDone, "vellum")
	if got := entryPaths(both); !reflect.DeepEqual(got, []string{"projects/vellum/b.md"}) {
		t.Errorf("status+project = %v", got)
	}
}

func TestListTasksAfterSetStatus(t *testing.T) {
	v := newTestVault(t)
	mustWrite(t, v, "projects/p/t.md", "---\ntype: task\nstatus: backlog\n---\n")
	ix := NewIndex(v)
	if err := ix.Build(); err != nil {
		t.Fatal(err)
	}
	if err := v.SetStatus("projects/p/t.md", StatusDone, ""); err != nil {
		t.Fatal(err)
	}
	if err := ix.Update("projects/p/t.md"); err != nil {
		t.Fatal(err)
	}
	if got := ix.ListTasks(StatusDone, "p"); len(got) != 1 {
		t.Errorf("done tasks after update = %v", got)
	}
	if got := ix.ListTasks(StatusBacklog, "p"); len(got) != 0 {
		t.Errorf("backlog should be empty, got %v", got)
	}
}

func TestTypeOfDefault(t *testing.T) {
	if TypeOf(Entry{Type: ""}) != TypeKnowledge {
		t.Error("missing type must default to knowledge")
	}
	if TypeOf(Entry{Type: "task"}) != TypeTask {
		t.Error("task type must stay task")
	}
}

func entryPaths(es []Entry) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Path
	}
	return out
}
