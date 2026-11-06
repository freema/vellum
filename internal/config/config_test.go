package config

import (
	"testing"
	"time"
)

// A typo in VELLUM_SHARING must not be the reason a vault starts answering
// anonymous requests, so anything unrecognized reads as off.
func TestSharingMode(t *testing.T) {
	cases := map[string]string{
		"on": SharingOn, "ON": SharingOn, "true": SharingOn, "1": SharingOn,
		"yes": SharingOn, " on ": SharingOn,
		"ui": SharingUI, "UI": SharingUI, "web": SharingUI,
		"": SharingOff, "off": SharingOff, "false": SharingOff, "0": SharingOff,
		"onn": SharingOff, "public": SharingOff, "yes please": SharingOff,
	}
	for in, want := range cases {
		if got := sharingMode(in); got != want {
			t.Errorf("sharingMode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSharingHelpers(t *testing.T) {
	cases := []struct {
		mode           string
		enabled, tools bool
	}{
		{SharingOff, false, false},
		{SharingUI, true, false},
		{SharingOn, true, true},
	}
	for _, tc := range cases {
		c := Config{Sharing: tc.mode}
		if got := c.SharingEnabled(); got != tc.enabled {
			t.Errorf("%q: SharingEnabled = %v, want %v", tc.mode, got, tc.enabled)
		}
		if got := c.ShareTools(); got != tc.tools {
			t.Errorf("%q: ShareTools = %v, want %v", tc.mode, got, tc.tools)
		}
	}
}

func TestLoadDefaultsSharingOff(t *testing.T) {
	cfg := Load()
	if cfg.Sharing != SharingOff || cfg.SharingEnabled() {
		t.Fatalf("sharing defaults to %q — it must be off without VELLUM_SHARING", cfg.Sharing)
	}
}

// A bare binary must not expose the vault to the network unless asked to:
// without VELLUM_HOST it listens on loopback only.
func TestLoadHost(t *testing.T) {
	t.Setenv("VELLUM_HOST", "")
	if got := Load().Host; got != "127.0.0.1" {
		t.Fatalf("default Host = %q, want 127.0.0.1", got)
	}
	t.Setenv("VELLUM_HOST", "0.0.0.0")
	if got := Load().Host; got != "0.0.0.0" {
		t.Fatalf("Host with VELLUM_HOST=0.0.0.0 = %q", got)
	}
}

// Folder descriptions live in _index.md unless VELLUM_CONTEXT_FILE names
// another file (NSO-573).
func TestLoadContextFile(t *testing.T) {
	t.Setenv("VELLUM_CONTEXT_FILE", "")
	if got := Load().ContextFile; got != "_index.md" {
		t.Fatalf("default ContextFile = %q, want _index.md", got)
	}
	t.Setenv("VELLUM_CONTEXT_FILE", "README.md")
	if got := Load().ContextFile; got != "README.md" {
		t.Fatalf("ContextFile with VELLUM_CONTEXT_FILE=README.md = %q", got)
	}
}

func TestLoadRanker(t *testing.T) {
	t.Setenv("VELLUM_RANKER", "")
	if got := Load().Ranker; got != "bm25f" {
		t.Fatalf("default Ranker = %q, want bm25f", got)
	}
	t.Setenv("VELLUM_RANKER", " Legacy ")
	if got := Load().Ranker; got != "legacy" {
		t.Fatalf("Ranker with VELLUM_RANKER=' Legacy ' = %q, want legacy", got)
	}
}

// VELLUM_RESCAN: a duration or whole seconds, off disables, and a typo
// keeps the default instead of silently turning the rescan off.
func TestRescanInterval(t *testing.T) {
	for raw, want := range map[string]time.Duration{
		"": DefaultRescan, "5s": 5 * time.Second, "250ms": 250 * time.Millisecond,
		"1m": time.Minute, "10": 10 * time.Second, " 2S ": 2 * time.Second,
		"off": 0, "OFF": 0, "0": 0, "0s": 0, "false": 0, "no": 0, "none": 0,
	} {
		got, ok := rescanInterval(raw)
		if !ok || got != want {
			t.Errorf("rescanInterval(%q) = %v, %v; want %v", raw, got, ok, want)
		}
	}
	for _, raw := range []string{"soon", "-5s", "-3", "5 s"} {
		if got, ok := rescanInterval(raw); ok || got != DefaultRescan {
			t.Errorf("rescanInterval(%q) = %v, %v; want the default, not ok", raw, got, ok)
		}
	}
}

func TestLoadRescan(t *testing.T) {
	t.Setenv("VELLUM_RESCAN", "")
	if cfg := Load(); cfg.Rescan != DefaultRescan || cfg.RescanInvalid != "" {
		t.Errorf("default rescan = %v (invalid %q), want %v", cfg.Rescan, cfg.RescanInvalid, DefaultRescan)
	}
	t.Setenv("VELLUM_RESCAN", "off")
	if cfg := Load(); cfg.Rescan != 0 {
		t.Errorf("VELLUM_RESCAN=off gives %v, want 0", cfg.Rescan)
	}
	t.Setenv("VELLUM_RESCAN", "sometimes")
	if cfg := Load(); cfg.Rescan != DefaultRescan || cfg.RescanInvalid != "sometimes" {
		t.Errorf("invalid VELLUM_RESCAN = %v (invalid %q)", cfg.Rescan, cfg.RescanInvalid)
	}
}
