package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"
)

// The REST search returns the BM25F score of every ranked hit (NSO-577);
// a pure tag filter ranks nothing and carries none.
func TestSearchReturnsScore(t *testing.T) {
	srv := newAPIServer(t)
	for path, content := range map[string]string{
		"garden/compost.md": "---\ntags: [garden]\n---\n# Compost\n\nTurn the heap.\n",
		"garden/beds.md":    "---\ntags: [garden]\n---\n# Beds\n\nA layer of compost in spring, and more words around it.\n",
	} {
		if resp, body := doReq(t, http.MethodPut, srv.URL+"/api/notes/"+path, content, nil); resp.StatusCode != http.StatusOK {
			t.Fatalf("PUT %s = %d: %s", path, resp.StatusCode, body)
		}
	}
	var out struct {
		Results []struct {
			Path  string   `json:"path"`
			Score *float64 `json:"score"`
		} `json:"results"`
	}
	resp, body := doReq(t, http.MethodGet, srv.URL+"/api/search?q=compost", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("search = %d %s", resp.StatusCode, body)
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Results) != 2 || out.Results[0].Path != "garden/compost.md" {
		t.Fatalf("results = %s", body)
	}
	for _, r := range out.Results {
		if r.Score == nil || *r.Score <= 0 || *r.Score > 1 {
			t.Errorf("%s: score %v, want within (0, 1]", r.Path, r.Score)
		}
	}
	if *out.Results[0].Score <= *out.Results[1].Score {
		t.Errorf("scores not descending: %s", body)
	}

	out.Results = nil
	_, body = doReq(t, http.MethodGet, srv.URL+"/api/search?tags=garden", "", nil)
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Results) != 2 || out.Results[0].Score != nil {
		t.Errorf("tag filter = %s, want two hits without a score", body)
	}
}
