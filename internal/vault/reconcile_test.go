package vault

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// writeOutside writes a note straight to disk, the way another editor or a
// git pull would: no vault method, no index update.
func writeOutside(t testing.TB, v *Vault, rel, content string) {
	t.Helper()
	abs := filepath.Join(v.Root(), filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func removeOutside(t testing.TB, v *Vault, rel string) {
	t.Helper()
	if err := os.Remove(filepath.Join(v.Root(), filepath.FromSlash(rel))); err != nil {
		t.Fatal(err)
	}
}

func moveOutside(t testing.TB, v *Vault, from, to string) {
	t.Helper()
	dst := filepath.Join(v.Root(), filepath.FromSlash(to))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(v.Root(), filepath.FromSlash(from)), dst); err != nil {
		t.Fatal(err)
	}
}

// recordChanges collects the index's change notifications.
func recordChanges(ix *Index) func() []Change {
	var mu sync.Mutex
	var got []Change
	ix.OnChange(func(c Change) {
		mu.Lock()
		got = append(got, c)
		mu.Unlock()
	})
	return func() []Change {
		mu.Lock()
		defer mu.Unlock()
		out := append([]Change(nil), got...)
		got = nil
		return out
	}
}

func searchHits(t *testing.T, s Searcher, query string) []string {
	t.Helper()
	rs, err := s.Search(query, SearchOpts{})
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, r := range rs {
		out = append(out, r.Path)
	}
	return out
}

func hasTag(ix *Index, tag string) bool {
	for _, tc := range ix.Tags() {
		if tc.Tag == tag {
			return true
		}
	}
	return false
}

// Acceptance: a note written straight to disk is found by search, its tag
// is listed and its link shows as a backlink once the interval has passed;
// deleted on disk, it disappears from all three. No explicit Reconcile:
// the reads themselves trigger it.
func TestRescanPicksUpOutsideChanges(t *testing.T) {
	v, ix := buildTestIndex(t)
	s := NewScanSearcher(v, ix)
	const every = 30 * time.Millisecond
	ix.SetRescanInterval(every)

	writeOutside(t, v, "inbox/outside.md", "---\ntags: [zvenku]\n---\n# Outside\n\nWritten in Obsidian, links to [[beta]]. Keyword kumquat.\n")

	time.Sleep(2 * every)
	if got := searchHits(t, s, "kumquat"); !reflect.DeepEqual(got, []string{"inbox/outside.md"}) {
		t.Errorf("search after the interval = %v, want the outside note", got)
	}
	if !hasTag(ix, "zvenku") {
		t.Error("tag of the outside note is not listed")
	}
	if got := ix.Backlinks("notes/beta.md"); !slices.Contains(got, "inbox/outside.md") {
		t.Errorf("backlinks(beta) = %v, want the outside note among them", got)
	}

	removeOutside(t, v, "inbox/outside.md")
	time.Sleep(2 * every)
	if got := searchHits(t, s, "kumquat"); len(got) != 0 {
		t.Errorf("search after delete = %v, want nothing", got)
	}
	if hasTag(ix, "zvenku") {
		t.Error("tag of the deleted note is still listed")
	}
	if got := ix.Backlinks("notes/beta.md"); slices.Contains(got, "inbox/outside.md") {
		t.Errorf("backlinks(beta) = %v still hold the deleted note", got)
	}
	if _, ok := ix.Get("inbox/outside.md"); ok {
		t.Error("deleted note still indexed")
	}
}

// Before the interval has passed the index answers from what it has, and
// with the rescan off (the default of a new Index) it never looks.
func TestRescanIsLazyAndCanBeOff(t *testing.T) {
	v, ix := buildTestIndex(t)
	if ix.RescanInterval() != 0 {
		t.Fatalf("new index rescans every %v, want off", ix.RescanInterval())
	}
	writeOutside(t, v, "inbox/late.md", "# Late\n\n#spat\n")
	if hasTag(ix, "spat") {
		t.Error("rescan off, yet the outside note was picked up")
	}

	ix.SetRescanInterval(time.Hour)
	if hasTag(ix, "spat") {
		t.Error("the interval has not passed, yet the index rescanned")
	}

	ix.SetRescanInterval(-time.Second) // negative means off
	if ix.RescanInterval() != 0 {
		t.Errorf("negative interval = %v, want off", ix.RescanInterval())
	}

	// An explicit Reconcile always looks.
	stats, err := ix.Reconcile()
	if err != nil {
		t.Fatal(err)
	}
	if stats != (ReconcileStats{Added: 1}) || !hasTag(ix, "spat") {
		t.Errorf("Reconcile = %+v, tag listed %v", stats, hasTag(ix, "spat"))
	}
}

// Acceptance: 500 changed notes cost one rebuild of the derived maps, not
// one per note as Update would.
func TestReconcileRebuildsOnce(t *testing.T) {
	v := newTestVault(t)
	for i := 0; i < 500; i++ {
		mustWrite(t, v, fmt.Sprintf("bulk/n%03d.md", i), fmt.Sprintf("# N%d\n\nfirst\n", i))
	}
	ix := NewIndex(v)
	if err := ix.Build(); err != nil {
		t.Fatal(err)
	}
	changes := recordChanges(ix)
	for i := 0; i < 500; i++ {
		writeOutside(t, v, fmt.Sprintf("bulk/n%03d.md", i), fmt.Sprintf("# N%d\n\nsecond version #v2\n", i))
	}

	before := ix.rebuildCount()
	stats, err := ix.Reconcile()
	if err != nil {
		t.Fatal(err)
	}
	if got := ix.rebuildCount() - before; got != 1 {
		t.Errorf("rebuildDerived ran %d times for 500 changes, want 1", got)
	}
	if stats != (ReconcileStats{Updated: 500}) {
		t.Errorf("stats = %+v, want 500 updated", stats)
	}
	if got := len(ix.PathsByTag("v2")); got != 500 {
		t.Errorf("notes with the new tag = %d, want 500", got)
	}
	if got := len(changes()); got != 500 {
		t.Errorf("changes emitted = %d, want one per note", got)
	}

	// Nothing changed since: no rebuild, no change, no read.
	before = ix.rebuildCount()
	stats, err = ix.Reconcile()
	if err != nil {
		t.Fatal(err)
	}
	if stats.Changed() || ix.rebuildCount() != before || len(changes()) != 0 {
		t.Errorf("unchanged vault: stats %+v, rebuilds +%d", stats, ix.rebuildCount()-before)
	}
}

func (ix *Index) rebuildCount() int {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return ix.rebuilds
}

// Writes through vellum keep the index current, so the next scan finds
// nothing to do for them.
func TestReconcileAfterVellumWritesIsNoop(t *testing.T) {
	v, ix := buildTestIndex(t)
	if err := v.Write("inbox/via.md", "# Via vellum\n", WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := ix.Update("inbox/via.md"); err != nil {
		t.Fatal(err)
	}
	stats, err := ix.Reconcile()
	if err != nil {
		t.Fatal(err)
	}
	if stats.Changed() {
		t.Errorf("reconcile after a vellum write changed %+v", stats)
	}
}

// Adds, edits and deletes each reach observers, deletions first, after
// the index already shows the new state.
func TestReconcileEmitsChanges(t *testing.T) {
	v, ix := buildTestIndex(t)
	var seenDuringEmit []bool
	changes := recordChanges(ix)
	ix.OnChange(func(c Change) {
		_, ok := ix.Get(c.Path)
		seenDuringEmit = append(seenDuringEmit, ok == !c.Deleted)
	})

	writeOutside(t, v, "inbox/new.md", "# New\n")
	writeOutside(t, v, "projects/p/gamma.md", "# Gamma\n\nRewritten outside, still links [[alpha]].\n")
	removeOutside(t, v, "notes/beta.md")

	stats, err := ix.Reconcile()
	if err != nil {
		t.Fatal(err)
	}
	if stats != (ReconcileStats{Added: 1, Updated: 1, Removed: 1}) {
		t.Errorf("stats = %+v", stats)
	}
	want := []Change{
		{Path: "notes/beta.md", Deleted: true},
		{Path: "inbox/new.md"},
		{Path: "projects/p/gamma.md"},
	}
	if got := changes(); !reflect.DeepEqual(got, want) {
		t.Errorf("changes = %+v, want %+v", got, want)
	}
	for i, ok := range seenDuringEmit {
		if !ok {
			t.Errorf("change %d emitted before the index showed it", i)
		}
	}
}

// A note renamed outside vellum, the only one with its file name and size,
// is reported as a move, so a share link can follow it. Links to it keep
// resolving by name.
func TestReconcileDetectsMove(t *testing.T) {
	v, ix := buildTestIndex(t)
	changes := recordChanges(ix)
	moveOutside(t, v, "notes/beta.md", "archive/2026/beta.md")

	stats, err := ix.Reconcile()
	if err != nil {
		t.Fatal(err)
	}
	if stats != (ReconcileStats{Moved: 1}) {
		t.Errorf("stats = %+v, want one move", stats)
	}
	want := []Change{
		{Path: "notes/beta.md", Deleted: true},
		{Path: "archive/2026/beta.md", From: "notes/beta.md"},
	}
	if got := changes(); !reflect.DeepEqual(got, want) {
		t.Errorf("changes = %+v, want %+v", got, want)
	}
	if got := ix.Backlinks("archive/2026/beta.md"); !reflect.DeepEqual(got, []string{"inbox/alpha.md"}) {
		t.Errorf("backlinks of the moved note = %v", got)
	}
}

// Ambiguous pairs are not guessed: two notes of the same name and size
// vanish and two appear, or the size differs, and it stays delete + add.
func TestReconcileAmbiguousMoveIsDeleteAndAdd(t *testing.T) {
	v := newTestVault(t)
	mustWrite(t, v, "a/same.md", "# Same\n")
	mustWrite(t, v, "b/same.md", "# Same\n")
	mustWrite(t, v, "c/grown.md", "# Grown\n")
	ix := NewIndex(v)
	if err := ix.Build(); err != nil {
		t.Fatal(err)
	}
	changes := recordChanges(ix)
	moveOutside(t, v, "a/same.md", "x/same.md")
	moveOutside(t, v, "b/same.md", "y/same.md")
	removeOutside(t, v, "c/grown.md")
	writeOutside(t, v, "z/grown.md", "# Grown\n\nand longer\n")

	stats, err := ix.Reconcile()
	if err != nil {
		t.Fatal(err)
	}
	if stats != (ReconcileStats{Added: 3, Removed: 3}) {
		t.Errorf("stats = %+v, want 3 added + 3 removed", stats)
	}
	for _, c := range changes() {
		if c.From != "" {
			t.Errorf("ambiguous change reported as a move: %+v", c)
		}
	}
}

// The scan sees exactly what List sees: dot-folders, dotfiles, other
// extensions and symlinks stay out.
func TestReconcileSkipsWhatListSkips(t *testing.T) {
	v, ix := buildTestIndex(t)
	writeOutside(t, v, ".obsidian/workspace.md", "# hidden\n")
	writeOutside(t, v, "inbox/.draft.md", "# hidden\n")
	writeOutside(t, v, "inbox/data.json", "{}")
	writeOutside(t, v, "inbox/real.md", "# Real\n")
	if err := os.Symlink(filepath.Join(v.Root(), "inbox", "real.md"), filepath.Join(v.Root(), "inbox", "link.md")); err != nil {
		t.Fatal(err)
	}

	if _, err := ix.Reconcile(); err != nil {
		t.Fatal(err)
	}
	listed, err := v.List("", true)
	if err != nil {
		t.Fatal(err)
	}
	var indexed []string
	for _, e := range ix.All() {
		indexed = append(indexed, e.Path)
	}
	if want := paths(listed); !reflect.DeepEqual(indexed, want) {
		t.Errorf("indexed = %v, listed = %v", indexed, want)
	}
}

// An edit that keeps the size and lands in the same second as the indexed
// version is still caught (nanosecond modification times), and the search
// cache, keyed by the same timestamp, serves the new text.
func TestReconcileSameSecondSameSizeEdit(t *testing.T) {
	v := newTestVault(t)
	mustWrite(t, v, "inbox/n.md", "# N\n\nplum\n")
	ix := NewIndex(v)
	if err := ix.Build(); err != nil {
		t.Fatal(err)
	}
	s := NewScanSearcher(v, ix)
	if got := searchHits(t, s, "plum"); len(got) != 1 {
		t.Fatalf("plum = %v", got)
	}
	e, _ := ix.Get("inbox/n.md")
	old := time.Unix(0, e.modNano)

	writeOutside(t, v, "inbox/n.md", "# N\n\npear\n") // same size
	abs := filepath.Join(v.Root(), "inbox", "n.md")
	newTime := old.Add(time.Microsecond)
	if newTime.Unix() != old.Unix() {
		newTime = old.Add(-time.Microsecond)
	}
	if err := os.Chtimes(abs, newTime, newTime); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(abs); fi.ModTime().UnixNano() == old.UnixNano() {
		t.Skip("filesystem without sub-second timestamps")
	}

	stats, err := ix.Reconcile()
	if err != nil {
		t.Fatal(err)
	}
	if stats.Updated != 1 {
		t.Fatalf("same-second edit not detected: %+v", stats)
	}
	if got := searchHits(t, s, "pear"); len(got) != 1 {
		t.Errorf("pear = %v, search cache served the old text", got)
	}
	if got := searchHits(t, s, "plum"); len(got) != 0 {
		t.Errorf("plum = %v after the edit", got)
	}
}

// Many readers past the interval at once: one of them reconciles, the
// rest answer from the current state, and an observer that reads the index
// while the scan emits does not deadlock.
func TestRescanConcurrentReaders(t *testing.T) {
	v, ix := buildTestIndex(t)
	ix.SetRescanInterval(time.Millisecond)
	ix.OnChange(func(Change) { _ = ix.Tags() })
	s := NewScanSearcher(v, ix)

	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				if w == 0 {
					abs := filepath.Join(v.Root(), "inbox", fmt.Sprintf("c%d.md", i))
					if err := os.WriteFile(abs, []byte("# C\n\n#conc\n"), 0o644); err != nil {
						t.Error(err)
					}
				}
				_ = ix.Tags()
				_ = ix.Backlinks("notes/beta.md")
				if _, err := s.Search("conc", SearchOpts{}); err != nil {
					t.Error(err)
				}
				time.Sleep(time.Millisecond)
			}
		}(w)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("readers deadlocked")
	}
	time.Sleep(2 * time.Millisecond)
	if got := len(ix.PathsByTag("conc")); got != 20 {
		t.Errorf("notes tagged conc = %d, want 20", got)
	}
}

