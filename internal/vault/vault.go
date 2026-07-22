// Package vault implements the dumb, deterministic file layer over the
// markdown vault: CRUD with path traversal protection and optimistic
// concurrency via content hashes (PHY-106).
package vault

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Sentinel errors. Callers (MCP tools, REST API) map these to protocol errors;
// the REST layer maps ErrConflict to HTTP 409.
var (
	ErrInvalidPath     = errors.New("invalid path")
	ErrNotMarkdown     = errors.New("not a markdown file")
	ErrNotFound        = errors.New("note not found")
	ErrExists          = errors.New("note already exists")
	ErrConflict        = errors.New("content hash mismatch")
	ErrTooLarge        = errors.New("file exceeds maximum size")
	ErrSectionNotFound = errors.New("section not found")
)

// DefaultMaxFileSize is the default cap for a single note (10 MB).
const DefaultMaxFileSize = 10 << 20

// Vault is a safe view over a directory of markdown files. All paths accepted
// by its methods are vault-relative, forward-slash separated.
type Vault struct {
	root    string // absolute path with symlinks resolved
	maxSize int64

	// mu makes the check-and-write of ExpectedHash atomic within this process.
	// There are deliberately no on-disk locks (single-binary design).
	mu sync.Mutex
}

// Option configures a Vault.
type Option func(*Vault)

// WithMaxFileSize overrides the per-note size cap in bytes.
func WithMaxFileSize(n int64) Option {
	return func(v *Vault) { v.maxSize = n }
}

// New opens a vault rooted at dir. The directory must exist.
func New(dir string, opts ...Option) (*Vault, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve vault root: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("resolve vault root: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return nil, fmt.Errorf("stat vault root: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("vault root %s is not a directory", dir)
	}
	v := &Vault{root: resolved, maxSize: DefaultMaxFileSize}
	for _, o := range opts {
		o(v)
	}
	return v, nil
}

// Root returns the absolute vault root directory.
func (v *Vault) Root() string { return v.root }

// isMarkdown reports whether the path has an allowed markdown extension.
func isMarkdown(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".markdown":
		return true
	}
	return false
}

// hasHiddenSegment reports whether any segment of a vault path starts with a
// dot. List and the index skip dotfiles and dot-directories — they are editor
// and OS metadata — so a note created under one would sit on disk yet vanish
// from vellum on the next index build. Writes refuse such a path; reads of an
// existing one still work, so nothing already there is trapped.
func hasHiddenSegment(rel string) bool {
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(rel)))
	for _, seg := range strings.Split(clean, "/") {
		if len(seg) > 1 && seg[0] == '.' && seg != ".." {
			return true
		}
	}
	return false
}

// checkNotHidden refuses a write whose target is hidden from the vault scan,
// lexically or through a symlink. The lexical check catches `.private/n.md`;
// the physical one catches `visible/n.md` where `visible` links to a hidden
// directory — the requested path looks clean but the note lands under a dot
// segment and disappears from the index on the next build. phys is the
// physical target from resolvePhysical, so no extra filesystem walk is done.
func (v *Vault) checkNotHidden(rel, phys string) error {
	if hasHiddenSegment(rel) {
		return fmt.Errorf("%w: %s is hidden from the vault", ErrInvalidPath, rel)
	}
	if inner, ok := v.relativeTo(phys); ok && hasHiddenSegment(filepath.ToSlash(inner)) {
		return fmt.Errorf("%w: %s resolves into a hidden path", ErrInvalidPath, rel)
	}
	return nil
}

// resolveDir validates a vault-relative directory path ("" means the root)
// and returns its absolute form. The directory does not have to exist.
func (v *Vault) resolveDir(rel string) (string, error) {
	if rel == "" || rel == "." {
		return v.root, nil
	}
	return v.resolve(rel)
}

// resolveNote validates a vault-relative note path: markdown extension
// required on top of the generic path rules.
func (v *Vault) resolveNote(rel string) (string, error) {
	if !isMarkdown(rel) {
		return "", fmt.Errorf("%w: %s", ErrNotMarkdown, rel)
	}
	return v.resolve(rel)
}

// resolveNotePhysical is resolveNote plus the physical landing path (symlinks
// in the existing ancestors resolved), so a write can inspect where the note
// really lands without walking the tree a second time.
func (v *Vault) resolveNotePhysical(rel string) (abs, phys string, err error) {
	if !isMarkdown(rel) {
		return "", "", fmt.Errorf("%w: %s", ErrNotMarkdown, rel)
	}
	return v.resolvePhysical(rel)
}

