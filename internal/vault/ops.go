package vault

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// List returns the notes in dir ("" = vault root), optionally recursive.
// Dotfiles, dot-directories and symlinks are skipped.
func (v *Vault) List(dir string, recursive bool) ([]NoteInfo, error) {
	infos, _, err := v.ListPage(dir, recursive, 0, 0)
	return infos, err
}

// ListPage is List cut to a window: at most limit notes (0 = no cap)
// from offset on, in path order, plus the number of notes in the whole
// listing. Only the notes in the window have their file opened for the
// title, so a page of a large vault costs a directory walk, not a read of
// every note.
func (v *Vault) ListPage(dir string, recursive bool, offset, limit int) ([]NoteInfo, int, error) {
	return v.ListWith(dir, recursive, ListOptions{Offset: offset, Limit: limit})
}

// ListOptions picks the order, a modification-time filter and the window
// of a listing (ListWith). The zero value is List: every note, by path.
type ListOptions struct {
	Offset int // skip the first Offset notes of the sorted listing
	Limit  int // at most Limit notes, 0 = no cap
	// Since keeps the notes modified at or after it; zero keeps all.
	Since time.Time
	// ByModified sorts by modification time, newest first, and notes
	// modified at the same time by path, so the order is deterministic.
	ByModified bool
}

// ListWith is ListPage with a filter and an order (NSO-576): the notes
// in dir modified since opts.Since, sorted by path or newest change
// first, cut to the window. The returned total counts the notes that pass
// the filter. Modification times are the files' own, read in the same
// walk, so a note edited in another editor sorts by that edit at once.
func (v *Vault) ListWith(dir string, recursive bool, opts ListOptions) ([]NoteInfo, int, error) {
	absDir, err := v.resolveDir(dir)
	if err != nil {
		return nil, 0, err
	}
	if fi, err := os.Stat(absDir); err != nil {
		if os.IsNotExist(err) {
			return nil, 0, fmt.Errorf("%w: directory %s", ErrNotFound, dir)
		}
		return nil, 0, err
	} else if !fi.IsDir() {
		return nil, 0, fmt.Errorf("%w: %s is not a directory", ErrInvalidPath, dir)
	}

	var infos []NoteInfo
	abs := map[string]string{} // vault path -> absolute path, for titles
	err = v.walkNotes(absDir, recursive, func(rel, path string, fi fs.FileInfo) {
		if !opts.Since.IsZero() && fi.ModTime().Before(opts.Since) {
			return
		}
		abs[rel] = path
		infos = append(infos, NoteInfo{
			Path:    rel,
			Size:    fi.Size(),
			ModTime: fi.ModTime(),
		})
	})
	if err != nil {
		return nil, 0, err
	}
	sort.Slice(infos, func(i, j int) bool {
		if opts.ByModified && !infos[i].ModTime.Equal(infos[j].ModTime) {
			return infos[i].ModTime.After(infos[j].ModTime)
		}
		return infos[i].Path < infos[j].Path
	})
	total := len(infos)
	infos = window(infos, opts.Offset, opts.Limit)
	for i := range infos {
		infos[i].Title = titleOf(abs[infos[i].Path])
	}
	return infos, total, nil
}

// walkNotes visits the notes under absDir by the rules every vault scan
// shares: dotfiles and dot-directories, symlinks and files without a
// markdown extension are skipped, and subfolders only when recursive. fn
// gets the vault-relative path, the absolute one and the file's info from
// the directory walk; no file is opened. List, the index build and
// Index.Reconcile all walk through here, so the index holds exactly the
// notes a listing shows. A file or folder that disappears during the walk
// (a git checkout, an editor's save-by-rename) is skipped, not an error.
func (v *Vault) walkNotes(absDir string, recursive bool, fn func(rel, abs string, fi fs.FileInfo)) error {
	rootPrefix := v.root + string(filepath.Separator)
	return filepath.WalkDir(absDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path != absDir && errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		name := d.Name()
		if path != absDir && strings.HasPrefix(name, ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if !recursive && path != absDir {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 || !isMarkdown(name) {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		rel, ok := strings.CutPrefix(path, rootPrefix) // no allocation per note
		if !ok {
			rel = v.relPath(path)
		}
		fn(filepath.ToSlash(rel), path, fi)
		return nil
	})
}

// CreateDir creates a directory (and any parents) inside the vault. An
// existing directory is a no-op; a file already at the path is an error.
func (v *Vault) CreateDir(dir string) error {
	dir = strings.Trim(dir, "/")
	if dir == "" {
		return fmt.Errorf("%w: empty directory", ErrInvalidPath)
	}
	abs, phys, err := v.resolveDirPhysical(dir)
	if err != nil {
		return err
	}
	// Same guard as note writes: a folder must not land under a dot segment,
	// lexically or through a symlinked ancestor, or the vault scan skips it.
	if err := v.checkNotHidden(dir, phys); err != nil {
		return err
	}
	if fi, err := os.Stat(abs); err == nil {
		if fi.IsDir() {
			return nil
		}
		return fmt.Errorf("%w: %s exists and is not a directory", ErrExists, dir)
	}
	return os.MkdirAll(abs, 0o755)
}

// DeleteDir removes a directory and everything under it (notes included).
// It refuses to touch the vault root. Callers are expected to confirm with the
// user first — this is a recursive, irreversible delete.
func (v *Vault) DeleteDir(dir string) error {
	dir = strings.Trim(dir, "/")
	if dir == "" {
		return fmt.Errorf("%w: empty directory", ErrInvalidPath)
	}
	abs, err := v.resolveDir(dir)
	if err != nil {
		return err
	}
	if abs == v.root {
		return fmt.Errorf("%w: refusing to delete the vault root", ErrInvalidPath)
	}
	fi, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: directory %s", ErrNotFound, dir)
		}
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("%w: %s is not a directory", ErrInvalidPath, dir)
	}
	return os.RemoveAll(abs)
}

