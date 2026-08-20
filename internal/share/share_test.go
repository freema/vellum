package share

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// openAt returns a store over a fresh vault directory with a controllable clock.
func openAt(t *testing.T, root string, now *time.Time) *Store {
	t.Helper()
	s, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if now != nil {
		s.now = func() time.Time { return *now }
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestCreateAndGet(t *testing.T) {
	dir := t.TempDir()
	s := openAt(t, dir, nil)

	sh, err := s.Create("inbox/note.md", 0)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(sh.Token) != TokenLen {
		t.Fatalf("token length = %d, want %d", len(sh.Token), TokenLen)
	}
	if !PlausibleToken(sh.Token) {
		t.Fatalf("minted token %q is not plausible", sh.Token)
	}
	if sh.ExpiresAt != nil {
		t.Fatalf("ttl 0 must not expire, got %v", sh.ExpiresAt)
	}

	got, ok := s.Get(sh.Token)
	if !ok || got.Path != "inbox/note.md" {
		t.Fatalf("Get(%q) = %+v, %v", sh.Token, got, ok)
	}
	if byPath, ok := s.ForPath("inbox/note.md"); !ok || byPath.Token != sh.Token {
		t.Fatalf("ForPath = %+v, %v", byPath, ok)
	}
	if _, ok := s.Get("nope"); ok {
		t.Fatal("unknown token resolved")
	}
}

// Re-sharing a note must not invalidate a URL somebody already has.
func TestCreateIsIdempotentPerPath(t *testing.T) {
	s := openAt(t, t.TempDir(), nil)

	first, err := s.Create("a.md", 0)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	second, err := s.Create("a.md", 48*time.Hour)
	if err != nil {
		t.Fatalf("Create again: %v", err)
	}
	if second.Token != first.Token {
		t.Fatalf("token changed on re-share: %q → %q", first.Token, second.Token)
	}
	if second.ExpiresAt == nil {
		t.Fatal("expiry was not applied on re-share")
	}
	if s.Len() != 1 {
		t.Fatalf("Len = %d, want 1", s.Len())
	}
	// And back to "until revoked".
	third, err := s.Create("a.md", 0)
	if err != nil {
		t.Fatalf("Create third: %v", err)
	}
	if third.ExpiresAt != nil {
		t.Fatalf("expiry was not cleared, got %v", third.ExpiresAt)
	}
}

func TestCreateRejectsBadTTL(t *testing.T) {
	s := openAt(t, t.TempDir(), nil)
	for _, ttl := range []time.Duration{time.Minute, MaxTTL + time.Hour, -time.Hour} {
		if _, err := s.Create("a.md", ttl); !errors.Is(err, ErrInvalidTTL) {
			t.Fatalf("Create(ttl=%v) error = %v, want ErrInvalidTTL", ttl, err)
		}
	}
	if _, err := s.Create("", 0); err == nil {
		t.Fatal("empty path accepted")
	}
}

func TestExpiryHidesShare(t *testing.T) {
	now := time.Now()
	s := openAt(t, t.TempDir(), &now)

	sh, err := s.Create("a.md", 2*time.Hour)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, ok := s.Get(sh.Token); !ok {
		t.Fatal("share missing before expiry")
	}

	now = now.Add(3 * time.Hour)
	if _, ok := s.Get(sh.Token); ok {
		t.Fatal("expired share still resolves")
	}
	if _, ok := s.ForPath("a.md"); ok {
		t.Fatal("expired share still found by path")
	}
	if got := s.List(); len(got) != 0 {
		t.Fatalf("List after expiry = %d shares, want 0", len(got))
	}
}

func TestRevoke(t *testing.T) {
	s := openAt(t, t.TempDir(), nil)
	sh, _ := s.Create("a.md", 0)

	if _, ok := s.Revoke("unknown"); ok {
		t.Fatal("revoking an unknown token reported success")
	}
	if _, ok := s.Revoke(sh.Token); !ok {
		t.Fatal("Revoke reported failure")
	}
	if _, ok := s.Get(sh.Token); ok {
		t.Fatal("revoked token still resolves")
	}
	if _, ok := s.ForPath("a.md"); ok {
		t.Fatal("revoked share still found by path")
	}

	again, _ := s.Create("a.md", 0)
	if again.Token == sh.Token {
		t.Fatal("re-sharing after a revoke reused the dead token")
	}
	if _, ok := s.RevokePath("a.md"); !ok {
		t.Fatal("RevokePath reported failure")
	}
	if s.Len() != 0 {
		t.Fatalf("Len = %d, want 0", s.Len())
	}
}

func TestRevokePrefix(t *testing.T) {
	s := openAt(t, t.TempDir(), nil)
	inside, _ := s.Create("projects/x/plan.md", 0)
	deeper, _ := s.Create("projects/x/sub/deep.md", 0)
	sibling, _ := s.Create("projects/xy/other.md", 0)
	outside, _ := s.Create("inbox/keep.md", 0)

	dropped := s.RevokePrefix("projects/x")
	if len(dropped) != 2 {
		t.Fatalf("dropped %d shares, want 2 (%+v)", len(dropped), dropped)
	}
	for _, tok := range []string{inside.Token, deeper.Token} {
		if _, ok := s.Get(tok); ok {
			t.Fatalf("token %q inside the folder survived", tok)
		}
	}
	// "projects/xy" merely starts with the same characters — it is a
	// different folder and must be untouched.
	if _, ok := s.Get(sibling.Token); !ok {
		t.Fatal("a sibling folder sharing a name prefix was revoked")
	}
	if _, ok := s.Get(outside.Token); !ok {
		t.Fatal("an unrelated share was revoked")
	}
	if got := s.RevokePrefix(""); got != nil {
		t.Fatalf("RevokePrefix(\"\") dropped %d shares — that would be the whole vault", len(got))
	}
}

func TestRenameFollowsTheNote(t *testing.T) {
	s := openAt(t, t.TempDir(), nil)
	sh, _ := s.Create("inbox/draft.md", 0)

	if !s.Rename("inbox/draft.md", "projects/x/final.md") {
		t.Fatal("Rename reported failure")
	}
	got, ok := s.Get(sh.Token)
	if !ok {
		t.Fatal("link died on rename")
	}
	if got.Path != "projects/x/final.md" {
		t.Fatalf("path = %q, want projects/x/final.md", got.Path)
	}
	if _, ok := s.ForPath("inbox/draft.md"); ok {
		t.Fatal("old path still indexed")
	}
	if s.Rename("nothing.md", "else.md") {
		t.Fatal("renaming an unshared note reported success")
	}
}

// Moving a shared note onto another shared note must not leave two shares
// claiming the same path — the overwritten note's link dies with it.
func TestRenameOverDestinationDropsIt(t *testing.T) {
	s := openAt(t, t.TempDir(), nil)
	src, _ := s.Create("a.md", 0)
	dst, _ := s.Create("b.md", 0)

	if !s.Rename("a.md", "b.md") {
		t.Fatal("Rename reported failure")
	}
	if _, ok := s.Get(dst.Token); ok {
		t.Fatal("the overwritten note's link still resolves")
	}
	got, ok := s.Get(src.Token)
	if !ok || got.Path != "b.md" {
		t.Fatalf("moved share = %+v, %v", got, ok)
	}
	if s.Len() != 1 {
		t.Fatalf("Len = %d, want 1", s.Len())
	}
}

func TestPersistsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	s := openAt(t, dir, nil)
	sh, _ := s.Create("inbox/note.md", 24*time.Hour)
	s.RecordView(sh.Token)
	s.RecordView(sh.Token)
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// A link handed out before a deploy must still work after it — this is
	// the whole reason shares are on disk and tokens are not.
	reopened := openAt(t, dir, nil)
	got, ok := reopened.Get(sh.Token)
	if !ok {
		t.Fatal("share did not survive a restart")
	}
	if got.Path != "inbox/note.md" {
		t.Fatalf("path = %q", got.Path)
	}
	if got.Views != 2 {
		t.Fatalf("views = %d, want 2 (Close must flush counters)", got.Views)
	}
	if got.ExpiresAt == nil {
		t.Fatal("expiry lost across restart")
	}
}

func TestExpiredSharesAreDroppedOnLoad(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	s := openAt(t, dir, &now)
	sh, _ := s.Create("a.md", time.Hour)
	live, _ := s.Create("b.md", 0)
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = reopened.Close() }()
	reopened.now = func() time.Time { return now.Add(48 * time.Hour) }

	if _, ok := reopened.Get(sh.Token); ok {
		t.Fatal("an expired share was loaded")
	}
	if _, ok := reopened.Get(live.Token); !ok {
		t.Fatal("a live share was dropped")
	}
}

