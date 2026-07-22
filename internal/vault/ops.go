package vault

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// List returns the notes in dir ("" = vault root), optionally recursive.
// Dotfiles, dot-directories and symlinks are skipped.
func (v *Vault) List(dir string, recursive bool) ([]NoteInfo, error) {
	absDir, err := v.resolveDir(dir)
	if err != nil {
		return nil, err
	}
	if fi, err := os.Stat(absDir); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: directory %s", ErrNotFound, dir)
		}
		return nil, err
	} else if !fi.IsDir() {
		return nil, fmt.Errorf("%w: %s is not a directory", ErrInvalidPath, dir)
	}

	var infos []NoteInfo
	err = filepath.WalkDir(absDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
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
			return err
		}
		infos = append(infos, NoteInfo{
			Path:    v.relPath(path),
			Title:   titleOf(path),
			Size:    fi.Size(),
			ModTime: fi.ModTime(),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].Path < infos[j].Path })
	return infos, nil
}

// CreateDir creates a directory (and any parents) inside the vault. An
// existing directory is a no-op; a file already at the path is an error.
func (v *Vault) CreateDir(dir string) error {
	dir = strings.Trim(dir, "/")
	if dir == "" {
		return fmt.Errorf("%w: empty directory", ErrInvalidPath)
	}
	if hasHiddenSegment(dir) {
		return fmt.Errorf("%w: %s is hidden from the vault", ErrInvalidPath, dir)
	}
	abs, err := v.resolveDir(dir)
	if err != nil {
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
	abs, err := v.resolveNote(path)
	if err != nil {
		return err
	}
	if err := v.checkNotHidden(path, abs); err != nil {
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
// same or higher level (or EOF).
func (v *Vault) Patch(path, section, newContent, expectedHash string) error {
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

	prefix := note.Content[:len(note.Content)-len(note.Body)]
	patched, err := replaceSection(note.Body, section, newContent)
	if err != nil {
		return fmt.Errorf("%w in %s", err, path)
	}
	data := prefix + patched
	if int64(len(data)) > v.maxSize {
		return fmt.Errorf("%w: %s (%d bytes)", ErrTooLarge, path, len(data))
	}
	return v.writeAtomic(abs, []byte(data))
}

// replaceSection swaps the body of the named section. Headings inside fenced
// code blocks are ignored.
func replaceSection(body, section, newContent string) (string, error) {
	lines := strings.SplitAfter(body, "\n")
	start, end, level := -1, len(lines), 0
	inFence := false
	for i, line := range lines {
		if fenceRe.MatchString(line) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		m := headingRe.FindStringSubmatch(strings.TrimRight(line, "\r\n"))
		if m == nil {
			continue
		}
		lvl := len(line) - len(strings.TrimLeft(line, "#"))
		if start == -1 {
			if strings.EqualFold(strings.TrimSpace(m[1]), strings.TrimSpace(section)) {
				start, level = i, lvl
			}
		} else if lvl <= level {
			end = i
			break
		}
	}
	if start == -1 {
		return "", fmt.Errorf("%w: %q", ErrSectionNotFound, section)
	}
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
	return b.String(), nil
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
	absTo, err := v.resolveNote(to)
	if err != nil {
		return err
	}
	if err := v.checkNotHidden(to, absTo); err != nil {
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
