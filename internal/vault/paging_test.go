package vault

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// pagingFixture has 12 notes matching "hit" with different strengths and
// one that does not.
func pagingFixture(t *testing.T) (*Vault, *Index, *ScanSearcher) {
	t.Helper()
	v := newTestVault(t)
	for i := range 12 {
		mustWrite(t, v, fmt.Sprintf("d%d/n%02d.md", i%3, i), "---\ntags: [p]\n---\n# N\n\nctx\n"+strings.Repeat("hit\n", 1+i%5)+"tail\n")
	}
	mustWrite(t, v, "d0/miss.md", "# Miss\n\nnothing\n")
	ix := NewIndex(v)
	if err := ix.Build(); err != nil {
		t.Fatal(err)
	}
	return v, ix, NewScanSearcher(v, ix)
}

func resultPaths(rs []Result) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Path
	}
	return out
}

func TestSearchPageWindowsTheRanking(t *testing.T) {
	_, _, s := pagingFixture(t)
	full, err := s.Search("hit", SearchOpts{})
	if err != nil || len(full) != 12 {
		t.Fatalf("full ranking = %d, %v", len(full), err)
	}

	for _, c := range []struct{ offset, max, want int }{
		{0, 5, 5}, {5, 5, 5}, {10, 5, 2}, {12, 5, 0}, {40, 5, 0}, {-3, 4, 4},
	} {
		p, err := s.SearchPage("hit", SearchOpts{Offset: c.offset, MaxResults: c.max})
		if err != nil {
			t.Fatal(err)
		}
		if p.Total != 12 || len(p.Results) != c.want {
			t.Errorf("offset %d max %d: %d results of %d, want %d of 12", c.offset, c.max, len(p.Results), p.Total, c.want)
			continue
		}
		from := min(max(c.offset, 0), len(full))
		if !reflect.DeepEqual(p.Results, full[from:from+c.want]) {
			t.Errorf("offset %d max %d = %v, want %v", c.offset, c.max, resultPaths(p.Results), resultPaths(full[from:from+c.want]))
		}
	}

	// Search itself is the first page, unchanged.
	first, _ := s.Search("hit", SearchOpts{MaxResults: 5})
	if !reflect.DeepEqual(first, full[:5]) {
		t.Errorf("Search with MaxResults 5 = %v", resultPaths(first))
	}

	// The pure tag filter and regex mode page the same way.
	p, _ := s.SearchPage("", SearchOpts{Tags: []string{"p"}, Offset: 10, MaxResults: 5})
	if p.Total != 12 || len(p.Results) != 2 || p.Results[0].Path != "d2/n08.md" {
		t.Errorf("tag filter page = %v of %d", resultPaths(p.Results), p.Total)
	}
	p, _ = s.SearchPage("^hit$", SearchOpts{Regex: true, Offset: 11})
	if p.Total != 12 || len(p.Results) != 1 {
		t.Errorf("regex page = %v of %d", resultPaths(p.Results), p.Total)
	}
	if p, _ := s.SearchPage("nothing-here", SearchOpts{}); p.Total != 0 || len(p.Results) != 0 {
		t.Errorf("no match = %+v", p)
	}
}

// plainSearcher hides ScanSearcher's paging, to exercise the fallback.
type plainSearcher struct{ s *ScanSearcher }

func (p plainSearcher) Search(q string, o SearchOpts) ([]Result, error) { return p.s.Search(q, o) }

func TestSearchPageFallbackMatchesNative(t *testing.T) {
	_, _, s := pagingFixture(t)
	for _, opts := range []SearchOpts{{}, {Offset: 3, MaxResults: 4}, {Offset: 11}, {Offset: 20}} {
		native, err := SearchPage(s, "hit", opts)
		if err != nil {
			t.Fatal(err)
		}
		fallback, err := SearchPage(plainSearcher{s}, "hit", opts)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(native, fallback) {
			t.Errorf("opts %+v: native %v of %d, fallback %v of %d", opts,
				resultPaths(native.Results), native.Total, resultPaths(fallback.Results), fallback.Total)
		}
	}
	if _, err := SearchPage(plainSearcher{s}, "([", SearchOpts{Regex: true}); err == nil {
		t.Error("fallback swallowed the regex error")
	}
}

