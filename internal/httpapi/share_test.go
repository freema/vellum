package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/freema/vellum/internal/activity"
	"github.com/freema/vellum/internal/share"
	"github.com/freema/vellum/internal/vault"
)

type shareEnv struct {
	srv    *httptest.Server
	store  *share.Store
	vault  *vault.Vault
	index  *vault.Index
	events *activity.Recorder
}

func newShareServer(t *testing.T) *shareEnv {
	t.Helper()
	dir := t.TempDir()
	v, err := vault.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	ix := vault.NewIndex(v)
	if err := ix.Build(); err != nil {
		t.Fatal(err)
	}
	store, err := share.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	rec := activity.New()
	spa := fstest.MapFS{"index.html": {Data: []byte("<html>vellum spa</html>")}}
	srv := httptest.NewServer(NewRouter("test", Options{
		API: &API{
			Vault:     v,
			Index:     ix,
			Searcher:  vault.NewScanSearcher(v, ix),
			Structure: vault.DefaultStructure(),
			Activity:  rec,
			Shares:    store,
			Sharing:   "on",
			PublicURL: "https://vellum.example.com",
		},
		SPA:      spa,
		Shares:   store,
		Vault:    v,
		Activity: rec,
	}))
	t.Cleanup(srv.Close)
	return &shareEnv{srv: srv, store: store, vault: v, index: ix, events: rec}
}

// writeNote puts a note through the API so the index stays in step.
func (e *shareEnv) writeNote(t *testing.T, path, content string) {
	t.Helper()
	resp, body := doReq(t, http.MethodPut, e.srv.URL+"/api/notes/"+path, content, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT %s = %d: %s", path, resp.StatusCode, body)
	}
}

func (e *shareEnv) createShare(t *testing.T, path string, expiresIn int64) shareView {
	t.Helper()
	req := `{"path":"` + path + `","expiresIn":` + itoa(int(expiresIn)) + `}`
	resp, body := doReq(t, http.MethodPost, e.srv.URL+"/api/shares", req,
		map[string]string{"Content-Type": "application/json"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/shares = %d: %s", resp.StatusCode, body)
	}
	var sv shareView
	if err := json.Unmarshal(body, &sv); err != nil {
		t.Fatalf("decode share: %v (%s)", err, body)
	}
	return sv
}

func TestShareRoundTrip(t *testing.T) {
	e := newShareServer(t)
	e.writeNote(t, "projects/p/plan.md", "---\ntags: [go]\n---\n# The plan\n\nStep one.\n")

	sv := e.createShare(t, "projects/p/plan.md", 0)
	if sv.Token == "" || sv.Title != "The plan" {
		t.Fatalf("share = %+v", sv)
	}
	if want := "https://vellum.example.com/s/" + sv.Token; sv.URL != want {
		t.Fatalf("url = %q, want %q", sv.URL, want)
	}
	if sv.ExpiresAt != "" {
		t.Fatalf("expiresAt = %q, want empty for a link with no expiry", sv.ExpiresAt)
	}

	// The public page is reachable without any Authorization header.
	resp, body := doReq(t, http.MethodGet, sv.URL2(e.srv.URL), "",
		map[string]string{"Accept": "text/html"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET share page = %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "vellum spa") {
		t.Fatalf("share page did not serve the app shell: %s", body)
	}
	for header, want := range map[string]string{
		"X-Robots-Tag":     "noindex, nofollow, noarchive",
		"Referrer-Policy":  "no-referrer",
		"Cache-Control":    "no-store",
		"X-Frame-Options":  "DENY",
		"X-Content-Type-O": "", // checked below with its real name
	} {
		if want == "" {
			continue
		}
		if got := resp.Header.Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}

	// The JSON the reader fetches carries the body without frontmatter.
	resp, body = doReq(t, http.MethodGet, sv.URL2(e.srv.URL)+"/note", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET note = %d: %s", resp.StatusCode, body)
	}
	var pn publicNote
	if err := json.Unmarshal(body, &pn); err != nil {
		t.Fatalf("decode note: %v (%s)", err, body)
	}
	if pn.Title != "The plan" || !strings.Contains(pn.Body, "Step one.") {
		t.Fatalf("public note = %+v", pn)
	}
	if strings.Contains(pn.Body, "tags:") || strings.Contains(pn.Body, "---") {
		t.Fatalf("frontmatter leaked into the public body: %q", pn.Body)
	}
	if pn.Name != "plan" {
		t.Fatalf("name = %q, want the file stem", pn.Name)
	}

	// And the markdown itself downloads.
	resp, body = doReq(t, http.MethodGet, sv.URL2(e.srv.URL)+"/raw", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET raw = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/markdown") {
		t.Errorf("Content-Type = %q, want text/markdown", ct)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, `filename="plan.md"`) {
		t.Errorf("Content-Disposition = %q", cd)
	}
	if !strings.Contains(string(body), "Step one.") {
		t.Fatalf("raw body = %q", body)
	}
}

// URL2 rebuilds the link against the test server, since the API hands out
// links on the configured public origin.
func (v shareView) URL2(base string) string { return base + "/s/" + v.Token }

func TestShareLinkNeedsNoAuthButTheRestDoes(t *testing.T) {
	e := newShareServer(t)
	e.writeNote(t, "secret.md", "# Secret\n\nhidden\n")
	sv := e.createShare(t, "secret.md", 0)

	// Anonymous read of the shared note: allowed.
	resp, _ := doReq(t, http.MethodGet, sv.URL2(e.srv.URL)+"/note", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("shared note = %d, want 200", resp.StatusCode)
	}
	// The public routes must not become a way to reach anything else: there
	// is no listing, no sub-resource, and a token that is really a path
	// never gets as far as the store.
	for _, path := range []string{
		"/s/",
		"/s/" + sv.Token + "/other",
		"/s/" + url.PathEscape("../../api/notes"),
		"/s/" + url.PathEscape("../"+sv.Token),
	} {
		resp, _ := doReq(t, http.MethodGet, e.srv.URL+path, "", nil)
		if resp.StatusCode == http.StatusOK {
			t.Errorf("GET %s = 200, want a refusal", path)
		}
	}
}

func TestShareUnknownAndExpiredLookAlike(t *testing.T) {
	e := newShareServer(t)
	e.writeNote(t, "a.md", "# A\n\nbody\n")

	// A token that was never minted.
	fake := strings.Repeat("a", share.TokenLen)
	resp, _ := doReq(t, http.MethodGet, e.srv.URL+"/s/"+fake, "", map[string]string{"Accept": "text/html"})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown token = %d, want 404", resp.StatusCode)
	}

	// A malformed one.
	resp, _ = doReq(t, http.MethodGet, e.srv.URL+"/s/nope", "", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("malformed token = %d, want 404", resp.StatusCode)
	}

	// A revoked one — same answer, so a probe cannot tell them apart.
	sv := e.createShare(t, "a.md", 0)
	resp, _ = doReq(t, http.MethodDelete, e.srv.URL+"/api/shares/"+sv.Token, "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("revoke = %d", resp.StatusCode)
	}
	resp, _ = doReq(t, http.MethodGet, sv.URL2(e.srv.URL), "", map[string]string{"Accept": "text/html"})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("revoked token = %d, want 404", resp.StatusCode)
	}
}

