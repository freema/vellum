// Package mcpserver exposes vault operations as MCP tools over the
// Streamable HTTP transport (PHY-111). It is a thin, deterministic layer:
// every tool maps 1:1 to a vault/index operation, and the tool surface is
// deliberately small (~15 tools) to keep agent context cheap.
package mcpserver

import (
	"context"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"

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
	}, nil)
	registerTools(server, d)
	if d.Curator {
		registerCuratorTools(server, d)
	}
	return server
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
}
type listNotesOut struct {
	Notes []vault.NoteInfo `json:"notes"`
}

type readNoteIn struct {
	Path string `json:"path" jsonschema:"vault-relative path of the note, e.g. projects/x/note.md"`
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
	Path         string `json:"path" jsonschema:"note to patch"`
	Section      string `json:"section" jsonschema:"heading text of the section to replace"`
	Content      string `json:"content" jsonschema:"new content of the section (heading line is kept)"`
	ExpectedHash string `json:"expected_hash,omitempty" jsonschema:"optional concurrency guard"`
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
	MaxResults int      `json:"max_results,omitempty" jsonschema:"cap on returned notes (default 50)"`
}
type searchNotesOut struct {
	Results []vault.Result `json:"results"`
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

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_notes",
		Description: "List markdown notes in the vault (optionally recursive).",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in listNotesIn) (*mcp.CallToolResult, listNotesOut, error) {
		notes, err := d.Vault.List(in.Dir, in.Recursive)
		if err != nil {
			return nil, listNotesOut{}, err
		}
		return nil, listNotesOut{Notes: notes}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "read_note",
		Description: "Read a note: content, frontmatter, tags, links and the content hash used for conflict-safe edits.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in readNoteIn) (*mcp.CallToolResult, *vault.Note, error) {
		note, err := d.Vault.Read(in.Path)
		if err != nil {
			return nil, nil, err
		}
		return nil, note, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "write_note",
		Description: "Create or replace a note. Without a path (or with a bare filename) the note lands in the inbox; the resolved path is returned. Pass expected_hash to update safely.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in writeNoteIn) (*mcp.CallToolResult, writeNoteOut, error) {
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

	mcp.AddTool(s, &mcp.Tool{
		Name:        "patch_note",
		Description: "Replace the content under one heading, leaving the rest of the note untouched.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in patchNoteIn) (*mcp.CallToolResult, writeNoteOut, error) {
		if err := d.Vault.Patch(in.Path, in.Section, in.Content, in.ExpectedHash); err != nil {
			return nil, writeNoteOut{}, err
		}
		_ = d.Index.Update(in.Path)
		return nil, writeNoteOut{Path: in.Path, Hash: rehash(in.Path)}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "append_to_note",
		Description: "Append markdown to the end of a note.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in editNoteIn) (*mcp.CallToolResult, writeNoteOut, error) {
		if err := d.Vault.Append(in.Path, in.Content, in.ExpectedHash); err != nil {
			return nil, writeNoteOut{}, err
		}
		_ = d.Index.Update(in.Path)
		return nil, writeNoteOut{Path: in.Path, Hash: rehash(in.Path)}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "prepend_to_note",
		Description: "Insert markdown at the top of a note's body (after frontmatter).",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in editNoteIn) (*mcp.CallToolResult, writeNoteOut, error) {
		if err := d.Vault.Prepend(in.Path, in.Content, in.ExpectedHash); err != nil {
			return nil, writeNoteOut{}, err
		}
		_ = d.Index.Update(in.Path)
		return nil, writeNoteOut{Path: in.Path, Hash: rehash(in.Path)}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "delete_note",
		Description: "Delete a note.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in deleteNoteIn) (*mcp.CallToolResult, deleteNoteOut, error) {
		if err := d.Vault.Delete(in.Path); err != nil {
			return nil, deleteNoteOut{}, err
		}
		d.Index.Remove(in.Path)
		return nil, deleteNoteOut{Deleted: in.Path}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "move_note",
		Description: "Move or rename a note. Backlink resolution follows the new location.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in moveNoteIn) (*mcp.CallToolResult, moveNoteOut, error) {
		if err := d.Vault.Move(in.From, in.To); err != nil {
			return nil, moveNoteOut{}, err
		}
		_ = d.Index.Rename(in.From, in.To)
		return nil, moveNoteOut(in), nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "search_notes",
		Description: "Ranked full-text search (title > tag > path > body), case-, diacritics- and typo-insensitive, with optional tag AND-filter and directory scope. Returns snippets with context; empty query lists notes matching the filters.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in searchNotesIn) (*mcp.CallToolResult, searchNotesOut, error) {
		results, err := d.Searcher.Search(in.Query, vault.SearchOpts{
			Tags:       in.Tags,
			Dir:        in.Dir,
			Regex:      in.Regex,
			MaxResults: in.MaxResults,
		})
		if err != nil {
			return nil, searchNotesOut{}, err
		}
		return nil, searchNotesOut{Results: results}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_tags",
		Description: "List all tags in the vault with note counts.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in struct{}) (*mcp.CallToolResult, listTagsOut, error) {
		return nil, listTagsOut{Tags: d.Index.Tags()}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "add_tags",
		Description: "Add tags to a note's frontmatter.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in tagsIn) (*mcp.CallToolResult, tagsOut, error) {
		tags, err := d.Vault.AddTags(in.Path, in.Tags, in.ExpectedHash)
		if err != nil {
			return nil, tagsOut{}, err
		}
		_ = d.Index.Update(in.Path)
		return nil, tagsOut{Path: in.Path, Tags: tags, Hash: rehash(in.Path)}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "remove_tags",
		Description: "Remove tags from a note's frontmatter (inline #tags in the body are left alone).",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in tagsIn) (*mcp.CallToolResult, tagsOut, error) {
		tags, err := d.Vault.RemoveTags(in.Path, in.Tags, in.ExpectedHash)
		if err != nil {
			return nil, tagsOut{}, err
		}
		_ = d.Index.Update(in.Path)
		return nil, tagsOut{Path: in.Path, Tags: tags, Hash: rehash(in.Path)}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_backlinks",
		Description: "Notes linking to this note (backlinks) and its outgoing resolved links.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in backlinksIn) (*mcp.CallToolResult, backlinksOut, error) {
		return nil, backlinksOut{
			Backlinks: d.Index.Backlinks(in.Path),
			Links:     d.Index.Links(in.Path),
		}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "set_status",
		Description: "Mark a note as a task with status backlog | in-progress | done. Only the status/type frontmatter lines change.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in setStatusIn) (*mcp.CallToolResult, setStatusOut, error) {
		if err := d.Vault.SetStatus(in.Path, in.Status, in.ExpectedHash); err != nil {
			return nil, setStatusOut{}, err
		}
		_ = d.Index.Update(in.Path)
		return nil, setStatusOut{Path: in.Path, Status: in.Status, Hash: rehash(in.Path)}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_tasks",
		Description: "List task notes, optionally filtered by status and project folder.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in listTasksIn) (*mcp.CallToolResult, listTasksOut, error) {
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
