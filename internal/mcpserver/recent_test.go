package mcpserver

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/freema/vellum/internal/vault"
)

// list_notes sort: modified and since (NSO-576).

// recentVault writes notes with known modification times relative to now:
// path -> age. Every body's first line is "Excerpt of <path>".
func recentVault(t *testing.T, now time.Time, ages map[string]time.Duration) string {
	t.Helper()
	root := t.TempDir()
	files := make(map[string]string, len(ages))
	for p := range ages {
		files[p] = "# " + p + "\n\nExcerpt of " + p + "\n\nmore text\n"
	}
	writeFiles(t, root, files)
	for p, age := range ages {
		setModTime(t, root, p, now.Add(-age))
	}
	return root
}

func setModTime(t *testing.T, root, path string, mt time.Time) {
	t.Helper()
	if err := os.Chtimes(filepath.Join(root, filepath.FromSlash(path)), mt, mt); err != nil {
		t.Fatal(err)
	}
}

var recentAges = map[string]time.Duration{
	"old.md":             30 * 24 * time.Hour,
	"projects/stale.md":  8 * 24 * time.Hour,
	"projects/week.md":   3 * 24 * time.Hour,
	"projects/c-tie.md":  2 * time.Hour,
	"projects/a-tie.md":  2 * time.Hour,
	"inbox/fresh.md":     10 * time.Minute,
	"projects/x/deep.md": 20 * 24 * time.Hour,
}

func notePaths(notes []vault.NoteInfo) []string {
	out := make([]string, len(notes))
	for i, n := range notes {
		out[i] = n.Path
	}
	return out
}

// Acceptance: sort: modified lists the newest change first, notes changed
// at the same time by path, each with its excerpt from the index.
func TestListNotesSortModified(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	s, _ := dirSession(t, recentVault(t, now, recentAges))

	out := decode[listNotesOut](t, s.call("list_notes", map[string]any{"recursive": true, "sort": "modified"}))
	want := []string{"inbox/fresh.md", "projects/a-tie.md", "projects/c-tie.md", "projects/week.md", "projects/stale.md", "projects/x/deep.md", "old.md"}
	if got := notePaths(out.Notes); !reflect.DeepEqual(got, want) {
		t.Fatalf("sort modified = %v, want %v", got, want)
	}
	for i, n := range out.Notes {
		if n.Excerpt != "Excerpt of "+n.Path {
			t.Errorf("%s excerpt = %q", n.Path, n.Excerpt)
		}
		if n.Title == "" || n.Size == 0 {
			t.Errorf("%s lost its title or size: %+v", n.Path, n)
		}
		if i > 0 && n.ModTime.After(out.Notes[i-1].ModTime) {
			t.Errorf("%s is newer than %s before it", n.Path, out.Notes[i-1].Path)
		}
	}
	if out.Total != len(want) || out.Truncated || out.Hint != "" {
		t.Errorf("total %d, truncated %v, hint %q", out.Total, out.Truncated, out.Hint)
	}

	// The order is the same on every call, and paging walks it.
	again := decode[listNotesOut](t, s.call("list_notes", map[string]any{"recursive": true, "sort": "modified", "offset": 1, "limit": 2}))
	if got := notePaths(again.Notes); !reflect.DeepEqual(got, want[1:3]) {
		t.Errorf("page = %v, want %v", got, want[1:3])
	}
	wantHint := "4 more notes: narrow with dir (dirs lists the subfolders and their note counts) or list without recursive or since (e.g. 7d), or continue with offset: 3"
	if !again.Truncated || again.Total != len(want) || again.Hint != wantHint {
		t.Errorf("page: truncated %v, total %d, hint %q", again.Truncated, again.Total, again.Hint)
	}

	// Without recursive only the folder's own notes, still newest first.
	out = decode[listNotesOut](t, s.call("list_notes", map[string]any{"dir": "projects", "sort": "modified"}))
	if got, want := notePaths(out.Notes), []string{"projects/a-tie.md", "projects/c-tie.md", "projects/week.md", "projects/stale.md"}; !reflect.DeepEqual(got, want) {
		t.Errorf("projects by modified = %v, want %v", got, want)
	}
}

