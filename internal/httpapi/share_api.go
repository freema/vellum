package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/freema/vellum/internal/share"
)

// ---- the authenticated side: minting, listing and revoking links ----
//
// Shares are addressed by token rather than by note path: a path is a
// wildcard segment that has to sit last in a ServeMux pattern, and the UI
// always knows the token of the link it is holding.

func (a *API) handleListShares(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	list := a.Shares.List()
	out := make([]shareView, 0, len(list))
	for _, sh := range list {
		out = append(out, a.shareView(sh, now))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"sharing": a.Sharing,
		"baseUrl": a.shareBase(),
		"shares":  out,
	})
}

func (a *API) handleCreateShare(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
		// ExpiresIn is a lifetime in seconds; 0 (or absent) means the link
		// lives until it is revoked.
		ExpiresIn int64 `json:"expiresIn"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	// Reading the note both proves it exists and canonicalizes the path, so
	// two spellings of the same note cannot end up with two links.
	note, err := a.Vault.Read(req.Path)
	if err != nil {
		apiError(w, err)
		return
	}
	sh, err := a.Shares.Create(note.Path, time.Duration(req.ExpiresIn)*time.Second)
	if err != nil {
		if errors.Is(err, share.ErrInvalidTTL) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		apiError(w, err)
		return
	}
	a.recordUser("share", note.Path, "created a public link ("+expiryPhrase(sh)+")")
	writeJSON(w, http.StatusOK, a.shareView(sh, time.Now()))
}

func (a *API) handleRevokeShare(w http.ResponseWriter, r *http.Request) {
	sh, ok := a.Shares.Revoke(r.PathValue("token"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such share"})
		return
	}
	a.recordUser("unshare", sh.Path, "revoked the public link")
	writeJSON(w, http.StatusOK, map[string]string{"revoked": sh.Path})
}

// shareView is what the workspace renders: the link itself plus enough
// context to decide whether it should still exist.
type shareView struct {
	Token     string `json:"token"`
	Path      string `json:"path"`
	Title     string `json:"title"`
	URL       string `json:"url"`
	Created   string `json:"created"`             // relative, e.g. "3d"
	ExpiresAt string `json:"expiresAt,omitempty"` // RFC3339, absent = never
	ExpiresIn string `json:"expiresIn,omitempty"` // relative, e.g. "6d"
	Views     int    `json:"views"`
	LastView  string `json:"lastView,omitempty"`
}

func (a *API) shareView(sh share.Share, now time.Time) shareView {
	v := shareView{
		Token:   sh.Token,
		Path:    sh.Path,
		Title:   a.prettyTarget(sh.Path),
		URL:     a.shareBase() + "/s/" + sh.Token,
		Created: reltime(now.Sub(sh.CreatedAt)),
		Views:   sh.Views,
	}
	if sh.ExpiresAt != nil {
		v.ExpiresAt = sh.ExpiresAt.Format(time.RFC3339)
		v.ExpiresIn = reltime(sh.ExpiresAt.Sub(now))
	}
	if sh.LastViewAt != nil {
		v.LastView = reltime(now.Sub(*sh.LastViewAt))
	}
	return v
}

// shareBase is the origin links are built on. It is the same public URL the
// OAuth issuer advertises, so a deployment that is reachable for Claude is
// reachable for a share link too.
func (a *API) shareBase() string {
	return strings.TrimRight(strings.TrimSuffix(a.PublicURL, "/mcp"), "/")
}

func expiryPhrase(sh share.Share) string {
	if sh.ExpiresAt == nil {
		return "no expiry"
	}
	return "expires in " + reltime(time.Until(*sh.ExpiresAt))
}

// ---- keeping links in step with the vault ----

// shareFollow moves a link to the note's new path. Renaming or filing a note
// must not break a link that was already handed out — the same promise
// move_note makes for backlinks.
func (a *API) shareFollow(from, to string) {
	if a.Shares != nil {
		a.Shares.Rename(from, to)
	}
}

// shareDrop revokes the link of a deleted note. The note is gone, so the link
// must die with it rather than resurrect if a note is later recreated at the
// same path.
func (a *API) shareDrop(path string) {
	if a.Shares != nil {
		a.Shares.RevokePath(path)
	}
}

// shareDropUnder revokes every link inside a deleted folder.
func (a *API) shareDropUnder(dir string) {
	if a.Shares != nil {
		a.Shares.RevokePrefix(dir)
	}
}