// ListDirs returns every subdirectory of the vault (vault-relative,
// forward-slash), dotfiles excluded. Empty directories are included so the
// SPA tree can show folders that do not hold any notes yet.
func (v *Vault) ListDirs() ([]string, error) {
	var dirs []string
	err := filepath.WalkDir(v.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if path == v.root {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			return filepath.SkipDir
		}
		dirs = append(dirs, v.relPath(path))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(dirs)
	return dirs, nil
}

// titleOf derives a note title from the file head (frontmatter or first
// heading fit comfortably in the first 8 KB).
func titleOf(abs string) string {
	f, err := os.Open(abs)
	if err != nil {
		return strings.TrimSuffix(filepath.Base(abs), filepath.Ext(abs))
	}
	defer func() { _ = f.Close() }()
	head := make([]byte, 8<<10)
	n, _ := io.ReadFull(f, head)
	content := string(head[:n])
	rawYAML, body := splitFrontmatter(content)
	return deriveTitle(abs, parseFrontmatter(rawYAML), body)
}

// Read returns the full note including parsed frontmatter, tags, links and
// the sha256 content hash used for optimistic concurrency.
func (v *Vault) Read(path string) (*Note, error) {
	abs, err := v.resolveNote(path)
	if err != nil {
		return nil, err
	}
	return v.readAbs(abs)
}

// ReadVisible is Read restricted to the notes the vault scan sees: on top
// of Read's path rules it refuses a path with a hidden (dot) segment, or
// one that resolves into a hidden directory through a symlink, with the
// same guard writes use (checkNotHidden). Read keeps serving such a note so
// nothing already there is trapped; bulk reads use ReadVisible so they can
// only reach what List and the index show.
func (v *Vault) ReadVisible(path string) (*Note, error) {
	abs, phys, err := v.resolveNotePhysical(path)
	if err != nil {
		return nil, err
	}
	if err := v.checkNotHidden(path, phys); err != nil {
		return nil, err
	}
	return v.readAbs(abs)
}

func (v *Vault) readAbs(abs string) (*Note, error) {
	fi, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, v.relPath(abs))
		}
		return nil, err
	}
	if fi.Size() > v.maxSize {
		return nil, fmt.Errorf("%w: %s (%d bytes)", ErrTooLarge, v.relPath(abs), fi.Size())
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}
	content := string(raw)
	rawYAML, body := splitFrontmatter(content)
	fm := parseFrontmatter(rawYAML)
	return &Note{
		Path:        v.relPath(abs),
		Title:       deriveTitle(abs, fm, body),
		Content:     content,
		Body:        body,
		Frontmatter: fm,
		Tags:        extractTags(fm, body),
		Links:       extractLinks(body),
		Hash:        hashOf(raw),
		Size:        fi.Size(),
		ModTime:     fi.ModTime(),
	}, nil
}

func hashOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// WriteOptions control Write behavior.
type WriteOptions struct {
	// Overwrite allows replacing an existing note (last-write-wins).
	Overwrite bool
	// ExpectedHash, when set, must match the sha256 of the current content,
	// otherwise ErrConflict is returned and nothing is written. A matching
	// hash implies permission to overwrite.
	ExpectedHash string
}

// Write creates or replaces a note. Parent directories are created as needed.
func (v *Vault) Write(path, content string, opts WriteOptions) error {
	abs, phys, err := v.resolveNotePhysical(path)
	if err != nil {
		return err
	}
	if err := v.checkNotHidden(path, phys); err != nil {
		return err
	}
	if int64(len(content)) > v.maxSize {
		return fmt.Errorf("%w: %s (%d bytes)", ErrTooLarge, path, len(content))
	}

	v.mu.Lock()
	defer v.mu.Unlock()

	current, err := os.ReadFile(abs)
	switch {
	case err == nil:
		if opts.ExpectedHash != "" {
			if hashOf(current) != opts.ExpectedHash {
				return fmt.Errorf("%w: %s", ErrConflict, path)
			}
		} else if !opts.Overwrite {
			return fmt.Errorf("%w: %s", ErrExists, path)
		}
	case os.IsNotExist(err):
		if opts.ExpectedHash != "" {
			return fmt.Errorf("%w: %s", ErrNotFound, path)
		}
		if !opts.Overwrite {
			// Create-only: the read above and the write below are two steps,
			// and the mutex only covers this process. O_EXCL lets the kernel
			// settle a race with an editor or a second vellum on the same
			// vault, so "create" can never turn into an overwrite.
			return v.writeExclusive(abs, []byte(content))
		}
	default:
		return err
	}

	return v.writeAtomic(abs, []byte(content))
}

