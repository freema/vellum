package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/freema/vellum/internal/activity"
	"github.com/freema/vellum/internal/obs"
	"github.com/freema/vellum/internal/vault"
)

// API is the JSON REST surface for the SPA, mirroring vault operations.
type API struct {
	Vault     *vault.Vault
	Index     *vault.Index
	Searcher  vault.Searcher
	Structure vault.Structure

	// Activity records sessions and events for the Connections/Activity
	// panels. Endpoint is the public MCP URL shown in those panels; Curator
	// reflects whether the curator tools are enabled. All optional.
	Activity *activity.Recorder
	Endpoint string
	Curator  bool
}

// routes registers /api/* handlers on mux.
func (a *API) routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/version", a.handleVersion)
	mux.HandleFunc("GET /api/notes", a.handleList)
	mux.HandleFunc("GET /api/notes/{path...}", a.handleGetNote)
	mux.HandleFunc("PUT /api/notes/{path...}", a.handlePutNote)
	mux.HandleFunc("DELETE /api/notes/{path...}", a.handleDeleteNote)
	mux.HandleFunc("POST /api/notes/move", a.handleMove)
	mux.HandleFunc("GET /api/search", a.handleSearch)
	mux.HandleFunc("GET /api/tags", a.handleTags)
	mux.HandleFunc("GET /api/backlinks/{path...}", a.handleBacklinks)
	mux.HandleFunc("GET /api/tasks", a.handleTasks)
	mux.HandleFunc("GET /api/folders", a.handleListFolders)
	mux.HandleFunc("POST /api/folders", a.handleCreateFolder)
	mux.HandleFunc("DELETE /api/folders/{path...}", a.handleDeleteFolder)
	if a.Activity != nil {
		mux.HandleFunc("GET /api/connections", a.handleConnections)
		mux.HandleFunc("DELETE /api/connections/{id}", a.handleRevoke)
		mux.HandleFunc("GET /api/activity", a.handleActivity)
		mux.HandleFunc("GET /api/notifications", a.handleNotifications)
		mux.HandleFunc("POST /api/curator/run", a.handleCuratorRun)
	}
}

var apiVersion = "dev" // set by NewRouter

func (a *API) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"version": apiVersion})
}

// apiError maps vault errors to HTTP status codes.
func apiError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, vault.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, vault.ErrExists):
		status = http.StatusConflict
	case errors.Is(err, vault.ErrInvalidPath), errors.Is(err, vault.ErrNotMarkdown),
		errors.Is(err, vault.ErrSectionNotFound):
		status = http.StatusBadRequest
	case errors.Is(err, vault.ErrTooLarge):
		status = http.StatusRequestEntityTooLarge
	}
	if status == http.StatusInternalServerError {
		obs.Capture(err, map[string]string{"layer": "rest"})
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

// handleList serves listings from the metadata index — rich entries
// (tags, type/status, excerpt) without touching the disk.
func (a *API) handleList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	dir := strings.Trim(q.Get("dir"), "/")
	recursive := q.Get("recursive") == "true"

	notes := []vault.Entry{}
	for _, e := range a.Index.All() {
		rel := e.Path
		if dir != "" {
			var ok bool
			rel, ok = strings.CutPrefix(e.Path, dir+"/")
			if !ok {
				continue
			}
		}
		if !recursive && strings.Contains(rel, "/") {
			continue
		}
		notes = append(notes, e)
	}
	writeJSON(w, http.StatusOK, map[string]any{"notes": notes})
}

func (a *API) handleGetNote(w http.ResponseWriter, r *http.Request) {
	note, err := a.Vault.Read(r.PathValue("path"))
	if err != nil {
		// On a read, a path that can never resolve to a note (wrong
		// extension, invalid shape) is simply "not found" — 400 is reserved
		// for malformed writes. This also keeps the UI's not-found state (a
		// nonsense deep link must not present as a server error) and avoids
		// hinting path-validation details to probes.
		if errors.Is(err, vault.ErrNotMarkdown) || errors.Is(err, vault.ErrInvalidPath) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "note not found"})
			return
		}
		apiError(w, err)
		return
	}
	w.Header().Set("ETag", `"`+note.Hash+`"`)
	// Conditional GET: the SPA caches notes and revalidates with the ETag it
	// saw last, so an unchanged note costs a 304 instead of the full body.
	if inm := r.Header.Get("If-None-Match"); inm != "" && strings.Contains(inm, note.Hash) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	writeJSON(w, http.StatusOK, note)
}

