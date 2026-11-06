// Package mcpserver exposes vault operations as MCP tools over the
// Streamable HTTP transport (PHY-111). It is a thin, deterministic layer:
// every tool maps 1:1 to a vault/index operation, and the tool surface is
// deliberately small (~15 tools) to keep agent context cheap.
package mcpserver

import (
	"context"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/freema/vellum/internal/share"
	"github.com/freema/vellum/internal/vault"
)

// Deps are the vault-layer collaborators of the MCP server.
type Deps struct {
	Vault     *vault.Vault
	Index     *vault.Index
	Searcher  vault.Searcher
	Structure vault.Structure
	Version   string
	// Curator registers the suggest_*/find_* context tools (VELLUM_CURATOR).
	Curator bool
	// Shares, when set, keeps public links in step with the vault: a moved
	// note takes its link along, a deleted one loses it. Set whenever
	// sharing is enabled at all, independently of ShareTools.
	Shares *share.Store
	// ShareTools registers share_note/unshare_note (VELLUM_SHARING=on).
	ShareTools bool
	// WebsiteURL is the server's public URL, advertised in the MCP server info.
	WebsiteURL string
}

// New builds the MCP server with all vellum tools registered.
func New(d Deps) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{
		Name:       "vellum",
		Title:      "vellum markdown vault",
		Version:    d.Version,
		WebsiteURL: d.WebsiteURL,
		Icons: []mcp.Icon{
			{Source: vellumIconDataURI(), MIMEType: "image/svg+xml", Sizes: []string{"any"}},
		},
	}, &mcp.ServerOptions{
		Instructions: instructions(d),
		// The empty Capabilities drops the SDK's historical `logging` default
		// (vellum never emits log messages); tools and resources — including
		// resources.subscribe — are still inferred from what is registered.
		Capabilities:       &mcp.ServerCapabilities{},
		SubscribeHandler:   subscribeNote,
		UnsubscribeHandler: func(context.Context, *mcp.UnsubscribeRequest) error { return nil },
	})
	registerTools(server, d)
	if d.Curator {
		registerCuratorTools(server, d)
	}
	if d.ShareTools && d.Shares != nil {
		registerShareTools(server, d)
	}
	registerResources(server, d)
	followOutsideMoves(d)
	return server
}

// instructions is the server-info guide agents read once per session. It
// carries the vault conventions a model cannot infer from tool schemas.
func instructions(d Deps) string {
	s := `vellum is a personal markdown vault. Notes are vault-relative .md paths like projects/x/note.md.

- New unclassified content: call write_note without a path — it lands in the inbox and the resolved path is returned. Don't invent folder structure; check list_notes first.
- Edits, smallest first: replace_in_note for a word, sentence or a few lines (exact old_text), patch_note for a whole section or to add to one (operation append, prepend, insert_before, insert_after; section takes a heading path like A > Notes when a name repeats), append_to_note / prepend_to_note to add, write_note to rewrite only when nothing else fits. Every write returns the note's content hash — pass it as expected_hash on the next edit of the same note for conflict-safe updates; after a hash-mismatch error, read_note again and retry.
- search_notes is case-, diacritics- and typo-insensitive; tags act as an AND-filter, and an empty query with tags is a pure tag filter. It returns 20 notes per call: total, truncated and hint say when more matched (narrow with tags or dir, or page with offset). response_format: concise gives just path, title, score and the line number of the first match per note. Each result has a 0 to 1 relevance score; min_score: 0.3 keeps the clear matches only.
- list_notes always returns dirs, the direct subfolders with their note counts, also without recursive: list the root first to learn the vault's structure, then list the folder you need (200 notes per call, page with offset). To see what is new, call list_notes with recursive: true, sort: modified and since: 7d (or 24h, 2w, a date).
- Long notes: ask read_note for the outline first, or use the line number from search_notes as start_line, then read just that section or range (lines count from 1 over the whole file, frontmatter included). The hash is always the whole note's, so it still works as expected_hash.
- Notes connect via [[wikilinks]]; move_note keeps backlinks resolving. get_backlinks shows both directions.
- Tasks are notes with status frontmatter: set_status / list_tasks use backlog | in-progress | done.
- Notes are also exposed as MCP resources (vellum://note/{path}); subscribe to one to receive resources/updated notifications when it changes.`
	s += "\n- " + folderContextInstruction(d)
	if d.Curator {
		s += "\n- Curator tools (suggest_*, find_*) return ranked context only — nothing moves automatically. Read the suggestions, decide, then act via the write/move/tag tools."
	}
	if d.ShareTools && d.Shares != nil {
		s += shareInstructions
	}
	if d.Index != nil {
		// The vault root's description, read once when the server is built
		// (capped at 500 characters by the index).
		if desc := d.Index.FolderDescription(""); desc != "" {
			s += "\n\nThis vault: " + desc
		}
	}
	return s
}

