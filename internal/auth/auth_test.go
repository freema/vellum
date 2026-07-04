package auth

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

const testSecret = "0123456789abcdef0123456789abcdef" // 32 chars

func newTestProvider(t *testing.T, mutate ...func(*Config)) *Provider {
	t.Helper()
	cfg := Config{
		Enabled:      true,
		ClientID:     "vellum",
		ClientSecret: testSecret,
		IssuerURL:    "https://vellum.example.com",
	}
	for _, m := range mutate {
		m(&cfg)
	}
	p, err := NewProvider(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

func newAuthServer(t *testing.T, p *Provider) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	p.Routes(mux)
	mux.Handle("/mcp", p.RequireBearer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("mcp ok"))
	})))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestProviderValidation(t *testing.T) {
	base := Config{Enabled: true, ClientID: "vellum", ClientSecret: testSecret, IssuerURL: "https://x.example"}

	short := base
	short.ClientSecret = "too-short"
	if _, err := NewProvider(short); err == nil {
		t.Error("short secret must be rejected")
	}
	badID := base
	badID.ClientID = "a b!"
	if _, err := NewProvider(badID); err == nil {
		t.Error("invalid client id must be rejected")
	}
	if _, err := NewProvider(Config{}); err == nil {
		t.Error("disabled config must error")
	}
}

func TestASMetadata(t *testing.T) {
	srv := newAuthServer(t, newTestProvider(t))
	resp, err := http.Get(srv.URL + "/.well-known/oauth-authorization-server")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var meta map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		t.Fatal(err)
	}
	if meta["issuer"] != "https://vellum.example.com" {
		t.Errorf("issuer = %v", meta["issuer"])
	}
	if meta["token_endpoint"] != "https://vellum.example.com/token" {
		t.Errorf("token_endpoint = %v", meta["token_endpoint"])
	}
	if meta["registration_endpoint"] != "https://vellum.example.com/register" {
		t.Errorf("registration_endpoint = %v (DCR must be advertised for MCP clients)", meta["registration_endpoint"])
	}
	if fmtSlice(meta["code_challenge_methods_supported"]) != "S256" {
		t.Errorf("challenge methods = %v", meta["code_challenge_methods_supported"])
	}
}

func TestPRMetadata(t *testing.T) {
	srv := newAuthServer(t, newTestProvider(t))
	for _, path := range []string{"/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp"} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		var meta map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&meta)
		resp.Body.Close()
		if meta["resource"] != "https://vellum.example.com/mcp" {
			t.Errorf("%s: resource = %v", path, meta["resource"])
		}
		if fmtSlice(meta["authorization_servers"]) != "https://vellum.example.com" {
			t.Errorf("%s: authorization_servers = %v", path, meta["authorization_servers"])
		}
	}
}

func fmtSlice(v any) string {
	items, _ := v.([]any)
	var out []string
	for _, i := range items {
		out = append(out, i.(string))
	}
	return strings.Join(out, ",")
}

// pkce returns a verifier and its S256 challenge.
func pkce() (verifier, challenge string) {
	verifier = "test-verifier-string-with-enough-entropy-123456"
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}

// authorizeAndGetCode walks the consent flow for the confidential "vellum"
// client and returns the authorization code.
func authorizeAndGetCode(t *testing.T, srv *httptest.Server, challenge string) string {
	t.Helper()
	return authorizeAndGetCodeFor(t, srv, "vellum", "https://claude.ai/api/mcp/auth_callback", challenge)
}