func TestShareOfDeletedNoteIsGone(t *testing.T) {
	e := newShareServer(t)
	e.writeNote(t, "gone.md", "# Gone\n\nbody\n")
	sv := e.createShare(t, "gone.md", 0)

	// Deleting through the API revokes the link outright.
	resp, _ := doReq(t, http.MethodDelete, e.srv.URL+"/api/notes/gone.md", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete = %d", resp.StatusCode)
	}
	resp, _ = doReq(t, http.MethodGet, sv.URL2(e.srv.URL), "", map[string]string{"Accept": "text/html"})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("link to a deleted note = %d, want 404", resp.StatusCode)
	}

	// A note that vanishes behind vellum's back (edited on disk) still has a
	// live share, and that is the 410 case.
	e.writeNote(t, "vanish.md", "# Vanish\n\nbody\n")
	sv2 := e.createShare(t, "vanish.md", 0)
	if err := e.vault.Delete("vanish.md"); err != nil {
		t.Fatal(err)
	}
	resp, body := doReq(t, http.MethodGet, sv2.URL2(e.srv.URL), "", map[string]string{"Accept": "text/html"})
	if resp.StatusCode != http.StatusGone {
		t.Fatalf("link to a note deleted on disk = %d, want 410", resp.StatusCode)
	}
	if !strings.Contains(string(body), "This note is gone") {
		t.Fatalf("410 page = %s", body)
	}
}