// folderContextInstruction is the one sentence that teaches the folder
// description convention (NSO-573) instead of a tool of its own: a tool's
// schema would cost every session several times these tokens, and a
// description is written a few times in a vault's life.
func folderContextInstruction(d Deps) string {
	name := vault.DefaultContextFile
	if d.Vault != nil {
		name = d.Vault.ContextFile()
	}
	return "Folders are described by <folder>/" + name + " with a `description:` frontmatter field. " +
		"Create or update it with write_note (read it first if it exists)."
}

// hintFalse gives the *bool annotation fields an address.
var hintFalse = false

// readTool describes a tool that never modifies the vault. All vellum tools
// are closed-world: they touch nothing beyond the vault directory.
func readTool(name, title, desc string) *mcp.Tool {
	return &mcp.Tool{
		Name: name, Title: title, Description: desc,
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &hintFalse},
	}
}

// editTool describes a vault mutation. destructive marks tools that can
// discard existing note content (delete, whole-note or section replace);
// metadata-only edits that are trivially reversible (tags, status) stay
// non-destructive. idempotent marks calls whose repetition leaves the
// vault as the first call left it.
func editTool(name, title, desc string, destructive, idempotent bool) *mcp.Tool {
	d := destructive
	return &mcp.Tool{
		Name: name, Title: title, Description: desc,
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: &d,
			IdempotentHint:  idempotent,
			OpenWorldHint:   &hintFalse,
		},
	}
}

// Handler wraps the Streamable HTTP transport for mounting at /mcp.
func Handler(server *mcp.Server) http.Handler {
	return mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{},
	)
}

// ---- tool inputs/outputs ----

type listNotesIn struct {
	Dir       string `json:"dir,omitempty" jsonschema:"vault directory to list, empty for the root"`
	Recursive bool   `json:"recursive,omitempty" jsonschema:"descend into subdirectories"`
	Limit     int    `json:"limit,omitempty" jsonschema:"cap on returned notes (default 200); total and hint tell when there are more"`
	Offset    int    `json:"offset,omitempty" jsonschema:"skip the first N notes of the sorted listing, to page through a long listing"`
	Sort      string `json:"sort,omitempty" jsonschema:"path (default) or modified: newest change first (ties by path), each note with an excerpt; to see what changed recently"`
	Since     string `json:"since,omitempty" jsonschema:"only notes modified since then: a duration back from now (24h, 7d, 2w) or a date (2026-11-01, local midnight)"`
}
type listNotesOut struct {
	Notes     []vault.NoteInfo `json:"notes"`
	Dirs      []vault.DirCount `json:"dirs"`      // direct subfolders of dir, with notes beneath each
	Total     int              `json:"total"`     // notes in the whole listing
	Truncated bool             `json:"truncated"` // more notes follow this page
	Hint      string           `json:"hint,omitempty"`
}

type readNoteIn struct {
	Path        string `json:"path" jsonschema:"vault-relative path of the note, e.g. projects/x/note.md"`
	StartLine   int    `json:"start_line,omitempty" jsonschema:"first line to return, 1-based over the whole file including frontmatter (the line numbers search_notes reports); without max_lines reads to the end"`
	MaxLines    int    `json:"max_lines,omitempty" jsonschema:"cap on returned lines, for a line range or a section"`
	Section     string `json:"section,omitempty" jsonschema:"heading text of one section to return: the heading and everything under it up to the next heading of the same or higher level (what patch_note replaces). A heading path such as Project A > Notes tells same-named sections apart; a name matching several headings is an error listing them. Cannot be combined with start_line"`
	Outline     bool   `json:"outline,omitempty" jsonschema:"return only the headings with their level and line number, plus frontmatter, tags, hash and line count; no content. Overrides the other read options"`
	LineNumbers bool   `json:"line_numbers,omitempty" jsonschema:"prefix every returned line with its file line number (N: ); off by default so content stays the exact file text"`
}

