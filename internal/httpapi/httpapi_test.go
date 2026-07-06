package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestHealthz(t *testing.T) {
	srv := httptest.NewServer(NewRouter("1.2.3", Options{}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf(`status = %v, want "ok"`, body["status"])
	}
	if body["version"] != "1.2.3" {
		t.Errorf(`version = %v, want "1.2.3"`, body["version"])
	}
	if body["auth"] != false {
		t.Errorf(`auth = %v, want false (no auth in this test)`, body["auth"])
	}
}

func TestHealthzMethodNotAllowed(t *testing.T) {
	srv := httptest.NewServer(NewRouter("dev", Options{}))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/healthz", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /healthz: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusMethodNotAllowed)
	}
}

func TestSPANoteDeepLinkServesApp(t *testing.T) {
	dist := fstest.MapFS{"index.html": {Data: []byte("<html>app</html>")}}
	srv := httptest.NewServer(NewRouter("test", Options{SPA: dist}))
	defer srv.Close()

	// A note deep link ends in .md but is a client route — it must load the
	// SPA shell, not 404 as a missing static file.
	for _, path := range []string{"/n/projects/x/tasks/53-audit.md", "/wl/roadmap"} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s: status = %d, want 200", path, resp.StatusCode)
		}
		if !strings.Contains(string(body), "app") {
			t.Errorf("%s: body = %q, want the SPA shell", path, body)
		}
	}
}

func TestSPANotFoundPage(t *testing.T) {
	dist := fstest.MapFS{"index.html": {Data: []byte("<html>app</html>")}}
	srv := httptest.NewServer(NewRouter("test", Options{SPA: dist}))
	defer srv.Close()

	// Browser navigation to a missing asset-like path → styled HTML page.
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/missing/thing.png", nil)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	if !strings.Contains(string(body), "No note lives here") {
		t.Error("styled 404 page missing its title")
	}
	if !strings.Contains(string(body), "missing/thing.png") {
		t.Error("styled 404 page should echo the requested path")
	}

	// Non-browser clients keep the plain text 404.
	resp2, err := http.Get(srv.URL + "/missing/thing.png")
	if err != nil {
		t.Fatal(err)
	}
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("plain status = %d, want 404", resp2.StatusCode)
	}
	if strings.Contains(string(body2), "<html") {
		t.Error("non-browser 404 must stay plain text")
	}
}

func TestMCPOriginCheck(t *testing.T) {
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(NewRouter("dev", Options{
		MCPHandler:     backend,
		AllowedOrigins: []string{"https://claude.ai"},
	}))
	defer srv.Close()

	tests := []struct {
		origin string
		want   int
	}{
		{"", http.StatusOK},                            // CLI client, no Origin
		{"https://claude.ai", http.StatusOK},           // allowlisted
		{"https://evil.example", http.StatusForbidden}, // cross-origin
		{"http://localhost:6274", http.StatusOK},       // MCP Inspector
		{"http://127.0.0.1:3000", http.StatusOK},       // loopback IP
		{"http://localhost.evil.example", http.StatusForbidden},
	}
	for _, tt := range tests {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/mcp", nil)
		if tt.origin != "" {
			req.Header.Set("Origin", tt.origin)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != tt.want {
			t.Errorf("origin %q: status = %d, want %d", tt.origin, resp.StatusCode, tt.want)
		}
	}
}
