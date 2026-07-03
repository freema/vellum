// Package auth implements OAuth 2.1 issuer + resource server with a single
// pre-configured client secret, ported from openclaw-mcp (PHY-112).
//
// The model: vellum is both the authorization server and the resource
// server. One confidential client (VELLUM_CLIENT_ID/VELLUM_CLIENT_SECRET),
// authorization-code flow with mandatory PKCE (S256), opaque in-memory
// tokens. No user database, no external identity provider, no persistence —
// tokens die with the process, clients silently re-authorize.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Lifetimes recycled 1:1 from openclaw-mcp (production-proven).
const (
	AccessTokenTTL  = time.Hour
	AuthCodeTTL     = 10 * time.Minute
	RefreshTokenTTL = 24 * time.Hour
	reaperInterval  = 5 * time.Minute
)

// Scopes advertised in metadata and shown on the consent screen.
var Scopes = []string{"vault.read", "vault.write", "vault.delete"}

var clientIDRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{2,63}$`)

// Config is the auth configuration (env names per PHY-112).
type Config struct {
	Enabled      bool     // AUTH_ENABLED
	ClientID     string   // VELLUM_CLIENT_ID (default "vellum")
	ClientSecret string   // VELLUM_CLIENT_SECRET (>= 32 chars)
	IssuerURL    string   // VELLUM_ISSUER_URL — public HTTPS URL behind a proxy
	RedirectURIs []string // VELLUM_REDIRECT_URIS — empty allows any non-empty URI
	TrustProxy   bool     // TRUST_PROXY — derive client IP from X-Forwarded-For
}

// TokenInfo is the server-side record of an access token.
type TokenInfo struct {
	Token     string
	ClientID  string
	Scopes    []string
	ExpiresAt time.Time
}

// Authenticator verifies bearer tokens. It is an interface so a future team
// mode (identity→role mapping in vellum.yaml, PHY-119) can slot in.
type Authenticator interface {
	VerifyAccessToken(token string) (*TokenInfo, error)
}

// ErrInvalidToken is returned for unknown or expired access tokens.
var ErrInvalidToken = errors.New("invalid or expired token")

type codeData struct {
	clientID    string
	redirectURI string
	challenge   string // PKCE S256 challenge
	scopes      []string
	resource    string
	createdAt   time.Time
}

type refreshData struct {
	clientID  string
	scopes    []string
	expiresAt time.Time
}

// Provider is the single-client OAuth 2.1 provider. All state is in memory;
// lazy expiry on every lookup plus a 5-minute reaper sweep.
type Provider struct {
	cfg    Config
	issuer string // normalized, no trailing slash

	mu      sync.Mutex
	codes   map[string]*codeData
	tokens  map[string]*TokenInfo
	refresh map[string]*refreshData

	stopReaper chan struct{}
}

// NewProvider validates the configuration and builds the provider.
func NewProvider(cfg Config) (*Provider, error) {
	if !cfg.Enabled {
		return nil, errors.New("auth disabled")
	}
	if !clientIDRe.MatchString(cfg.ClientID) {
		return nil, fmt.Errorf("invalid client id %q: need 3-64 chars [a-zA-Z0-9_-]", cfg.ClientID)
	}
	if len(cfg.ClientSecret) < 32 {
		return nil, errors.New("VELLUM_CLIENT_SECRET must be at least 32 characters (openssl rand -hex 32)")
	}
	issuer := strings.TrimRight(cfg.IssuerURL, "/")
	if issuer == "" {
		return nil, errors.New("issuer URL must not be empty")
	}
	if _, err := url.Parse(issuer); err != nil {
		return nil, fmt.Errorf("invalid issuer URL: %w", err)
	}
	p := &Provider{
		cfg:        cfg,
		issuer:     issuer,
		codes:      map[string]*codeData{},
		tokens:     map[string]*TokenInfo{},
		refresh:    map[string]*refreshData{},
		stopReaper: make(chan struct{}),
	}
	go p.reaper()
	return p, nil
}

// Close stops the background reaper.
func (p *Provider) Close() { close(p.stopReaper) }

// Issuer returns the normalized issuer URL (no trailing slash).
func (p *Provider) Issuer() string { return p.issuer }

func (p *Provider) reaper() {
	t := time.NewTicker(reaperInterval)
	defer t.Stop()
	for {
		select {
		case <-p.stopReaper:
			return
		case now := <-t.C:
			p.mu.Lock()
			for c, d := range p.codes {
				if now.Sub(d.createdAt) > AuthCodeTTL {
					delete(p.codes, c)
				}
			}
			for tk, d := range p.tokens {
				if now.After(d.ExpiresAt) {
					delete(p.tokens, tk)
				}
			}
			for rt, d := range p.refresh {
				if now.After(d.expiresAt) {
					delete(p.refresh, rt)
				}
			}
			p.mu.Unlock()
		}
	}
}

func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand failure is unrecoverable
	}
	return hex.EncodeToString(b)
}

// secretEquals is a constant-time comparison of the client secret.
func (p *Provider) secretEquals(secret string) bool {
	return subtle.ConstantTimeCompare([]byte(p.cfg.ClientSecret), []byte(secret)) == 1
}

// redirectAllowed applies the openclaw rules: empty allowlist accepts any
// non-empty URI; otherwise exact match, with loopback hosts matching on any
// port (RFC 8252).
func (p *Provider) redirectAllowed(uri string) bool {
	if uri == "" {
		return false
	}
	if len(p.cfg.RedirectURIs) == 0 {
		return true
	}
	req, err := url.Parse(uri)
	if err != nil {
		return false
	}
	for _, allowed := range p.cfg.RedirectURIs {
		if uri == allowed {
			return true
		}
		al, err := url.Parse(allowed)
		if err != nil {
			continue
		}
		if isLoopback(al.Hostname()) && isLoopback(req.Hostname()) &&
			al.Scheme == req.Scheme && al.Hostname() == req.Hostname() && al.Path == req.Path {
			return true // loopback: any port matches
		}
	}
	return false
}

func isLoopback(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

// issueCode stores a fresh single-use authorization code.
func (p *Provider) issueCode(clientID, redirectURI, challenge, resource string, scopes []string) string {
	code := randomToken()
	p.mu.Lock()
	p.codes[code] = &codeData{
		clientID:    clientID,
		redirectURI: redirectURI,
		challenge:   challenge,
		scopes:      scopes,
		resource:    resource,
		createdAt:   time.Now(),
	}
	p.mu.Unlock()
	return code
}

// exchangeCode redeems an authorization code (single use) after verifying
// the PKCE verifier: BASE64URL(SHA256(verifier)) must equal the challenge.
func (p *Provider) exchangeCode(code, verifier, redirectURI string) (access, refresh string, scopes []string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	data, ok := p.codes[code]
	if !ok || time.Since(data.createdAt) > AuthCodeTTL {
		delete(p.codes, code)
		return "", "", nil, errors.New("invalid authorization code")
	}
	delete(p.codes, code) // single use, even on failure below

	sum := sha256.Sum256([]byte(verifier))
	if base64.RawURLEncoding.EncodeToString(sum[:]) != data.challenge {
		return "", "", nil, errors.New("PKCE verification failed")
	}
	if redirectURI != "" && redirectURI != data.redirectURI {
		return "", "", nil, errors.New("redirect_uri mismatch")
	}

	scopes = data.scopes
	if len(scopes) == 0 {
		scopes = Scopes
	}
	access, refresh = p.issueTokensLocked(scopes)
	return access, refresh, scopes, nil
}

// exchangeRefresh rotates a refresh token and issues a new pair.
func (p *Provider) exchangeRefresh(token string, requested []string) (access, refresh string, scopes []string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	data, ok := p.refresh[token]
	if !ok || time.Now().After(data.expiresAt) {
		delete(p.refresh, token)
		return "", "", nil, errors.New("invalid refresh token")
	}
	delete(p.refresh, token) // rotation

	scopes = data.scopes
	if len(requested) > 0 {
		scopes = requested
	}
	access, refresh = p.issueTokensLocked(scopes)
	return access, refresh, scopes, nil
}

func (p *Provider) issueTokensLocked(scopes []string) (access, refresh string) {
	access, refresh = randomToken(), randomToken()
	now := time.Now()
	p.tokens[access] = &TokenInfo{
		Token:     access,
		ClientID:  p.cfg.ClientID,
		Scopes:    scopes,
		ExpiresAt: now.Add(AccessTokenTTL),
	}
	p.refresh[refresh] = &refreshData{
		clientID:  p.cfg.ClientID,
		scopes:    scopes,
		expiresAt: now.Add(RefreshTokenTTL),
	}
	return access, refresh
}

// grantDirect issues a token pair for the client_credentials grant. The
// caller must have authenticated the client secret already.
func (p *Provider) grantDirect(scopes []string) (access, refresh string, granted []string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	access, refresh = p.issueTokensLocked(scopes)
	return access, refresh, scopes, nil
}

// VerifyAccessToken implements Authenticator.
func (p *Provider) VerifyAccessToken(token string) (*TokenInfo, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	info, ok := p.tokens[token]
	if !ok || time.Now().After(info.ExpiresAt) {
		delete(p.tokens, token)
		return nil, ErrInvalidToken
	}
	return info, nil
}

// revokeToken drops an access or refresh token (RFC 7009: unknown tokens
// are not an error).
func (p *Provider) revokeToken(token string) {
	p.mu.Lock()
	delete(p.tokens, token)
	delete(p.refresh, token)
	p.mu.Unlock()
}
