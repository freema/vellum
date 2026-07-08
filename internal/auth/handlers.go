package auth

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Routes registers the OAuth endpoints on mux. Call only when auth is
// enabled — without auth none of these routes exist (matching openclaw-mcp).
func (p *Provider) Routes(mux *http.ServeMux) {
	limited := p.rateLimit
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", p.handleASMetadata)
	mux.HandleFunc("GET /.well-known/oauth-protected-resource", p.handlePRMetadata)
	mux.HandleFunc("GET /.well-known/oauth-protected-resource/{rest...}", p.handlePRMetadata)
	mux.Handle("POST /register", limited(http.HandlerFunc(p.handleRegister)))
	mux.Handle("GET /authorize", limited(http.HandlerFunc(p.handleAuthorizeGet)))
	mux.Handle("POST /authorize", limited(http.HandlerFunc(p.handleAuthorizePost)))
	mux.Handle("POST /token", limited(http.HandlerFunc(p.handleToken)))
	mux.Handle("POST /revoke", limited(http.HandlerFunc(p.handleRevoke)))
}

// handleASMetadata serves RFC 8414 authorization server metadata. Every URL
// derives from the issuer — which is why VELLUM_ISSUER_URL must be set
// behind a reverse proxy (otherwise clients are pointed at localhost).
func (p *Provider) handleASMetadata(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                     p.issuer,
		"authorization_endpoint":                     p.issuer + "/authorize",
		"token_endpoint":                             p.issuer + "/token",
		"revocation_endpoint":                        p.issuer + "/revoke",
		"registration_endpoint":                      p.issuer + "/register",
		"scopes_supported":                           Scopes,
		"response_types_supported":                   []string{"code"},
		"grant_types_supported":                      []string{"authorization_code", "refresh_token", "client_credentials"},
		"token_endpoint_auth_methods_supported":      []string{"none", "client_secret_post", "client_secret_basic"},
		"revocation_endpoint_auth_methods_supported": []string{"none", "client_secret_post", "client_secret_basic"},
		"code_challenge_methods_supported":           []string{"S256"},
	})
}

