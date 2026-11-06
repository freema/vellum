package vault

import (
	"os"
	"sort"
	"strings"
)

// DirCount is a folder with the number of notes beneath it, nested
// folders included.
type DirCount struct {
	Path  string `json:"path"`  // vault-relative, usable as a dir argument
	Notes int    `json:"notes"` // notes in the folder and all its subfolders
	// Description is the folder's description (NSO-573), if it has one.
	Description string `json:"description,omitempty"`
}

// Subdirs returns the direct subfolders of dir ("" = vault root) in path
// order, each with the number of indexed notes beneath it. The counts come
// from the index, not from another walk of the tree; folders without notes
// (which the index cannot know about) are added from one read of dir
// itself, with a count of 0. Dot-folders and symlinks are skipped, as in
// List.
func (ix *Index) Subdirs(dir string) ([]DirCount, error) {
	absDir, err := ix.vault.resolveDir(dir)
	if err != nil {
		return nil, err
	}
	prefix := ""
	if rel := ix.vault.relPath(absDir); rel != "" && rel != "." {
		prefix = rel + "/"
	}

	counts := map[string]int{}
	for _, e := range ix.snapshot() {
		rest, ok := strings.CutPrefix(e.Path, prefix)
		if !ok {
			continue
		}
		if sub, _, nested := strings.Cut(rest, "/"); nested {
			counts[prefix+sub]++
		}
	}

	entries, err := os.ReadDir(absDir)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, d := range entries {
		if d.IsDir() && !strings.HasPrefix(d.Name(), ".") {
			if p := prefix + d.Name(); !hasKey(counts, p) {
				counts[p] = 0
			}
		}
	}

	out := make([]DirCount, 0, len(counts))
	for p, n := range counts {
		out = append(out, DirCount{Path: p, Notes: n, Description: ix.FolderDescription(p)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func hasKey(m map[string]int, k string) bool {
	_, ok := m[k]
	return ok
}
