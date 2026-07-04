package notify

import (
	"strings"
	"testing"

	"github.com/freema/vellum/internal/vault"
)

type fakeTasks struct{ byStatus map[string][]vault.Entry }

func (f fakeTasks) ListTasks(status, _ string) []vault.Entry { return f.byStatus[status] }

type fakeMailer struct {
	subject, body string
	called        int
}

func (m *fakeMailer) Send(subject, body string) error {
	m.called++
	m.subject, m.body = subject, body
	return nil
}

func TestDigestWithTasks(t *testing.T) {
	ft := fakeTasks{byStatus: map[string][]vault.Entry{
		"in-progress": {{Title: "Ship SPA"}},
		"backlog":     {{Title: "Write docs"}, {Title: "Dark mode"}},
	}}
	subject, body, count := Digest(ft, "https://vellum.example/")
	if count != 3 {
		t.Fatalf("count = %d, want 3", count)
	}
	if !strings.Contains(subject, "3 tasks") {
		t.Errorf("subject = %q", subject)
	}
	for _, want := range []string{"Ship SPA", "Write docs", "Dark mode", "In progress (1)", "Backlog (2)", "https://vellum.example"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q:\n%s", want, body)
		}
	}
}

func TestDigestAllClear(t *testing.T) {
	ft := fakeTasks{byStatus: map[string][]vault.Entry{}}
	subject, _, count := Digest(ft, "")
	if count != 0 || !strings.Contains(subject, "all clear") {
		t.Fatalf("got subject=%q count=%d", subject, count)
	}
}

func TestSendDigest(t *testing.T) {
	fm := &fakeMailer{}
	n := &Notifier{
		mailer: fm,
		tasks:  fakeTasks{byStatus: map[string][]vault.Entry{"backlog": {{Title: "X"}}}},
		cfg:    Config{To: []string{"me@example.com"}},
	}
	n.SendDigest()
	if fm.called != 1 {
		t.Fatalf("mailer called %d times, want 1", fm.called)
	}
	if !strings.Contains(fm.body, "X") {
		t.Errorf("body = %q", fm.body)
	}
}

func TestConfigValid(t *testing.T) {
	base := Config{Enabled: true, Host: "smtp.example", From: "a@b.c", To: []string{"x@y.z"}}
	if !base.Valid() {
		t.Fatal("expected valid")
	}
	bad := base
	bad.To = nil
	if bad.Valid() {
		t.Fatal("missing To should be invalid")
	}
	off := base
	off.Enabled = false
	if off.Valid() {
		t.Fatal("disabled should be invalid")
	}
}