// Acceptance: since: 7d returns nothing older, and combines with dir,
// recursive, limit and offset.
func TestListNotesSince(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	s, _ := dirSession(t, recentVault(t, now, recentAges))

	cutoff := time.Now().Add(-7 * 24 * time.Hour)
	out := decode[listNotesOut](t, s.call("list_notes", map[string]any{"recursive": true, "since": "7d"}))
	want := []string{"inbox/fresh.md", "projects/a-tie.md", "projects/c-tie.md", "projects/week.md"} // path order
	if got := notePaths(out.Notes); !reflect.DeepEqual(got, want) {
		t.Errorf("since 7d = %v, want %v", got, want)
	}
	for _, n := range out.Notes {
		if n.ModTime.Before(cutoff) {
			t.Errorf("since 7d returned %s, modified %v", n.Path, n.ModTime)
		}
		if n.Excerpt != "" {
			t.Errorf("path order carries an excerpt: %+v", n)
		}
	}
	if out.Total != 4 || out.Truncated {
		t.Errorf("since 7d: total %d, truncated %v", out.Total, out.Truncated)
	}
	// dirs still describe the whole folder: they are the vault's structure,
	// not a view of the recent changes.
	if len(out.Dirs) != 2 {
		t.Errorf("dirs = %+v", out.Dirs)
	}

	for _, c := range []struct {
		args map[string]any
		want []string
	}{
		{map[string]any{"recursive": true, "since": "24h"}, []string{"inbox/fresh.md", "projects/a-tie.md", "projects/c-tie.md"}},
		{map[string]any{"recursive": true, "since": "1w", "sort": "modified"}, []string{"inbox/fresh.md", "projects/a-tie.md", "projects/c-tie.md", "projects/week.md"}},
		{map[string]any{"recursive": true, "since": "2w"}, []string{"inbox/fresh.md", "projects/a-tie.md", "projects/c-tie.md", "projects/stale.md", "projects/week.md"}},
		{map[string]any{"dir": "projects", "since": "7d"}, []string{"projects/a-tie.md", "projects/c-tie.md", "projects/week.md"}},
		{map[string]any{"dir": "projects", "recursive": true, "since": "30d", "sort": "modified"}, []string{"projects/a-tie.md", "projects/c-tie.md", "projects/week.md", "projects/stale.md", "projects/x/deep.md"}},
		{map[string]any{"dir": "projects/x", "since": "7d"}, []string{}},
		{map[string]any{"recursive": true, "since": now.Add(-5 * 24 * time.Hour).Format(time.DateOnly)}, []string{"inbox/fresh.md", "projects/a-tie.md", "projects/c-tie.md", "projects/week.md"}},
		{map[string]any{"recursive": true, "since": now.Add(time.Hour).Format(time.RFC3339)}, []string{}},
	} {
		got := notePaths(decode[listNotesOut](t, s.call("list_notes", c.args)).Notes)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%v = %v, want %v", c.args, got, c.want)
		}
	}

	// since with limit and offset: total counts the filtered listing.
	page := decode[listNotesOut](t, s.call("list_notes", map[string]any{"recursive": true, "since": "7d", "sort": "modified", "limit": 2, "offset": 1}))
	if got := notePaths(page.Notes); !reflect.DeepEqual(got, []string{"projects/a-tie.md", "projects/c-tie.md"}) {
		t.Errorf("since page = %v", got)
	}
	wantHint := "1 more note: narrow with dir (dirs lists the subfolders and their note counts) or list without recursive or a shorter since, or continue with offset: 3"
	if page.Total != 4 || !page.Truncated || page.Hint != wantHint {
		t.Errorf("since page: total %d, truncated %v, hint %q", page.Total, page.Truncated, page.Hint)
	}
}

// An invalid since or sort is a tool error that shows the forms that work.
func TestListNotesSinceInvalid(t *testing.T) {
	s, _ := dirSession(t, recentVault(t, time.Now(), map[string]time.Duration{"a.md": time.Hour}))
	for _, since := range []string{"last week", "7", "d", "0d", "-1d", "7y", "1.5d", "2026-13-01", "10001w", "99999999999999999999h"} {
		res := s.call("list_notes", map[string]any{"since": since})
		if !res.IsError || !strings.Contains(res.Text, "24h, 7d or 2w") || !strings.Contains(res.Text, "2026-11-01") {
			t.Errorf("since %q: error %v, %q", since, res.IsError, res.Text)
		}
	}
	res := s.call("list_notes", map[string]any{"sort": "date"})
	if !res.IsError || !strings.Contains(res.Text, `"modified"`) {
		t.Errorf("sort date: error %v, %q", res.IsError, res.Text)
	}
}

