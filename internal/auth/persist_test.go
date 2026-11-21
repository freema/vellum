package auth

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStateFileSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth-state.json")
	withState := func(c *Config) { c.StateFile = path }

	p1 := newTestProvider(t, withState)
	reg := p1.registerClient([]string{"https://claude.ai/cb"}, true)
	access, refresh, _, err := p1.grantDirect("vellum", Scopes)
	if err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("state file: %v perm %v", err, fi.Mode().Perm())
	}

	p2 := newTestProvider(t, withState) // "restart"
	if _, err := p2.VerifyAccessToken(access); err != nil {
		t.Fatalf("access token lost across restart: %v", err)
	}
	if _, ok := p2.lookupClient(reg.id); !ok {
		t.Fatal("registered client lost across restart")
	}
	_, refresh2, _, err := p2.exchangeRefresh("vellum", refresh, nil)
	if err != nil {
		t.Fatalf("refresh lost across restart: %v", err)
	}

	p2.revokeToken(access)
	p3 := newTestProvider(t, withState)
	if _, err := p3.VerifyAccessToken(access); err == nil {
		t.Fatal("revoked token came back after restart")
	}
	if _, _, _, err := p3.exchangeRefresh("vellum", refresh, nil); err == nil {
		t.Fatal("rotated-out refresh token came back after restart")
	}
	if _, _, _, err := p3.exchangeRefresh("vellum", refresh2, nil); err != nil {
		t.Fatalf("current refresh token lost: %v", err)
	}
}

func TestNoStateFileStaysInMemory(t *testing.T) {
	p1 := newTestProvider(t)
	access, _, _, _ := p1.grantDirect("vellum", Scopes)
	p2 := newTestProvider(t)
	if _, err := p2.VerifyAccessToken(access); err == nil {
		t.Fatal("token survived without a state file")
	}
}