type writeNoteIn struct {
	Path         string `json:"path,omitempty" jsonschema:"target path; empty or bare filename falls back to the inbox"`
	Content      string `json:"content" jsonschema:"full markdown content including optional frontmatter"`
	Overwrite    bool   `json:"overwrite,omitempty" jsonschema:"allow replacing an existing note"`
	ExpectedHash string `json:"expected_hash,omitempty" jsonschema:"sha256 of the current content for optimistic concurrency; mismatch fails with a conflict"`
}
type writeNoteOut struct {
	Path string `json:"path"`
	Hash string `json:"hash"`
}

type patchNoteIn struct {
	Path    string `json:"path" jsonschema:"note to patch"`
	Section string `json:"section" jsonschema:"heading text of the section (case-insensitive), or a heading path to tell same-named sections apart, outermost first: Project A > Notes. A name that matches several headings is an error listing their paths and lines; nothing is written"`
	Content string `json:"content" jsonschema:"for replace the new content of the section (heading line is kept); for the other operations the markdown to add"`
	// Operation picks the edit (NSO-575); empty is replace.
	Operation    string `json:"operation,omitempty" jsonschema:"replace (default): swap the section's content; append: add at the end of the section (after its subsections); prepend: add right under its heading; insert_before: insert before its heading; insert_after: insert after the section's end. Use insert_before/insert_after with content starting with a heading to add a new section. A heading is always set off by one blank line"`
	ExpectedHash string `json:"expected_hash,omitempty" jsonschema:"optional concurrency guard"`
}

// patchNoteOut is writeNoteOut plus the file lines the new content
// occupies (1-based, frontmatter counted, as read_note numbers them).
type patchNoteOut struct {
	Path      string `json:"path"`
	Hash      string `json:"hash"`
	StartLine int    `json:"startLine"`
	EndLine   int    `json:"endLine"`
}

type editNoteIn struct {
	Path         string `json:"path" jsonschema:"note to edit"`
	Content      string `json:"content" jsonschema:"markdown to add"`
	ExpectedHash string `json:"expected_hash,omitempty" jsonschema:"optional concurrency guard"`
}

type deleteNoteIn struct {
	Path string `json:"path" jsonschema:"note to delete"`
}
type deleteNoteOut struct {
	Deleted string `json:"deleted"`
}

type moveNoteIn struct {
	From string `json:"from" jsonschema:"current path"`
	To   string `json:"to" jsonschema:"new path (directories are created)"`
}
type moveNoteOut struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type searchNotesIn struct {
	Query      string   `json:"query,omitempty" jsonschema:"text to search for; empty with tags set = pure tag filter"`
	Tags       []string `json:"tags,omitempty" jsonschema:"tags that must all be present"`
	Dir        string   `json:"dir,omitempty" jsonschema:"limit to a vault subtree"`
	Regex      bool     `json:"regex,omitempty" jsonschema:"treat query as a case-insensitive regular expression"`
	MaxResults int      `json:"max_results,omitempty" jsonschema:"cap on returned notes (default 20); total and hint tell when more matched"`
	Offset     int      `json:"offset,omitempty" jsonschema:"skip the first N results, to page through the ranking"`
	// ResponseFormat, ContextLines and MaxSnippets shape the hits (NSO-572,
	// see searchNotes in listing.go).
	ResponseFormat string `json:"response_format,omitempty" jsonschema:"detailed (default): snippets with context lines; concise: per note only path, title and the line number of its first match (no snippet text)"`
	ContextLines   *int   `json:"context_lines,omitempty" jsonschema:"lines of context around each match (default 2, 0 = the matching line alone)"`
	MaxSnippets    int    `json:"max_snippets,omitempty" jsonschema:"snippets per note (default 3)"`
	// MinScore drops weak matches by their BM25F score (NSO-577). The bands
	// in the description come from the golden set (docs/search-quality.md).
	MinScore float64 `json:"min_score,omitempty" jsonschema:"drop results whose score is below this (0 to 1, default 0 = keep all). Scores: about 0.7 and up = the terms are in the title, a heading or a tag, or all over the body; 0.4 to 0.7 = mentioned in the body; under 0.3 = weak (a passing mention in a long note, a typo or a part of a longer word). 0.3 keeps the clear matches; leave 0 when a typo or another word form may be all there is. Ignored for regex and pure tag filters, which have no score"`
}

