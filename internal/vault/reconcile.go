package vault

import (
	"io/fs"
	"path"
	"sort"
	"time"
)

// ReconcileStats counts what one Reconcile changed in the index.
type ReconcileStats struct {
	Added   int // notes new on disk
	Updated int // notes whose size or modification time changed
	Removed int // notes gone from disk
	Moved   int // a removal and an addition recognized as one rename
}

// Changed reports whether the index changed at all.
func (s ReconcileStats) Changed() bool {
	return s.Added+s.Updated+s.Removed+s.Moved > 0
}

// SetRescanInterval turns on the lazy reconcile (NSO-562): an index read
// (search, tags, backlinks, tasks, the curator, folder contexts) first runs
// Reconcile when the last one is older than d. Only one reconcile runs at a
// time; a read that finds one in progress does not wait and answers from
// the current state. There is no background goroutine: an idle server does
// no work. d <= 0 turns it off (the default of a new Index).
func (ix *Index) SetRescanInterval(d time.Duration) {
	ix.rescanEvery.Store(max(int64(d), 0))
}

// RescanInterval returns the interval set by SetRescanInterval, 0 if off.
func (ix *Index) RescanInterval() time.Duration {
	return time.Duration(ix.rescanEvery.Load())
}

// sinceEpoch is the monotonic time since the index was created.
func (ix *Index) sinceEpoch() int64 { return int64(time.Since(ix.epoch)) }

// markScanned records that the index now matches the disk.
func (ix *Index) markScanned() { ix.lastScan.Store(ix.sinceEpoch()) }

// due reports whether the rescan interval has passed since the last scan.
func (ix *Index) due() bool {
	every := ix.rescanEvery.Load()
	return every > 0 && ix.sinceEpoch()-ix.lastScan.Load() >= every
}

// maybeReconcile runs Reconcile when it is due and no other one is running.
// It is called at the top of the index's read methods, before they take
// ix.mu. The fast path is two atomic loads and a clock read. Errors are
// dropped: the index keeps serving what it has, and the next interval
// tries again.
func (ix *Index) maybeReconcile() {
	if !ix.due() || !ix.scanMu.TryLock() {
		return
	}
	defer ix.scanMu.Unlock()
	if !ix.due() { // another caller finished one while we got here
		return
	}
	_, _ = ix.reconcile()
}

// Reconcile brings the index in line with the disk after changes made
// outside vellum: an edit in another editor, a git pull, a second vellum
// process over the same vault. It walks the vault by the rules of List
// and compares each note's size and modification time with its entry; only
// new and changed notes are read, entries of vanished notes are dropped,
// and the derived maps are rebuilt once at the end, however many notes
// changed. A note that disappears while one with the same file name and
// size appears elsewhere, the only such pair in this scan, is taken as
// moved (Change.From), so a share link can follow it. Observers get a
// Change for every touched path after the index lock is released.
//
// An edit that keeps both the size and the modification time is not
// detected; with nanosecond timestamps that is rare.
func (ix *Index) Reconcile() (ReconcileStats, error) {
	ix.scanMu.Lock()
	defer ix.scanMu.Unlock()
	return ix.reconcile()
}

// fileStat is what the directory walk knows about a note.
type fileStat struct {
	path string
	size int64
	mod  int64 // nanoseconds
}

