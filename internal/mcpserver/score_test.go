package mcpserver

import (
	"encoding/json"
	"testing"
)

// BM25F scores in search_notes (NSO-577): every ranked hit carries a score
// from 0 to 1, and min_score drops the weak ones before paging.

func scoreSession(t *testing.T) *readSession {
	t.Helper()
	return newReadSession(t, map[string]string{
		// The topic in the title: a strong match.
		"garden/compost.md": "---\ntags: [garden]\n---\n# Compost\n\nTurn the compost heap every two weeks.\n",
		// A passing mention in a long note: a weak one.
		"journal/2026.md": "# Journal 2026\n\n" + repeatLine("Rain all day, read a book and cooked soup.", 40) +
			"Took the kitchen scraps out to the compost.\n" + repeatLine("Quiet evening, nothing to note.", 40),
		"garden/beds.md": "# Raised beds\n\nThe beds get a layer of compost in spring.\n",
	})
}

func repeatLine(line string, n int) string {
	out := ""
	for range n {
		out += line + "\n"
	}
	return out
}

func TestSearchNotesScore(t *testing.T) {
	s := scoreSession(t)
	out := decode[searchNotesOut](t, s.call("search_notes", map[string]any{"query": "compost"}))
	if len(out.Results) != 3 || out.Total != 3 {
		t.Fatalf("results = %+v", out)
	}
	if out.Results[0].Path != "garden/compost.md" || out.Results[2].Path != "journal/2026.md" {
		t.Errorf("ranking = %s, %s, %s; want the compost note first and the long journal last",
			out.Results[0].Path, out.Results[1].Path, out.Results[2].Path)
	}
	for i, r := range out.Results {
		if r.Score <= 0 || r.Score > 1 {
			t.Errorf("%s: score %v, want within (0, 1]", r.Path, r.Score)
		}
		if i > 0 && r.Score > out.Results[i-1].Score {
			t.Errorf("%s: score %v above the previous hit's %v", r.Path, r.Score, out.Results[i-1].Score)
		}
	}

	// min_score drops the weak hit before paging: total counts what passes.
	cut := (out.Results[1].Score + out.Results[2].Score) / 2
	strong := decode[searchNotesOut](t, s.call("search_notes", map[string]any{"query": "compost", "min_score": cut}))
	if len(strong.Results) != 2 || strong.Total != 2 || strong.Truncated || strong.Hint != "" {
		t.Errorf("min_score %v = %+v, want the two strong hits and nothing truncated", cut, strong)
	}
	paged := decode[searchNotesOut](t, s.call("search_notes", map[string]any{"query": "compost", "min_score": cut, "max_results": 1}))
	if len(paged.Results) != 1 || paged.Total != 2 || !paged.Truncated || paged.Results[0].Path != "garden/compost.md" {
		t.Errorf("min_score with a page of 1 = %+v", paged)
	}
	top := decode[searchNotesOut](t, s.call("search_notes", map[string]any{"query": "compost", "min_score": 1}))
	if top.Total != len(top.Results) {
		t.Errorf("min_score 1 = %+v", top)
	}
	for _, r := range top.Results {
		if r.Score < 1 {
			t.Errorf("min_score 1 kept %s at %v", r.Path, r.Score)
		}
	}

	// The concise format carries the same scores.
	concise := decode[searchNotesOut](t, s.call("search_notes", map[string]any{"query": "compost", "response_format": "concise"}))
	for i, r := range concise.Results {
		if r.Score != out.Results[i].Score {
			t.Errorf("concise %s score %v, detailed %v", r.Path, r.Score, out.Results[i].Score)
		}
	}
}

// Regex search and a pure tag filter rank nothing: no score, and min_score
// does not empty them.
func TestSearchNotesScoreUnranked(t *testing.T) {
	s := scoreSession(t)
	for _, args := range []map[string]any{
		{"query": "comp(o|a)st", "regex": true, "min_score": 0.9},
		{"tags": []string{"garden"}, "min_score": 0.9},
	} {
		res := s.call("search_notes", args)
		out := decode[searchNotesOut](t, res)
		if len(out.Results) == 0 {
			t.Errorf("%v: min_score emptied an unranked search", args)
		}
		var raw struct {
			Results []map[string]json.RawMessage `json:"results"`
		}
		if err := json.Unmarshal(res.Structured, &raw); err != nil {
			t.Fatal(err)
		}
		for _, r := range raw.Results {
			if r["score"] != nil {
				t.Errorf("%v: unranked hit carries a score: %v", args, r)
			}
		}
	}
}

func TestSearchNotesMinScoreRange(t *testing.T) {
	s := scoreSession(t)
	for _, v := range []float64{-0.1, 1.5} {
		if res := s.call("search_notes", map[string]any{"query": "compost", "min_score": v}); !res.IsError {
			t.Errorf("min_score %v: want a tool error, got %s", v, res.Structured)
		}
	}
}
