package vault

import (
	"path"
	"sort"
	"strings"
	"time"
)

// Curator heuristics (PHY-113). Everything here is deterministic context
// preparation for the agent — vellum never calls an LLM and never decides.
// Moves stay human-in-the-loop: these tools only return candidates.

// LocationSuggestion is a candidate directory for new content.
type LocationSuggestion struct {
	Dir        string   `json:"dir"`
	SharedTags []string `json:"sharedTags,omitempty"`
	NoteCount  int      `json:"noteCount"`
	Reason     string   `json:"reason"`
}

// SuggestLocation ranks existing directories by tag overlap with the given
// content. The inbox is always the fallback candidate.
func (ix *Index) SuggestLocation(content string, s Structure) []LocationSuggestion {
	rawYAML, body := splitFrontmatter(content)
	contentTags := map[string]bool{}
	for _, t := range extractTags(parseFrontmatter(rawYAML), body) {
		contentTags[t] = true
	}

	type dirProfile struct {
		count int
		tags  map[string]int
	}
	profiles := map[string]*dirProfile{}
	for _, e := range ix.All() {
		dir := path.Dir(e.Path)
		if dir == "." {
			continue
		}
		p := profiles[dir]
		if p == nil {
			p = &dirProfile{tags: map[string]int{}}
			profiles[dir] = p
		}
		p.count++
		for _, t := range e.Tags {
			p.tags[t]++
		}
	}

	var out []LocationSuggestion
	for dir, p := range profiles {
		var shared []string
		for t := range contentTags {
			if p.tags[t] > 0 {
				shared = append(shared, t)
			}
		}
		if len(shared) == 0 {
			continue
		}
		sort.Strings(shared)
		out = append(out, LocationSuggestion{
			Dir:        dir,
			SharedTags: shared,
			NoteCount:  p.count,
			Reason:     "notes in this folder share tags: " + strings.Join(shared, ", "),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if len(out[i].SharedTags) != len(out[j].SharedTags) {
			return len(out[i].SharedTags) > len(out[j].SharedTags)
		}
		return out[i].Dir < out[j].Dir
	})
	if len(out) > 5 {
		out = out[:5]
	}
	out = append(out, LocationSuggestion{
		Dir:    s.Inbox,
		Reason: "fallback: unfiled notes go to the inbox",
	})
	return out
}

// TagsContext is what an agent needs to propose tags for a note.
type TagsContext struct {
	Path        string     `json:"path"`
	CurrentTags []string   `json:"currentTags,omitempty"`
	Excerpt     string     `json:"excerpt"`
	VaultTags   []TagCount `json:"vaultTags"`
	// NeighborTags are tags used by notes linked from/to this note.
	NeighborTags []string `json:"neighborTags,omitempty"`
}

// SuggestTags gathers the context for tagging: the note excerpt, its
// current tags, the vault-wide tag vocabulary and tags of linked notes.
func (ix *Index) SuggestTags(v *Vault, notePath string) (*TagsContext, error) {
	note, err := v.Read(notePath)
	if err != nil {
		return nil, err
	}
	excerpt := note.Body
	if len(excerpt) > 1200 {
		excerpt = excerpt[:1200] + "…"
	}

	neighborSet := map[string]bool{}
	for _, p := range append(ix.Links(note.Path), ix.Backlinks(note.Path)...) {
		if e, ok := ix.Get(p); ok {
			for _, t := range e.Tags {
				neighborSet[t] = true
			}
		}
	}
	for _, t := range note.Tags {
		delete(neighborSet, t)
	}

	return &TagsContext{
		Path:         note.Path,
		CurrentTags:  note.Tags,
		Excerpt:      excerpt,
		VaultTags:    ix.Tags(),
		NeighborTags: sortedKeys(neighborSet),
	}, nil
}

// LinkCandidate is a note that might deserve a link.
type LinkCandidate struct {
	Path   string `json:"path"`
	Title  string `json:"title"`
	Reason string `json:"reason"`
}

// SuggestLinks returns link candidates for a note: shared tags, title
// mentions in the body, and backlinks without a forward link.
func (ix *Index) SuggestLinks(v *Vault, notePath string) ([]LinkCandidate, error) {
	note, err := v.Read(notePath)
	if err != nil {
		return nil, err
	}
	linked := map[string]bool{note.Path: true}
	for _, p := range ix.Links(note.Path) {
		linked[p] = true
	}

	bodyLower := strings.ToLower(note.Body)
	noteTags := map[string]bool{}
	for _, t := range note.Tags {
		noteTags[t] = true
	}

	seen := map[string]bool{}
	var out []LinkCandidate
	add := func(p, title, reason string) {
		if linked[p] || seen[p] {
			return
		}
		seen[p] = true
		out = append(out, LinkCandidate{Path: p, Title: title, Reason: reason})
	}

	// Backlinks without a forward link first — strongest signal.
	for _, p := range ix.Backlinks(note.Path) {
		if e, ok := ix.Get(p); ok {
			add(p, e.Title, "links here, but is not linked back")
		}
	}
	for _, e := range ix.All() {
		if e.Path == note.Path {
			continue
		}
		if len(e.Title) >= 4 && strings.Contains(bodyLower, strings.ToLower(e.Title)) {
			add(e.Path, e.Title, "its title appears in this note's text")
			continue
		}
		var shared []string
		for _, t := range e.Tags {
			if noteTags[t] {
				shared = append(shared, t)
			}
		}
		if len(shared) > 0 {
			sort.Strings(shared)
			add(e.Path, e.Title, "shares tags: "+strings.Join(shared, ", "))
		}
	}
	if len(out) > 20 {
		out = out[:20]
	}
	return out, nil
}

// FindUntagged returns notes without any tags.
func (ix *Index) FindUntagged() []Entry {
	var out []Entry
	for _, e := range ix.All() {
		if len(e.Tags) == 0 {
			out = append(out, e)
		}
	}
	return out
}

// FindOrphans returns notes with no incoming and no outgoing resolved links.
func (ix *Index) FindOrphans() []Entry {
	var out []Entry
	for _, e := range ix.All() {
		if len(ix.Links(e.Path)) == 0 && len(ix.Backlinks(e.Path)) == 0 {
			out = append(out, e)
		}
	}
	return out
}

// FindInboxStale returns inbox notes untouched for longer than maxAge,
// oldest first.
func (ix *Index) FindInboxStale(inbox string, maxAge time.Duration) []Entry {
	cutoff := time.Now().Add(-maxAge).Unix()
	prefix := strings.Trim(inbox, "/") + "/"
	var out []Entry
	for _, e := range ix.All() {
		if strings.HasPrefix(e.Path, prefix) && e.ModTime < cutoff {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ModTime < out[j].ModTime })
	return out
}