func TestCorruptFileIsAnError(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, StateDir), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, StateDir, fileName), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Loading zero shares from an unreadable file would silently break every
	// link that was already handed out; the operator has to hear about it.
	if _, err := Open(dir); err == nil {
		t.Fatal("Open accepted a corrupt shares.json")
	}
}

func TestFilePermissionsAndShape(t *testing.T) {
	dir := t.TempDir()
	s := openAt(t, dir, nil)
	if _, err := s.Create("a.md", 0); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if runtime.GOOS != "windows" {
		st, err := os.Stat(filepath.Join(dir, StateDir))
		if err != nil {
			t.Fatal(err)
		}
		if perm := st.Mode().Perm(); perm != 0o700 {
			t.Fatalf("state dir mode = %o, want 700", perm)
		}
		fi, err := os.Stat(filepath.Join(dir, StateDir, fileName))
		if err != nil {
			t.Fatal(err)
		}
		if perm := fi.Mode().Perm(); perm != 0o600 {
			t.Fatalf("shares.json mode = %o, want 600", perm)
		}
	}

	data, err := os.ReadFile(filepath.Join(dir, StateDir, fileName))
	if err != nil {
		t.Fatal(err)
	}
	var f fileFormat
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("shares.json is not valid JSON: %v", err)
	}
	if f.Version != 1 || len(f.Shares) != 1 {
		t.Fatalf("file = %+v, want version 1 with 1 share", f)
	}

	// No temp files left behind by the atomic write.
	entries, err := os.ReadDir(filepath.Join(dir, StateDir))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Fatalf("leftover temp file %q", e.Name())
		}
	}
}

