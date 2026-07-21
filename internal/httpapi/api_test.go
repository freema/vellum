package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/freema/vellum/internal/vault"
)

func newAPIServer(t *testing.T) *httptest.Server {
	t.Helper()
	v, err := vault.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ix := vault.NewIndex(v)
	if err := ix.Build(); err != nil {
		t.Fatal(err)
	}
	spa := fstest.MapFS{
		"index.html":    {Data: []byte("<html>vellum spa</html>")},
		"assets/app.js": {Data: []byte("console.log('app')")},
	}
	srv := httptest.NewServer(NewRouter("test", Options{
		API: &API{
			Vault:     v,
			Index:     ix,
			Searcher:  vault.NewScanSearcher(v, ix),
			Structure: vault.DefaultStructure(),
		},
		SPA: spa,
	}))
	t.Cleanup(srv.Close)
	return srv
}

func doReq(t *testing.T, method, url string, body string, headers map[string]string) (*http.Response, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = bytes.NewBufferString(body)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp, data
}

func TestAPICrudFlow(t *testing.T) {
	srv := newAPIServer(t)

	// PUT creates (no If-Match needed for new notes).
	resp, body := doReq(t, http.MethodPut, srv.URL+"/api/notes/projects/p/note.md",
		"---\ntags: [go]\n---\n# Note\n\nthe needle\n", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT = %d: %s", resp.StatusCode, body)
	}
	var put struct{ Path, Etag string }
	_ = json.Unmarshal(body, &put)
	if put.Etag == "" {
		t.Fatal("PUT must return the new etag")
	}

	// GET returns the note + ETag header.
	resp, body = doReq(t, http.MethodGet, srv.URL+"/api/notes/projects/p/note.md", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET = %d", resp.StatusCode)
	}
	if etag := resp.Header.Get("ETag"); etag != `"`+put.Etag+`"` {
		t.Errorf("ETag = %s, want %q", etag, put.Etag)
	}
	var note struct {
		Title string   `json:"title"`
		Tags  []string `json:"tags"`
	}
	_ = json.Unmarshal(body, &note)
	if note.Title != "Note" || len(note.Tags) != 1 {
		t.Errorf("note = %+v", note)
	}

	// List, search, tags, tasks, backlinks respond.
	resp, body = doReq(t, http.MethodGet, srv.URL+"/api/notes?recursive=true", "", nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "projects/p/note.md") {
		t.Errorf("list = %d %s", resp.StatusCode, body)
	}
	resp, body = doReq(t, http.MethodGet, srv.URL+"/api/search?q=needle&tags=go", "", nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "projects/p/note.md") {
		t.Errorf("search = %d %s", resp.StatusCode, body)
	}
	resp, body = doReq(t, http.MethodGet, srv.URL+"/api/tags", "", nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"go"`) {
		t.Errorf("tags = %d %s", resp.StatusCode, body)
	}
	resp, _ = doReq(t, http.MethodGet, srv.URL+"/api/backlinks/projects/p/note.md", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("backlinks = %d", resp.StatusCode)
	}

	// Move.
	resp, body = doReq(t, http.MethodPost, srv.URL+"/api/notes/move",
		`{"from":"projects/p/note.md","to":"archive/note.md"}`, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("move = %d: %s", resp.StatusCode, body)
	}

	// DELETE.
	resp, _ = doReq(t, http.MethodDelete, srv.URL+"/api/notes/archive/note.md", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete = %d", resp.StatusCode)
	}
	resp, _ = doReq(t, http.MethodGet, srv.URL+"/api/notes/archive/note.md", "", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET after delete = %d, want 404", resp.StatusCode)
	}
}

func TestAPIConflictPut(t *testing.T) {
	srv := newAPIServer(t)

	_, body := doReq(t, http.MethodPut, srv.URL+"/api/notes/n.md", "v1", nil)
	var put struct{ Etag string }
	_ = json.Unmarshal(body, &put)

	// Concurrent writer wins with last-write-wins.
	doReq(t, http.MethodPut, srv.URL+"/api/notes/n.md", "v2-other", nil)

	// Our stale If-Match must 409 and carry the current content + etag.
	resp, body := doReq(t, http.MethodPut, srv.URL+"/api/notes/n.md", "v2-mine",
		map[string]string{"If-Match": `"` + put.Etag + `"`})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("stale PUT = %d, want 409", resp.StatusCode)
	}
	var conflict struct{ Error, Content, Etag string }
	_ = json.Unmarshal(body, &conflict)
	if conflict.Error != "conflict" || conflict.Content != "v2-other" || conflict.Etag == "" {
		t.Errorf("conflict body = %+v", conflict)
	}

	// Retry with the fresh etag succeeds.
	resp, _ = doReq(t, http.MethodPut, srv.URL+"/api/notes/n.md", "v3",
		map[string]string{"If-Match": `"` + conflict.Etag + `"`})
	if resp.StatusCode != http.StatusOK {
		t.Errorf("fresh PUT = %d", resp.StatusCode)
	}

	// No If-Match = last-write-wins.
	resp, _ = doReq(t, http.MethodPut, srv.URL+"/api/notes/n.md", "v4", nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("LWW PUT = %d", resp.StatusCode)
	}
}

func TestAPICreateOnlyPut(t *testing.T) {
	srv := newAPIServer(t)

	create := map[string]string{"If-None-Match": "*"}
	resp, body := doReq(t, http.MethodPut, srv.URL+"/api/notes/inbox/untitled.md", "# Untitled\n", create)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create = %d: %s", resp.StatusCode, body)
	}

	// The name is taken now: a second create must fail instead of wiping it.
	resp, _ = doReq(t, http.MethodPut, srv.URL+"/api/notes/inbox/untitled.md", "# Other\n", create)
	if resp.StatusCode != http.StatusPreconditionFailed {
		t.Fatalf("create over existing = %d, want 412", resp.StatusCode)
	}
	_, body = doReq(t, http.MethodGet, srv.URL+"/api/notes/inbox/untitled.md", "", nil)
	if !strings.Contains(string(body), "# Untitled") {
		t.Errorf("existing note was overwritten: %s", body)
	}

	// The next free name goes through.
	resp, _ = doReq(t, http.MethodPut, srv.URL+"/api/notes/inbox/untitled-2.md", "# Other\n", create)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("create untitled-2 = %d", resp.StatusCode)
	}

	// Contradictory preconditions are rejected, not silently resolved one way.
	resp, _ = doReq(t, http.MethodPut, srv.URL+"/api/notes/inbox/untitled.md", "# Nope\n",
		map[string]string{"If-Match": `"deadbeef"`, "If-None-Match": "*"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("If-Match + If-None-Match = %d, want 400", resp.StatusCode)
	}
	_, body = doReq(t, http.MethodGet, srv.URL+"/api/notes/inbox/untitled.md", "", nil)
	if !strings.Contains(string(body), "# Untitled") {
		t.Errorf("note changed by a rejected write: %s", body)
	}
}

func TestAPIGetNoteBogusPathIs404(t *testing.T) {
	srv := newAPIServer(t)

	// A read of a path that can never be a note (wrong extension, invalid
	// shape) is 404, not 400 — a nonsense deep link must render the UI's
	// not-found state, never the server-error one.
	for _, p := range []string{
		"projects/alpha/alpha.mddafsd", // wrong extension
		"projects/alpha/alpha",         // no extension
		"nope.txt",
	} {
		resp, _ := doReq(t, http.MethodGet, srv.URL+"/api/notes/"+p, "", nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s: status = %d, want 404", p, resp.StatusCode)
		}
	}

	// Writes keep the explicit 400 so API clients learn the path is invalid.
	resp, _ := doReq(t, http.MethodPut, srv.URL+"/api/notes/bad.txt", "hi",
		map[string]string{"Content-Type": "text/markdown"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("PUT bad.txt: status = %d, want 400", resp.StatusCode)
	}
}

func TestAPITraversalRejected(t *testing.T) {
	srv := newAPIServer(t)
	resp, _ := doReq(t, http.MethodPut, srv.URL+"/api/notes/..%2Fescape.md", "x", nil)
	if resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusNotFound {
		t.Errorf("traversal PUT = %d, want 4xx", resp.StatusCode)
	}
}

func TestSPAFallback(t *testing.T) {
	srv := newAPIServer(t)

	// Root and client-side routes serve index.html.
	for _, path := range []string{"/", "/notes/projects/x", "/settings"} {
		resp, body := doReq(t, http.MethodGet, srv.URL+path, "", nil)
		if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "vellum spa") {
			t.Errorf("GET %s = %d %s, want index.html", path, resp.StatusCode, body)
		}
	}
	// Real static assets are served directly.
	resp, body := doReq(t, http.MethodGet, srv.URL+"/assets/app.js", "", nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "console.log") {
		t.Errorf("asset = %d %s", resp.StatusCode, body)
	}
	// API routes are not shadowed by the fallback.
	resp, body = doReq(t, http.MethodGet, srv.URL+"/api/tags", "", nil)
	if resp.StatusCode != http.StatusOK || strings.Contains(string(body), "vellum spa") {
		t.Errorf("api behind fallback = %d %s", resp.StatusCode, body)
	}
	// Unknown API routes 404 as JSON, not index.html.
	resp, body = doReq(t, http.MethodGet, srv.URL+"/api/nope", "", nil)
	if resp.StatusCode != http.StatusNotFound || strings.Contains(string(body), "vellum spa") {
		t.Errorf("unknown api = %d %s", resp.StatusCode, body)
	}
	// /healthz works without auth.
	resp, _ = doReq(t, http.MethodGet, srv.URL+"/healthz", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("healthz = %d", resp.StatusCode)
	}
}

func TestAPIVersion(t *testing.T) {
	srv := newAPIServer(t)
	resp, body := doReq(t, http.MethodGet, srv.URL+"/api/version", "", nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "test") {
		t.Errorf("version = %d %s", resp.StatusCode, body)
	}
}
