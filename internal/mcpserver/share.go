package mcpserver

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/freema/vellum/internal/share"
)

// Share tools — registered only when VELLUM_SHARING=on.
//
// Publishing is the one vault operation an agent cannot undo for the person
// on the other side: once a link has been read, it has been read. So it is
// opt-in separately from sharing itself (VELLUM_SHARING=ui keeps minting with
// the human) and the tool is annotated as neither read-only nor idempotent-
// looking-harmless, so a client that surfaces annotations asks first.

type shareNoteIn struct {
	Path string `json:"path" jsonschema:"vault-relative note path"`
	// ExpiresIn is a human duration rather than seconds: an agent writes
	// "7d" far more reliably than 604800.
	ExpiresIn string `json:"expires_in,omitempty" jsonschema:"optional lifetime such as 24h, 7d or 4w; omit for a link that lives until it is revoked"`
}

type shareNoteOut struct {
	Path      string `json:"path"`
	URL       string `json:"url"`
	Token     string `json:"token"`
	ExpiresAt string `json:"expires_at,omitempty"`
	Reused    bool   `json:"reused"` // the note was already shared
}

type unshareNoteOut struct {
	Path     string `json:"path"`
	Unshared bool   `json:"unshared"`
}

func registerShareTools(s *mcp.Server, d Deps) {
	base := strings.TrimRight(strings.TrimSuffix(d.WebsiteURL, "/mcp"), "/")

	mcp.AddTool(s, editTool("share_note", "Share note",
		"Create a public, read-only link to a note and return its URL. Anyone with the link can read the note without signing in, and always sees its current content. Re-sharing a note returns the link it already has.",
		false, true, // creates nothing and destroys nothing in the vault
	), func(ctx context.Context, req *mcp.CallToolRequest, in shareNoteIn) (*mcp.CallToolResult, shareNoteOut, error) {
		ttl, err := parseShareTTL(in.ExpiresIn)
		if err != nil {
			return nil, shareNoteOut{}, err
		}
		// Reading the note first proves it exists and canonicalizes the
		// path, so two spellings cannot produce two links.
		note, err := d.Vault.Read(in.Path)
		if err != nil {
			return nil, shareNoteOut{}, err
		}
		_, existed := d.Shares.ForPath(note.Path)
		sh, err := d.Shares.Create(note.Path, ttl)
		if err != nil {
			return nil, shareNoteOut{}, err
		}
		out := shareNoteOut{
			Path:   sh.Path,
			URL:    base + "/s/" + sh.Token,
			Token:  sh.Token,
			Reused: existed,
		}
		if sh.ExpiresAt != nil {
			out.ExpiresAt = sh.ExpiresAt.Format(time.RFC3339)
		}
		return nil, out, nil
	})

	mcp.AddTool(s, editTool("unshare_note", "Stop sharing note",
		"Revoke a note's public link. The URL stops working immediately; the note itself is untouched.",
		false, true,
	), func(ctx context.Context, req *mcp.CallToolRequest, in notePathIn) (*mcp.CallToolResult, unshareNoteOut, error) {
		sh, ok := d.Shares.RevokePath(in.Path)
		if !ok {
			return nil, unshareNoteOut{Path: in.Path, Unshared: false}, nil
		}
		return nil, unshareNoteOut{Path: sh.Path, Unshared: true}, nil
	})
}

// parseShareTTL reads a lifetime. Go's ParseDuration stops at hours, but the
// useful units for a link are days and weeks, so those are accepted too.
// An empty string means "until revoked".
func parseShareTTL(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" || raw == "never" {
		return 0, nil
	}
	var d time.Duration
	var err error
	switch {
	case strings.HasSuffix(raw, "d"):
		d, err = scaledDuration(strings.TrimSuffix(raw, "d"), 24*time.Hour)
	case strings.HasSuffix(raw, "w"):
		d, err = scaledDuration(strings.TrimSuffix(raw, "w"), 7*24*time.Hour)
	default:
		if d, err = time.ParseDuration(raw); err != nil {
			err = fmt.Errorf("cannot read %q as a lifetime: use something like 24h, 7d or 4w", raw)
		}
	}
	if err != nil {
		return 0, err
	}
	if d < share.MinTTL || d > share.MaxTTL {
		return 0, fmt.Errorf("a lifetime of %q is out of range: use between %s and 365d, or omit it for a link with no expiry",
			raw, share.MinTTL)
	}
	return d, nil
}

func scaledDuration(n string, unit time.Duration) (time.Duration, error) {
	v, err := strconv.ParseFloat(n, 64)
	if err != nil {
		return 0, fmt.Errorf("cannot read %q as a number of units", n)
	}
	return time.Duration(v * float64(unit)), nil
}

// ---- keeping links in step with the vault ----

// shareFollow moves a link with its note, so renaming or filing a shared note
// does not break a URL somebody already has.
func (d Deps) shareFollow(from, to string) {
	if d.Shares != nil {
		d.Shares.Rename(from, to)
	}
}

// shareDrop revokes the link of a deleted note.
func (d Deps) shareDrop(path string) {
	if d.Shares != nil {
		d.Shares.RevokePath(path)
	}
}

// shareInstructions is appended to the server instructions when an agent can
// publish, so it knows the link is public before it hands one out.
const shareInstructions = "\n- share_note creates a public, read-only link that needs no sign-in — treat it as publishing. It returns a URL you can hand to a person; unshare_note revokes it. A shared note stays live: the reader always sees the current content."
