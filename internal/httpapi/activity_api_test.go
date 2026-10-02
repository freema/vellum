package httpapi

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/freema/vellum/internal/activity"
	"github.com/freema/vellum/internal/auth"
	"github.com/freema/vellum/internal/vault"
)

func newActivityServer(t *testing.T) (*httptest.Server, *activity.Recorder) {
	t.Helper()
	v, err := vault.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ix := vault.NewIndex(v)
	if err := ix.Build(); err != nil {
		t.Fatal(err)
	}
	rec := activity.New()
	srv := httptest.NewServer(NewRouter("test", Options{
		API: &API{
			Vault:     v,
			Index:     ix,
			Searcher:  vault.NewScanSearcher(v, ix),
			Structure: vault.DefaultStructure(),
			Activity:  rec,
			Endpoint:  "https://vellum.example/mcp",
			Curator:   true,
		},
		Activity: rec,
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

func TestFoldersCreateAndList(t *testing.T) {
	srv, _ := newActivityServer(t)

	resp, _ := doReq(t, http.MethodPost, srv.URL+"/api/folders", `{"path":"projects/newthing"}`, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create folder status = %d", resp.StatusCode)
	}
	resp, body := doReq(t, http.MethodGet, srv.URL+"/api/folders", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list folders status = %d", resp.StatusCode)
	}
	var got struct {
		Folders []string `json:"folders"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, d := range got.Folders {
		if d == "projects/newthing" {
			found = true
		}
	}
	if !found {
		t.Fatalf("created folder not listed: %v", got.Folders)
	}
}

func TestFolderDelete(t *testing.T) {
	srv, _ := newActivityServer(t)
	doReq(t, http.MethodPost, srv.URL+"/api/folders", `{"path":"projects/gone"}`, nil)
	doReq(t, http.MethodPut, srv.URL+"/api/notes/projects/gone/x.md", "# x",
		map[string]string{"Content-Type": "text/markdown"})

	resp, body := doReq(t, http.MethodDelete, srv.URL+"/api/folders/projects/gone", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete folder = %d: %s", resp.StatusCode, body)
	}
	var got struct {
		Deleted string `json:"deleted"`
		Notes   int    `json:"notes"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got.Deleted != "projects/gone" || got.Notes != 1 {
		t.Fatalf("delete payload = %s", body)
	}
	_, lb := doReq(t, http.MethodGet, srv.URL+"/api/folders", "", nil)
	if strings.Contains(string(lb), "projects/gone") {
		t.Errorf("deleted folder still listed: %s", lb)
	}
}

func TestConnectionsAndActivity(t *testing.T) {
	srv, rec := newActivityServer(t)
	rec.Touch("sk-abc", "", "Claude Code", "CLI", "write_note")
	rec.Record(activity.Event{Source: "mcp", Actor: "Claude Code", Kind: "write", Target: "a.md", Detail: "write_note"})

	resp, body := doReq(t, http.MethodGet, srv.URL+"/api/connections", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("connections status = %d", resp.StatusCode)
	}
	var conns struct {
		Endpoint    string `json:"endpoint"`
		ActiveCount int    `json:"activeCount"`
		Connections []struct {
			ID   string `json:"id"`
			Mono string `json:"mono"`
		} `json:"connections"`
	}
	if err := json.Unmarshal(body, &conns); err != nil {
		t.Fatal(err)
	}
	if conns.Endpoint != "https://vellum.example/mcp" || conns.ActiveCount != 1 || len(conns.Connections) != 1 {
		t.Fatalf("unexpected connections payload: %s", body)
	}
	if conns.Connections[0].Mono != "CC" {
		t.Errorf("monogram = %q, want CC", conns.Connections[0].Mono)
	}

	resp, body = doReq(t, http.MethodGet, srv.URL+"/api/activity?filter=mcp", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("activity status = %d", resp.StatusCode)
	}
	var act struct {
		Events []struct {
			Verb string `json:"verb"`
		} `json:"events"`
	}
	if err := json.Unmarshal(body, &act); err != nil {
		t.Fatal(err)
	}
	if len(act.Events) != 1 || act.Events[0].Verb != "wrote" {
		t.Fatalf("unexpected activity payload: %s", body)
	}

	// Without auth there is no token behind a connection, so Revoke must
	// not pretend it cut anything off.
	resp, body = doReq(t, http.MethodDelete, srv.URL+"/api/connections/sk-abc", "", nil)
	if resp.StatusCode != http.StatusConflict || !strings.Contains(string(body), "Auth is off") {
		t.Fatalf("revoke without auth = %d %s, want 409", resp.StatusCode, body)
	}
	if len(rec.Sessions()) != 1 {
		t.Error("a refused revoke dropped the session")
	}
	if resp, _ = doReq(t, http.MethodDelete, srv.URL+"/api/connections/sk-nope", "", nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("revoke of unknown session = %d, want 404", resp.StatusCode)
	}
}

// TestRevokeCutsTheClientOff is the regression test for a Revoke that only
// hid the session: the client's token kept working and its next call put the
// session back in the panel.
func TestRevokeCutsTheClientOff(t *testing.T) {
	const secret = "0123456789abcdef0123456789abcdef"
	provider, err := auth.NewProvider(auth.Config{
		Enabled: true, ClientID: "vellum", ClientSecret: secret, IssuerURL: "https://vellum.example",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(provider.Close)
	v, err := vault.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ix := vault.NewIndex(v)
	if err := ix.Build(); err != nil {
		t.Fatal(err)
	}
	rec := activity.New()
	srv := httptest.NewServer(NewRouter("test", Options{
		MCPHandler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }),
		API: &API{
			Vault: v, Index: ix, Searcher: vault.NewScanSearcher(v, ix), Structure: vault.DefaultStructure(),
			Activity: rec, Revoker: provider,
		},
		Auth:     provider,
		Activity: rec,
	}))
	t.Cleanup(srv.Close)

	token := func(form url.Values) (access, refresh string, status int) {
		t.Helper()
		resp, err := http.PostForm(srv.URL+"/token", form)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var b struct {
			Access  string `json:"access_token"`
			Refresh string `json:"refresh_token"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&b)
		return b.Access, b.Refresh, resp.StatusCode
	}
	callTool := func(access string) int {
		t.Helper()
		resp, _ := doReq(t, http.MethodPost, srv.URL+"/mcp",
			`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"read_note","arguments":{"path":"a.md"}}}`,
			map[string]string{"Authorization": "Bearer " + access, "User-Agent": "claude-code/2.0"})
		return resp.StatusCode
	}
	type connection struct {
		ID        string `json:"id"`
		Revocable bool   `json:"revocable"`
	}
	connections := func(owner string) []connection {
		t.Helper()
		resp, body := doReq(t, http.MethodGet, srv.URL+"/api/connections", "", map[string]string{"Authorization": "Bearer " + owner})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /api/connections = %d", resp.StatusCode)
		}
		var out struct {
			Connections []connection `json:"connections"`
		}
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatal(err)
		}
		return out.Connections
	}

	// The owner signs in to the web UI with the client secret.
	owner, _, status := token(url.Values{"grant_type": {"client_credentials"}, "client_id": {"vellum"}, "client_secret": {secret}})
	if status != http.StatusOK {
		t.Fatalf("client_credentials = %d", status)
	}

	// An MCP client registers, the owner approves it, it gets a token pair.
	redirect := "http://localhost:6274/callback"
	reg, _ := json.Marshal(map[string]any{"redirect_uris": []string{redirect}, "token_endpoint_auth_method": "none"})
	resp, body := doReq(t, http.MethodPost, srv.URL+"/register", string(reg), map[string]string{"Content-Type": "application/json"})
	var client struct {
		ID string `json:"client_id"`
	}
	if err := json.Unmarshal(body, &client); err != nil || client.ID == "" {
		t.Fatalf("POST /register = %d %s", resp.StatusCode, body)
	}
	verifier := "test-verifier-string-with-enough-entropy-123456"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	approve, err := noFollow.PostForm(srv.URL+"/authorize", url.Values{
		"decision": {"approve"}, "client_id": {client.ID}, "redirect_uri": {redirect}, "state": {"s"},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"}, "secret": {secret},
	})
	if err != nil {
		t.Fatal(err)
	}
	approve.Body.Close()
	loc, _ := url.Parse(approve.Header.Get("Location"))
	code := loc.Query().Get("code")
	if code == "" {
		t.Fatalf("approve = %d, Location %q", approve.StatusCode, approve.Header.Get("Location"))
	}
	access, refresh, status := token(url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "code_verifier": {verifier},
		"client_id": {client.ID}, "redirect_uri": {redirect},
	})
	if status != http.StatusOK {
		t.Fatalf("code exchange = %d", status)
	}

	if got := callTool(access); got != http.StatusOK {
		t.Fatalf("tool call before revoke = %d", got)
	}
	conns := connections(owner)
	if len(conns) != 1 || !conns[0].Revocable {
		t.Fatalf("connections before revoke = %+v, want one revocable", conns)
	}

	resp, body = doReq(t, http.MethodDelete, srv.URL+"/api/connections/"+conns[0].ID, "", map[string]string{"Authorization": "Bearer " + owner})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("revoke = %d %s", resp.StatusCode, body)
	}
	if got := callTool(access); got != http.StatusUnauthorized {
		t.Errorf("tool call with the revoked token = %d, want 401", got)
	}
	if _, _, status := token(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {client.ID}}); status != http.StatusBadRequest {
		t.Errorf("refresh with the revoked token = %d, want 400", status)
	}
	if conns := connections(owner); len(conns) != 0 {
		t.Errorf("connections after revoke = %+v, want none", conns)
	}

	// The web UI's own token is shown when used on /mcp, but revoking it
	// would not disconnect a holder of the client secret.
	if got := callTool(owner); got != http.StatusOK {
		t.Fatalf("tool call with the owner token = %d", got)
	}
	conns = connections(owner)
	if len(conns) != 1 || conns[0].Revocable {
		t.Fatalf("owner connection = %+v, want one that is not revocable", conns)
	}
	resp, body = doReq(t, http.MethodDelete, srv.URL+"/api/connections/"+conns[0].ID, "", map[string]string{"Authorization": "Bearer " + owner})
	if resp.StatusCode != http.StatusConflict || !strings.Contains(string(body), "VELLUM_CLIENT_SECRET") {
		t.Errorf("revoke of the secret client = %d %s, want 409", resp.StatusCode, body)
	}
}

func TestRecoverRecordsErrorInActivity(t *testing.T) {
	v, err := vault.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ix := vault.NewIndex(v)
	if err := ix.Build(); err != nil {
		t.Fatal(err)
	}
	rec := activity.New()
	panicky := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom in tool") })
	srv := httptest.NewServer(NewRouter("test", Options{
		MCPHandler: panicky,
		API: &API{
			Vault:     v,
			Index:     ix,
			Searcher:  vault.NewScanSearcher(v, ix),
			Structure: vault.DefaultStructure(),
			Activity:  rec,
		},
		Activity: rec,
	}))
	t.Cleanup(srv.Close)

	// A panic in the handler is recovered as a 500…
	resp, err := http.Post(srv.URL+"/mcp", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("panic route = %d, want 500", resp.StatusCode)
	}

	// …and surfaces in the Activity feed under the errors filter.
	_, body := doReq(t, http.MethodGet, srv.URL+"/api/activity?filter=errors", "", nil)
	var got struct {
		Events []struct {
			IsError bool   `json:"isError"`
			Tool    string `json:"tool"`
			Level   string `json:"level"`
		} `json:"events"`
		ErrorCount int `json:"errorCount"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got.ErrorCount != 1 || len(got.Events) != 1 || !got.Events[0].IsError {
		t.Fatalf("errors payload = %s", body)
	}
	if got.Events[0].Tool != "/mcp" || got.Events[0].Level != "error" {
		t.Errorf("error event = %+v", got.Events[0])
	}
}

func TestCuratorRun(t *testing.T) {
	srv, _ := newActivityServer(t)
	resp, body := doReq(t, http.MethodPost, srv.URL+"/api/curator/run", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("curator run status = %d", resp.StatusCode)
	}
	var got struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if !got.Enabled {
		t.Fatal("curator should be enabled in test server")
	}
}
