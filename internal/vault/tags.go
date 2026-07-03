package vault

import (
	"fmt"
	"regexp"
	"strings"
)

// AddTags adds tags to the note's frontmatter `tags:` field (surgical edit,
// other fields untouched). Inline #tags in the body are not modified.
// Returns the resulting frontmatter tag list.
func (v *Vault) AddTags(path string, tags []string, expectedHash string) ([]string, error) {
	return v.mutateTags(path, expectedHash, func(current []string) []string {
		seen := map[string]bool{}
		out := append([]string{}, current...)
		for _, t := range current {
			seen[t] = true
		}
		for _, t := range normalizeTags(tags) {
			if !seen[t] {
				seen[t] = true
				out = append(out, t)
			}
		}
		return out
	})
}

// RemoveTags removes tags from the note's frontmatter `tags:` field.
// Inline #tags in the body are not modified. Returns the remaining list.
func (v *Vault) RemoveTags(path string, tags []string, expectedHash string) ([]string, error) {
	drop := map[string]bool{}
	for _, t := range normalizeTags(tags) {
		drop[t] = true
	}
	return v.mutateTags(path, expectedHash, func(current []string) []string {
		var out []string
		for _, t := range current {
			if !drop[t] {
				out = append(out, t)
			}
		}
		return out
	})
}

func normalizeTags(tags []string) []string {
	var out []string
	for _, t := range tags {
		if t = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(t), "#")); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// mutateTags runs the shared read–transform–write cycle for tag edits.
func (v *Vault) mutateTags(path, expectedHash string, transform func([]string) []string) ([]string, error) {
	abs, err := v.resolveNote(path)
	if err != nil {
		return nil, err
	}

	v.mu.Lock()
	defer v.mu.Unlock()

	note, err := v.readAbs(abs)
	if err != nil {
		return nil, err
	}
	if expectedHash != "" && note.Hash != expectedHash {
		return nil, fmt.Errorf("%w: %s", ErrConflict, path)
	}

	current := frontmatterTags(note.Frontmatter)
	result := transform(current)
	if strings.Join(current, "\x00") == strings.Join(result, "\x00") {
		return result, nil // no change, no write
	}

	prefix := note.Content[:len(note.Content)-len(note.Body)]
	var data string
	if prefix != "" {
		data = setFrontmatterTags(prefix, result) + note.Body
	} else if len(result) > 0 {
		data = "---\ntags: [" + strings.Join(result, ", ") + "]\n---\n" + note.Content
	} else {
		return result, nil
	}
	if err := v.writeAtomic(abs, []byte(data)); err != nil {
		return nil, err
	}
	return result, nil
}

var (
	tagsKeyRe  = regexp.MustCompile(`^tags\s*:(.*)$`)
	listItemRe = regexp.MustCompile(`^\s*-\s+\S`)
)

// setFrontmatterTags rewrites only the tags entry inside the raw frontmatter
// block (fences included). Flow lists are replaced in place; block-style
// lists are collapsed to a flow list; a missing entry is inserted right
// after the opening fence. An empty tag list removes the entry.
func setFrontmatterTags(block string, tags []string) string {
	value := "tags: [" + strings.Join(tags, ", ") + "]"
	lines := strings.Split(block, "\n")
	var out []string
	done := false
	for i := 0; i < len(lines); i++ {
		m := tagsKeyRe.FindStringSubmatch(lines[i])
		if m == nil || done {
			out = append(out, lines[i])
			continue
		}
		done = true
		// Block-style list: consume the following "- item" lines.
		if strings.TrimSpace(m[1]) == "" {
			for i+1 < len(lines) && listItemRe.MatchString(lines[i+1]) {
				i++
			}
		}
		if len(tags) > 0 {
			out = append(out, value)
		}
	}
	if !done && len(tags) > 0 {
		// Insert after the opening fence.
		out = append([]string{out[0], value}, out[1:]...)
	}
	return strings.Join(out, "\n")
}
