package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
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
		{"", http.StatusOK},                        // CLI client, no Origin
		{"https://claude.ai", http.StatusOK},       // allowlisted
		{"https://evil.example", http.StatusForbidden}, // cross-origin
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