func TestShareFollowsAMovedNote(t *testing.T) {
	e := newShareServer(t)
	e.writeNote(t, "inbox/draft.md", "# Draft\n\nbody\n")
	sv := e.createShare(t, "inbox/draft.md", 0)

	resp, body := doReq(t, http.MethodPost, e.srv.URL+"/api/notes/move",
		`{"from":"inbox/draft.md","to":"projects/p/final.md"}`,
		map[string]string{"Content-Type": "application/json"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("move = %d: %s", resp.StatusCode, body)
	}

	// The URL handed out before the move still resolves.
	resp, body = doReq(t, http.MethodGet, sv.URL2(e.srv.URL)+"/note", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("moved note = %d: %s", resp.StatusCode, body)
	}
	var pn publicNote
	if err := json.Unmarshal(body, &pn); err != nil {
		t.Fatal(err)
	}
	if pn.Title != "Draft" {
		t.Fatalf("title = %q", pn.Title)
	}
	if got, ok := e.store.Get(sv.Token); !ok || got.Path != "projects/p/final.md" {
		t.Fatalf("stored path = %+v, %v", got, ok)
	}
}

func TestShareDroppedWithItsFolder(t *testing.T) {
	e := newShareServer(t)
	e.writeNote(t, "projects/doomed/a.md", "# A\n\nbody\n")
	sv := e.createShare(t, "projects/doomed/a.md", 0)

	resp, body := doReq(t, http.MethodDelete, e.srv.URL+"/api/folders/projects/doomed", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete folder = %d: %s", resp.StatusCode, body)
	}
	if _, ok := e.store.Get(sv.Token); ok {
		t.Fatal("a link inside a deleted folder survived")
	}
}

func TestShareCreateValidation(t *testing.T) {
	e := newShareServer(t)
	e.writeNote(t, "a.md", "# A\n\nbody\n")

	cases := []struct {
		name, body string
		want       int
	}{
		{"missing note", `{"path":"nope.md"}`, http.StatusNotFound},
		// A path that can never be a note is a bad request rather than a
		// missing one: this endpoint is authenticated, so saying which it
		// was discloses nothing and tells the caller what to fix.
		{"not markdown", `{"path":"a.txt"}`, http.StatusBadRequest},
		{"traversal", `{"path":"../../etc/passwd"}`, http.StatusBadRequest},
		{"ttl too short", `{"path":"a.md","expiresIn":60}`, http.StatusBadRequest},
		{"ttl too long", `{"path":"a.md","expiresIn":40000000}`, http.StatusBadRequest},
		{"garbage", `not json`, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, body := doReq(t, http.MethodPost, e.srv.URL+"/api/shares", tc.body,
				map[string]string{"Content-Type": "application/json"})
			if resp.StatusCode != tc.want {
				t.Fatalf("status = %d, want %d (%s)", resp.StatusCode, tc.want, body)
			}
		})
	}
	if e.store.Len() != 0 {
		t.Fatalf("a rejected request still minted %d link(s)", e.store.Len())
	}
}

func TestShareExpiry(t *testing.T) {
	e := newShareServer(t)
	e.writeNote(t, "a.md", "# A\n\nbody\n")
	sv := e.createShare(t, "a.md", int64((48 * time.Hour).Seconds()))
	if sv.ExpiresAt == "" || sv.ExpiresIn == "" {
		t.Fatalf("share = %+v, want an expiry", sv)
	}
	when, err := time.Parse(time.RFC3339, sv.ExpiresAt)
	if err != nil {
		t.Fatalf("expiresAt = %q: %v", sv.ExpiresAt, err)
	}
	if d := time.Until(when); d < 47*time.Hour || d > 49*time.Hour {
		t.Fatalf("expiry is %v away, want ~48h", d)
	}
}