func TestSearchNoContext(t *testing.T) {
	_, _, s := pagingFixture(t)
	rs, _ := s.Search("hit", SearchOpts{ContextLines: NoContext, MaxSnippets: 1, MaxResults: 1})
	if len(rs) != 1 || len(rs[0].Snippets) != 1 || rs[0].Snippets[0].Context != "hit" {
		t.Errorf("NoContext snippet = %+v", rs)
	}
	// Zero still means the default of two lines.
	rs, _ = s.Search("hit", SearchOpts{MaxSnippets: 1, MaxResults: 1})
	if got := strings.Count(rs[0].Snippets[0].Context, "\n"); got != 4 {
		t.Errorf("default context has %d line breaks, want 4: %q", got, rs[0].Snippets[0].Context)
	}
}

func TestWindow(t *testing.T) {
	items := []int{1, 2, 3, 4, 5}
	for _, c := range []struct {
		offset, limit int
		want          []int
	}{
		{0, 0, []int{1, 2, 3, 4, 5}},
		{0, 2, []int{1, 2}},
		{3, 10, []int{4, 5}},
		{5, 2, []int{}},
		{9, 2, []int{}},
		{-1, 2, []int{1, 2}},
	} {
		if got := window(items, c.offset, c.limit); !reflect.DeepEqual(got, c.want) {
			t.Errorf("window(%d, %d) = %v, want %v", c.offset, c.limit, got, c.want)
		}
	}
	if got := window([]int(nil), 0, 3); got != nil {
		t.Errorf("window(nil) = %v", got)
	}
}

func TestListPage(t *testing.T) {
	v, _, _ := pagingFixture(t)
	all, err := v.List("", true)
	if err != nil || len(all) != 13 {
		t.Fatalf("List = %d, %v", len(all), err)
	}
	for _, n := range all {
		if n.Title == "" {
			t.Errorf("%s has no title", n.Path)
		}
	}
	page, total, err := v.ListPage("", true, 4, 3)
	if err != nil || total != 13 || !reflect.DeepEqual(page, all[4:7]) {
		t.Errorf("ListPage(4, 3) = %v of %d, %v", page, total, err)
	}
	page, total, _ = v.ListPage("d1", false, 0, 0)
	if total != 4 || len(page) != 4 || page[0].Path != "d1/n01.md" {
		t.Errorf("ListPage(d1) = %v of %d", page, total)
	}
	if page, total, _ := v.ListPage("", false, 0, 0); total != 0 || page != nil {
		t.Errorf("root without notes = %v of %d", page, total)
	}
	if _, _, err := v.ListPage("nope", false, 0, 0); err == nil {
		t.Error("missing directory listed")
	}
}

func TestSubdirs(t *testing.T) {
	v, ix, _ := pagingFixture(t)
	for _, d := range []string{"d1/deep/er", "empty", ".hidden", "d2/.git"} {
		if err := os.MkdirAll(filepath.Join(v.Root(), d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite(t, v, "d1/deep/er/x.md", "# X\n")
	if err := ix.Update("d1/deep/er/x.md"); err != nil {
		t.Fatal(err)
	}
	// A note at the root does not count as a folder.
	mustWrite(t, v, "root.md", "# Root\n")
	if err := ix.Update("root.md"); err != nil {
		t.Fatal(err)
	}

	got, err := ix.Subdirs("")
	if err != nil {
		t.Fatal(err)
	}
	want := []DirCount{{Path: "d0", Notes: 5}, {Path: "d1", Notes: 5}, {Path: "d2", Notes: 4}, {Path: "empty", Notes: 0}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Subdirs(root) = %+v, want %+v", got, want)
	}
	for _, dir := range []string{"d1", "d1/", "./d1"} {
		got, err = ix.Subdirs(dir)
		if want := []DirCount{{Path: "d1/deep", Notes: 1}}; err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("Subdirs(%q) = %+v, %v; want %+v", dir, got, err, want)
		}
	}
	if got, _ := ix.Subdirs("d2"); len(got) != 0 || got == nil {
		t.Errorf("Subdirs(d2) = %#v, want an empty list", got)
	}
	if _, err := ix.Subdirs("../outside"); err == nil {
		t.Error("Subdirs escaped the vault")
	}
}
