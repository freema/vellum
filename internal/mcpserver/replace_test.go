package mcpserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/freema/vellum/internal/vault"
)

// replaceNote is the note the replace_in_note tests edit: frontmatter,
// headings and a sentence that occurs once.
const replaceNote = "---\ntags: [draft, garden]\n---\n# Garden\n\n## Plan\n\nSow tomatoes in March.\nWater daily.\nThe delivery arrives on 14 March.\nPrune in June.\nHarvest in August.\n\n## Log\n\nWater daily.\n"

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// noteOnDisk is the note's content as the vault reads it now.
func (s *readSession) noteOnDisk(path string) string {
	s.t.Helper()
	n, err := s.vault.Read(path)
	if err != nil {
		s.t.Fatal(err)
	}
	return n.Content
}

// replace calls replace_in_note and fails the test on a tool error.
func (s *readSession) replace(args map[string]any) (replaceInNoteOut, wireResult) {
	s.t.Helper()
	res := s.call("replace_in_note", args)
	if res.IsError {
		s.t.Fatalf("replace_in_note %v: %s", args, res.Text)
	}
	return decode[replaceInNoteOut](s.t, res), res
}

// replaceErr calls replace_in_note expecting a tool error and checks that
// the note was left exactly as it was.
func (s *readSession) replaceErr(args map[string]any) string {
	s.t.Helper()
	path := args["path"].(string)
	before := s.noteOnDisk(path)
	res := s.call("replace_in_note", args)
	if !res.IsError {
		s.t.Fatalf("replace_in_note %v succeeded, want an error: %s", args, res.Structured)
	}
	if after := s.noteOnDisk(path); after != before {
		s.t.Fatalf("a failed replace_in_note wrote the note:\n%s", after)
	}
	return res.Text
}

// Acceptance: one match is replaced, the hash changes and the response
// carries the excerpt.
func TestReplaceInNoteOneMatch(t *testing.T) {
	s := newReadSession(t, map[string]string{"g.md": replaceNote})

	out, res := s.replace(map[string]any{
		"path": "g.md", "old_text": "14 March", "new_text": "21 March",
		"expected_hash": sha256Hex(replaceNote),
	})
	want := strings.Replace(replaceNote, "14 March", "21 March", 1)
	if got := s.noteOnDisk("g.md"); got != want {
		t.Fatalf("note = %q, want %q", got, want)
	}
	if out.Hash != sha256Hex(want) || out.Hash == sha256Hex(replaceNote) {
		t.Errorf("hash = %s, want the new content's %s", out.Hash, sha256Hex(want))
	}
	if out.Path != "g.md" || out.Replacements != 1 {
		t.Errorf("out = %+v", out)
	}
	// Two lines before and after line 10, numbered as read_note numbers them.
	wantExcerpt := "8: Sow tomatoes in March.\n9: Water daily.\n10: The delivery arrives on 21 March.\n11: Prune in June.\n12: Harvest in August.\n"
	if out.Excerpt != wantExcerpt {
		t.Errorf("excerpt =\n%s\nwant\n%s", out.Excerpt, wantExcerpt)
	}
	part, _ := s.read(map[string]any{"path": "g.md", "start_line": 8, "max_lines": 5, "line_numbers": true})
	if part.Content == nil || *part.Content != wantExcerpt {
		t.Errorf("read_note of the same lines differs from the excerpt: %v", part.Content)
	}

	// The text block says the same without JSON: header, blank line, excerpt.
	wantText := "path: g.md\nhash: " + out.Hash + "\nreplacements: 1\n\n" + wantExcerpt
	if res.Text != wantText {
		t.Errorf("text block =\n%s\nwant\n%s", res.Text, wantText)
	}

	// The returned hash chains into the next edit.
	s.replace(map[string]any{
		"path": "g.md", "old_text": "Prune in June.", "new_text": "Prune in July.", "expected_hash": out.Hash,
	})
	if !strings.Contains(s.noteOnDisk("g.md"), "Prune in July.") {
		t.Error("chained replace with the returned hash did not apply")
	}
}