// resolve turns a vault-relative path into an absolute one, rejecting
// anything that would escape the root: absolute paths, ../ traversal,
// null bytes, and symlinks pointing outside the vault.
func (v *Vault) resolve(rel string) (string, error) {
	abs, _, err := v.resolvePhysical(rel)
	return abs, err
}

// resolvePhysical does the work of resolve and additionally returns the
// physical target (see checkSymlinks) so callers that need to inspect the real
// landing location reuse the single ancestor walk instead of repeating it.
func (v *Vault) resolvePhysical(rel string) (abs, phys string, err error) {
	if rel == "" {
		return "", "", fmt.Errorf("%w: empty path", ErrInvalidPath)
	}
	if strings.ContainsRune(rel, 0) {
		return "", "", fmt.Errorf("%w: null byte in path", ErrInvalidPath)
	}
	// Vault paths are forward-slash; convert before any filepath logic.
	rel = filepath.FromSlash(rel)
	if filepath.IsAbs(rel) {
		return "", "", fmt.Errorf("%w: absolute path %s", ErrInvalidPath, rel)
	}
	clean := filepath.Clean(rel)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("%w: path escapes vault: %s", ErrInvalidPath, rel)
	}
	abs = filepath.Join(v.root, clean)
	if _, ok := v.relativeTo(abs); !ok {
		return "", "", fmt.Errorf("%w: path escapes vault: %s", ErrInvalidPath, rel)
	}
	// Symlink check: resolve the deepest existing ancestor (the path itself
	// may not exist yet for writes) and require it to stay inside the root.
	phys, err = v.checkSymlinks(abs)
	if err != nil {
		return "", "", err
	}
	return abs, phys, nil
}

// checkSymlinks walks from abs up to the first existing path component,
// resolves it, and verifies the result is still inside the vault. It also
// rejects a final component that is itself a symlink, wherever it points. It
// returns the physical target — the resolved existing ancestor with the
// not-yet-existing tail re-appended — which is where a write to abs lands.
func (v *Vault) checkSymlinks(abs string) (string, error) {
	existing := abs
	var tail []string
	for {
		fi, err := os.Lstat(existing)
		if err == nil {
			if fi.Mode()&os.ModeSymlink != 0 && existing == abs {
				return "", fmt.Errorf("%w: %s is a symlink", ErrInvalidPath, existing)
			}
			break
		}
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("resolve %s: %w", abs, err)
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			break
		}
		tail = append([]string{filepath.Base(existing)}, tail...)
		existing = parent
	}
	resolved, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", abs, err)
	}
	if _, ok := v.relativeTo(resolved); !ok {
		return "", fmt.Errorf("%w: path escapes vault via symlink: %s", ErrInvalidPath, abs)
	}
	return filepath.Join(append([]string{resolved}, tail...)...), nil
}

// relativeTo returns p's path relative to the vault root and whether p is
// inside the root (or is the root itself, for which the relative path is ""). A
// single home for the "is this inside the vault, and what is it called there"
// question the escape and hidden-path checks both ask, so they cannot drift.
func (v *Vault) relativeTo(p string) (string, bool) {
	if p == v.root {
		return "", true
	}
	return strings.CutPrefix(p, v.root+string(filepath.Separator))
}

// relPath converts an absolute path inside the vault back to the canonical
// vault-relative, forward-slash form.
func (v *Vault) relPath(abs string) string {
	rel, err := filepath.Rel(v.root, abs)
	if err != nil {
		return abs
	}
	return filepath.ToSlash(rel)
}

// NoteInfo is the lightweight listing entry.
type NoteInfo struct {
	Path    string    `json:"path"`
	Title   string    `json:"title"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
}

// Note is a fully read note.
type Note struct {
	Path        string         `json:"path"`
	Title       string         `json:"title"`
	Content     string         `json:"content"` // raw file content incl. frontmatter
	Body        string         `json:"body"`    // content without the frontmatter block
	Frontmatter map[string]any `json:"frontmatter,omitempty"`
	Tags        []string       `json:"tags,omitempty"`
	Links       []string       `json:"links,omitempty"`
	Hash        string         `json:"hash"` // sha256 hex of Content
	Size        int64          `json:"size"`
	ModTime     time.Time      `json:"modTime"`
}
