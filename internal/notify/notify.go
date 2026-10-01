// Package notify sends a periodic e-mail digest of open tasks over SMTP.
// It is deliberately small: STARTTLS submission (port 587) via the standard
// library, a plain-text and an HTML body built from the metadata index, and a
// background ticker. Disabled unless VELLUM_NOTIFY=on and SMTP settings are
// present.
package notify

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/smtp"
	"net/textproto"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/freema/vellum/internal/vault"
)

// Config holds SMTP settings and the digest cadence.
type Config struct {
	Enabled   bool
	Host      string
	Port      string
	User      string
	Pass      string
	From      string
	To        []string
	Interval  time.Duration
	PublicURL string
}

// FromEnv reads the SMTP configuration from the environment.
func FromEnv(publicURL string) Config {
	interval := 24 * time.Hour
	if raw := os.Getenv("VELLUM_NOTIFY_INTERVAL"); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			interval = d
		}
	}
	var to []string
	for _, s := range strings.Split(os.Getenv("SMTP_TO"), ",") {
		if s = strings.TrimSpace(s); s != "" {
			to = append(to, s)
		}
	}
	return Config{
		Enabled:   envBool("VELLUM_NOTIFY"),
		Host:      os.Getenv("SMTP_HOST"),
		Port:      envOr("SMTP_PORT", "587"),
		User:      os.Getenv("SMTP_USER"),
		Pass:      os.Getenv("SMTP_PASS"),
		From:      envOr("SMTP_FROM", os.Getenv("SMTP_USER")),
		To:        to,
		Interval:  interval,
		PublicURL: publicURL,
	}
}

// Valid reports whether enough is set to actually send mail.
func (c Config) Valid() bool {
	return c.Enabled && c.Host != "" && c.From != "" && len(c.To) > 0
}

// Mailer sends one message. Abstracted so the digest logic is testable.
type Mailer interface {
	Send(m Message) error
}

// smtpMailer submits over STARTTLS (or plain if no auth is configured).
type smtpMailer struct{ cfg Config }

func (m smtpMailer) Send(msg Message) error {
	addr := net.JoinHostPort(m.cfg.Host, m.cfg.Port)
	var auth smtp.Auth
	if m.cfg.User != "" {
		auth = smtp.PlainAuth("", m.cfg.User, m.cfg.Pass, m.cfg.Host)
	}
	raw, err := buildMessage(m.cfg.From, m.cfg.To, msg, time.Now())
	if err != nil {
		return err
	}
	return smtp.SendMail(addr, auth, m.cfg.From, m.cfg.To, raw)
}

// buildMessage renders msg as multipart/alternative: the plain-text part
// first, the HTML part last (the one clients prefer). Both parts are
// quoted-printable, so long lines and UTF-8 survive any relay, and the
// subject is RFC 2047 encoded when it is not plain ASCII.
func buildMessage(from string, to []string, msg Message, now time.Time) ([]byte, error) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	parts := []struct{ ctype, content string }{
		{"text/plain; charset=UTF-8", msg.Text},
		{"text/html; charset=UTF-8", msg.HTML},
	}
	for _, p := range parts {
		if p.content == "" {
			continue
		}
		w, err := mw.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {p.ctype},
			"Content-Transfer-Encoding": {"quoted-printable"},
		})
		if err != nil {
			return nil, err
		}
		qp := quotedprintable.NewWriter(w)
		if _, err := qp.Write([]byte(p.content)); err != nil {
			return nil, err
		}
		if err := qp.Close(); err != nil {
			return nil, err
		}
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}

	var b bytes.Buffer
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", strings.Join(to, ", "))
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("UTF-8", msg.Subject))
	fmt.Fprintf(&b, "Date: %s\r\n", now.Format(time.RFC1123Z))
	fmt.Fprintf(&b, "Message-ID: %s\r\n", messageID(from))
	b.WriteString("MIME-Version: 1.0\r\n")
	fmt.Fprintf(&b, "Content-Type: multipart/alternative; boundary=%q\r\n", mw.Boundary())
	b.WriteString("\r\n")
	b.Write(body.Bytes())
	return b.Bytes(), nil
}

// messageID returns a unique Message-ID on the sender's domain. Mail
// without one is scored as more likely spam.
func messageID(from string) string {
	domain := "vellum.invalid"
	if at := strings.LastIndex(from, "@"); at >= 0 {
		if d := strings.Trim(from[at+1:], "> "); d != "" {
			domain = d
		}
	}
	var r [12]byte
	_, _ = rand.Read(r[:])
	return "<" + hex.EncodeToString(r[:]) + "@" + domain + ">"
}

// Tasks is the slice of the index the digest needs.
type Tasks interface {
	ListTasks(status, project string) []vault.Entry
}

// Notifier runs the periodic digest.
type Notifier struct {
	mailer Mailer
	tasks  Tasks
	cfg    Config
	log    *slog.Logger
}

// New builds a Notifier that mails via SMTP using cfg.
func New(cfg Config, tasks Tasks, log *slog.Logger) *Notifier {
	return &Notifier{mailer: smtpMailer{cfg: cfg}, tasks: tasks, cfg: cfg, log: log}
}

// Loop sends a digest shortly after boot (to confirm the setup) and then
// every cfg.Interval until ctx is cancelled.
func (n *Notifier) Loop(ctx context.Context) {
	first := time.NewTimer(30 * time.Second)
	defer first.Stop()
	t := time.NewTicker(n.cfg.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-first.C:
			n.SendDigest()
		case <-t.C:
			n.SendDigest()
		}
	}
}

// SendDigest builds and sends one digest now. Tasks done within the last
// interval are listed as well.
func (n *Notifier) SendDigest() {
	msg := Digest(n.tasks, n.cfg.PublicURL, time.Now(), n.cfg.Interval)
	if err := n.mailer.Send(msg); err != nil {
		if n.log != nil {
			n.log.Error("digest send failed", "error", err)
		}
		return
	}
	if n.log != nil {
		n.log.Info("digest sent", "tasks", msg.Open, "to", strings.Join(n.cfg.To, ","))
	}
}

func envBool(key string) bool {
	switch os.Getenv(key) {
	case "1", "true", "TRUE", "True", "yes", "on":
		return true
	}
	return false
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}