// reconcile does the work of Reconcile. Callers must hold ix.scanMu.
func (ix *Index) reconcile() (ReconcileStats, error) {
	ix.mu.RLock()
	disk := make([]fileStat, 0, len(ix.entries))
	ix.mu.RUnlock()
	err := ix.vault.walkNotes(ix.vault.root, true, func(rel, _ string, fi fs.FileInfo) {
		disk = append(disk, fileStat{path: rel, size: fi.Size(), mod: fi.ModTime().UnixNano()})
	})
	if err != nil {
		ix.markScanned() // do not retry on every read; the next interval will
		return ReconcileStats{}, err
	}

	// Compare with the index. prev remembers the entry each stale path had
	// (nil for a new note), so the apply step below can tell whether a
	// write through vellum replaced it in the meantime.
	var stale []string
	prev := map[string]*Entry{}
	var gone []*Entry
	ix.mu.RLock()
	matched := 0
	for _, f := range disk {
		e := ix.entries[f.path]
		if e != nil {
			matched++
		}
		if e == nil || e.modNano != f.mod || e.Size != f.size {
			stale = append(stale, f.path)
			prev[f.path] = e
		}
	}
	if matched < len(ix.entries) {
		// Something indexed is no longer on disk. The set of disk paths is
		// only built in this case; an unchanged vault allocates no map.
		onDisk := make(map[string]struct{}, len(disk))
		for _, f := range disk {
			onDisk[f.path] = struct{}{}
		}
		for p, e := range ix.entries {
			if _, ok := onDisk[p]; !ok {
				gone = append(gone, e)
			}
		}
	}
	ix.mu.RUnlock()
	if len(stale) == 0 && len(gone) == 0 {
		ix.markScanned()
		return ReconcileStats{}, nil
	}

	fresh := ix.vault.readEntries(stale)
	var added []*Entry
	for _, p := range stale {
		switch e := fresh[p]; {
		case e == nil && prev[p] != nil:
			// Vanished or became unreadable since the walk: drop it, as
			// Build would not have indexed it either.
			gone = append(gone, prev[p])
		case e != nil && prev[p] == nil:
			added = append(added, e)
		}
	}
	moves := detectMoves(gone, added)

	// Apply. An entry that changed since the comparison above was written
	// through vellum meanwhile (Update/Remove/Rename); that newer state
	// wins and this scan leaves the path alone.
	var stats ReconcileStats
	var deleted, updated []Change
	removedNow := map[string]bool{}
	ix.mu.Lock()
	for _, e := range gone {
		if ix.entries[e.Path] == e {
			delete(ix.entries, e.Path)
			removedNow[e.Path] = true
			deleted = append(deleted, Change{Path: e.Path, Deleted: true})
		}
	}
	for _, p := range stale {
		e := fresh[p]
		if e == nil || ix.entries[p] != prev[p] {
			continue
		}
		ix.entries[p] = e
		c := Change{Path: p}
		switch from, ok := moves[p]; {
		case ok && removedNow[from]:
			c.From = from
			stats.Moved++
		case prev[p] == nil:
			stats.Added++
		default:
			stats.Updated++
		}
		updated = append(updated, c)
	}
	stats.Removed = len(deleted) - stats.Moved
	if len(deleted)+len(updated) > 0 {
		ix.rebuildDerived()
	}
	ix.mu.Unlock()
	ix.markScanned()

	sort.Slice(deleted, func(i, j int) bool { return deleted[i].Path < deleted[j].Path })
	sort.Slice(updated, func(i, j int) bool { return updated[i].Path < updated[j].Path })
	for _, c := range deleted {
		ix.emit(c)
	}
	for _, c := range updated {
		ix.emit(c)
	}
	return stats, nil
}

// detectMoves pairs vanished notes with new ones that have the same file
// name and size, when the pair is unambiguous on both sides: exactly one
// vanished note and exactly one new note with that name and size. Anything
// else stays a removal plus an addition. Returns new path -> old path.
func detectMoves(gone, added []*Entry) map[string]string {
	if len(gone) == 0 || len(added) == 0 {
		return nil
	}
	type key struct {
		name string
		size int64
	}
	from := map[key][]string{}
	for _, e := range gone {
		k := key{path.Base(e.Path), e.Size}
		from[k] = append(from[k], e.Path)
	}
	to := map[key][]string{}
	for _, e := range added {
		k := key{path.Base(e.Path), e.Size}
		if _, ok := from[k]; ok {
			to[k] = append(to[k], e.Path)
		}
	}
	moves := map[string]string{}
	for k, ts := range to {
		if fs := from[k]; len(fs) == 1 && len(ts) == 1 {
			moves[ts[0]] = fs[0]
		}
	}
	return moves
}
