package vault

import (
	"os"
	"reflect"
	"testing"
	"time"
)

func listPaths(infos []NoteInfo) []string {
	out := make([]string, len(infos))
	for i, n := range infos {
		out[i] = n.Path
	}
	return out
}

// ListWith filters by modification time, sorts newest first with path as
// the tie-break, and cuts the window after both (NSO-576).
func TestListWith(t *testing.T) {
	v := newTestVault(t)
	base := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	times := map[string]time.Time{
		"a.md":     base.Add(-48 * time.Hour),
		"b.md":     base,
		"d/c.md":   base,
		"d/e.md":   base.Add(time.Hour),
		"d/x/f.md": base.Add(-72 * time.Hour),
	}
	for p, mt := range times {
		mustWrite(t, v, p, "# "+p+"\n")
		abs, err := v.resolveNote(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(abs, mt, mt); err != nil {
			t.Fatal(err)
		}
	}

	for _, c := range []struct {
		name      string
		dir       string
		recursive bool
		opts      ListOptions
		want      []string
		total     int
	}{
		{"zero value is List", "", true, ListOptions{}, []string{"a.md", "b.md", "d/c.md", "d/e.md", "d/x/f.md"}, 5},
		{"by modified", "", true, ListOptions{ByModified: true}, []string{"d/e.md", "b.md", "d/c.md", "a.md", "d/x/f.md"}, 5},
		{"since inclusive", "", true, ListOptions{Since: base}, []string{"b.md", "d/c.md", "d/e.md"}, 3},
		{"since and window", "", true, ListOptions{Since: base.Add(-49 * time.Hour), ByModified: true, Offset: 1, Limit: 2}, []string{"b.md", "d/c.md"}, 4},
		{"since in a folder", "d", false, ListOptions{Since: base.Add(-time.Minute), ByModified: true}, []string{"d/e.md", "d/c.md"}, 2},
		{"nothing that recent", "", true, ListOptions{Since: base.Add(2 * time.Hour)}, []string{}, 0},
	} {
		got, total, err := v.ListWith(c.dir, c.recursive, c.opts)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if !reflect.DeepEqual(listPaths(got), c.want) || total != c.total {
			t.Errorf("%s = %v (total %d), want %v (total %d)", c.name, listPaths(got), total, c.want, c.total)
		}
		for _, n := range got {
			if n.Title == "" || !n.ModTime.Equal(times[n.Path]) || n.Excerpt != "" {
				t.Errorf("%s: entry %+v", c.name, n)
			}
		}
	}

	// ListPage is ListWith in path order.
	page, total, err := v.ListPage("", true, 1, 2)
	if err != nil || total != 5 || !reflect.DeepEqual(listPaths(page), []string{"b.md", "d/c.md"}) {
		t.Errorf("ListPage = %v, %d, %v", listPaths(page), total, err)
	}
}