// Two indexes over one folder stand in for two vellum processes (Claude
// Code and Claude Desktop each starting their own): a write through one
// reaches the other's search within the interval.
func TestRescanSecondIndexSeesWrites(t *testing.T) {
	dir := t.TempDir()
	open := func() (*Vault, *Index, *ScanSearcher) {
		v, err := New(dir)
		if err != nil {
			t.Fatal(err)
		}
		ix := NewIndex(v)
		if err := ix.Build(); err != nil {
			t.Fatal(err)
		}
		return v, ix, NewScanSearcher(v, ix)
	}
	va, ixa, _ := open()
	_, ixb, sb := open()
	const every = 30 * time.Millisecond
	ixb.SetRescanInterval(every)

	if err := va.Write("inbox/from-a.md", "# From A\n\nquokka\n", WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := ixa.Update("inbox/from-a.md"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * every)
	for {
		if got := searchHits(t, sb, "quokka"); len(got) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the second index did not see the write within the interval")
		}
		time.Sleep(every / 5)
	}
}

// The derived maps after a reconcile equal a fresh Build of the same disk.
func TestReconcileMatchesFreshBuild(t *testing.T) {
	v, ix := buildTestIndex(t)
	writeOutside(t, v, "inbox/n1.md", "---\ntags: [x]\n---\nSee [[gamma]] and [[n2]].\n")
	writeOutside(t, v, "inbox/n2.md", "# N2\n\n#y back to [[n1]]\n")
	writeOutside(t, v, "inbox/alpha.md", "# Alpha\n\nNo links any more. #z\n")
	removeOutside(t, v, "notes/beta.md")
	if _, err := ix.Reconcile(); err != nil {
		t.Fatal(err)
	}
	fresh := NewIndex(v)
	if err := fresh.Build(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ix.Tags(), fresh.Tags()) {
		t.Errorf("tags = %v, fresh build %v", ix.Tags(), fresh.Tags())
	}
	for _, e := range fresh.All() {
		if !reflect.DeepEqual(ix.Backlinks(e.Path), fresh.Backlinks(e.Path)) ||
			!reflect.DeepEqual(ix.Links(e.Path), fresh.Links(e.Path)) {
			t.Errorf("%s: links %v / %v, fresh %v / %v", e.Path,
				ix.Links(e.Path), ix.Backlinks(e.Path), fresh.Links(e.Path), fresh.Backlinks(e.Path))
		}
	}
	got, want := ix.All(), fresh.All()
	if len(got) != len(want) {
		t.Fatalf("entries %d, fresh %d", len(got), len(want))
	}
	for i := range got {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Errorf("entry %s = %+v, fresh %+v", got[i].Path, got[i], want[i])
		}
	}
}

// BenchmarkReconcileUnchanged measures the rescan of a vault nothing
// touched: the cost every read pays once per interval. The target is
// under 20 ms for 5,000 notes.
func BenchmarkReconcileUnchanged(b *testing.B) {
	for _, n := range []int{2000, 5000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			v := newTestVault(b)
			for i := 0; i < n; i++ {
				writeOutside(b, v, fmt.Sprintf("bulk/p%d/n%d.md", i%25, i),
					"---\ntags: [t"+fmt.Sprint(i%7)+"]\n---\n# Note "+fmt.Sprint(i)+"\n\n"+strings.Repeat("Body text line.\n", 20))
			}
			ix := NewIndex(v)
			if err := ix.Build(); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				stats, err := ix.Reconcile()
				if err != nil || stats.Changed() {
					b.Fatalf("stats %+v, err %v", stats, err)
				}
			}
		})
	}
}
