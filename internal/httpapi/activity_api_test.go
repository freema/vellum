package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/freema/vellum/internal/activity"
	"github.com/freema/vellum/internal/vault"
)

func newActivityServer(t *testing.T) (*httptest.Server, *activity.Recorder) {
	t.Helper()
	v, err := vault.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ix := vault.NewIndex(v)
	if err := ix.Build(); err != nil {
		t.Fatal(err)
	}
	rec := activity.New()
	srv := httptest.NewServer(NewRouter("test", Options{
		API: &API{
			Vault:     v,
			Index:     ix,
			Searcher:  vault.NewScanSearcher(v, ix),
			Structure: vault.DefaultStructure(),
			Activity:  rec,
			Endpoint:  "https://vellum.example/mcp",
			Curator:   true,
		},
		Activity: rec,
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

func TestFoldersCreateAndList(t *testing.T) {
	srv, _ := newActivityServer(t)

	resp, _ := doReq(t, http.MethodPost, srv.URL+"/api/folders", `{"path":"projects/newthing"}`, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create folder status = %d", resp.StatusCode)
	}
	resp, body := doReq(t, http.MethodGet, srv.URL+"/api/folders", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list folders status = %d", resp.StatusCode)
	}
	var got struct {
		Folders []string `json:"folders"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, d := range got.Folders {
		if d == "projects/newthing" {
			found = true
		}
	}
	if !found {
		t.Fatalf("created folder not listed: %v", got.Folders)
	}
}

func TestConnectionsAndActivity(t *testing.T) {
	srv, rec := newActivityServer(t)
	rec.Touch("sk-abc", "Claude Code", "CLI", "write_note")
	rec.Record(activity.Event{Source: "mcp", Actor: "Claude Code", Kind: "write", Target: "a.md", Detail: "write_note"})

	resp, body := doReq(t, http.MethodGet, srv.URL+"/api/connections", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("connections status = %d", resp.StatusCode)
	}
	var conns struct {
		Endpoint    string `json:"endpoint"`
		ActiveCount int    `json:"activeCount"`
		Connections []struct {
			ID   string `json:"id"`
			Mono string `json:"mono"`
		} `json:"connections"`
	}
	if err := json.Unmarshal(body, &conns); err != nil {
		t.Fatal(err)
	}
	if conns.Endpoint != "https://vellum.example/mcp" || conns.ActiveCount != 1 || len(conns.Connections) != 1 {
		t.Fatalf("unexpected connections payload: %s", body)
	}
	if conns.Connections[0].Mono != "CC" {
		t.Errorf("monogram = %q, want CC", conns.Connections[0].Mono)
	}

	resp, body = doReq(t, http.MethodGet, srv.URL+"/api/activity?filter=mcp", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("activity status = %d", resp.StatusCode)
	}
	var act struct {
		Events []struct {
			Verb string `json:"verb"`
		} `json:"events"`
	}
	if err := json.Unmarshal(body, &act); err != nil {
		t.Fatal(err)
	}
	if len(act.Events) != 1 || act.Events[0].Verb != "wrote" {
		t.Fatalf("unexpected activity payload: %s", body)
	}

	// revoke
	resp, _ = doReq(t, http.MethodDelete, srv.URL+"/api/connections/sk-abc", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("revoke status = %d", resp.StatusCode)
	}
}

func TestCuratorRun(t *testing.T) {
	srv, _ := newActivityServer(t)
	resp, body := doReq(t, http.MethodPost, srv.URL+"/api/curator/run", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("curator run status = %d", resp.StatusCode)
	}
	var got struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if !got.Enabled {
		t.Fatal("curator should be enabled in test server")
	}
}
