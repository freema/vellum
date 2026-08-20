package config

import "testing"

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
