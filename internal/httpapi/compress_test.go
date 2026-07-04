package httpapi

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/freema/vellum/internal/vault"
)

func newAPIRouter(t *testing.T) http.Handler {
	t.Helper()
	v, err := vault.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ix := vault.NewIndex(v)
	if err := ix.Build(); err != nil {
		t.Fatal(err)
	}
	return NewRouter("test", Options{
		API: &API{
			Vault:     v,
			Index:     ix,
			Searcher:  vault.NewScanSearcher(v, ix),
			Structure: vault.DefaultStructure(),
		},
	})
}

func TestAPIGzipCompression(t *testing.T) {
	srv := httptest.NewServer(newAPIRouter(t))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/notes?recursive=true", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	tr := &http.Transport{DisableCompression: true} // see the raw encoding
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if ce := resp.Header.Get("Content-Encoding"); ce != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", ce)
	}
	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(gz)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"notes"`) {
		t.Errorf("decompressed body = %q", body)
	}
}

func TestNoGzipWithoutAcceptEncoding(t *testing.T) {
	srv := httptest.NewServer(newAPIRouter(t))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/notes", nil)
	tr := &http.Transport{DisableCompression: true}
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ce := resp.Header.Get("Content-Encoding"); ce != "" {
		t.Errorf("Content-Encoding = %q, want none", ce)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"notes"`) {
		t.Errorf("plain body = %q", body)
	}
}

func TestSPAAssetCacheHeaders(t *testing.T) {
	dist := fstest.MapFS{
		"index.html":           {Data: []byte("<html>app</html>")},
		"assets/index-abc.js":  {Data: []byte("console.log('hi')")},
		"assets/index-abc.css": {Data: []byte("body{}")},
	}
	srv := httptest.NewServer(NewRouter("test", Options{SPA: dist}))
	defer srv.Close()

	// Hashed assets are immutable for a year.
	resp, err := http.Get(srv.URL + "/assets/index-abc.js")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("asset Cache-Control = %q, want immutable", cc)
	}

	// The HTML shell always revalidates, so deploys are picked up.
	resp, err = http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if cc := resp.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("index Cache-Control = %q, want no-cache", cc)
	}
}

func TestSPAAssetGzip(t *testing.T) {
	dist := fstest.MapFS{
		"index.html":          {Data: []byte("<html>app</html>")},
		"assets/index-abc.js": {Data: []byte(strings.Repeat("console.log('hi');", 50))},
	}
	srv := httptest.NewServer(NewRouter("test", Options{SPA: dist}))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/assets/index-abc.js", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	tr := &http.Transport{DisableCompression: true}
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ce := resp.Header.Get("Content-Encoding"); ce != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", ce)
	}
	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(gz); err != nil {
		t.Errorf("gunzip: %v", err)
	}
}

func TestGetNoteConditional304(t *testing.T) {
	srv := httptest.NewServer(newAPIRouter(t))
	defer srv.Close()

	// Create, then GET to learn the ETag.
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/notes/inbox/a.md", strings.NewReader("# A\n\nbody\n"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	resp, err = http.Get(srv.URL + "/api/notes/inbox/a.md")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	etag := resp.Header.Get("ETag")
	if etag == "" {
		t.Fatal("GET note returned no ETag")
	}

	// Same ETag → 304 with an empty body.
	req, _ = http.NewRequest(http.MethodGet, srv.URL+"/api/notes/inbox/a.md", nil)
	req.Header.Set("If-None-Match", etag)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotModified {
		t.Fatalf("status = %d, want 304", resp.StatusCode)
	}
	if len(body) != 0 {
		t.Errorf("304 body = %q, want empty", body)
	}

	// Different ETag → full 200.
	req, _ = http.NewRequest(http.MethodGet, srv.URL+"/api/notes/inbox/a.md", nil)
	req.Header.Set("If-None-Match", `"deadbeef"`)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "body") {
		t.Errorf("stale ETag: status = %d body = %q, want 200 + content", resp.StatusCode, body)
	}
}
