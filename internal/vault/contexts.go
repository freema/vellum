package vault

import (
	"fmt"
	"path"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// Folder context (NSO-573). A folder can carry a short description of what
// it holds ("Development of vellum: specs, decisions, release notes"). The
// description lives in an ordinary note inside the folder, _index.md by
// default (VELLUM_CONTEXT_FILE): no hidden file, no database, visible and
// editable in any editor, and it moves with its folder. The text is the
// note's `description:` frontmatter field, or else the first paragraph of
// its body. The description of the vault root describes the whole vault.
//
// The index keeps folder -> description in memory, one entry per described
// folder, and hands it out where an agent decides what to open or where to
// put something: search_notes, list_notes, suggest_location, the read_note
// outline and the server instructions.

// DefaultContextFile is the file name of a folder's description note.
const DefaultContextFile = "_index.md"

const (
	// maxFolderDescription caps a folder's description, in characters.
	maxFolderDescription = 300
	// maxVaultDescription caps the vault root's description, which goes
	// into the server instructions once per session.
	maxVaultDescription = 500
)

// WithContextFile sets the file name of the folder description notes
// (VELLUM_CONTEXT_FILE), for vaults whose convention is README.md or
// index.md. New rejects a name that is not a plain markdown file name.
func WithContextFile(name string) Option {
	return func(v *Vault) { v.contextFile = name }
}

// ValidateContextFile checks a folder description file name: a markdown
// file name without a directory, not hidden (the vault scan skips dotfiles).
func ValidateContextFile(name string) error {
	switch {
	case name == "" || strings.ContainsAny(name, `/\`):
		return fmt.Errorf("%w: folder description file %q must be a file name without a directory", ErrInvalidPath, name)
	case strings.HasPrefix(name, "."):
		return fmt.Errorf("%w: folder description file %q must not be hidden", ErrInvalidPath, name)
	case !isMarkdown(name):
		return fmt.Errorf("%w: folder description file %q", ErrNotMarkdown, name)
	}
	return nil
}

// ContextFile returns the file name of the folder description notes.
func (v *Vault) ContextFile() string { return v.contextFile }

// IsContextNote reports whether p is a folder description note.
func (v *Vault) IsContextNote(p string) bool {
	return path.Base(p) == v.contextFile
}

// folderOf returns the folder of a note path, "" for the vault root.
func folderOf(p string) string {
	if d := path.Dir(p); d != "." {
		return d
	}
	return ""
}

// folderDescription distills a description note: the `description:`
// frontmatter field, or else the first paragraph of the body (headings,
// blank lines and rules skipped, as for a note's excerpt). Whitespace is
// collapsed to single spaces and the text capped at limit characters.
func folderDescription(note *Note, limit int) string {
	desc := ""
	if d, ok := note.Frontmatter["description"].(string); ok {
		desc = strings.Join(strings.Fields(d), " ")
	}
	if desc == "" {
		desc = firstParagraph(note.Body)
	}
	return capRunes(desc, limit)
}

// firstParagraph returns the first run of text lines of a markdown body,
// joined with single spaces. Leading headings, blank lines and rules are
// skipped; the paragraph ends at a blank line or a heading.
func firstParagraph(body string) string {
	var words []string
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		skip := line == "" || strings.HasPrefix(line, "#") || line == "---"
		if skip {
			if len(words) > 0 {
				break
			}
			continue
		}
		words = append(words, strings.Fields(line)...)
	}
	return strings.Join(words, " ")
}

// capRunes cuts s to at most limit characters, the last one an ellipsis
// when it had to cut. It counts runes, never cutting inside a multi-byte
// character (č, ř, …).
func capRunes(s string, limit int) string {
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	n := 0
	for i := range s {
		if n == limit-1 {
			return strings.TrimRight(s[:i], " ") + "…"
		}
		n++
	}
	return s
}

// contextNote is a parsed description note, cached against the index entry
// it was read for. The index replaces an entry pointer on every re-read and
// never mutates one in place, so a different pointer means the note changed.
type contextNote struct {
	entry *Entry
	desc  string
}

// rebuildContexts recomputes folder -> description from the description
// notes among the entries. Called from rebuildDerived, so every path that
// changes the entries (Build, Update, Remove, Rename) keeps it current.
// Only a description note whose entry changed since the last rebuild is
// read from disk: a handful of small files at startup, at most one per
// write afterwards. Memory grows with the number of described folders, not
// with the number of notes. Callers must hold ix.mu.
func (ix *Index) rebuildContexts() {
	notes := map[string]contextNote{}
	contexts := map[string]string{}
	for p, e := range ix.entries {
		if !ix.vault.IsContextNote(p) {
			continue
		}
		cn, ok := ix.contextNotes[p]
		if !ok || cn.entry != e {
			cn = contextNote{entry: e}
			if note, err := ix.vault.Read(p); err == nil {
				limit := maxFolderDescription
				if folderOf(p) == "" {
					limit = maxVaultDescription
				}
				cn.desc = folderDescription(note, limit)
			}
		}
		notes[p] = cn
		if cn.desc != "" {
			contexts[folderOf(p)] = cn.desc
		}
	}
	if len(notes) == 0 {
		notes, contexts = nil, nil // nothing held for a vault without them
	}
	ix.contextNotes, ix.contexts = notes, contexts
}

// FolderDescription returns the description of dir ("" = the vault root),
// or "" when it has none.
func (ix *Index) FolderDescription(dir string) string {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return ix.contexts[strings.Trim(dir, "/")]
}

// FolderContexts returns the descriptions of the folders holding the given
// notes and of all their parent folders, keyed by folder path, each level
// separately (they are not merged). The vault root is left out: its
// description goes into the server instructions once per session. Nil when
// none of these folders is described, so a vault without description notes
// answers exactly as before. Map keys sort parent before child, so the JSON
// of the map reads from the top folder down.
func (ix *Index) FolderContexts(notePaths ...string) map[string]string {
	ix.maybeReconcile()
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	if len(ix.contexts) == 0 {
		return nil
	}
	var out map[string]string
	for _, p := range notePaths {
		for dir := folderOf(p); dir != ""; dir = folderOf(dir) {
			if _, done := out[dir]; done {
				break // this folder and its parents are already in
			}
			if desc, ok := ix.contexts[dir]; ok {
				if out == nil {
					out = map[string]string{}
				}
				out[dir] = desc
			}
		}
	}
	return out
}

// UndescribedFolder is a folder that holds notes but no description note,
// with what an agent needs to propose a description.
type UndescribedFolder struct {
	Dir    string   `json:"dir"`            // "" is the vault root
	Path   string   `json:"path"`           // where its description note goes
	Notes  int      `json:"notes"`          // notes directly in the folder
	Titles []string `json:"titles"`         // up to five note titles, by path
	Tags   []string `json:"tags,omitempty"` // up to five most used tags
}

const (
	undescribedSampleTitles = 5
	undescribedTopTags      = 5
)

// FindUndescribedFolders returns the folders with at least minNotes notes
// of their own (subfolders not counted) and no description, most notes
// first. Description notes themselves are not counted.
func (ix *Index) FindUndescribedFolders(minNotes int) []UndescribedFolder {
	type profile struct {
		titles []string
		tags   map[string]int
	}
	profiles := map[string]*profile{}
	for _, e := range ix.All() { // sorted by path: titles come in path order
		if ix.vault.IsContextNote(e.Path) {
			continue
		}
		dir := folderOf(e.Path)
		p := profiles[dir]
		if p == nil {
			p = &profile{tags: map[string]int{}}
			profiles[dir] = p
		}
		p.titles = append(p.titles, e.Title)
		for _, t := range e.Tags {
			p.tags[t]++
		}
	}

	var out []UndescribedFolder
	for dir, p := range profiles {
		if len(p.titles) < minNotes || ix.FolderDescription(dir) != "" {
			continue
		}
		var tags []string
		for t := range p.tags {
			tags = append(tags, t)
		}
		sort.Slice(tags, func(i, j int) bool {
			if p.tags[tags[i]] != p.tags[tags[j]] {
				return p.tags[tags[i]] > p.tags[tags[j]]
			}
			return tags[i] < tags[j]
		})
		out = append(out, UndescribedFolder{
			Dir:    dir,
			Path:   path.Join(dir, ix.vault.contextFile),
			Notes:  len(p.titles),
			Titles: p.titles[:min(len(p.titles), undescribedSampleTitles)],
			Tags:   tags[:min(len(tags), undescribedTopTags)],
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Notes != out[j].Notes {
			return out[i].Notes > out[j].Notes
		}
		return out[i].Dir < out[j].Dir
	})
	return out
}

// contextWords returns the words of text that can tie content to a folder
// description, folded as search folds them (lowercase, no diacritics) and
// mapped to their first spelling in text: the word splitting of search
// (collectWords), minus numbers, words under three characters and common
// function words, which would match almost any description.
func contextWords(text string) map[string]string {
	out := map[string]string{}
	for _, w := range collectWords("", []string{norm.NFC.String(text)}) {
		f := fold(w.s)
		if _, seen := out[f]; seen || utf8.RuneCountInString(f) < 3 || contextStopWords[f] || isNumber(f) {
			continue
		}
		out[f] = w.s
	}
	return out
}

func isNumber(s string) bool {
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// contextStopWords are folded English and Czech function words of three
// characters and more, plus the words every vault description uses.
var contextStopWords = func() map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(`
		the and for are but not you all any can had her was one our out has his
		how its may new now see who did get let put say she too use with this
		that from have they will your what when where which there their them
		then than been were into also about more some only other over such just
		like these those each here very would could should after before
		note notes folder folders file files
		pro jak ale tak jen pri pod nad kde kdy nez ani aby byt byl byla bylo
		jsou jsem jste neni nebo take jako ktery ktera ktere kteri kdyz jeho
		jeji jejich tento tato toto tyto tady tam jeste proto protoze podle
		mezi pred pres bez pak vse vsech vsechno nas vas ten tom tim maji mit
		muze lze zde jsme budou bude jine jiny dalsi poznamka poznamky poznamek
		slozka slozky soubor soubory`) {
		m[w] = true
	}
	return m
}()