// handlePutNote creates or updates a note. If-Match carries the ETag from a
// previous GET; a stale one yields 409 with the current content + ETag so
// the UI can offer reload/diff. If-None-Match: * is the create-only form —
// an existing note answers 412 instead of being overwritten. No condition at
// all = last-write-wins.
func (a *API) handlePutNote(w http.ResponseWriter, r *http.Request) {
	path := r.PathValue("path")
	body, err := io.ReadAll(io.LimitReader(r.Body, vault.DefaultMaxFileSize+1))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "read body: " + err.Error()})
		return
	}

	opts := vault.WriteOptions{Overwrite: true}
	createOnly := false
	if match := r.Header.Get("If-Match"); match != "" && match != "*" {
		opts.ExpectedHash = strings.Trim(match, `"`)
		opts.Overwrite = false
	} else if r.Header.Get("If-None-Match") == "*" {
		// The workspace picks the first free name for a new note from a listing
		// that may be stale (an agent can have written it a second ago), so a
		// create must never land on top of an existing note.
		createOnly = true
		opts.Overwrite = false
	}

	if err := a.Vault.Write(path, string(body), opts); err != nil {
		if createOnly && errors.Is(err, vault.ErrExists) {
			writeJSON(w, http.StatusPreconditionFailed, map[string]string{
				"error": "note already exists",
				"path":  path,
			})
			return
		}
		if errors.Is(err, vault.ErrConflict) {
			current, readErr := a.Vault.Read(path)
			if readErr != nil {
				apiError(w, readErr)
				return
			}
			w.Header().Set("ETag", `"`+current.Hash+`"`)
			writeJSON(w, http.StatusConflict, map[string]string{
				"error":   "conflict",
				"path":    current.Path,
				"content": current.Content,
				"etag":    current.Hash,
			})
			return
		}
		apiError(w, err)
		return
	}
	_ = a.Index.Update(path)
	a.recordUser("write", path, "saved from the workspace")

	note, err := a.Vault.Read(path)
	if err != nil {
		apiError(w, err)
		return
	}
	w.Header().Set("ETag", `"`+note.Hash+`"`)
	writeJSON(w, http.StatusOK, map[string]string{"path": note.Path, "etag": note.Hash})
}

func (a *API) handleDeleteNote(w http.ResponseWriter, r *http.Request) {
	path := r.PathValue("path")
	if err := a.Vault.Delete(path); err != nil {
		apiError(w, err)
		return
	}
	a.Index.Remove(path)
	a.recordUser("delete", path, "deleted from the workspace")
	writeJSON(w, http.StatusOK, map[string]string{"deleted": path})
}

func (a *API) handleMove(w http.ResponseWriter, r *http.Request) {
	var req struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	if err := a.Vault.Move(req.From, req.To); err != nil {
		apiError(w, err)
		return
	}
	_ = a.Index.Rename(req.From, req.To)
	a.recordUser("move", req.To, "moved from "+req.From)
	writeJSON(w, http.StatusOK, map[string]string{"from": req.From, "to": req.To})
}

func (a *API) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var tags []string
	if t := q.Get("tags"); t != "" {
		for _, tag := range strings.Split(t, ",") {
			if tag = strings.TrimSpace(tag); tag != "" {
				tags = append(tags, tag)
			}
		}
	}
	limit, _ := strconv.Atoi(q.Get("limit")) // 0 → server default
	results, err := a.Searcher.Search(q.Get("q"), vault.SearchOpts{
		Tags:       tags,
		Dir:        q.Get("dir"),
		MaxResults: limit,
	})
	if err != nil {
		apiError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

func (a *API) handleTags(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"tags": a.Index.Tags()})
}

func (a *API) handleBacklinks(w http.ResponseWriter, r *http.Request) {
	path := r.PathValue("path")
	writeJSON(w, http.StatusOK, map[string]any{
		"backlinks": a.Index.Backlinks(path),
		"links":     a.Index.Links(path),
	})
}

func (a *API) handleTasks(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	status := q.Get("status")
	if status != "" && !vault.ValidStatus(status) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid status"})
		return
	}
	entries := a.Index.ListTasks(status, q.Get("project"))
	type task struct {
		Path   string `json:"path"`
		Title  string `json:"title"`
		Status string `json:"status"`
	}
	tasks := make([]task, len(entries))
	for i, e := range entries {
		tasks[i] = task{Path: e.Path, Title: e.Title, Status: e.Status}
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": tasks})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
