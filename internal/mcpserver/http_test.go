package mcpserver_test

// Integration tests over real HTTP (PHY-129): full router, streamable
// transport, OAuth bearer — the same wire path production uses.

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/freema/vellum/internal/auth"
	"github.com/freema/vellum/internal/httpapi"
	"github.com/freema/vellum/internal/mcpserver"
	"github.com/freema/vellum/internal/vault"
)

const testSecret = "0123456789abcdef0123456789abcdef"

// newHTTPServer boots the full stack: fixture vault copy, index, MCP server,
// OAuth provider, router — served by httptest.
func newHTTPServer(t *testing.T) *httptest.Server {
	t.Helper()

	// Work on a throwaway copy of the fixture so tests can mutate notes.
	root := t.TempDir()
	fixture, err := filepath.Abs("../../testdata/vault")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(root, os.DirFS(fixture)); err != nil {
		t.Fatal(err)
	}

	v, err := vault.New(root)
	if err != nil {
		t.Fatal(err)
	}
	ix := vault.NewIndex(v)
	if err := ix.Build(); err != nil {
		t.Fatal(err)
	}
	deps := mcpserver.Deps{
		Vault:     v,
		Index:     ix,
		Searcher:  vault.NewScanSearcher(v, ix),
		Structure: vault.DefaultStructure(),
		Version:   "http-test",
		Curator:   true,
	}
	provider, err := auth.NewProvider(auth.Config{
		Enabled:      true,
		ClientID:     "vellum",
		ClientSecret: testSecret,
		IssuerURL:    "http://vellum.test",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(provider.Close)

	srv := httptest.NewServer(httpapi.NewRouter("http-test", httpapi.Options{
		MCPHandler: mcpserver.Handler(mcpserver.New(deps)),
		Auth:       provider,
	}))
	t.Cleanup(srv.Close)
	return srv
}

// fetchToken walks the full OAuth code flow (consent POST + PKCE exchange).
func fetchToken(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	verifier := "integration-test-verifier-0123456789abcdef"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.PostForm(srv.URL+"/authorize", url.Values{
		"decision": {"approve"}, "client_id": {"vellum"},
		"redirect_uri":   {"https://claude.ai/api/mcp/auth_callback"},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	loc, _ := url.Parse(resp.Header.Get("Location"))
	code := loc.Query().Get("code")
	if code == "" {
		t.Fatalf("no code in %s", loc)
	}

	resp, err = http.PostForm(srv.URL+"/token", url.Values{
		"grant_type": {"authorization_code"}, "code": {code},
		"code_verifier": {verifier},
		"client_id":     {"vellum"}, "client_secret": {testSecret},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil || body.AccessToken == "" {
		t.Fatalf("token exchange failed: %v %v", err, body)
	}
	return body.AccessToken
}

type bearerTransport struct {
	token string
	base  http.RoundTripper
}

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.base.RoundTrip(r)
}

func connectHTTP(t *testing.T, srv *httptest.Server, token string) *mcp.ClientSession {
	t.Helper()
	transport := &mcp.StreamableClientTransport{
		Endpoint:   srv.URL + "/mcp",
		HTTPClient: &http.Client{Transport: bearerTransport{token: token, base: http.DefaultTransport}},
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "integration", Version: "0"}, nil)
	session, err := client.Connect(context.Background(), transport, nil)
	if err != nil {
		t.Fatalf("connect over HTTP: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func TestMCPOverHTTPRequiresAuth(t *testing.T) {
	srv := newHTTPServer(t)

	// Raw POST without a token → 401 with a Bearer challenge.
	resp, err := http.Post(srv.URL+"/mcp", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no token = %d, want 401", resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("WWW-Authenticate"), "invalid_token") {
		t.Errorf("WWW-Authenticate = %q", resp.Header.Get("WWW-Authenticate"))
	}

	// SDK client without a token cannot initialize.
	client := mcp.NewClient(&mcp.Implementation{Name: "anon", Version: "0"}, nil)
	if _, err := client.Connect(context.Background(),
		&mcp.StreamableClientTransport{Endpoint: srv.URL + "/mcp"}, nil); err == nil {
		t.Error("unauthenticated SDK connect should fail")
	}
}

func TestMCPOverHTTPFullFlow(t *testing.T) {
	srv := newHTTPServer(t)
	session := connectHTTP(t, srv, fetchToken(t, srv))
	ctx := context.Background()

	// tools/list: 15 core + 6 curator tools.
	tools, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 21 {
		t.Errorf("tools = %d, want 21 (15 core + 6 curator)", len(tools.Tools))
	}

	call := func(name string, args map[string]any) *mcp.CallToolResult {
		t.Helper()
		res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if res.IsError {
			text := ""
			for _, c := range res.Content {
				if tc, ok := c.(*mcp.TextContent); ok {
					text += tc.Text
				}
			}
			t.Fatalf("%s returned error: %s", name, text)
		}
		return res
	}

	// Happy path across the fixture: read, search, tasks, curator, write.
	call("read_note", map[string]any{"path": "inbox/welcome.md"})
	res := call("search_notes", map[string]any{"query": "SEARCH-NEEDLE-ALPHA"})
	raw, _ := json.Marshal(res.StructuredContent)
	if !strings.Contains(string(raw), "task-in-progress.md") {
		t.Errorf("search over HTTP = %s", raw)
	}
	call("list_tasks", map[string]any{"status": "in-progress"})
	call("get_backlinks", map[string]any{"path": "projects/demo/deep-dive.md"})
	call("find_untagged", nil)
	call("write_note", map[string]any{"content": "# HTTP Test Note\n\nfrom integration test\n"})
	call("set_status", map[string]any{"path": "projects/demo/task-backlog.md", "status": "in-progress"})
	call("delete_note", map[string]any{"path": "inbox/http-test-note.md"})

	// Traversal attempts fail as tool errors, not transport errors.
	for _, path := range []string{"../../etc/passwd.md", "/etc/passwd.md", "a\x00.md"} {
		res, err := session.CallTool(ctx, &mcp.CallToolParams{
			Name: "read_note", Arguments: map[string]any{"path": path},
		})
		if err != nil {
			t.Fatalf("traversal call transport error: %v", err)
		}
		if !res.IsError {
			t.Errorf("read_note(%q) must fail", path)
		}
	}
}