// authorizeAndGetCodeFor walks GET /authorize (consent) + POST approve for an
// arbitrary client_id/redirect_uri and extracts the code from the redirect.
func authorizeAndGetCodeFor(t *testing.T, srv *httptest.Server, clientID, redirectURI, challenge string) string {
	t.Helper()
	authURL := srv.URL + "/authorize?response_type=code&client_id=" + url.QueryEscape(clientID) +
		"&redirect_uri=" + url.QueryEscape(redirectURI) +
		"&code_challenge=" + challenge + "&code_challenge_method=S256&state=xyz"

	resp, err := http.Get(authURL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /authorize = %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "Authorize <em>Claude</em> to connect") {
		t.Fatal("consent page missing title")
	}
	if !strings.Contains(string(body), "delete_note") {
		t.Fatal("consent page missing tool list")
	}

	form := url.Values{
		"decision": {"approve"}, "client_id": {clientID},
		"redirect_uri":  {redirectURI},
		"state":         {"xyz"},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"},
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp2, err := client.PostForm(srv.URL+"/authorize", form)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusFound {
		t.Fatalf("POST /authorize = %d", resp2.StatusCode)
	}
	loc, err := url.Parse(resp2.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if loc.Query().Get("state") != "xyz" {
		t.Errorf("state not echoed: %s", loc)
	}
	code := loc.Query().Get("code")
	if code == "" {
		t.Fatalf("no code in redirect: %s", loc)
	}
	return code
}

func tokenRequest(t *testing.T, srv *httptest.Server, form url.Values) (map[string]any, int) {
	t.Helper()
	resp, err := http.PostForm(srv.URL+"/token", form)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return body, resp.StatusCode
}

// TestDynamicClientRegistration exercises the full flow an MCP client (the
// Inspector, claude.ai, …) uses: register via DCR, authorize with the dynamic
// client_id, then exchange the code with PKCE and NO client secret.
func TestDynamicClientRegistration(t *testing.T) {
	srv := newAuthServer(t, newTestProvider(t))
	redirect := "http://localhost:6274/oauth/callback"

	// 1) register a public client (RFC 7591).
	regBody, _ := json.Marshal(map[string]any{
		"redirect_uris":              []string{redirect},
		"token_endpoint_auth_method": "none",
		"client_name":                "MCP Inspector",
	})
	resp, err := http.Post(srv.URL+"/register", "application/json", bytes.NewReader(regBody))
	if err != nil {
		t.Fatal(err)
	}
	var reg map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&reg)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /register = %d", resp.StatusCode)
	}
	clientID, _ := reg["client_id"].(string)
	if !strings.HasPrefix(clientID, "mcp-") {
		t.Fatalf("client_id = %q, want mcp-* prefix", clientID)
	}
	if reg["token_endpoint_auth_method"] != "none" {
		t.Errorf("auth method = %v, want none", reg["token_endpoint_auth_method"])
	}

	// 2) authorize with the dynamic client + its loopback redirect.
	verifier, challenge := pkce()
	code := authorizeAndGetCodeFor(t, srv, clientID, redirect, challenge)

	// 3) exchange the code with NO client secret — PKCE is the gate.
	body, status := tokenRequest(t, srv, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"code_verifier": {verifier},
		"client_id":     {clientID},
		"redirect_uri":  {redirect},
	})
	if status != http.StatusOK {
		t.Fatalf("public token exchange = %d: %v", status, body)
	}
	access, _ := body["access_token"].(string)
	if access == "" {
		t.Fatal("no access token for public client")
	}

	// 4) the bearer token works on /mcp.
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+access)
	r2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	r2.Body.Close()
	if r2.StatusCode != http.StatusOK {
		t.Errorf("authed /mcp = %d, want 200", r2.StatusCode)
	}
}

// TestAuthorizeRejectsUnregisteredClient ensures a client_id that never
// registered cannot start the flow.
func TestAuthorizeRejectsUnregisteredClient(t *testing.T) {
	srv := newAuthServer(t, newTestProvider(t))
	_, challenge := pkce()
	u := srv.URL + "/authorize?response_type=code&client_id=mcp-neverregistered&redirect_uri=" +
		url.QueryEscape("http://localhost:6274/cb") +
		"&code_challenge=" + challenge + "&code_challenge_method=S256&state=x"
	resp, err := http.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("unregistered authorize = %d, want 400", resp.StatusCode)
	}
}