func TestListNewestFirst(t *testing.T) {
	now := time.Now()
	s := openAt(t, t.TempDir(), &now)
	first, _ := s.Create("a.md", 0)
	now = now.Add(time.Minute)
	second, _ := s.Create("b.md", 0)

	list := s.List()
	if len(list) != 2 {
		t.Fatalf("List = %d shares, want 2", len(list))
	}
	if list[0].Token != second.Token || list[1].Token != first.Token {
		t.Fatalf("List is not newest-first: %+v", list)
	}
}

func TestRecordView(t *testing.T) {
	now := time.Now()
	s := openAt(t, t.TempDir(), &now)
	sh, _ := s.Create("a.md", 0)

	s.RecordView(sh.Token)
	s.RecordView("unknown") // must not panic or count
	got, _ := s.Get(sh.Token)
	if got.Views != 1 {
		t.Fatalf("views = %d, want 1", got.Views)
	}
	if got.LastViewAt == nil || !got.LastViewAt.Equal(now) {
		t.Fatalf("lastViewAt = %v, want %v", got.LastViewAt, now)
	}
}

func TestPlausibleToken(t *testing.T) {
	valid, err := newToken()
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]bool{
		valid:            true,
		"":               false,
		"short":          false,
		"../../etc/pass": false,
	}
	// Right length, wrong alphabet.
	bad := ""
	for len(bad) < TokenLen {
		bad += "$"
	}
	cases[bad] = false

	for in, want := range cases {
		if got := PlausibleToken(in); got != want {
			t.Fatalf("PlausibleToken(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}
