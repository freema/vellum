package auth

import (
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// Optional persistence (Config.StateFile / VELLUM_AUTH_STATE_FILE). Without it
// every restart logs every client out; with it, access and refresh tokens and
// dynamically-registered clients survive. Authorization codes (10 min,
// single use) are not kept. The file holds live bearer tokens: it is written
// 0600 and must live outside the vault directory.

type stateFile struct {
	Tokens  map[string]stateToken  `json:"tokens"`
	Refresh map[string]stateToken  `json:"refresh"`
	Clients map[string]stateClient `json:"clients"`
}

type stateToken struct {
	ClientID  string    `json:"client_id"`
	Scopes    []string  `json:"scopes"`
	ExpiresAt time.Time `json:"expires_at"`
}

type stateClient struct {
	RedirectURIs []string  `json:"redirect_uris"`
	Public       bool      `json:"public"`
	CreatedAt    time.Time `json:"created_at"`
}

// loadState fills the provider from the state file; a missing file is fine.
func (p *Provider) loadState() error {
	if p.cfg.StateFile == "" {
		return nil
	}
	b, err := os.ReadFile(p.cfg.StateFile)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var s stateFile
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	now := time.Now()
	for k, t := range s.Tokens {
		if now.Before(t.ExpiresAt) {
			p.tokens[k] = &TokenInfo{Token: k, ClientID: t.ClientID, Scopes: t.Scopes, ExpiresAt: t.ExpiresAt}
		}
	}
	for k, t := range s.Refresh {
		if now.Before(t.ExpiresAt) {
			p.refresh[k] = &refreshData{clientID: t.ClientID, scopes: t.Scopes, expiresAt: t.ExpiresAt}
		}
	}
	for id, c := range s.Clients {
		if id == p.cfg.ClientID || !c.Public { // only DCR public clients; the configured one comes from env
			continue
		}
		p.clients[id] = &clientReg{id: id, redirectURIs: c.RedirectURIs, public: true, createdAt: c.CreatedAt}
	}
	return nil
}

// saveLocked writes the state file atomically. Caller holds p.mu. A failed
// write is logged; auth keeps working from memory.
func (p *Provider) saveLocked() {
	if p.cfg.StateFile == "" {
		return
	}
	s := stateFile{Tokens: map[string]stateToken{}, Refresh: map[string]stateToken{}, Clients: map[string]stateClient{}}
	for k, t := range p.tokens {
		s.Tokens[k] = stateToken{t.ClientID, t.Scopes, t.ExpiresAt}
	}
	for k, t := range p.refresh {
		s.Refresh[k] = stateToken{t.clientID, t.scopes, t.expiresAt}
	}
	for id, c := range p.clients {
		if c.public {
			s.Clients[id] = stateClient{c.redirectURIs, true, c.createdAt}
		}
	}
	if err := writeAtomic(p.cfg.StateFile, s); err != nil {
		slog.Warn("auth state file not saved; tokens will not survive a restart", "error", err)
	}
}

func writeAtomic(path string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".auth-state-*") // created 0600
	if err != nil {
		return err
	}
	_, werr := tmp.Write(b)
	if err := errors.Join(werr, tmp.Close()); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}