// handleRegister implements OAuth 2.0 Dynamic Client Registration (RFC 7591).
// MCP clients (Inspector, claude.ai, Cursor, …) call this to obtain a
// client_id before the authorization-code flow. Registration is open and
// anonymous — it only issues an identifier; the human consent at /authorize is
// the actual gate. Clients are registered as public (PKCE, no secret).
func (p *Provider) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RedirectURIs            []string `json:"redirect_uris"`
		TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
		ClientName              string   `json:"client_name"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req)
	}
	// vellum does not mint per-client secrets — every dynamic client is public
	// and authenticates the token exchange with PKCE.
	reg := p.registerClient(req.RedirectURIs, true)
	writeJSON(w, http.StatusCreated, map[string]any{
		"client_id":                  reg.id,
		"client_id_issued_at":        time.Now().Unix(),
		"redirect_uris":              reg.redirectURIs,
		"token_endpoint_auth_method": "none",
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
	})
}

// handlePRMetadata serves RFC 9728 protected resource metadata.
func (p *Provider) handlePRMetadata(w http.ResponseWriter, r *http.Request) {
	resource := p.issuer + "/mcp"
	writeJSON(w, http.StatusOK, map[string]any{
		"resource":              resource,
		"authorization_servers": []string{p.issuer},
		"scopes_supported":      Scopes,
	})
}

// authorizeParams are the query/form fields carried through the consent page.
type authorizeParams struct {
	clientID            string
	redirectURI         string
	state               string
	scope               string
	codeChallenge       string
	codeChallengeMethod string
	resource            string
}

func readAuthorizeParams(get func(string) string) authorizeParams {
	return authorizeParams{
		clientID:            get("client_id"),
		redirectURI:         get("redirect_uri"),
		state:               get("state"),
		scope:               get("scope"),
		codeChallenge:       get("code_challenge"),
		codeChallengeMethod: get("code_challenge_method"),
		resource:            get("resource"),
	}
}

// validate returns an oauth error code ("" when valid). Invalid client or
// redirect URI must never redirect — the caller renders a 400 instead.
func (p *Provider) validate(params authorizeParams, responseType string) string {
	if responseType != "code" {
		return "unsupported_response_type"
	}
	reg, ok := p.lookupClient(params.clientID)
	if !ok {
		return "invalid_client"
	}
	if !p.redirectAllowedFor(reg, params.redirectURI) {
		return "invalid_request"
	}
	if params.codeChallenge == "" {
		return "invalid_request" // PKCE is mandatory
	}
	if params.codeChallengeMethod != "" && params.codeChallengeMethod != "S256" {
		return "invalid_request" // S256 only, no plain
	}
	return ""
}

// handleAuthorizeGet validates the request and renders the consent screen
// (design artboard 1b). The client secret is NOT entered here — it is
// verified at /token; the consent screen is the human checkpoint.
func (p *Provider) handleAuthorizeGet(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	params := readAuthorizeParams(q.Get)
	if errCode := p.validate(params, q.Get("response_type")); errCode != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": errCode})
		return
	}
	p.renderConsent(w, params)
}

// handleAuthorizePost processes the consent decision. Deny redirects with
// error=access_denied; approve issues a single-use code. There is no cookie
// session, so there is nothing for CSRF to ride on — security rests on
// PKCE + the client secret at /token, exactly like openclaw's auto-approve.
func (p *Provider) handleAuthorizePost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	params := readAuthorizeParams(r.PostForm.Get)
	if errCode := p.validate(params, "code"); errCode != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": errCode})
		return
	}

	redirect, _ := url.Parse(params.redirectURI)
	qs := redirect.Query()
	if r.PostForm.Get("decision") != "approve" {
		qs.Set("error", "access_denied")
	} else {
		var scopes []string
		if params.scope != "" {
			scopes = strings.Fields(params.scope)
		}
		code := p.issueCode(params.clientID, params.redirectURI, params.codeChallenge, params.resource, scopes)
		qs.Set("code", code)
	}
	if params.state != "" {
		qs.Set("state", params.state)
	}
	redirect.RawQuery = qs.Encode()
	http.Redirect(w, r, redirect.String(), http.StatusFound)
}

// clientCredentials pulls client_id/client_secret from the form body
// (client_secret_post) or the Authorization header (client_secret_basic).
func clientCredentials(r *http.Request) (id, secret string) {
	id, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	if id == "" && secret == "" {
		if bid, bsecret, ok := r.BasicAuth(); ok {
			id, secret = bid, bsecret
		}
	}
	return id, secret
}

func (p *Provider) authenticateClient(w http.ResponseWriter, r *http.Request) bool {
	id, secret := clientCredentials(r)
	if id != p.cfg.ClientID || !p.secretEquals(secret) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_client"})
		return false
	}
	return true
}

// authenticateTokenClient authenticates the client for the authorization_code
// and refresh_token grants. Public (dynamically-registered) clients present no
// secret — PKCE and the code/refresh→client binding are the gate. Confidential
// clients must present the shared secret.
func (p *Provider) authenticateTokenClient(w http.ResponseWriter, r *http.Request, clientID string) bool {
	reg, ok := p.lookupClient(clientID)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_client"})
		return false
	}
	if reg.public {
		return true
	}
	_, secret := clientCredentials(r)
	if !p.secretEqualsFor(reg, secret) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_client"})
		return false
	}
	return true
}

// handleToken implements the token endpoint: authorization_code (with PKCE
// verification) and refresh_token (with rotation). The client secret check
// here is the actual authorization gate.
func (p *Provider) handleToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}

	var access, refresh string
	var scopes []string
	var err error
	switch r.PostForm.Get("grant_type") {
	case "client_credentials":
		// Direct secret-for-token exchange — the embedded SPA's connect screen
		// (design artboard 1a). The confidential client secret is the gate.
		if !p.authenticateClient(w, r) {
			return
		}
		var requested []string
		if s := r.PostForm.Get("scope"); s != "" {
			requested = strings.Fields(s)
		}
		if len(requested) == 0 {
			requested = Scopes
		}
		access, refresh, scopes, err = p.grantDirect(p.cfg.ClientID, requested)
	case "authorization_code":
		// MCP clients (public, PKCE) and the confidential client both use this.
		clientID := r.PostForm.Get("client_id")
		if !p.authenticateTokenClient(w, r, clientID) {
			return
		}
		access, refresh, scopes, err = p.exchangeCode(
			clientID,
			r.PostForm.Get("code"),
			r.PostForm.Get("code_verifier"),
			r.PostForm.Get("redirect_uri"),
		)
	case "refresh_token":
		clientID := r.PostForm.Get("client_id")
		// A valid, unexpired refresh token — rotated on every use and bound to
		// its client_id by exchangeRefresh — is itself sufficient proof for this
		// grant. The embedded SPA keeps no client secret after the initial
		// client_credentials login, so it (a confidential client) refreshes on
		// the refresh token alone, exactly as the public MCP clients already do.
		// The secret still gates minting the first token pair, so possession of a
		// refresh token already implies a prior successful secret auth.
		if _, ok := p.lookupClient(clientID); !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_client"})
			return
		}
		var requested []string
		if s := r.PostForm.Get("scope"); s != "" {
			requested = strings.Fields(s)
		}
		access, refresh, scopes, err = p.exchangeRefresh(clientID, r.PostForm.Get("refresh_token"), requested)
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unsupported_grant_type"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid_grant", "error_description": err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":  access,
		"token_type":    "bearer",
		"expires_in":    int(AccessTokenTTL / time.Second),
		"refresh_token": refresh,
		"scope":         strings.Join(scopes, " "),
	})
}

// handleRevoke implements RFC 7009. Unknown tokens are a success.
func (p *Provider) handleRevoke(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	// Revoking only affects a token the caller already holds. The confidential
	// client must still present its secret; public (DCR) clients may not.
	if r.PostForm.Get("client_id") == p.cfg.ClientID {
		if !p.authenticateClient(w, r) {
			return
		}
	}
	p.revokeToken(r.PostForm.Get("token"))
	writeJSON(w, http.StatusOK, map[string]string{})
}

// RequireBearer guards a handler with bearer token verification. 401s carry
// a WWW-Authenticate challenge including the resource_metadata hint so MCP
// clients can discover the authorization server.
func (p *Provider) RequireBearer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if ok {
			if _, err := p.VerifyAccessToken(strings.TrimSpace(token)); err == nil {
				next.ServeHTTP(w, r)
				return
			}
		}
		w.Header().Set("WWW-Authenticate", fmt.Sprintf(
			`Bearer error="invalid_token", error_description="invalid or expired token", resource_metadata=%q`,
			p.issuer+"/.well-known/oauth-protected-resource/mcp"))
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"error": "invalid_token", "error_description": "invalid or expired token",
		})
	})
}

// ---- rate limiting (OAuth endpoints only, per client IP) ----

const (
	rateWindow = time.Minute
	rateLimit  = 60
)

type rateBucket struct {
	windowStart time.Time
	count       int
}

var (
	rateMu      sync.Mutex
	rateBuckets = map[string]*rateBucket{}
)

// rateLimit caps requests per IP on the OAuth endpoints. The MCP data plane
// is deliberately not rate-limited (matching openclaw-mcp).
func (p *Provider) rateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := p.clientIP(r)
		now := time.Now()
		rateMu.Lock()
		b := rateBuckets[ip]
		if b == nil || now.Sub(b.windowStart) > rateWindow {
			b = &rateBucket{windowStart: now}
			rateBuckets[ip] = b
			if len(rateBuckets) > 10000 { // bound memory under IP churn
				for k, old := range rateBuckets {
					if now.Sub(old.windowStart) > rateWindow {
						delete(rateBuckets, k)
					}
				}
			}
		}
		b.count++
		over := b.count > rateLimit
		rateMu.Unlock()
		if over {
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate_limited"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// clientIP honors X-Forwarded-For only when TRUST_PROXY is set — without a
// trusted proxy the header is attacker-controlled.
func (p *Provider) clientIP(r *http.Request) string {
	if p.cfg.TrustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			return strings.TrimSpace(parts[len(parts)-1]) // nearest proxy hop
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// LoopbackOrigin reports whether a browser Origin points at the client's
// own machine (localhost, 127.0.0.1 or [::1], any port). Such origins are
// always CORS-allowed: the page behind them already runs on the user's
// machine (MCP Inspector, local dev tools), the browser sets the Origin
// header so a remote site cannot fake one, and authentication is still
// required for anything the response would reveal.
func LoopbackOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Path != "" {
		return false
	}
	host := u.Hostname()
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

// CORS returns a middleware applying CORS headers for the allowed browser
// origins (CORS_ORIGINS) plus loopback origins. OPTIONS preflights
// short-circuit with 204.
func CORS(origins []string, next http.Handler) http.Handler {
	allowed := map[string]bool{}
	for _, o := range origins {
		allowed[strings.TrimRight(o, "/")] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && (allowed[origin] || LoopbackOrigin(origin)) {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
			h.Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Mcp-Session-Id, mcp-protocol-version")
			h.Set("Access-Control-Expose-Headers", "Mcp-Session-Id")
			h.Set("Vary", "Origin")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