func TestShareListAndViewCount(t *testing.T) {
	e := newShareServer(t)
	e.writeNote(t, "a.md", "# A\n\nbody\n")
	sv := e.createShare(t, "a.md", 0)

	// Two page loads and one raw download count; the JSON the page fetches
	// for itself does not, or every visit would count twice.
	doReq(t, http.MethodGet, sv.URL2(e.srv.URL), "", map[string]string{"Accept": "text/html"})
	doReq(t, http.MethodGet, sv.URL2(e.srv.URL), "", map[string]string{"Accept": "text/html"})
	doReq(t, http.MethodGet, sv.URL2(e.srv.URL)+"/note", "", nil)
	doReq(t, http.MethodGet, sv.URL2(e.srv.URL)+"/raw", "", nil)

	resp, body := doReq(t, http.MethodGet, e.srv.URL+"/api/shares", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/shares = %d", resp.StatusCode)
	}
	var out struct {
		Sharing string      `json:"sharing"`
		BaseURL string      `json:"baseUrl"`
		Shares  []shareView `json:"shares"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	if out.Sharing != "on" || out.BaseURL != "https://vellum.example.com" {
		t.Fatalf("list meta = %+v", out)
	}
	if len(out.Shares) != 1 {
		t.Fatalf("shares = %d, want 1", len(out.Shares))
	}
	if got := out.Shares[0].Views; got != 3 {
		t.Fatalf("views = %d, want 3", got)
	}
	if out.Shares[0].LastView == "" {
		t.Fatal("lastView is empty after a visit")
	}
}

func TestShareRecordedInActivity(t *testing.T) {
	e := newShareServer(t)
	e.writeNote(t, "a.md", "# A\n\nbody\n")
	sv := e.createShare(t, "a.md", 0)
	doReq(t, http.MethodDelete, e.srv.URL+"/api/shares/"+sv.Token, "", nil)

	kinds := map[string]bool{}
	for _, ev := range e.events.Events("user") {
		kinds[ev.Kind] = true
	}
	if !kinds["share"] || !kinds["unshare"] {
		t.Fatalf("activity kinds = %v, want share and unshare", kinds)
	}
}

// With sharing off, nothing about it exists: no endpoints to call and no
// public route to probe.
func TestSharingOffMountsNothing(t *testing.T) {
	dir := t.TempDir()
	v, err := vault.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	ix := vault.NewIndex(v)
	if err := ix.Build(); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(NewRouter("test", Options{
		API: &API{Vault: v, Index: ix, Searcher: vault.NewScanSearcher(v, ix),
			Structure: vault.DefaultStructure()},
		SPA: fstest.MapFS{"index.html": {Data: []byte("<html>spa</html>")}},
	}))
	defer srv.Close()

	fake := strings.Repeat("a", share.TokenLen)
	// /s/<token> has no route of its own, so it falls through to the SPA —
	// which is exactly what any unknown client route does. What must not
	// happen is a share being served.
	resp, body := doReq(t, http.MethodGet, srv.URL+"/s/"+fake+"/note", "", nil)
	if resp.StatusCode == http.StatusOK && strings.Contains(string(body), `"body"`) {
		t.Fatalf("a note was served with sharing off: %s", body)
	}

	for _, call := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/shares", ""},
		{http.MethodPost, "/api/shares", `{"path":"a.md"}`},
		{http.MethodDelete, "/api/shares/" + fake, ""},
	} {
		resp, _ := doReq(t, call.method, srv.URL+call.path, call.body, nil)
		if resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("%s %s = %d with sharing off, want 404/405", call.method, call.path, resp.StatusCode)
		}
	}

	// And /api/version says so, so the workspace hides the button.
	_, body = doReq(t, http.MethodGet, srv.URL+"/api/version", "", nil)
	var ver map[string]any
	if err := json.Unmarshal(body, &ver); err != nil {
		t.Fatal(err)
	}
	if ver["sharing"] != "off" {
		t.Fatalf("version.sharing = %v, want off", ver["sharing"])
	}
}

func TestShareRateLimit(t *testing.T) {
	e := newShareServer(t)
	e.writeNote(t, "a.md", "# A\n\nbody\n")
	sv := e.createShare(t, "a.md", 0)

	limited := false
	for i := 0; i < shareRateLimit+5; i++ {
		resp, _ := doReq(t, http.MethodGet, sv.URL2(e.srv.URL)+"/note", "", nil)
		if resp.StatusCode == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatalf("no 429 after %d requests", shareRateLimit+5)
	}
	// The authenticated surface is a separate mount and must be unaffected.
	resp, _ := doReq(t, http.MethodGet, e.srv.URL+"/api/shares", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/api/shares = %d after the public endpoint was throttled", resp.StatusCode)
	}
}

func TestIPLimiterWindowResets(t *testing.T) {
	now := time.Now()
	l := newIPLimiter(2, time.Minute)
	l.now = func() time.Time { return now }

	for i := range 2 {
		if !l.allow("1.2.3.4") {
			t.Fatalf("request %d was refused inside the limit", i+1)
		}
	}
	if l.allow("1.2.3.4") {
		t.Fatal("third request in the window was allowed")
	}
	if !l.allow("5.6.7.8") {
		t.Fatal("a different address was caught by another's limit")
	}
	now = now.Add(2 * time.Minute)
	if !l.allow("1.2.3.4") {
		t.Fatal("the window did not reset")
	}
}

func TestClientIPTrustsProxyOnlyWhenTold(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/s/x", nil)
	r.RemoteAddr = "10.0.0.1:5555"
	r.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.2")

	if got := clientIP(r, false); got != "10.0.0.1" {
		t.Errorf("untrusted proxy: clientIP = %q, want the socket address", got)
	}
	if got := clientIP(r, true); got != "203.0.113.9" {
		t.Errorf("trusted proxy: clientIP = %q, want the first forwarded address", got)
	}
}
