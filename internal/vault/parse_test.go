package vault

import (
	"reflect"
	"testing"
)

func TestSplitFrontmatter(t *testing.T) {
	tests := []struct {
		name, content, wantYAML, wantBody string
	}{
		{"with frontmatter", "---\ntitle: X\n---\nbody\n", "title: X", "body\n"},
		{"no frontmatter", "# Hello\n", "", "# Hello\n"},
		{"unclosed fence", "---\ntitle: X\nbody", "", "---\ntitle: X\nbody"},
		{"closed at EOF", "---\ntitle: X\n---", "title: X", ""},
		{"crlf", "---\r\ntitle: X\r\n---\r\nbody", "title: X", "body"},
		{"empty", "", "", ""},
		{"dash line in body", "body\n---\nmore\n", "", "body\n---\nmore\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotYAML, gotBody := splitFrontmatter(tt.content)
			if gotYAML != tt.wantYAML || gotBody != tt.wantBody {
				t.Errorf("splitFrontmatter(%q) = (%q, %q), want (%q, %q)",
					tt.content, gotYAML, gotBody, tt.wantYAML, tt.wantBody)
			}
		})
	}
}

func TestParseFrontmatterMalformed(t *testing.T) {
	if m := parseFrontmatter("title: [unclosed"); m != nil {
		t.Errorf("malformed YAML should yield nil, got %v", m)
	}
	if m := parseFrontmatter("   "); m != nil {
		t.Errorf("blank YAML should yield nil, got %v", m)
	}
}

func TestFrontmatterTags(t *testing.T) {
	tests := []struct {
		name string
		fm   map[string]any
		want []string
	}{
		{"list", map[string]any{"tags": []any{"go", "mcp"}}, []string{"go", "mcp"}},
		{"comma string", map[string]any{"tags": "go, mcp"}, []string{"go", "mcp"}},
		{"hash prefixes stripped", map[string]any{"tags": []any{"#go"}}, []string{"go"}},
		{"missing", map[string]any{}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := frontmatterTags(tt.fm); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("frontmatterTags() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestInlineTags(t *testing.T) {
	body := "Intro #alpha text\n#beta-2 at line start\n" +
		"```\n#not-a-tag in code\n```\n" +
		"URL https://example.com/page#fragment stays\n" +
		"nested #proj/sub and unicode #český\n"
	got := inlineTags(body)
	want := []string{"alpha", "beta-2", "proj/sub", "český"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("inlineTags() = %v, want %v", got, want)
	}
}

func TestExtractTagsMergesAndSorts(t *testing.T) {
	fm := map[string]any{"tags": []any{"zeta", "alpha"}}
	got := extractTags(fm, "body with #alpha and #mid\n")
	want := []string{"alpha", "mid", "zeta"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("extractTags() = %v, want %v", got, want)
	}
}

func TestExtractLinks(t *testing.T) {
	body := "See [[target-note]] and [[other|alias]] and [[anchored#section]].\n" +
		"Relative [doc](notes/doc.md) and [anchor](notes/doc.md#top).\n" +
		"Skip [ext](https://example.com/x.md), [mail](mailto:a@b.md),\n" +
		"[anchor-only](#local) and [image](img.png).\n" +
		"```\n[[in-code]] [code](code.md)\n```\n"
	got := extractLinks(body)
	want := []string{"target-note", "other", "anchored", "notes/doc.md"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("extractLinks() = %v, want %v", got, want)
	}
}

func TestDeriveTitle(t *testing.T) {
	tests := []struct {
		name string
		fm   map[string]any
		body string
		want string
	}{
		{"frontmatter wins", map[string]any{"title": "FM Title"}, "# Heading\n", "FM Title"},
		{"first heading", nil, "text\n## Sub Heading\n", "Sub Heading"},
		{"filename fallback", nil, "no headings\n", "my-note"},
		{"heading in code ignored", nil, "```\n# fake\n```\nreal text\n", "my-note"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := deriveTitle("/x/my-note.md", tt.fm, tt.body); got != tt.want {
				t.Errorf("deriveTitle() = %q, want %q", got, tt.want)
			}
		})
	}
}
