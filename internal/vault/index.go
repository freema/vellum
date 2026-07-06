package vault

import (
	"errors"
	"path"
	"sort"
	"strings"
	"sync"
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
}

// Index is the in-memory metadata index: tags and backlinks as instant
// lookups. It is rebuilt from disk once at startup and updated
// incrementally on every write that goes through the layers above
// (the vault file layer itself stays dumb).
type Index struct {
	vault *Vault

	mu        sync.RWMutex
	entries   map[string]*Entry          // path -> entry
	tags      map[string]map[string]bool // tag -> set of paths
	forward   map[string]map[string]bool // path -> set of resolved targets
	backlinks map[string]map[string]bool // path -> set of paths linking to it
	observers []func(Change)
}

// Change is one incremental index mutation, published to OnChange observers.
// A rename arrives as two changes: the old path deleted, the new one updated.
type Change struct {
	Path    string
	Deleted bool
}

// OnChange registers an observer called after every incremental mutation
// (Update/Remove/Rename). Build does not emit — it runs once at startup,
// before observers exist. Observers run synchronously outside the index
// lock, so they may read the index but must not block for long.
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
	}
}

// Build scans the whole vault in one pass. Files are read concurrently;
// entries and link resolution are computed once at the end.
func (ix *Index) Build() error {
	infos, err := ix.vault.List("", true)
	if err != nil {
		return err
	}

	type result struct {
		entry *Entry
	}
	jobs := make(chan NoteInfo)
	results := make(chan result)
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for info := range jobs {
				if e, err := ix.vault.entryOf(info.Path); err == nil {
					results <- result{entry: e}
				}
				// Unreadable/oversized files are skipped: the index is a
				// cache, not the source of truth.
			}
		}()
	}
	go func() {
		for _, info := range infos {
			jobs <- info
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()

	entries := map[string]*Entry{}
	for r := range results {
		entries[r.entry.Path] = r.entry
	}

	ix.mu.Lock()
	defer ix.mu.Unlock()
	ix.entries = entries
	ix.rebuildDerived()
	return nil
}

// entryOf reads one note and distills its index entry.
func (v *Vault) entryOf(path string) (*Entry, error) {
	note, err := v.Read(path)
	if err != nil {
		return nil, err
	}
	e := &Entry{
		Path:    note.Path,
		Title:   note.Title,
		Excerpt: excerptOf(note.Body),
		Tags:    note.Tags,
		Links:   note.Links,
		ModTime: note.ModTime.Unix(),
		Size:    note.Size,
	}
	if t, ok := note.Frontmatter["type"].(string); ok {
		e.Type = t
	}
	if s, ok := note.Frontmatter["status"].(string); ok {
		e.Status = s
	}
	return e, nil
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
// mutation keeps resolution (e.g. wikilink ambiguity) always correct.
// Callers must hold ix.mu.
func (ix *Index) rebuildDerived() {
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
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return sortedKeys(ix.tags[tag])
}

// Backlinks returns the sorted paths of notes linking to path.
func (ix *Index) Backlinks(path string) []string {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return sortedKeys(ix.backlinks[path])
}

// Links returns the sorted resolved outgoing links of a note.
func (ix *Index) Links(path string) []string {
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
