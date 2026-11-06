package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/freema/vellum/internal/vault"
)

// replace_in_note (NSO-574): exact find-and-replace for small edits.
//
// For a one-sentence fix patch_note makes the agent send the whole section
// back and write_note the whole note, both of which risk changing text by
// accident on the way. replace_in_note takes only the text to change and
// its replacement, and answers with the new hash and a numbered excerpt of
// the edited place, so the agent can check the result without reading the
// note again.

type replaceInNoteIn struct {
	Path         string `json:"path"`
	OldText      string `json:"old_text" jsonschema:"exact text to replace, character for character (whitespace and line breaks included); searched over the whole file, frontmatter included; must occur exactly once unless replace_all is set"`
	NewText      string `json:"new_text" jsonschema:"replacement text; empty deletes old_text"`
	ReplaceAll   bool   `json:"replace_all,omitempty" jsonschema:"replace every occurrence of old_text instead of requiring exactly one"`
	ExpectedHash string `json:"expected_hash,omitempty" jsonschema:"optional concurrency guard"`
}

type replaceInNoteOut struct {
	Path         string `json:"path"`
	Hash         string `json:"hash"`
	Replacements int    `json:"replacements"`
	// Excerpt is the edited place after the change: the new text's lines
	// with two lines before and after, each as "N: line" (N the file
	// line, as read_note's line_numbers numbers them).
	Excerpt string `json:"excerpt"`
}

const (
	// excerptContext is how many unchanged lines frame each edited place.
	excerptContext = 2
	// excerptMaxRegions caps the places shown for replace_all.
	excerptMaxRegions = 3
	// excerptMaxRegionLines caps one place; a longer one keeps its first
	// and last half of this and leaves out the middle. An unchanged line
	// is cut to maxTextLine runes (clipLine); lines of the new text never
	// are.
	excerptMaxRegionLines = 14
)

func registerReplaceInNote(s *mcp.Server, d Deps) {
	mcp.AddTool(s, editTool("replace_in_note", "Replace text in note",
		"Replace an exact piece of text in a note: the smallest edit, for changing a word, a sentence or a few lines. "+
			"old_text must match character for character and occur exactly once (or pass replace_all); it is searched over the whole file, frontmatter included. "+
			"Returns the new hash, the number of replacements and the edited lines with two lines around them, numbered.",
		true, false, // can drop content; repeating it fails or replaces again
	), func(ctx context.Context, req *mcp.CallToolRequest, in replaceInNoteIn) (*mcp.CallToolResult, replaceInNoteOut, error) {
		res, err := d.Vault.Replace(in.Path, in.OldText, in.NewText, in.ReplaceAll, in.ExpectedHash)
		if err != nil {
			return nil, replaceInNoteOut{}, replaceError(err)
		}
		_ = d.Index.Update(in.Path)
		out := replaceInNoteOut{
			Path:         in.Path,
			Hash:         res.Hash,
			Replacements: res.Count,
			Excerpt:      replaceExcerpt(res.Content, res.Spans),
		}
		return textResult(replaceText(out)), out, nil
	})
}

// replaceError adds what to do next to the errors an agent can fix itself.
func replaceError(err error) error {
	switch {
	case errors.Is(err, vault.ErrTextNotFound):
		return fmt.Errorf("%w; old_text must match the note exactly, character for character, whitespace and line breaks included. "+
			"Read the current lines with read_note (start_line and max_lines, or section) and copy old_text from there", err)
	case errors.Is(err, vault.ErrTextNotUnique):
		return fmt.Errorf("%w. Add surrounding text to old_text so it matches only once, "+
			"or pass replace_all: true to replace every occurrence", err)
	case errors.Is(err, vault.ErrConflict):
		return fmt.Errorf("%w; the note changed since you read it: read_note again and retry", err)
	}
	return err
}

// replaceText renders the text block: path, hash and count as a header, a
// blank line, then the excerpt.
func replaceText(out replaceInNoteOut) string {
	return "path: " + out.Path + "\n" +
		"hash: " + out.Hash + "\n" +
		"replacements: " + strconv.Itoa(out.Replacements) + "\n\n" +
		out.Excerpt
}

// replaceExcerpt renders the edited places of content: per span its lines
// plus excerptContext lines on either side, numbered "N: line". Places
// whose context touches or overlaps are merged; places are separated by a
// "..." line. At most excerptMaxRegions places are shown, followed by a
// count of the rest.
func replaceExcerpt(content string, spans []vault.LineSpan) string {
	lines := vault.SplitLines(content)
	if len(lines) == 0 {
		return "(the note is empty)\n"
	}
	type region struct{ from, to int } // 1-based, inclusive
	var regions []region
	for _, sp := range spans {
		r := region{max(sp.First-excerptContext, 1), min(sp.Last+excerptContext, len(lines))}
		if n := len(regions); n > 0 && r.from <= regions[n-1].to+1 {
			regions[n-1].to = max(regions[n-1].to, r.to)
			continue
		}
		regions = append(regions, r)
	}

	changed := func(line int) bool {
		for _, sp := range spans {
			if line >= sp.First && line <= sp.Last {
				return true
			}
		}
		return false
	}
	var b strings.Builder
	writeLine := func(n int) {
		l := strings.TrimRight(lines[n-1], "\r\n")
		if !changed(n) {
			l = clipLine(l, "") // its start shows where the edit sits
		}
		b.WriteString(strconv.Itoa(n) + ": " + l + "\n")
	}
	for i, r := range regions {
		if i == excerptMaxRegions {
			fmt.Fprintf(&b, "... (%d more edited places not shown)\n", len(regions)-i)
			break
		}
		if i > 0 {
			b.WriteString("...\n")
		}
		if r.to-r.from+1 <= excerptMaxRegionLines {
			for n := r.from; n <= r.to; n++ {
				writeLine(n)
			}
			continue
		}
		half := excerptMaxRegionLines / 2
		for n := r.from; n < r.from+half; n++ {
			writeLine(n)
		}
		fmt.Fprintf(&b, "... (%d lines)\n", r.to-r.from+1-2*half)
		for n := r.to - half + 1; n <= r.to; n++ {
			writeLine(n)
		}
	}
	return b.String()
}
