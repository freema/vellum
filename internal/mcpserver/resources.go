package mcpserver

// Notes double as MCP resources so clients can attach them as context
// (resource pickers, @-mentions) and subscribe to per-note change
// notifications. The advertised resource list mirrors the metadata index;
// content reads go to the vault. URIs are vellum://note/{vault path}.

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/freema/vellum/internal/vault"
)

const noteURIPrefix = "vellum://note/"

// noteURI is the canonical resource URI of a vault path. Building it
// through url.URL percent-encodes what needs encoding, so paths with
// spaces or diacritics survive the round trip.
func noteURI(path string) string {
	u := url.URL{Scheme: "vellum", Host: "note", Path: "/" + path}
	return u.String()
}

// noteURIPath extracts the vault path from a note resource URI.
func noteURIPath(uri string) (string, bool) {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "vellum" || u.Host != "note" {
		return "", false
	}
	p := strings.TrimPrefix(u.Path, "/")
	return p, p != ""
}

// subscribeNote accepts subscriptions for anything shaped like a note URI —
// including notes that don't exist yet, so a client can watch a path it is
// about to create. The SDK tracks the sessions; we only validate.
func subscribeNote(_ context.Context, req *mcp.SubscribeRequest) error {
	if _, ok := noteURIPath(req.Params.URI); !ok {
		return mcp.ResourceNotFoundError(req.Params.URI)
	}
	return nil
}

func registerResources(s *mcp.Server, d Deps) {
	read := func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		path, ok := noteURIPath(req.Params.URI)
		if !ok {
			return nil, mcp.ResourceNotFoundError(req.Params.URI)
		}
		note, err := d.Vault.Read(path)
		if err != nil {
			if errors.Is(err, vault.ErrNotFound) || errors.Is(err, vault.ErrInvalidPath) || errors.Is(err, vault.ErrNotMarkdown) {
				return nil, mcp.ResourceNotFoundError(req.Params.URI)
			}
			return nil, err
		}
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{
			URI:      req.Params.URI,
			MIMEType: "text/markdown",
			Text:     note.Content,
		}}}, nil
	}

	// The template makes any vault path addressable, even before it is
	// listed (or when the client constructs the URI itself).
	s.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: noteURIPrefix + "{+path}",
		Name:        "note",
		Title:       "Vault note",
		Description: "A markdown note addressed by its vault-relative path, e.g. " + noteURIPrefix + "projects/x/note.md.",
		MIMEType:    "text/markdown",
	}, read)

	announce := func(e vault.Entry) {
		s.AddResource(&mcp.Resource{
			URI:      noteURI(e.Path),
			Name:     e.Path,
			Title:    e.Title,
			MIMEType: "text/markdown",
			Size:     e.Size,
		}, read)
	}

	// titles remembers what the list advertises, so routine content saves
	// (SPA autosaves every second while typing) don't turn into a
	// list_changed broadcast — only creations, deletions and title changes
	// re-announce the list. Subscribers still get every resources/updated.
	var mu sync.Mutex
	titles := map[string]string{}
	for _, e := range d.Index.All() {
		titles[e.Path] = e.Title
		announce(e)
	}

	d.Index.OnChange(func(c vault.Change) {
		uri := noteURI(c.Path)
		if c.Deleted {
			mu.Lock()
			_, known := titles[c.Path]
			delete(titles, c.Path)
			mu.Unlock()
			if known {
				s.RemoveResources(uri)
			}
		} else if e, ok := d.Index.Get(c.Path); ok {
			mu.Lock()
			prev, known := titles[c.Path]
			titles[c.Path] = e.Title
			mu.Unlock()
			if !known || prev != e.Title {
				announce(e)
			}
		}
		_ = s.ResourceUpdated(context.Background(), &mcp.ResourceUpdatedNotificationParams{URI: uri})
	})
}