// Acceptance: two matches without replace_all write nothing and the error
// names both lines.
func TestReplaceInNoteAmbiguous(t *testing.T) {
	s := newReadSession(t, map[string]string{"g.md": replaceNote})

	msg := s.replaceErr(map[string]any{"path": "g.md", "old_text": "Water daily.", "new_text": "Water twice a day."})
	for _, want := range []string{"2 occurrences", "lines 9, 16", "replace_all"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not contain %q", msg, want)
		}
	}

	// More context makes it unique.
	s.replace(map[string]any{"path": "g.md", "old_text": "## Log\n\nWater daily.", "new_text": "## Log\n\nWatered."})

	// replace_all replaces every occurrence and shows each place.
	s2 := newReadSession(t, map[string]string{"g.md": replaceNote})
	out, _ := s2.replace(map[string]any{"path": "g.md", "old_text": "Water daily.", "new_text": "Water twice a day.", "replace_all": true})
	if out.Replacements != 2 || strings.Count(s2.noteOnDisk("g.md"), "Water twice a day.") != 2 {
		t.Errorf("replace_all: %+v", out)
	}
	wantExcerpt := "7: \n8: Sow tomatoes in March.\n9: Water twice a day.\n10: The delivery arrives on 14 March.\n11: Prune in June.\n" +
		"...\n14: ## Log\n15: \n16: Water twice a day.\n"
	if out.Excerpt != wantExcerpt {
		t.Errorf("replace_all excerpt =\n%s\nwant\n%s", out.Excerpt, wantExcerpt)
	}
}

// Acceptance: no match writes nothing and the error says what to do next.
func TestReplaceInNoteNotFound(t *testing.T) {
	s := newReadSession(t, map[string]string{"g.md": replaceNote})

	msg := s.replaceErr(map[string]any{"path": "g.md", "old_text": "The delivery arrives on 15 March.", "new_text": "x"})
	for _, want := range []string{"text not found in g.md", "character for character", "read_note", "start_line"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not contain %q", msg, want)
		}
	}
	// Whitespace is not tolerated either.
	s.replaceErr(map[string]any{"path": "g.md", "old_text": "Water  daily.", "new_text": "x"})
}

// Acceptance: a stale expected_hash is a conflict and writes nothing.
func TestReplaceInNoteConflict(t *testing.T) {
	s := newReadSession(t, map[string]string{"g.md": replaceNote})

	msg := s.replaceErr(map[string]any{
		"path": "g.md", "old_text": "14 March", "new_text": "21 March",
		"expected_hash": sha256Hex("an older version"),
	})
	if !strings.Contains(msg, "content hash mismatch") || !strings.Contains(msg, "read_note again") {
		t.Errorf("conflict error = %q", msg)
	}
}

// Acceptance: a replacement in the frontmatter reaches the index, so the
// changed tag shows in list_tags (and search's tag filter).
func TestReplaceInNoteFrontmatterUpdatesIndex(t *testing.T) {
	s := newSession(t)
	call(t, s, "write_note", map[string]any{"path": "garden/g.md", "content": replaceNote}, nil)

	var out replaceInNoteOut
	call(t, s, "replace_in_note", map[string]any{"path": "garden/g.md", "old_text": "tags: [draft, garden]", "new_text": "tags: [final, garden]"}, &out)
	if !strings.HasPrefix(out.Excerpt, "1: ---\n2: tags: [final, garden]\n3: ---\n4: # Garden\n") {
		t.Errorf("excerpt = %q", out.Excerpt)
	}

	var tags listTagsOut
	call(t, s, "list_tags", nil, &tags)
	got := map[string]int{}
	for _, tc := range tags.Tags {
		got[tc.Tag] = tc.Count
	}
	if got["final"] != 1 || got["draft"] != 0 || got["garden"] != 1 {
		t.Errorf("list_tags after the replace = %+v", tags.Tags)
	}
	var found searchNotesOut
	call(t, s, "search_notes", map[string]any{"tags": []string{"final"}}, &found)
	if len(found.Results) != 1 || found.Results[0].Path != "garden/g.md" {
		t.Errorf("search by the new tag = %+v", found.Results)
	}
}

func TestReplaceInNoteDeleteAndInvalid(t *testing.T) {
	s := newReadSession(t, map[string]string{"g.md": replaceNote})

	// An empty new_text deletes.
	out, _ := s.replace(map[string]any{"path": "g.md", "old_text": "Prune in June.\n", "new_text": ""})
	if strings.Contains(s.noteOnDisk("g.md"), "Prune") || out.Replacements != 1 {
		t.Errorf("delete: %+v", out)
	}
	if !strings.Contains(out.Excerpt, "10: The delivery arrives on 14 March.\n11: Harvest in August.\n") {
		t.Errorf("delete excerpt = %q", out.Excerpt)
	}

	// An empty old_text is an error, so is a missing note.
	if msg := s.replaceErr(map[string]any{"path": "g.md", "old_text": "", "new_text": "x"}); !strings.Contains(msg, "old_text must not be empty") {
		t.Errorf("empty old_text: %q", msg)
	}
	if res := s.call("replace_in_note", map[string]any{"path": "nope.md", "old_text": "a", "new_text": "b"}); !res.IsError || !strings.Contains(res.Text, "not found") {
		t.Errorf("missing note: %+v", res)
	}
}