type listTagsOut struct {
	Tags []vault.TagCount `json:"tags"`
}

type tagsIn struct {
	Path         string   `json:"path" jsonschema:"note to modify"`
	Tags         []string `json:"tags" jsonschema:"tags to add or remove (frontmatter tags only)"`
	ExpectedHash string   `json:"expected_hash,omitempty" jsonschema:"optional concurrency guard"`
}
type tagsOut struct {
	Path string   `json:"path"`
	Tags []string `json:"tags"`
	Hash string   `json:"hash"`
}

type backlinksIn struct {
	Path string `json:"path" jsonschema:"note whose connections to report"`
}
type backlinksOut struct {
	Backlinks []string `json:"backlinks"`
	Links     []string `json:"links"`
}

type setStatusIn struct {
	Path         string `json:"path" jsonschema:"note to mark"`
	Status       string `json:"status" jsonschema:"backlog | in-progress | done"`
	ExpectedHash string `json:"expected_hash,omitempty" jsonschema:"optional concurrency guard"`
}
type setStatusOut struct {
	Path   string `json:"path"`
	Status string `json:"status"`
	Hash   string `json:"hash"`
}

type listTasksIn struct {
	Status  string `json:"status,omitempty" jsonschema:"filter: backlog | in-progress | done"`
	Project string `json:"project,omitempty" jsonschema:"filter: project folder name under projects/"`
}
type taskEntry struct {
	Path   string `json:"path"`
	Title  string `json:"title"`
	Status string `json:"status"`
}
type listTasksOut struct {
	Tasks []taskEntry `json:"tasks"`
}

// ---- registration ----

