// Package notify sends a periodic e-mail digest of open tasks over SMTP.
// It is deliberately small: STARTTLS submission (port 587) via the standard
// library, a plain-text body built from the metadata index, and a background
// ticker. Disabled unless VELLUM_NOTIFY=on and SMTP settings are present.
package notify

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/smtp"
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
	Send(subject, body string) error
}

// smtpMailer submits over STARTTLS (or plain if no auth is configured).
type smtpMailer struct{ cfg Config }

func (m smtpMailer) Send(subject, body string) error {
	addr := net.JoinHostPort(m.cfg.Host, m.cfg.Port)
	var auth smtp.Auth
	if m.cfg.User != "" {
		auth = smtp.PlainAuth("", m.cfg.User, m.cfg.Pass, m.cfg.Host)
	}
	msg := buildMessage(m.cfg.From, m.cfg.To, subject, body)
	return smtp.SendMail(addr, auth, m.cfg.From, m.cfg.To, msg)
}

func buildMessage(from string, to []string, subject, body string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", strings.Join(to, ", "))
	fmt.Fprintf(&b, "Subject: %s\r\n", subject)
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("\r\n")
	b.WriteString(strings.ReplaceAll(body, "\n", "\r\n"))
	return []byte(b.String())
}

// Tasks is the slice of the index the digest needs.
type Tasks interface {
	ListTasks(status, project string) []vault.Entry
}

// Digest builds the subject, plain-text body and open-task count.
func Digest(ix Tasks, publicURL string) (subject, body string, count int) {
	inprog := ix.ListTasks("in-progress", "")
	backlog := ix.ListTasks("backlog", "")
	count = len(inprog) + len(backlog)

	var b strings.Builder
	if count == 0 {
		return "Vellum — all clear", "No open tasks in your vault. Nice and tidy.\n", 0
	}
	fmt.Fprintf(&b, "You have %s open in your vault.\n\n", plural(count, "task", "tasks"))
	if len(inprog) > 0 {
		fmt.Fprintf(&b, "In progress (%d):\n", len(inprog))
		for _, e := range inprog {
			fmt.Fprintf(&b, "  • %s\n", e.Title)
		}
		b.WriteString("\n")
	}
	if len(backlog) > 0 {
		fmt.Fprintf(&b, "Backlog (%d):\n", len(backlog))
		for _, e := range backlog {
			fmt.Fprintf(&b, "  • %s\n", e.Title)
		}
		b.WriteString("\n")
	}
	if publicURL != "" {
		fmt.Fprintf(&b, "Open your vault: %s\n", strings.TrimRight(publicURL, "/"))
	}
	return fmt.Sprintf("Vellum digest — %s open", plural(count, "task", "tasks")), b.String(), count
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

// Loop sends a digest every cfg.Interval until ctx is cancelled.
func (n *Notifier) Loop(ctx context.Context) {
	t := time.NewTicker(n.cfg.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			n.SendDigest()
		}
	}
}

// SendDigest builds and sends one digest now.
func (n *Notifier) SendDigest() {
	subject, body, count := Digest(n.tasks, n.cfg.PublicURL)
	if err := n.mailer.Send(subject, body); err != nil {
		if n.log != nil {
			n.log.Error("digest send failed", "error", err)
		}
		return
	}
	if n.log != nil {
		n.log.Info("digest sent", "tasks", count, "to", strings.Join(n.cfg.To, ","))
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