// The result may not exceed the note size limit, as with patch_note.
func TestReplaceInNoteSizeLimit(t *testing.T) {
	v, err := vault.New(t.TempDir(), vault.WithMaxFileSize(int64(len(replaceNote)+10)))
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Write("g.md", replaceNote, vault.WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	ix := vault.NewIndex(v)
	if err := ix.Build(); err != nil {
		t.Fatal(err)
	}
	s := newSession(t, func(d *Deps) { d.Vault, d.Index = v, ix })
	if msg := callErr(t, s, "replace_in_note", map[string]any{"path": "g.md", "old_text": "June", "new_text": strings.Repeat("June ", 10)}); !strings.Contains(msg, "exceeds maximum size") {
		t.Errorf("oversized result: %q", msg)
	}
	if n, _ := v.Read("g.md"); n.Content != replaceNote {
		t.Error("an oversized replace wrote the note")
	}
}

func TestReplaceInNoteAnnotations(t *testing.T) {
	s := newSession(t)
	res, err := s.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range res.Tools {
		if tool.Name != "replace_in_note" {
			continue
		}
		a := tool.Annotations
		if a.DestructiveHint == nil || !*a.DestructiveHint || a.IdempotentHint || a.ReadOnlyHint {
			t.Errorf("annotations = %+v, want destructive and not idempotent", a)
		}
		schema, _ := json.Marshal(tool.InputSchema)
		var in struct {
			Required []string `json:"required"`
		}
		_ = json.Unmarshal(schema, &in)
		if strings.Join(in.Required, ",") != "path,old_text,new_text" {
			t.Errorf("required = %v, want path, old_text, new_text", in.Required)
		}
		return
	}
	t.Fatal("replace_in_note is not registered")
}

// The instructions rank the edit tools, smallest edit first.
func TestInstructionsRankEdits(t *testing.T) {
	got := instructions(Deps{})
	order := []string{"replace_in_note", "patch_note", "append_to_note", "write_note"}
	line := ""
	for _, l := range strings.Split(got, "\n") {
		if strings.HasPrefix(l, "- Edits") {
			line = l
		}
	}
	last := -1
	for _, name := range order {
		i := strings.Index(line, name)
		if i <= last {
			t.Fatalf("edit preference out of order at %s: %q", name, line)
		}
		last = i
	}
}

func TestReplaceExcerpt(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 60; i++ {
		fmt.Fprintf(&b, "l%d\n", i)
	}
	content := b.String()
	sp := func(a, z int) vault.LineSpan { return vault.LineSpan{First: a, Last: z} }

	cases := []struct {
		name  string
		spans []vault.LineSpan
		want  string
	}{
		{"first line", []vault.LineSpan{sp(1, 1)}, "1: l1\n2: l2\n3: l3\n"},
		{"last line", []vault.LineSpan{sp(60, 60)}, "58: l58\n59: l59\n60: l60\n"},
		{"touching places merge", []vault.LineSpan{sp(10, 10), sp(15, 15)},
			"8: l8\n9: l9\n10: l10\n11: l11\n12: l12\n13: l13\n14: l14\n15: l15\n16: l16\n17: l17\n"},
		{"apart places split", []vault.LineSpan{sp(10, 10), sp(16, 16)},
			"8: l8\n9: l9\n10: l10\n11: l11\n12: l12\n...\n14: l14\n15: l15\n16: l16\n17: l17\n18: l18\n"},
		{"long place elided", []vault.LineSpan{sp(10, 39)},
			"8: l8\n9: l9\n10: l10\n11: l11\n12: l12\n13: l13\n14: l14\n... (20 lines)\n35: l35\n36: l36\n37: l37\n38: l38\n39: l39\n40: l40\n41: l41\n"},
		{"at most three places", []vault.LineSpan{sp(5, 5), sp(15, 15), sp(25, 25), sp(35, 35), sp(45, 45)},
			"3: l3\n4: l4\n5: l5\n6: l6\n7: l7\n...\n13: l13\n14: l14\n15: l15\n16: l16\n17: l17\n...\n23: l23\n24: l24\n25: l25\n26: l26\n27: l27\n" +
				"... (2 more edited places not shown)\n"},
	}
	for _, c := range cases {
		if got := replaceExcerpt(content, c.spans); got != c.want {
			t.Errorf("%s:\n%s\nwant\n%s", c.name, got, c.want)
		}
	}

	if got := replaceExcerpt("", []vault.LineSpan{sp(1, 1)}); got != "(the note is empty)\n" {
		t.Errorf("empty note: %q", got)
	}
	// No trailing newline, CRLF endings: lines come out clean.
	if got := replaceExcerpt("a\r\nb\r\nc", []vault.LineSpan{sp(3, 3)}); got != "1: a\n2: b\n3: c\n" {
		t.Errorf("CRLF note: %q", got)
	}
	// A long unchanged line is clipped, a long changed line is not.
	long := strings.Repeat("é", 500)
	got := replaceExcerpt(long+"\n"+long+"\n", []vault.LineSpan{sp(2, 2)})
	parts := strings.Split(got, "\n")
	if want := "1: " + strings.Repeat("é", maxTextLine) + "…"; parts[0] != want {
		t.Errorf("context line not clipped to %d runes: %d bytes", maxTextLine, len(parts[0]))
	}
	if parts[1] != "2: "+long {
		t.Error("changed line was clipped")
	}
}

// smallFixPatch is the small-fix scenario as it ran before NSO-574: the
// section read, then patch_note sending the whole section back with the
// one sentence changed.
var smallFixPatch = payloadScenario{"small-fix (patch_note)", "read_note of the section, then patch_note of that section", func(r *payloadRun) {
	var note payloadNote
	r.call("read_note", map[string]any{"path": r.fx.longNote, "section": r.fx.section}, &note)
	body, ok := sectionBody(note.Content, r.fx.section)
	if !ok || !strings.Contains(body, r.fx.sentence) {
		r.t.Fatalf("small-fix: section %q with the target sentence not found", r.fx.section)
	}
	r.call("patch_note", map[string]any{
		"path":          r.fx.longNote,
		"section":       r.fx.section,
		"content":       strings.Replace(body, r.fx.sentence, r.fx.fixed, 1),
		"expected_hash": note.Hash,
	}, nil)
}}

func payloadScenarioNamed(t *testing.T, name string) payloadScenario {
	t.Helper()
	for _, sc := range payloadScenarios {
		if sc.name == name {
			return sc
		}
	}
	t.Fatalf("scenario %s missing", name)
	return payloadScenario{}
}

// small-fix in v1.12.1 (docs/payload-baseline.md): read_note of the whole
// note, then patch_note of the section.
const (
	smallFixV1121Args   = 1059
	smallFixV1121Result = 130020
)

// Acceptance: the small-fix scenario drops by at least 70 %. Against
// v1.12.1, the reference of the payload baseline and of M7, it drops by
// far more; the guards below keep what replace_in_note itself brings
// against the patch_note flow it replaces: the model writes a fraction of
// the arguments, and the excerpt that replaces a verification read stays
// small.
func TestSmallFixPayloadBudget(t *testing.T) {
	now := runPayloadScenarios(t, nil, []payloadScenario{payloadScenarioNamed(t, "small-fix")})
	patch := runPayloadScenarios(t, nil, []payloadScenario{smallFixPatch})
	t.Logf("\n%s\n%s", markdownTable("Scenario", append(patch.Scenarios, now.Scenarios...)),
		markdownTable("Tool", append(patch.Tools, now.Tools...)))

	sc := now.Scenarios[0]
	if before, after := smallFixV1121Args+smallFixV1121Result, sc.Args+sc.Result(); after > before*30/100 {
		t.Errorf("small-fix sends %d B (args and result), want at most 30 %% of v1.12.1's %d B", after, before)
	}
	tool := func(rep payloadReport, name string) payloadStats {
		for _, r := range rep.Tools {
			if r.Name == name {
				return r.payloadStats
			}
		}
		t.Fatalf("no %s call in the scenario", name)
		return payloadStats{}
	}
	replace, patched := tool(now, "replace_in_note"), tool(patch, "patch_note")
	if replace.Args > patched.Args*40/100 {
		t.Errorf("replace_in_note arguments are %d B, want at most 40 %% of patch_note's %d B", replace.Args, patched.Args)
	}
	// A one-line fix answers with five numbered lines and a short header;
	// on this fixture's lines (60 to 110 bytes) that stays under 600 B a
	// channel.
	if replace.Structured > 600 || replace.Text > 600 {
		t.Errorf("replace_in_note result is %d B structuredContent and %d B text, want at most 600 B each", replace.Structured, replace.Text)
	}
}