// writeExclusive creates a note that must not exist yet, letting the kernel
// arbitrate: an existing path fails with ErrExists rather than being replaced.
func (v *Vault) writeExclusive(abs string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(abs, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return fmt.Errorf("%w: %s", ErrExists, filepath.Base(abs))
		}
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(abs)
		return err
	}
	return f.Close()
}

// writeAtomic writes via a temp file + rename so readers never see a torn note.
func (v *Vault) writeAtomic(abs string, data []byte) error {
	dir := filepath.Dir(abs)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".vellum-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op after successful rename
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, abs)
}

// Patch surgically replaces the content under the heading whose text equals
// section, leaving the heading itself, the frontmatter and the rest of the
// note untouched. The replaced region ends before the next heading of the
// same or higher level (or EOF). It is PatchSection with PatchReplace.
func (v *Vault) Patch(path, section, newContent, expectedHash string) error {
	_, err := v.PatchSection(path, section, PatchReplace, newContent, expectedHash)
	return err
}

// replaceSection swaps the body of the section lines[start:end] (start the
// heading line, as findSection returns them) for newContent and returns
// the new body with the index of the replacement's first line and its
// line count. Its bytes are what patch_note has always written.
func replaceSection(lines []string, start, end int, newContent string) (body string, first, n int) {
	replacement := strings.TrimRight(newContent, "\n") + "\n"
	var b strings.Builder
	for _, l := range lines[:start+1] {
		b.WriteString(l)
	}
	if !strings.HasSuffix(b.String(), "\n") {
		b.WriteString("\n") // heading was the last line without newline
	}
	b.WriteString(replacement)
	for _, l := range lines[end:] {
		b.WriteString(l)
	}
	return b.String(), start + 1, strings.Count(replacement, "\n")
}

// Append adds content at the end of an existing note.
func (v *Vault) Append(path, content, expectedHash string) error {
	return v.edit(path, expectedHash, func(note *Note) string {
		base := note.Content
		if base != "" && !strings.HasSuffix(base, "\n") {
			base += "\n"
		}
		return base + strings.TrimRight(content, "\n") + "\n"
	})
}

// Prepend inserts content at the top of the body of an existing note,
// after the frontmatter block if present.
func (v *Vault) Prepend(path, content, expectedHash string) error {
	return v.edit(path, expectedHash, func(note *Note) string {
		prefix := note.Content[:len(note.Content)-len(note.Body)]
		return prefix + strings.TrimRight(content, "\n") + "\n" + note.Body
	})
}

// edit is the shared read–check–transform–write cycle for Append/Prepend.
func (v *Vault) edit(path, expectedHash string, transform func(*Note) string) error {
	abs, err := v.resolveNote(path)
	if err != nil {
		return err
	}

	v.mu.Lock()
	defer v.mu.Unlock()

	note, err := v.readAbs(abs)
	if err != nil {
		return err
	}
	if expectedHash != "" && note.Hash != expectedHash {
		return fmt.Errorf("%w: %s", ErrConflict, path)
	}
	data := transform(note)
	if int64(len(data)) > v.maxSize {
		return fmt.Errorf("%w: %s (%d bytes)", ErrTooLarge, path, len(data))
	}
	return v.writeAtomic(abs, []byte(data))
}

// Delete removes a note.
func (v *Vault) Delete(path string) error {
	abs, err := v.resolveNote(path)
	if err != nil {
		return err
	}
	if err := os.Remove(abs); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %s", ErrNotFound, path)
		}
		return err
	}
	return nil
}

// Move renames a note. The target must not exist; parent directories are
// created as needed.
func (v *Vault) Move(from, to string) error {
	absFrom, err := v.resolveNote(from)
	if err != nil {
		return err
	}
	absTo, physTo, err := v.resolveNotePhysical(to)
	if err != nil {
		return err
	}
	if err := v.checkNotHidden(to, physTo); err != nil {
		return err
	}

	v.mu.Lock()
	defer v.mu.Unlock()

	if _, err := os.Stat(absFrom); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %s", ErrNotFound, from)
		}
		return err
	}
	// A case-only rename (notes.md → Notes.md) points at the same file on a
	// case-insensitive volume (macOS, Windows) — os.Stat finding "the target"
	// there means the source itself, not a note we would clobber. Both halves
	// matter: same inode alone would also match a hard link under a different
	// name, where the rename really would drop a second note from the vault.
	if toInfo, err := os.Stat(absTo); err == nil {
		fromInfo, statErr := os.Stat(absFrom)
		alias := strings.EqualFold(absFrom, absTo) && statErr == nil && os.SameFile(fromInfo, toInfo)
		if !alias {
			return fmt.Errorf("%w: %s", ErrExists, to)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(absTo), 0o755); err != nil {
		return err
	}
	return os.Rename(absFrom, absTo)
}
