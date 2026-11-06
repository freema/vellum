package vault

import (
	"errors"
	"io/fs"
	"path"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

// Entry is the indexed metadata of a single note. Raw link targets are kept
// as written; resolution to vault paths happens in the index maps.
type Entry struct {
	Path    string   `json:"path"`
	Title   string   `json:"title"`
	Excerpt string   `json:"excerpt,omitempty"` // first body line, for list views
	Tags    []string `json:"tags,omitempty"`
	Links   []string `json:"links,omitempty"` // raw targets as written in the note
	Type    string   `json:"type,omitempty"`  // frontmatter `type` (task|knowledge)
	Status  string   `json:"status,omitempty"`
	ModTime int64    `json:"modTime"`
	Size    int64    `json:"size"`

	// modNano is the file's modification time in nanoseconds. ModTime
	// keeps its seconds for the JSON views; change detection (Reconcile,
	// the search cache) needs the full resolution, or an edit within the
	// same second that keeps the size would go unnoticed.
	modNano int64
}

// Index is the in-memory metadata index: tags and backlinks as instant
// lookups. It is rebuilt from disk once at startup and updated
// incrementally on every write that goes through the layers above
// (the vault file layer itself stays dumb). Changes made outside vellum
// (another editor, git, a second vellum process) are picked up lazily by
// Reconcile, see SetRescanInterval.
type Index struct {
	vault *Vault

	mu        sync.RWMutex
	entries   map[string]*Entry          // path -> entry
	tags      map[string]map[string]bool // tag -> set of paths
	forward   map[string]map[string]bool // path -> set of resolved targets
	backlinks map[string]map[string]bool // path -> set of paths linking to it
	observers []func(Change)

	// Folder descriptions (NSO-573, contexts.go): folder -> description,
	// and the parsed description notes it was built from.
	contexts     map[string]string
	contextNotes map[string]contextNote

	// Lazy reconcile with the disk (NSO-562, reconcile.go). scanMu lets
	// one Reconcile run at a time; epoch anchors lastScan on the monotonic
	// clock, so a wall-clock jump neither stalls nor floods rescans.
	scanMu      sync.Mutex
	epoch       time.Time
	rescanEvery atomic.Int64 // nanoseconds, 0 = off
	lastScan    atomic.Int64 // nanoseconds since epoch at the last scan
	rebuilds    int          // rebuildDerived calls, for tests; under mu
}

// Change is one incremental index mutation, published to OnChange observers.
// A rename arrives as two changes: the old path deleted, the new one updated.
type Change struct {
	Path    string
	Deleted bool
	// From is set on the update half of a rename that Reconcile inferred
	// from the disk (a note moved outside vellum): the path the note had
	// before. Renames through vellum (Rename) leave it empty; their caller
	// already knows both paths.
	From string
}

// OnChange registers an observer called after every incremental mutation
// (Update/Remove/Rename/Reconcile). Build does not emit — it runs once at
// startup, before observers exist. Observers run synchronously outside the
// index lock, so they may read the index but must not block for long.
func (ix *Index) OnChange(fn func(Change)) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	ix.observers = append(ix.observers, fn)
}

func (ix *Index) emit(c Change) {
	ix.mu.RLock()
	observers := ix.observers
	ix.mu.RUnlock()
	for _, fn := range observers {
		fn(c)
	}
}

// NewIndex creates an empty index bound to a vault. Call Build to populate.
func NewIndex(v *Vault) *Index {
	return &Index{
		vault:     v,
		entries:   map[string]*Entry{},
		tags:      map[string]map[string]bool{},
		forward:   map[string]map[string]bool{},
		backlinks: map[string]map[string]bool{},
		epoch:     time.Now(),
	}
}

// Build scans the whole vault in one pass. Files are read concurrently;
// entries and link resolution are computed once at the end. Build does not
// emit changes: it runs at startup, before observers exist.
func (ix *Index) Build() error {
	var paths []string
	err := ix.vault.walkNotes(ix.vault.root, true, func(rel, _ string, _ fs.FileInfo) {
		paths = append(paths, rel)
	})
	if err != nil {
		return err
	}
	entries := ix.vault.readEntries(paths)

	ix.mu.Lock()
	ix.entries = entries
	ix.rebuildDerived()
	ix.mu.Unlock()
	ix.markScanned()
	return nil
}

