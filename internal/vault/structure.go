package vault

import (
	"fmt"
	"os"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Structure names the conventional top-level directories. It is convention
// plus configuration, not a vellum-specific lock-in: the layout must make
// sense when the vault is opened in any other editor.
type Structure struct {
	Inbox    string
	Projects string
	Archive  string
}

// DefaultStructure returns the conventional inbox/projects/archive layout.
func DefaultStructure() Structure {
	return Structure{Inbox: "inbox", Projects: "projects", Archive: "archive"}
}

// InitStructure creates the structure directories when the vault root is
// empty. It is idempotent and never touches an already-populated vault
// (so pointing vellum at an existing Obsidian vault changes nothing).
// It reports whether the directories were created by this call.
func (v *Vault) InitStructure(s Structure) (bool, error) {
	entries, err := os.ReadDir(v.root)
	if err != nil {
		return false, fmt.Errorf("read vault root: %w", err)
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), ".") {
			return false, nil // non-empty vault: leave the layout alone
		}
	}
	for _, dir := range []string{s.Inbox, s.Projects, s.Archive} {
		abs, err := v.resolveDir(dir)
		if err != nil {
			return false, err
		}
		if err := os.MkdirAll(abs, 0o755); err != nil {
			return false, err
		}
	}
	return true, nil
}

// ResolveWritePath decides where a new note lands. When the agent names a
// directory the request is honored; a bare filename or no path at all falls
// back to the inbox. With no path the filename is derived from the content
// title and uniquified against existing notes.
func (v *Vault) ResolveWritePath(requested, content string, s Structure) (string, error) {
	if requested != "" {
		clean := path.Clean(strings.ReplaceAll(requested, "\\", "/"))
		if strings.Contains(clean, "/") {
			return clean, nil // explicit directory: use as requested
		}
		return path.Join(s.Inbox, clean), nil // bare filename: inbox
	}

	rawYAML, body := splitFrontmatter(content)
	title := deriveTitle("untitled.md", parseFrontmatter(rawYAML), body)
	slug := slugify(title)

	candidate := path.Join(s.Inbox, slug+".md")
	for i := 2; ; i++ {
		abs, err := v.resolveNote(candidate)
		if err != nil {
			return "", err
		}
		if _, err := os.Stat(abs); os.IsNotExist(err) {
			return candidate, nil
		} else if err != nil && !os.IsNotExist(err) {
			return "", err
		}
		candidate = path.Join(s.Inbox, fmt.Sprintf("%s-%d.md", slug, i))
	}
}

var (
	slugSpaceRe   = regexp.MustCompile(`\s+`)
	slugInvalidRe = regexp.MustCompile(`[^\p{L}\p{N}-]+`)
	slugDashRe    = regexp.MustCompile(`-{2,}`)
)

// maxSlugBytes bounds a derived filename well inside the 255-byte name limit
// every common filesystem shares, leaving room for a "-12.md" suffix. Without
// it a long enough title produces a name the kernel refuses (ENAMETOOLONG).
const maxSlugBytes = 200

// slugify turns a title into a safe, readable filename stem.
func slugify(title string) string {
	s := strings.ToLower(strings.TrimSpace(title))
	s = slugSpaceRe.ReplaceAllString(s, "-")
	s = slugInvalidRe.ReplaceAllString(s, "")
	s = slugDashRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	s = truncateSlug(s)
	if s == "" {
		return "untitled"
	}
	return s
}

// truncateSlug cuts a slug to maxSlugBytes on a rune boundary.
func truncateSlug(s string) string {
	if len(s) <= maxSlugBytes {
		return s
	}
	end := 0
	for i, r := range s {
		n := utf8.RuneLen(r)
		if i+n > maxSlugBytes {
			break
		}
		end = i + n
	}
	return strings.Trim(s[:end], "-")
}