func TestFullOAuthFlow(t *testing.T) {
	srv := newAuthServer(t, newTestProvider(t))
	verifier, challenge := pkce()
	code := authorizeAndGetCode(t, srv, challenge)

	// Exchange the code.
	body, status := tokenRequest(t, srv, url.Values{
		"grant_type": {"authorization_code"}, "code": {code},
		"code_verifier": {verifier},
		"client_id":     {"vellum"}, "client_secret": {testSecret},
		"redirect_uri": {"https://claude.ai/api/mcp/auth_callback"},
	})
	if status != http.StatusOK {
		t.Fatalf("token = %d: %v", status, body)
	}
	if body["token_type"] != "bearer" || body["expires_in"] != float64(3600) {
		t.Errorf("token response = %v", body)
	}
	access := body["access_token"].(string)
	refresh := body["refresh_token"].(string)

	// Bearer works on /mcp.
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+access)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("authed /mcp = %d", resp.StatusCode)
	}

	// Code is single-use.
	if _, status := tokenRequest(t, srv, url.Values{
		"grant_type": {"authorization_code"}, "code": {code},
		"code_verifier": {verifier},
		"client_id":     {"vellum"}, "client_secret": {testSecret},
	}); status != http.StatusBadRequest {
		t.Errorf("code reuse = %d, want 400", status)
	}

	// Refresh rotates.
	body2, status := tokenRequest(t, srv, url.Values{
		"grant_type": {"refresh_token"}, "refresh_token": {refresh},
		"client_id": {"vellum"}, "client_secret": {testSecret},
	})
	if status != http.StatusOK {
		t.Fatalf("refresh = %d: %v", status, body2)
	}
	if _, status := tokenRequest(t, srv, url.Values{
		"grant_type": {"refresh_token"}, "refresh_token": {refresh},
		"client_id": {"vellum"}, "client_secret": {testSecret},
	}); status != http.StatusBadRequest {
		t.Errorf("rotated refresh reuse = %d, want 400", status)
	}

	// Revoke the new access token; /mcp goes back to 401.
	access2 := body2["access_token"].(string)
	resp, err = http.PostForm(srv.URL+"/revoke", url.Values{
		"token": {access2}, "client_id": {"vellum"}, "client_secret": {testSecret},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	req2, _ := http.NewRequest(http.MethodPost, srv.URL+"/mcp", nil)
	req2.Header.Set("Authorization", "Bearer "+access2)
	resp2, _ := http.DefaultClient.Do(req2)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Errorf("revoked token = %d, want 401", resp2.StatusCode)
	}
}

func TestClientCredentialsGrant(t *testing.T) {
	srv := newAuthServer(t, newTestProvider(t))

	body, status := tokenRequest(t, srv, url.Values{
		"grant_type": {"client_credentials"},
		"client_id":  {"vellum"}, "client_secret": {testSecret},
	})
	if status != http.StatusOK {
		t.Fatalf("client_credentials = %d: %v", status, body)
	}
	access := body["access_token"].(string)

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+access)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("bearer from client_credentials = %d", resp.StatusCode)
	}

	// Wrong secret still 401s.
	if _, status := tokenRequest(t, srv, url.Values{
		"grant_type": {"client_credentials"},
		"client_id":  {"vellum"}, "client_secret": {"wrong-secret-wrong-secret-wrong!"},
	}); status != http.StatusUnauthorized {
		t.Errorf("bad secret = %d, want 401", status)
	}
}

func TestTokenRejectsBadClientAndPKCE(t *testing.T) {
	srv := newAuthServer(t, newTestProvider(t))
	verifier, challenge := pkce()

	// Wrong secret -> 401 invalid_client.
	code := authorizeAndGetCode(t, srv, challenge)
	body, status := tokenRequest(t, srv, url.Values{
		"grant_type": {"authorization_code"}, "code": {code},
		"code_verifier": {verifier},
		"client_id":     {"vellum"}, "client_secret": {"wrong-secret-wrong-secret-wrong!"},
	})
	if status != http.StatusUnauthorized || body["error"] != "invalid_client" {
		t.Errorf("bad secret = %d %v", status, body)
	}

	// Bad verifier -> invalid_grant (and the code is burned).
	code2 := authorizeAndGetCode(t, srv, challenge)
	body, status = tokenRequest(t, srv, url.Values{
		"grant_type": {"authorization_code"}, "code": {code2},
		"code_verifier": {"not-the-right-verifier"},
		"client_id":     {"vellum"}, "client_secret": {testSecret},
	})
	if status != http.StatusBadRequest || body["error"] != "invalid_grant" {
		t.Errorf("bad verifier = %d %v", status, body)
	}
}

