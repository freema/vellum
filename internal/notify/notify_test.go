package notify

import (
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/freema/vellum/internal/vault"
)

type fakeTasks struct{ byStatus map[string][]vault.Entry }

func (f fakeTasks) ListTasks(status, _ string) []vault.Entry {
	return append([]vault.Entry(nil), f.byStatus[status]...)
}

type fakeMailer struct {
	msg    Message
	called int
}

func (m *fakeMailer) Send(msg Message) error {
	m.called++
	m.msg = msg
	return nil
}

var now = time.Date(2026, 10, 1, 7, 0, 0, 0, time.UTC)

func task(path, title string, age time.Duration) vault.Entry {
	return vault.Entry{Path: path, Title: title, Type: vault.TypeTask, ModTime: now.Add(-age).Unix()}
}

func TestDigestWithTasks(t *testing.T) {
	ft := fakeTasks{byStatus: map[string][]vault.Entry{
		"in-progress": {task("projects/web/ship-spa.md", "Ship SPA", 2*time.Hour)},
		"backlog": {
			task("inbox/write-docs.md", "Write docs", 3*24*time.Hour),
			task("dark-mode.md", "Dark mode", 40*24*time.Hour),
		},
	}}
	m := Digest(ft, "https://vellum.example/", now, 24*time.Hour)
	if m.Open != 3 {
		t.Fatalf("Open = %d, want 3", m.Open)
	}
	if m.Subject != "Vellum digest · 1 in progress, 2 in backlog" {
		t.Errorf("subject = %q", m.Subject)
	}
	for _, want := range []string{
		"3 open tasks", "In progress (1)", "Backlog (2)",
		"Ship SPA", "projects/web · updated 2 hours ago",
		"https://vellum.example/n/projects/web/ship-spa.md",
		"updated 3 days ago", "updated 5 weeks ago",
		"Open your vault: https://vellum.example\n",
	} {
		if !strings.Contains(m.Text, want) {
			t.Errorf("text missing %q:\n%s", want, m.Text)
		}
	}
	if strings.Index(m.Text, "Write docs") > strings.Index(m.Text, "Dark mode") {
		t.Error("backlog should list the most recently updated task first")
	}
	for _, want := range []string{
		`href="https://vellum.example/n/projects/web/ship-spa.md"`,
		`href="https://vellum.example"`, "Ship SPA", "#C08A3E", "vellum.example",
	} {
		if !strings.Contains(m.HTML, want) {
			t.Errorf("html missing %q", want)
		}
	}
}

func TestDigestAllClear(t *testing.T) {
	m := Digest(fakeTasks{}, "", now, 24*time.Hour)
	if m.Open != 0 || m.Subject != "Vellum digest · all clear" {
		t.Fatalf("got subject=%q open=%d", m.Subject, m.Open)
	}
	if !strings.Contains(m.Text, "No open tasks") || strings.Contains(m.Text, "Open your vault") {
		t.Errorf("text = %q", m.Text)
	}
}

func TestDigestListsRecentlyDone(t *testing.T) {
	ft := fakeTasks{byStatus: map[string][]vault.Entry{
		"done": {
			task("a.md", "Closed this morning", 3*time.Hour),
			task("b.md", "Closed last week", 6*24*time.Hour),
		},
	}}
	m := Digest(ft, "", now, 24*time.Hour)
	if !strings.Contains(m.Text, "Done in the last 24 hours (1)") || !strings.Contains(m.Text, "Closed this morning") {
		t.Errorf("recently done task missing:\n%s", m.Text)
	}
	if strings.Contains(m.Text, "Closed last week") {
		t.Error("a task done before the window is listed")
	}
	if m.Open != 0 {
		t.Errorf("done tasks counted as open: %d", m.Open)
	}
}

func TestDigestCapsLongSections(t *testing.T) {
	var backlog []vault.Entry
	for i := range 14 {
		backlog = append(backlog, task(fmt.Sprintf("t%02d.md", i), fmt.Sprintf("Task %02d", i), time.Duration(i)*time.Hour))
	}
	m := Digest(fakeTasks{byStatus: map[string][]vault.Entry{"backlog": backlog}}, "https://v.example", now, 24*time.Hour)
	if got := strings.Count(m.Text, "  • "); got != maxPerSection {
		t.Errorf("listed %d tasks, want %d", got, maxPerSection)
	}
	if !strings.Contains(m.Text, "+ 4 more: https://v.example/?type=task&status=backlog") {
		t.Errorf("missing the more link:\n%s", m.Text)
	}
	if !strings.Contains(m.HTML, "+ 4 more") {
		t.Error("html missing the more link")
	}
}

func TestDigestEscapes(t *testing.T) {
	ft := fakeTasks{byStatus: map[string][]vault.Entry{
		"backlog": {task("projects/čaj a káva/<b>.md", `<script>alert(1)</script>`, time.Hour)},
	}}
	m := Digest(ft, "https://v.example", now, 24*time.Hour)
	if strings.Contains(m.HTML, "<script>alert") {
		t.Error("title is not HTML-escaped")
	}
	if !strings.Contains(m.Text, "https://v.example/n/projects/%C4%8Daj%20a%20k%C3%A1va/%3Cb%3E.md") {
		t.Errorf("path is not escaped in the link:\n%s", m.Text)
	}
}

