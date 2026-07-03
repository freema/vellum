package mcpserver

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/freema/vellum/internal/vault"
)

// Curator tools (PHY-113) — registered only when VELLUM_CURATOR=on.
// They return structured context; the agent decides, vellum never calls
// an LLM. Any resulting move goes through the regular move_note tool
// (human-in-the-loop).

type suggestLocationIn struct {
	Content string `json:"content" jsonschema:"markdown content of the note to place"`
}
type suggestLocationOut struct {
	Suggestions []vault.LocationSuggestion `json:"suggestions"`
}

type notePathIn struct {
	Path string `json:"path" jsonschema:"vault-relative note path"`
}

type suggestLinksOut struct {
	Candidates []vault.LinkCandidate `json:"candidates"`
}

type findOut struct {
	Notes []noteRef `json:"notes"`
}
type noteRef struct {
	Path  string `json:"path"`
	Title string `json:"title"`
}

type inboxStaleIn struct {
	Days int `json:"days,omitempty" jsonschema:"minimum age in days (default 14)"`
}

func registerCuratorTools(s *mcp.Server, d Deps) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "suggest_location",
		Description: "Rank existing vault folders for new content by tag overlap. Returns candidates with reasons; the agent decides.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in suggestLocationIn) (*mcp.CallToolResult, suggestLocationOut, error) {
		return nil, suggestLocationOut{Suggestions: d.Index.SuggestLocation(in.Content, d.Structure)}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "suggest_tags",
		Description: "Context for tagging a note: excerpt, current tags, the vault tag vocabulary and tags of linked notes.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in notePathIn) (*mcp.CallToolResult, *vault.TagsContext, error) {
		tc, err := d.Index.SuggestTags(d.Vault, in.Path)
		if err != nil {
			return nil, nil, err
		}
		return nil, tc, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "suggest_links",
		Description: "Link candidates for a note: unreciprocated backlinks, title mentions, shared tags — each with a reason.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in notePathIn) (*mcp.CallToolResult, suggestLinksOut, error) {
		cands, err := d.Index.SuggestLinks(d.Vault, in.Path)
		if err != nil {
			return nil, suggestLinksOut{}, err
		}
		return nil, suggestLinksOut{Candidates: cands}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "find_untagged",
		Description: "Notes without any tags (from the metadata index).",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in struct{}) (*mcp.CallToolResult, findOut, error) {
		return nil, toFindOut(d.Index.FindUntagged()), nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "find_orphans",
		Description: "Notes with no links in either direction (from the link graph).",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in struct{}) (*mcp.CallToolResult, findOut, error) {
		return nil, toFindOut(d.Index.FindOrphans()), nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "find_inbox_stale",
		Description: "Inbox notes that have been sitting untouched (default 14 days), oldest first.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in inboxStaleIn) (*mcp.CallToolResult, findOut, error) {
		days := in.Days
		if days <= 0 {
			days = 14
		}
		stale := d.Index.FindInboxStale(d.Structure.Inbox, time.Duration(days)*24*time.Hour)
		return nil, toFindOut(stale), nil
	})
}

func toFindOut(entries []vault.Entry) findOut {
	notes := make([]noteRef, len(entries))
	for i, e := range entries {
		notes[i] = noteRef{Path: e.Path, Title: e.Title}
	}
	return findOut{Notes: notes}
}
