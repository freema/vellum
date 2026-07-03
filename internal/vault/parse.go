package vault

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// splitFrontmatter separates a leading YAML frontmatter block ("---" fenced)
// from the body. Returns the raw YAML (without fences) and the body. If there
// is no frontmatter the yaml part is empty and body equals content.
func splitFrontmatter(content string) (rawYAML, body string) {
	rest, ok := strings.CutPrefix(content, "---\n")
	if !ok {
		if rest, ok = strings.CutPrefix(content, "---\r\n"); !ok {
			return "", content
		}
	}
	for _, delim := range []string{"\r\n---\r\n", "\r\n---\n", "\n---\r\n", "\n---\n"} {
		if i := strings.Index(rest, delim); i >= 0 {
			return strings.TrimSuffix(rest[:i], "\r"), rest[i+len(delim):]
		}
	}
	// Frontmatter closed at EOF without trailing newline.
	for _, delim := range []string{"\n---", "\r\n---"} {
		if strings.HasSuffix(rest, delim) {
			return strings.TrimSuffix(rest, delim), ""
		}
	}
	return "", content
}

// parseFrontmatter decodes the YAML block into a generic map.
// Malformed YAML is not an error at the vault layer — the note is still a
// valid file; the frontmatter is simply reported as absent.
func parseFrontmatter(rawYAML string) map[string]any {
	if strings.TrimSpace(rawYAML) == "" {
		return nil
	}
	var m map[string]any
	if err := yaml.Unmarshal([]byte(rawYAML), &m); err != nil {
		return nil
	}
	return m
}

// frontmatterTags extracts tags from a parsed frontmatter map. Accepts both
// a YAML list (tags: [a, b]) and a comma-separated string (tags: a, b).
func frontmatterTags(fm map[string]any) []string {
	raw, ok := fm["tags"]
	if !ok {
		return nil
	}
	var out []string
	switch t := raw.(type) {
	case []any:
		for _, item := range t {
			if s, ok := item.(string); ok {
				if s = strings.TrimSpace(strings.TrimPrefix(s, "#")); s != "" {
					out = append(out, s)
				}
			}
		}
	case string:
		for _, s := range strings.Split(t, ",") {
			if s = strings.TrimSpace(strings.TrimPrefix(s, "#")); s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

var (
	fenceRe     = regexp.MustCompile("(?m)^\\s{0,3}(```|~~~)")
	inlineTagRe = regexp.MustCompile(`(^|\s)#([\p{L}\p{N}][\p{L}\p{N}/_-]*)`)
	wikilinkRe  = regexp.MustCompile(`\[\[([^\[\]]+)\]\]`)
	mdLinkRe    = regexp.MustCompile(`\[[^\]]*\]\(([^)\s]+)\)`)
	headingRe   = regexp.MustCompile(`(?m)^#{1,6}\s+(.+?)\s*$`)
)

// stripFencedCode removes fenced code blocks (``` / ~~~) so that tags and
// links inside them are not extracted.
func stripFencedCode(body string) string {
	var b strings.Builder
	inFence := false
	for _, line := range strings.SplitAfter(body, "\n") {
		if fenceRe.MatchString(line) {
			inFence = !inFence
			continue
		}
		if !inFence {
			b.WriteString(line)
		}
	}
	return b.String()
}

// inlineTags extracts #tags from the body, skipping fenced code blocks.
// A tag must follow start-of-line or whitespace, so URL fragments
// (https://x/page#frag) never match.
func inlineTags(body string) []string {
	var out []string
	for _, m := range inlineTagRe.FindAllStringSubmatch(stripFencedCode(body), -1) {
		out = append(out, m[2])
	}
	return out
}

// extractTags merges frontmatter and inline tags, deduplicated and sorted.
func extractTags(fm map[string]any, body string) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range append(frontmatterTags(fm), inlineTags(body)...) {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	sort.Strings(out)
	return out
}

// extractLinks collects link targets from the body, skipping fenced code:
// [[wikilinks]] (alias part after | dropped) and relative markdown links to
// .md files. External URLs and anchors are ignored.
func extractLinks(body string) []string {
	text := stripFencedCode(body)
	seen := map[string]bool{}
	var out []string
	add := func(target string) {
		target = strings.TrimSpace(target)
		if target != "" && !seen[target] {
			seen[target] = true
			out = append(out, target)
		}
	}
	for _, m := range wikilinkRe.FindAllStringSubmatch(text, -1) {
		target := m[1]
		if i := strings.IndexByte(target, '|'); i >= 0 {
			target = target[:i]
		}
		if i := strings.IndexByte(target, '#'); i >= 0 {
			target = target[:i] // drop heading anchor
		}
		add(target)
	}
	for _, m := range mdLinkRe.FindAllStringSubmatch(text, -1) {
		target := m[1]
		lower := strings.ToLower(target)
		if strings.Contains(lower, "://") || strings.HasPrefix(lower, "mailto:") ||
			strings.HasPrefix(target, "#") {
			continue
		}
		if i := strings.IndexByte(target, '#'); i >= 0 {
			target = target[:i]
		}
		if isMarkdown(target) {
			add(target)
		}
	}
	return out
}

// deriveTitle picks the note title: frontmatter `title`, else the first
// heading, else the file name without extension.
func deriveTitle(path string, fm map[string]any, body string) string {
	if t, ok := fm["title"].(string); ok && strings.TrimSpace(t) != "" {
		return strings.TrimSpace(t)
	}
	if m := headingRe.FindStringSubmatch(stripFencedCode(body)); m != nil {
		return m[1]
	}
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}
