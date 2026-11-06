package mcpserver

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// Relaxed AND in search_notes (NSO-578): when no note holds every query
// word, the notes missing one come back with relaxed: true, and the text
// says so. A strict result carries no relaxed field at all.
func TestSearchNotesRelaxed(t *testing.T) {
	notes := map[string]string{
		"homelab/certs.md": "# Certificates\n\nRotate the TLS certificates on the reverse proxy every quarter.\n",
	}
	for i := range 6 {
		notes[fmt.Sprintf("misc/n%d.md", i)] = "# Note\n\nThe weather on the weekend, a walk in the park.\n"
	}
	s := newReadSession(t, notes)

	res := s.call("search_notes", map[string]any{"query": "rotate tls certificates monthly"})
	out := decode[searchNotesOut](t, res)
	if !out.Relaxed || out.Total != 1 || out.Results[0].Path != "homelab/certs.md" {
		t.Fatalf("relaxed search = %+v", out)
	}
	if out.Results[0].Score <= 0 || out.Results[0].Score >= 1 {
		t.Errorf("relaxed score %v, want within (0, 1)", out.Results[0].Score)
	}
	if !strings.Contains(res.Text, "1 note that misses one query word (relaxed") {
		t.Errorf("text = %q, want the relaxed header", res.Text)
	}

	res = s.call("search_notes", map[string]any{"query": "rotate tls certificates"})
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(res.Structured, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["relaxed"]; ok {
		t.Errorf("strict search carries relaxed: %s", res.Structured)
	}
	if strings.Contains(res.Text, "relaxed") {
		t.Errorf("strict text mentions relaxed: %q", res.Text)
	}

	// Concise format: same flag, plural header.
	notes2 := map[string]string{}
	for k, v := range notes {
		notes2[k] = v
	}
	notes2["homelab/certs-2.md"] = "# More certificates\n\nRotate TLS certificates before they expire.\n"
	s = newReadSession(t, notes2)
	res = s.call("search_notes", map[string]any{"query": "rotate tls certificates monthly", "response_format": "concise"})
	out = decode[searchNotesOut](t, res)
	if !out.Relaxed || out.Total != 2 {
		t.Fatalf("concise relaxed = %+v", out)
	}
	if !strings.Contains(res.Text, "2 notes that miss one query word each (relaxed") {
		t.Errorf("text = %q, want the plural relaxed header", res.Text)
	}
}
