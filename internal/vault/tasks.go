package vault

import (
	"fmt"
	"regexp"
	"strings"
)

// Note type and task status conventions. Plain frontmatter, no task engine:
// a note is a task when `type: task`; without a type it is knowledge.
const (
	TypeTask      = "task"
	TypeKnowledge = "knowledge"

	StatusBacklog    = "backlog"
	StatusInProgress = "in-progress"
	StatusDone       = "done"
)

// ValidStatus reports whether s is one of the task statuses.
func ValidStatus(s string) bool {
	switch s {
	case StatusBacklog, StatusInProgress, StatusDone:
		return true
	}
	return false
}

// TypeOf returns the effective note type of an index entry, applying the
// knowledge default.
func TypeOf(e Entry) string {
	if e.Type == TypeTask {
		return TypeTask
	}
	return TypeKnowledge
}

var (
	statusLineRe = regexp.MustCompile(`(?m)^status\s*:.*$`)
	typeLineRe   = regexp.MustCompile(`(?m)^type\s*:.*$`)
)

// SetStatus marks a note as a task with the given status via a surgical
// frontmatter edit: only the `status` (and if needed `type`) lines change,
// every other frontmatter field and the body stay byte-identical. A note
// without frontmatter gets a minimal block prepended.
func (v *Vault) SetStatus(path, status, expectedHash string) error {
	if !ValidStatus(status) {
		return fmt.Errorf("%w: invalid status %q (want %s|%s|%s)",
			ErrInvalidPath, status, StatusBacklog, StatusInProgress, StatusDone)
	}
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

	var data string
	if prefix := note.Content[:len(note.Content)-len(note.Body)]; prefix != "" {
		data = patchFrontmatterBlock(prefix, status) + note.Body
	} else {
		data = "---\ntype: task\nstatus: " + status + "\n---\n" + note.Content
	}
	return v.writeAtomic(abs, []byte(data))
}

// patchFrontmatterBlock rewrites only the type/status lines inside the raw
// frontmatter block (fences included), preserving formatting and order of
// everything else.
func patchFrontmatterBlock(block, status string) string {
	if statusLineRe.MatchString(block) {
		block = statusLineRe.ReplaceAllString(block, "status: "+status)
	} else if typeLineRe.MatchString(block) {
		// Insert status right after the type line.
		block = typeLineRe.ReplaceAllStringFunc(block, func(line string) string {
			return line + "\nstatus: " + status
		})
	} else {
		// Append before the closing fence.
		if i := strings.LastIndex(block, "---"); i >= 0 {
			block = block[:i] + "status: " + status + "\n" + block[i:]
		}
	}
	// Setting a status makes the note a task.
	if typeLineRe.MatchString(block) {
		block = typeLineRe.ReplaceAllString(block, "type: "+TypeTask)
	} else if i := strings.LastIndex(block, "---"); i >= 0 {
		block = block[:i] + "type: " + TypeTask + "\n" + block[i:]
	}
	return block
}

// ListTasks returns task entries from the index, optionally filtered by
// status and project (a folder under projects/, e.g. "vellum" matches
// projects/vellum/**). Instant lookup — no file IO.
func (ix *Index) ListTasks(status, project string) []Entry {
	var out []Entry
	for _, e := range ix.All() {
		if TypeOf(e) != TypeTask {
			continue
		}
		if status != "" && e.Status != status {
			continue
		}
		if project != "" {
			prefix := "projects/" + strings.Trim(project, "/") + "/"
			if !strings.HasPrefix(e.Path, prefix) {
				continue
			}
		}
		out = append(out, e)
	}
	return out
}