func TestAuthorizeValidation(t *testing.T) {
	srv := newAuthServer(t, newTestProvider(t))

	get := func(query string) int {
		resp, err := http.Get(srv.URL + "/authorize?" + query)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	_, challenge := pkce()
	base := "response_type=code&client_id=vellum&redirect_uri=https%3A%2F%2Fx.example%2Fcb&code_challenge=" + challenge

	if got := get(strings.Replace(base, "client_id=vellum", "client_id=evil", 1)); got != http.StatusBadRequest {
		t.Errorf("unknown client = %d", got)
	}
	if got := get(strings.Replace(base, "response_type=code", "response_type=token", 1)); got != http.StatusBadRequest {
		t.Errorf("wrong response_type = %d", got)
	}
	if got := get("response_type=code&client_id=vellum&code_challenge=" + challenge); got != http.StatusBadRequest {
		t.Errorf("missing redirect_uri = %d", got)
	}
	if got := get("response_type=code&client_id=vellum&redirect_uri=https%3A%2F%2Fx.example%2Fcb"); got != http.StatusBadRequest {
		t.Errorf("missing PKCE challenge = %d", got)
	}
	if got := get(base + "&code_challenge_method=plain"); got != http.StatusBadRequest {
		t.Errorf("plain PKCE = %d", got)
	}
	if got := get(base); got != http.StatusOK {
		t.Errorf("valid request = %d, want consent page", got)
	}
}

func TestRedirectURIAllowlist(t *testing.T) {
	p := newTestProvider(t, func(c *Config) {
		c.RedirectURIs = []string{"https://claude.ai/api/mcp/auth_callback", "http://localhost/callback"}
	})
	srv := newAuthServer(t, p)
	_, challenge := pkce()

	get := func(uri string) int {
		resp, err := http.Get(srv.URL + "/authorize?response_type=code&client_id=vellum&code_challenge=" +
			challenge + "&redirect_uri=" + url.QueryEscape(uri))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if got := get("https://claude.ai/api/mcp/auth_callback"); got != http.StatusOK {
		t.Errorf("allowlisted = %d", got)
	}
	if got := get("https://evil.example/cb"); got != http.StatusBadRequest {
		t.Errorf("unlisted = %d", got)
	}
	// Loopback matches on any port (RFC 8252).
	if got := get("http://localhost:53123/callback"); got != http.StatusOK {
		t.Errorf("loopback any-port = %d", got)
	}
}

func TestDenyRedirectsWithError(t *testing.T) {
	srv := newAuthServer(t, newTestProvider(t))
	_, challenge := pkce()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.PostForm(srv.URL+"/authorize", url.Values{
		"decision": {"deny"}, "client_id": {"vellum"},
		"redirect_uri":   {"https://x.example/cb"},
		"code_challenge": {challenge}, "state": {"s1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	loc, _ := url.Parse(resp.Header.Get("Location"))
	if loc.Query().Get("error") != "access_denied" || loc.Query().Get("state") != "s1" {
		t.Errorf("deny redirect = %s", loc)
	}
}

func TestBearer401Challenge(t *testing.T) {
	srv := newAuthServer(t, newTestProvider(t))
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/mcp", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no token = %d, want 401", resp.StatusCode)
	}
	www := resp.Header.Get("WWW-Authenticate")
	if !strings.Contains(www, `Bearer error="invalid_token"`) ||
		!strings.Contains(www, "oauth-protected-resource/mcp") {
		t.Errorf("WWW-Authenticate = %q", www)
	}
}

func TestCORS(t *testing.T) {
	handler := CORS([]string{"https://claude.ai"}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	srv := httptest.NewServer(handler)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodOptions, srv.URL, nil)
	req.Header.Set("Origin", "https://claude.ai")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("preflight = %d, want 204", resp.StatusCode)
	}
	if resp.Header.Get("Access-Control-Allow-Origin") != "https://claude.ai" {
		t.Errorf("ACAO = %q", resp.Header.Get("Access-Control-Allow-Origin"))
	}

	req2, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	req2.Header.Set("Origin", "https://evil.example")
	resp2, _ := http.DefaultClient.Do(req2)
	resp2.Body.Close()
	if resp2.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Error("disallowed origin must not get CORS headers")
	}
}