// readEntries reads the given notes concurrently into index entries, keyed
// by path. Unreadable or oversized files are left out: the index is a
// cache, not the source of truth.
func (v *Vault) readEntries(paths []string) map[string]*Entry {
	entries := make(map[string]*Entry, len(paths))
	if len(paths) == 0 {
		return entries
	}
	jobs := make(chan string)
	results := make(chan *Entry)
	var wg sync.WaitGroup
	for w := 0; w < min(8, len(paths)); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range jobs {
				if e, err := v.entryOf(p); err == nil {
					results <- e
				}
			}
		}()
	}
	go func() {
		for _, p := range paths {
			jobs <- p
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()
	for e := range results {
		entries[e.Path] = e
	}
	return entries
}

// entryOf reads one note and distills its index entry.
func (v *Vault) entryOf(path string) (*Entry, error) {
	note, err := v.Read(path)
	if err != nil {
		return nil, err
	}
	// Every string is cloned: the title, excerpt, tags and links are
	// substrings of the note's content, and an entry holding one would keep
	// the whole file in memory for as long as the index lives (about 7 KB
	// per note on a 5,000-note vault, most of the index's heap).
	e := &Entry{
		Path:    strings.Clone(note.Path),
		Title:   strings.Clone(note.Title),
		Excerpt: strings.Clone(excerptOf(note.Body)),
		Tags:    cloneStrings(note.Tags),
		Links:   cloneStrings(note.Links),
		ModTime: note.ModTime.Unix(),
		Size:    note.Size,
		modNano: note.ModTime.UnixNano(),
	}
	if t, ok := note.Frontmatter["type"].(string); ok {
		e.Type = strings.Clone(t)
	}
	if s, ok := note.Frontmatter["status"].(string); ok {
		e.Status = strings.Clone(s)
	}
	return e, nil
}

// cloneStrings copies a slice and every string in it, so nothing in the
// copy shares memory with the original (nil stays nil).
func cloneStrings(ss []string) []string {
	if ss == nil {
		return nil
	}
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = strings.Clone(s)
	}
	return out
}

// excerptOf returns the first non-heading, non-blank body text, capped for
// list views.
func excerptOf(body string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || line == "---" {
			continue
		}
		if len(line) > 160 {
			// Cut on a rune boundary — a byte slice through the middle of a
			// multi-byte character (č, ř, …) renders as U+FFFD in the UI.
			cut := 160
			for cut > 0 && !utf8.RuneStart(line[cut]) {
				cut--
			}
			return line[:cut] + "…"
		}
		return line
	}
	return ""
}

// Update re-reads a single note after a write (or first write) and refreshes
// the derived maps. If the file no longer exists it is removed instead.
func (ix *Index) Update(path string) error {
	entry, err := ix.vault.entryOf(path)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			ix.Remove(path)
			return nil
		}
		return err
	}
	ix.mu.Lock()
	ix.entries[entry.Path] = entry
	ix.rebuildDerived()
	ix.mu.Unlock()
	ix.emit(Change{Path: entry.Path})
	return nil
}

// Remove drops a note from the index (after Delete).
func (ix *Index) Remove(path string) {
	ix.mu.Lock()
	delete(ix.entries, path)
	ix.rebuildDerived()
	ix.mu.Unlock()
	ix.emit(Change{Path: path, Deleted: true})
}

// Rename moves an entry (after Move) without re-reading unaffected files.
func (ix *Index) Rename(from, to string) error {
	ix.mu.Lock()
	delete(ix.entries, from)
	ix.mu.Unlock()
	ix.emit(Change{Path: from, Deleted: true})
	return ix.Update(to)
}