func TestBuildMessage(t *testing.T) {
	msg := Message{Subject: "Vellum digest · 2 in backlog", Text: "plain ž\n" + strings.Repeat("x", 120), HTML: "<p>html ž</p>"}
	raw, err := buildMessage("Vellum <vellum@example.com>", []string{"me@example.com"}, msg, now)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	if subj, err := new(mime.WordDecoder).DecodeHeader(parsed.Header.Get("Subject")); err != nil || subj != msg.Subject {
		t.Errorf("subject = %q (%v)", subj, err)
	}
	if strings.ContainsRune(parsed.Header.Get("Subject"), '·') {
		t.Error("non-ASCII subject sent raw")
	}
	if id := parsed.Header.Get("Message-ID"); !strings.HasSuffix(id, "@example.com>") {
		t.Errorf("Message-ID = %q", id)
	}
	if _, err := parsed.Header.Date(); err != nil {
		t.Errorf("Date: %v", err)
	}
	mt, params, err := mime.ParseMediaType(parsed.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/alternative" {
		t.Fatalf("Content-Type = %q (%v)", mt, err)
	}
	mr := multipart.NewReader(parsed.Body, params["boundary"])
	var types, bodies []string
	for {
		p, err := mr.NextPart() // decodes quoted-printable
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(p)
		types = append(types, p.Header.Get("Content-Type"))
		bodies = append(bodies, string(b))
	}
	if len(types) != 2 || !strings.HasPrefix(types[0], "text/plain") || !strings.HasPrefix(types[1], "text/html") {
		t.Fatalf("parts = %v", types)
	}
	if bodies[0] != strings.ReplaceAll(msg.Text, "\n", "\r\n") || bodies[1] != msg.HTML {
		t.Errorf("bodies do not round-trip: %q", bodies)
	}
	for _, line := range strings.Split(string(raw), "\r\n") {
		if len(line) > 78 && !strings.HasPrefix(line, "Content-Type: multipart") {
			t.Errorf("line longer than 78 octets: %q", line)
		}
	}
}

func TestSendDigest(t *testing.T) {
	fm := &fakeMailer{}
	n := &Notifier{
		mailer: fm,
		tasks:  fakeTasks{byStatus: map[string][]vault.Entry{"backlog": {task("x.md", "X", time.Hour)}}},
		cfg:    Config{To: []string{"me@example.com"}, Interval: 24 * time.Hour},
	}
	n.SendDigest()
	if fm.called != 1 {
		t.Fatalf("mailer called %d times, want 1", fm.called)
	}
	if !strings.Contains(fm.msg.Text, "X") || !strings.Contains(fm.msg.HTML, "X") {
		t.Errorf("msg = %+v", fm.msg)
	}
}

// TestWriteDigestPreview renders a sample digest to the file named by
// VELLUM_DIGEST_PREVIEW, to look at the e-mail in a browser:
//
//	VELLUM_DIGEST_PREVIEW=/tmp/digest.html go test ./internal/notify -run Preview
func TestWriteDigestPreview(t *testing.T) {
	out := os.Getenv("VELLUM_DIGEST_PREVIEW")
	if out == "" {
		t.Skip("set VELLUM_DIGEST_PREVIEW to write a preview")
	}
	h := time.Hour
	ft := fakeTasks{byStatus: map[string][]vault.Entry{
		"in-progress": {
			task("projects/website/redesign-landing-page.md", "Redesign the landing page", 3*h),
			task("projects/book/chapter-4.md", "Chapter 4 — first draft", 2*24*h),
		},
		"backlog": {
			task("inbox/call-the-plumber.md", "Call the plumber about the boiler", 5*h),
			task("projects/website/write-changelog.md", "Write the changelog for 2.0", 26*h),
			task("projects/book/cover-ideas.md", "Collect cover ideas", 4*24*h),
			task("inbox/tax-return.md", "Prepare the tax return documents", 9*24*h),
			task("projects/garden/raised-beds.md", "Build two raised beds", 16*24*h),
			task("projects/website/newsletter.md", "Set up the newsletter", 20*24*h),
			task("inbox/backup-photos.md", "Back up the 2025 photos", 30*24*h),
			task("projects/book/research-notes.md", "Sort research notes", 45*24*h),
			task("archive/someday/learn-piano.md", "Learn the first piano piece", 80*24*h),
			task("projects/garden/compost.md", "Start a compost", 100*24*h),
			task("inbox/renew-passport.md", "Renew the passport", 120*24*h),
			task("projects/website/analytics.md", "Pick an analytics tool", 200*24*h),
		},
		"done": {task("projects/website/fix-contact-form.md", "Fix the contact form", 6*h)},
	}}
	m := Digest(ft, "https://vellum.example.com", now, 24*h)
	if err := os.WriteFile(out, []byte(m.HTML), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(strings.TrimSuffix(out, ".html")+".txt", []byte(m.Subject+"\n\n"+m.Text), 0o600); err != nil {
		t.Fatal(err)
	}
}