// Acceptance: without the new parameters the output is what it was after
// NSO-572: path order, no excerpt key. sort: path is the same call.
func TestListNotesDefaultOrderUnchanged(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	s, _ := dirSession(t, recentVault(t, now, recentAges))

	plain := s.call("list_notes", map[string]any{"recursive": true})
	byPath := s.call("list_notes", map[string]any{"recursive": true, "sort": "path"})
	if plain.IsError || string(plain.Structured) != string(byPath.Structured) || plain.Text != byPath.Text {
		t.Errorf("sort path differs from the default:\n%s\n%s", plain.Structured, byPath.Structured)
	}
	if strings.Contains(string(plain.Structured), "excerpt") {
		t.Errorf("default listing has an excerpt: %s", plain.Structured)
	}
	want, err := s.vault.List("", true)
	if err != nil {
		t.Fatal(err)
	}
	if out := decode[listNotesOut](t, plain); !sameNotes(out.Notes, want) {
		t.Errorf("default listing = %v, vault.List %v", notePaths(out.Notes), notePaths(want))
	}
}

// Acceptance: a note edited outside vellum comes out on top. The time is
// the file's own, so the order follows at once; the excerpt comes from the
// index and follows once the index has re-read the note.
func TestListNotesOutsideEditOnTop(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	root := recentVault(t, now, recentAges)
	s, _, ix := dirSessionIndex(t, root)

	if err := os.WriteFile(filepath.Join(root, "old.md"), []byte("# Old\n\nRewritten in another editor\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	setModTime(t, root, "old.md", now.Add(time.Minute)) // newer than inbox/fresh.md, whatever the clock's granularity

	args := map[string]any{"recursive": true, "sort": "modified", "since": "24h", "limit": 2}
	out := decode[listNotesOut](t, s.call("list_notes", args))
	if len(out.Notes) != 2 || out.Notes[0].Path != "old.md" || out.Notes[1].Path != "inbox/fresh.md" {
		t.Fatalf("before reindex = %v", notePaths(out.Notes))
	}

	if err := ix.Build(); err != nil {
		t.Fatal(err)
	}
	out = decode[listNotesOut](t, s.call("list_notes", args))
	if len(out.Notes) != 2 || out.Notes[0].Path != "old.md" || out.Notes[0].Excerpt != "Rewritten in another editor" || out.Notes[0].Title != "Old" {
		t.Errorf("after reindex = %+v", out.Notes)
	}
	if out.Total != 4 {
		t.Errorf("total = %d, want the 3 recent notes plus old.md", out.Total)
	}
}

func TestParseSince(t *testing.T) {
	loc := time.FixedZone("test", 2*3600)
	now := time.Date(2026, 11, 5, 14, 30, 0, 0, loc)
	for in, want := range map[string]time.Time{
		"24h":                  now.Add(-24 * time.Hour),
		"1h":                   now.Add(-time.Hour),
		"7d":                   now.Add(-7 * 24 * time.Hour),
		" 7D ":                 now.Add(-7 * 24 * time.Hour),
		"2w":                   now.Add(-14 * 24 * time.Hour),
		"10000w":               now.Add(-10000 * 7 * 24 * time.Hour),
		"2026-11-01":           time.Date(2026, 11, 1, 0, 0, 0, 0, loc),
		"2026-11-01T08:00:00Z": time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC),
	} {
		got, err := parseSince(in, now)
		if err != nil || !got.Equal(want) {
			t.Errorf("parseSince(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"", " ", "h", "0h", "-2d", "3m", "3 d", "yesterday", "11/01/2026", "2026-02-30", "10001w"} {
		if _, err := parseSince(in, now); err == nil {
			t.Errorf("parseSince(%q) accepted", in)
		}
	}
}