// rebuildDerived recomputes tag and link maps from the entries. Pure RAM:
// no file IO, linear in the number of tags+links, so doing it on every
// mutation keeps resolution (e.g. wikilink ambiguity) always correct. The
// one exception is a folder description note that changed since the last
// rebuild, which rebuildContexts reads (a small file, rarely).
// Callers must hold ix.mu.
func (ix *Index) rebuildDerived() {
	ix.rebuilds++
	ix.tags = map[string]map[string]bool{}
	ix.forward = map[string]map[string]bool{}
	ix.backlinks = map[string]map[string]bool{}

	// Stem lookup for wikilink resolution: "note" -> candidate paths.
	stems := map[string][]string{}
	for p := range ix.entries {
		stem := strings.ToLower(strings.TrimSuffix(path.Base(p), path.Ext(p)))
		stems[stem] = append(stems[stem], p)
	}
	for _, paths := range stems {
		sort.Strings(paths) // deterministic pick on ambiguity
	}

	for p, e := range ix.entries {
		for _, tag := range e.Tags {
			if ix.tags[tag] == nil {
				ix.tags[tag] = map[string]bool{}
			}
			ix.tags[tag][p] = true
		}
		for _, raw := range e.Links {
			target := ix.resolveLink(p, raw, stems)
			if target == "" || target == p {
				continue
			}
			if ix.forward[p] == nil {
				ix.forward[p] = map[string]bool{}
			}
			ix.forward[p][target] = true
			if ix.backlinks[target] == nil {
				ix.backlinks[target] = map[string]bool{}
			}
			ix.backlinks[target][p] = true
		}
	}
	ix.rebuildContexts()
}

// resolveLink maps a raw link target to an indexed path, or "" if the link
// does not resolve. Markdown links are relative to the linking note;
// wikilinks with a slash are vault-relative; bare wikilinks match by stem.
func (ix *Index) resolveLink(from, raw string, stems map[string][]string) string {
	target := strings.TrimSpace(raw)
	if target == "" {
		return ""
	}
	if isMarkdown(target) {
		// Relative markdown link: resolve against the note's directory.
		rel := path.Clean(path.Join(path.Dir(from), target))
		if _, ok := ix.entries[rel]; ok {
			return rel
		}
		// Also accept vault-root-relative targets.
		clean := path.Clean(target)
		if _, ok := ix.entries[clean]; ok {
			return clean
		}
		return ""
	}
	// Wikilink. With a slash it is a vault-relative path (extension optional).
	if strings.Contains(target, "/") {
		clean := path.Clean(target)
		if !isMarkdown(clean) {
			clean += ".md"
		}
		if _, ok := ix.entries[clean]; ok {
			return clean
		}
		return ""
	}
	// Bare wikilink: match by filename stem, deterministic on ambiguity.
	if paths := stems[strings.ToLower(target)]; len(paths) > 0 {
		return paths[0]
	}
	return ""
}

// Get returns the entry for a path.
func (ix *Index) Get(path string) (Entry, bool) {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	e, ok := ix.entries[path]
	if !ok {
		return Entry{}, false
	}
	return *e, true
}

// Len returns the number of indexed notes.
func (ix *Index) Len() int {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return len(ix.entries)
}

// snapshot returns the current entries as shared pointers, unsorted. Safe to
// hold across the lock: Update/Build replace entry pointers wholesale and
// never mutate an *Entry in place. Used by the search hot path, which sorts
// by score anyway — All() below stays for callers wanting sorted copies.
func (ix *Index) snapshot() []*Entry {
	ix.maybeReconcile()
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	out := make([]*Entry, 0, len(ix.entries))
	for _, e := range ix.entries {
		out = append(out, e)
	}
	return out
}

// All returns a snapshot of all entries, sorted by path.
func (ix *Index) All() []Entry {
	ix.maybeReconcile()
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	out := make([]Entry, 0, len(ix.entries))
	for _, e := range ix.entries {
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// TagCount is a tag with its number of notes.
type TagCount struct {
	Tag   string `json:"tag"`
	Count int    `json:"count"`
}

// Tags returns all tags with counts, sorted by tag.
func (ix *Index) Tags() []TagCount {
	ix.maybeReconcile()
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	out := make([]TagCount, 0, len(ix.tags))
	for t, paths := range ix.tags {
		out = append(out, TagCount{Tag: t, Count: len(paths)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Tag < out[j].Tag })
	return out
}

// PathsByTag returns the sorted paths carrying a tag.
func (ix *Index) PathsByTag(tag string) []string {
	ix.maybeReconcile()
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return sortedKeys(ix.tags[tag])
}

// Backlinks returns the sorted paths of notes linking to path.
func (ix *Index) Backlinks(path string) []string {
	ix.maybeReconcile()
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return sortedKeys(ix.backlinks[path])
}

// Links returns the sorted resolved outgoing links of a note.
func (ix *Index) Links(path string) []string {
	ix.maybeReconcile()
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return sortedKeys(ix.forward[path])
}

func sortedKeys(set map[string]bool) []string {
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
