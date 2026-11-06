package httpapi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/freema/vellum/internal/vault"
)

// NSO-562: the REST views of the index (search, tags, backlinks, tasks)
// pick up a note written outside vellum once the rescan interval passed,
// the same way the MCP tools do.
func TestRESTSeesOutsideChanges(t *testing.T) {
	v, err := vault.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Write("notes/target.md", "# Target\n", vault.WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	ix := vault.NewIndex(v)
	if err := ix.Build(); err != nil {
		t.Fatal(err)
	}
	const every = 20 * time.Millisecond
	ix.SetRescanInterval(every)
	srv := httptest.NewServer(NewRouter("test", Options{API: &API{
		Vault: v, Index: ix, Searcher: vault.NewScanSearcher(v, ix), Structure: vault.DefaultStructure(),
	}}))
	t.Cleanup(srv.Close)

	abs := filepath.Join(v.Root(), "inbox", "outside.md")
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\ntype: task\nstatus: in-progress\ntags: [zvenku]\n---\n# Outside\n\nmarmot, see [[target]]\n"
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * every)

	for _, c := range []struct{ url, want string }{
		{"/api/search?q=marmot", "inbox/outside.md"},
		{"/api/tags", `"zvenku"`},
		{"/api/backlinks/notes/target.md", "inbox/outside.md"},
		{"/api/tasks?status=in-progress", "inbox/outside.md"},
	} {
		resp, body := doReq(t, http.MethodGet, srv.URL+c.url, "", nil)
		if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), c.want) {
			t.Errorf("GET %s = %d %s, want %s in it", c.url, resp.StatusCode, body, c.want)
		}
	}
}