func registerTools(s *mcp.Server, d Deps) {
	// rehash re-reads a note to report its post-write hash for chained edits.
	rehash := func(path string) string {
		if note, err := d.Vault.Read(path); err == nil {
			return note.Hash
		}
		return ""
	}

	mcp.AddTool(s, readTool("list_notes", "List notes",
		"List markdown notes in a folder (optionally recursive), sorted by path, at most limit (default 200) per call. Also returns dirs, the direct subfolders with their note counts, even without recursive: list the root to see the vault's structure. For what changed recently use sort: modified (newest first, with an excerpt) and since (7d, 24h, 2026-11-01).",
	), func(ctx context.Context, req *mcp.CallToolRequest, in listNotesIn) (*mcp.CallToolResult, listNotesOut, error) {
		out, err := listNotes(d, in)
		return nil, out, err
	})

	mcp.AddTool(s, readTool("read_note", "Read note",
		"Read a note: content, frontmatter, tags, links and the content hash used for conflict-safe edits. For a long note ask for outline first (headings with line numbers), then read one section or a line range (start_line, max_lines); hash is always that of the whole note.",
	), func(ctx context.Context, req *mcp.CallToolRequest, in readNoteIn) (*mcp.CallToolResult, readNoteOut, error) {
		if err := in.validate(); err != nil {
			return nil, readNoteOut{}, err
		}
		note, err := d.Vault.Read(in.Path)
		if err != nil {
			return nil, readNoteOut{}, err
		}
		if !in.partial() {
			out := newReadNoteOut(note)
			return textResult(noteText(out)), out, nil
		}
		out, text, err := readPart(note, in, d.Index)
		if err != nil {
			return nil, readNoteOut{}, err
		}
		return textResult(text), out, nil
	})

	registerReadNotes(s, d) // read_notes, readnotes.go

	mcp.AddTool(s, editTool("write_note", "Write note",
		"Create or replace a note. Without a path (or with a bare filename) the note lands in the inbox; the resolved path is returned. Pass expected_hash to update safely.",
		true, true, // overwrite replaces the whole note; same content, same result
	), func(ctx context.Context, req *mcp.CallToolRequest, in writeNoteIn) (*mcp.CallToolResult, writeNoteOut, error) {
		path, err := d.Vault.ResolveWritePath(in.Path, in.Content, d.Structure)
		if err != nil {
			return nil, writeNoteOut{}, err
		}
		if err := d.Vault.Write(path, in.Content, vault.WriteOptions{
			Overwrite:    in.Overwrite,
			ExpectedHash: in.ExpectedHash,
		}); err != nil {
			return nil, writeNoteOut{}, err
		}
		_ = d.Index.Update(path)
		return nil, writeNoteOut{Path: path, Hash: rehash(path)}, nil
	})

	mcp.AddTool(s, editTool("patch_note", "Patch section",
		"Edit one section, leaving the rest of the note untouched: replace the content under its heading (default), append or prepend to it, or insert a block before or after it. "+
			"section is the heading text, or a heading path like Project A > Notes when the name occurs more than once. "+
			"Returns the new hash and the lines the new content occupies.",
		true, false, // replace discards the section's content; append and inserts add again when repeated
	), func(ctx context.Context, req *mcp.CallToolRequest, in patchNoteIn) (*mcp.CallToolResult, patchNoteOut, error) {
		op, err := vault.ParsePatchOp(in.Operation)
		if err != nil {
			return nil, patchNoteOut{}, err
		}
		res, err := d.Vault.PatchSection(in.Path, in.Section, op, in.Content, in.ExpectedHash)
		if err != nil {
			return nil, patchNoteOut{}, err
		}
		_ = d.Index.Update(in.Path)
		return nil, patchNoteOut{Path: in.Path, Hash: res.Hash, StartLine: res.Lines.First, EndLine: res.Lines.Last}, nil
	})

	registerReplaceInNote(s, d) // replace_in_note, replace.go

	mcp.AddTool(s, editTool("append_to_note", "Append to note",
		"Append markdown to the end of a note.",
		false, false, // purely additive; appending twice duplicates
	), func(ctx context.Context, req *mcp.CallToolRequest, in editNoteIn) (*mcp.CallToolResult, writeNoteOut, error) {
		if err := d.Vault.Append(in.Path, in.Content, in.ExpectedHash); err != nil {
			return nil, writeNoteOut{}, err
		}
		_ = d.Index.Update(in.Path)
		return nil, writeNoteOut{Path: in.Path, Hash: rehash(in.Path)}, nil
	})

	mcp.AddTool(s, editTool("prepend_to_note", "Prepend to note",
		"Insert markdown at the top of a note's body (after frontmatter).",
		false, false,
	), func(ctx context.Context, req *mcp.CallToolRequest, in editNoteIn) (*mcp.CallToolResult, writeNoteOut, error) {
		if err := d.Vault.Prepend(in.Path, in.Content, in.ExpectedHash); err != nil {
			return nil, writeNoteOut{}, err
		}
		_ = d.Index.Update(in.Path)
		return nil, writeNoteOut{Path: in.Path, Hash: rehash(in.Path)}, nil
	})

	mcp.AddTool(s, editTool("delete_note", "Delete note",
		"Delete a note.",
		true, true,
	), func(ctx context.Context, req *mcp.CallToolRequest, in deleteNoteIn) (*mcp.CallToolResult, deleteNoteOut, error) {
		if err := d.Vault.Delete(in.Path); err != nil {
			return nil, deleteNoteOut{}, err
		}
		d.Index.Remove(in.Path)
		d.shareDrop(in.Path)
		return nil, deleteNoteOut{Deleted: in.Path}, nil
	})

	mcp.AddTool(s, editTool("move_note", "Move note",
		"Move or rename a note. Backlink resolution follows the new location.",
		false, false, // refuses existing targets, so content is never lost
	), func(ctx context.Context, req *mcp.CallToolRequest, in moveNoteIn) (*mcp.CallToolResult, moveNoteOut, error) {
		if err := d.Vault.Move(in.From, in.To); err != nil {
			return nil, moveNoteOut{}, err
		}
		_ = d.Index.Rename(in.From, in.To)
		d.shareFollow(in.From, in.To)
		return nil, moveNoteOut(in), nil
	})

	mcp.AddTool(s, readTool("search_notes", "Search vault",
		"Ranked full-text search (BM25F: title, path, headings, tags and body; rare words count more, short notes on the topic beat long ones that mention it), case-, diacritics- and typo-insensitive, matches Czech word forms (faktury finds fakturu), with optional tag AND-filter and directory scope. Every result has a relevance score from 0 to 1; min_score drops weak ones. When no note has every word, notes missing one come back with relaxed: true. Returns up to 20 notes with snippets and context, plus total and a hint when more matched; response_format concise returns only path, title, score and the line number of the first match. Empty query lists notes matching the filters.",
	), func(ctx context.Context, req *mcp.CallToolRequest, in searchNotesIn) (*mcp.CallToolResult, searchNotesOut, error) {
		out, text, err := searchNotes(d, in)
		if err != nil {
			return nil, searchNotesOut{}, err
		}
		return textResult(text), out, nil
	})

	mcp.AddTool(s, readTool("list_tags", "List tags",
		"List all tags in the vault with note counts.",
	), func(ctx context.Context, req *mcp.CallToolRequest, in struct{}) (*mcp.CallToolResult, listTagsOut, error) {
		return nil, listTagsOut{Tags: d.Index.Tags()}, nil
	})

	mcp.AddTool(s, editTool("add_tags", "Add tags",
		"Add tags to a note's frontmatter.",
		false, true,
	), func(ctx context.Context, req *mcp.CallToolRequest, in tagsIn) (*mcp.CallToolResult, tagsOut, error) {
		tags, err := d.Vault.AddTags(in.Path, in.Tags, in.ExpectedHash)
		if err != nil {
			return nil, tagsOut{}, err
		}
		_ = d.Index.Update(in.Path)
		return nil, tagsOut{Path: in.Path, Tags: tags, Hash: rehash(in.Path)}, nil
	})

	mcp.AddTool(s, editTool("remove_tags", "Remove tags",
		"Remove tags from a note's frontmatter (inline #tags in the body are left alone).",
		false, true, // metadata-only and reversible via add_tags
	), func(ctx context.Context, req *mcp.CallToolRequest, in tagsIn) (*mcp.CallToolResult, tagsOut, error) {
		tags, err := d.Vault.RemoveTags(in.Path, in.Tags, in.ExpectedHash)
		if err != nil {
			return nil, tagsOut{}, err
		}
		_ = d.Index.Update(in.Path)
		return nil, tagsOut{Path: in.Path, Tags: tags, Hash: rehash(in.Path)}, nil
	})

	mcp.AddTool(s, readTool("get_backlinks", "Get backlinks",
		"Notes linking to this note (backlinks) and its outgoing resolved links.",
	), func(ctx context.Context, req *mcp.CallToolRequest, in backlinksIn) (*mcp.CallToolResult, backlinksOut, error) {
		return nil, backlinksOut{
			Backlinks: d.Index.Backlinks(in.Path),
			Links:     d.Index.Links(in.Path),
		}, nil
	})

	mcp.AddTool(s, editTool("set_status", "Set task status",
		"Mark a note as a task with status backlog | in-progress | done. Only the status/type frontmatter lines change.",
		false, true,
	), func(ctx context.Context, req *mcp.CallToolRequest, in setStatusIn) (*mcp.CallToolResult, setStatusOut, error) {
		if err := d.Vault.SetStatus(in.Path, in.Status, in.ExpectedHash); err != nil {
			return nil, setStatusOut{}, err
		}
		_ = d.Index.Update(in.Path)
		return nil, setStatusOut{Path: in.Path, Status: in.Status, Hash: rehash(in.Path)}, nil
	})

	mcp.AddTool(s, readTool("list_tasks", "List tasks",
		"List task notes, optionally filtered by status and project folder.",
	), func(ctx context.Context, req *mcp.CallToolRequest, in listTasksIn) (*mcp.CallToolResult, listTasksOut, error) {
		if in.Status != "" && !vault.ValidStatus(in.Status) {
			return nil, listTasksOut{}, vault.ErrInvalidPath
		}
		entries := d.Index.ListTasks(in.Status, in.Project)
		tasks := make([]taskEntry, len(entries))
		for i, e := range entries {
			tasks[i] = taskEntry{Path: e.Path, Title: e.Title, Status: e.Status}
		}
		return nil, listTasksOut{Tasks: tasks}, nil
	})
}
