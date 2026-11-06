package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"
)

// The REST search flags relaxed results (NSO-578): no note holds every
// word, these miss one. A strict search has no relaxed key.
func TestSearchRelaxedFlag(t *testing.T) {
	srv := newAPIServer(t)
	for path, content := range map[string]string{
		"homelab/certs.md": "# Certificates\n\nRotate the TLS certificates on the reverse proxy.\n",
		"misc/a.md":        "# Note\n\nThe weather on the weekend.\n",
		"misc/b.md":        "# Note\n\nA walk in the park.\n",
	} {
		if resp, body := doReq(t, http.MethodPut, srv.URL+"/api/notes/"+path, content, nil); resp.StatusCode != http.StatusOK {
			t.Fatalf("PUT %s = %d: %s", path, resp.StatusCode, body)
		}
	}
	search := func(q string) map[string]json.RawMessage {
		t.Helper()
		resp, body := doReq(t, http.MethodGet, srv.URL+"/api/search?q="+q, "", nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("search %q = %d %s", q, resp.StatusCode, body)
		}
		var out map[string]json.RawMessage
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	if out := search("rotate+tls+certificates+xylofon"); string(out["relaxed"]) != "true" {
		t.Errorf("relaxed search = %v, want relaxed: true", out)
	}
	if out := search("rotate+tls+certificates"); out["relaxed"] != nil {
		t.Errorf("strict search = %v, want no relaxed key", out)
	}
}
