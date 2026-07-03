package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/freema/vellum/internal/vault"
)

// API is the JSON REST surface for the SPA, mirroring vault operations.
type API struct {
	Vault     *vault.Vault
	Index     *vault.Index
	Searcher  vault.Searcher
	Structure vault.Structure
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
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func (a *API) handleList(w http.ResponseWriter, r *http.Request) {
	recursive := r.URL.Query().Get("recursive") == "true"
	notes, err := a.Vault.List(r.URL.Query().Get("dir"), recursive)
	if err != nil {
		apiError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"notes": notes})
}

func (a *API) handleGetNote(w http.ResponseWriter, r *http.Request) {
	note, err := a.Vault.Read(r.PathValue("path"))
	if err != nil {
		apiError(w, err)
		return
	}
	w.Header().Set("ETag", `"`+note.Hash+`"`)
	writeJSON(w, http.StatusOK, note)
}

// handlePutNote creates or updates a note. If-Match carries the ETag from a
// previous GET; a stale one yields 409 with the current content + ETag so
// the UI can offer reload/diff. No If-Match = last-write-wins.
func (a *API) handlePutNote(w http.ResponseWriter, r *http.Request) {
	path := r.PathValue("path")
	body, err := io.ReadAll(io.LimitReader(r.Body, vault.DefaultMaxFileSize+1))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "read body: " + err.Error()})
		return
	}

	opts := vault.WriteOptions{Overwrite: true}
	if match := r.Header.Get("If-Match"); match != "" && match != "*" {
		opts.ExpectedHash = strings.Trim(match, `"`)
		opts.Overwrite = false
	}

	if err := a.Vault.Write(path, string(body), opts); err != nil {
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
	results, err := a.Searcher.Search(q.Get("q"), vault.SearchOpts{
		Tags: tags,
		Dir:  q.Get("dir"),
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
