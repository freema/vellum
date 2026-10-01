package notify

import (
	"bytes"
	_ "embed"
	"fmt"
	"html/template"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/freema/vellum/internal/vault"
)

// maxPerSection caps how many tasks one section lists; the rest is a
// "+N more" link into the workspace, so a large backlog stays a short mail.
const maxPerSection = 10

// Message is one rendered digest: a plain-text and an HTML body of the same
// content.
type Message struct {
	Subject string
	Text    string
	HTML    string
	Open    int // open tasks (in progress + backlog)
}

//go:embed digest.html
var digestHTML string

var digestTmpl = template.Must(template.New("digest").Parse(digestHTML))

// statusColors are the DESIGN.md status tokens: dot, soft background, text.
var statusColors = map[string][3]string{
	"in-progress": {"#C08A3E", "#F7EEDD", "#976A28"},
	"backlog":     {"#9A938A", "#F3F1EC", "#6E6A62"},
	"done":        {"#5F8D5F", "#E9F0E7", "#4A714A"},
}

type digestTask struct {
	Title  string
	Folder string // parent directory, "" at the vault root
	URL    string // deep link into the workspace, "" without a public URL
	Age    string // "updated 3 days ago"
}

type digestSection struct {
	Label string
	Kind  string // in-progress | backlog | done
	Total int
	Dot   string // status colours from DESIGN.md
	Soft  string
	Ink   string
	Tasks []digestTask
	More  int
	URL   string // the workspace filtered to this section
}

type digestData struct {
	Subject   string
	Preheader string
	Headline  string
	Summary   string
	Date      string
	Sections  []digestSection
	VaultURL  string
	Host      string
	Every     string
}

// Digest builds the digest of open tasks, plus the tasks marked done within
// the last window (normally the digest interval). Tasks are listed most
// recently updated first.
func Digest(ix Tasks, publicURL string, now time.Time, window time.Duration) Message {
	base := strings.TrimRight(publicURL, "/")
	inprog := ix.ListTasks("in-progress", "")
	backlog := ix.ListTasks("backlog", "")
	var done []vault.Entry
	for _, e := range ix.ListTasks("done", "") {
		if now.Sub(time.Unix(e.ModTime, 0)) <= window {
			done = append(done, e)
		}
	}
	open := len(inprog) + len(backlog)

	d := digestData{
		Date:     now.Format("Monday, 2 January"),
		VaultURL: base,
		Every:    humanDuration(window),
	}
	if u, err := url.Parse(base); err == nil {
		d.Host = u.Host
	}
	if len(inprog) > 0 {
		d.Sections = append(d.Sections, section("In progress", "in-progress", inprog, base, now))
	}
	if len(backlog) > 0 {
		d.Sections = append(d.Sections, section("Backlog", "backlog", backlog, base, now))
	}
	if len(done) > 0 {
		d.Sections = append(d.Sections, section("Done in the last "+humanDuration(window), "done", done, base, now))
	}

	var counts []string
	if len(inprog) > 0 {
		counts = append(counts, fmt.Sprintf("%d in progress", len(inprog)))
	}
	if len(backlog) > 0 {
		counts = append(counts, fmt.Sprintf("%d in backlog", len(backlog)))
	}
	if open == 0 {
		d.Subject = "Vellum digest · all clear"
		d.Headline = "All clear"
		d.Summary = "No open tasks in your vault."
	} else {
		d.Subject = "Vellum digest · " + strings.Join(counts, ", ")
		d.Headline = plural(open, "open task", "open tasks")
		d.Summary = strings.Join(counts, " · ")
	}
	if len(done) > 0 {
		d.Summary += fmt.Sprintf(" · %d done", len(done))
	}
	d.Preheader = d.Summary

	var html bytes.Buffer
	if err := digestTmpl.Execute(&html, d); err != nil {
		// The template is static and its data is plain strings, so this
		// only fails on a programming error; the text part still goes out.
		html.Reset()
	}
	return Message{Subject: d.Subject, Text: digestText(d), HTML: html.String(), Open: open}
}

func section(label, kind string, entries []vault.Entry, base string, now time.Time) digestSection {
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].ModTime > entries[j].ModTime })
	c := statusColors[kind]
	s := digestSection{Label: label, Kind: kind, Total: len(entries), Dot: c[0], Soft: c[1], Ink: c[2]}
	if base != "" {
		s.URL = base + "/?type=task&status=" + kind
	}
	for i, e := range entries {
		if i == maxPerSection {
			s.More = len(entries) - maxPerSection
			break
		}
		t := digestTask{Title: e.Title, Age: "updated " + ago(now.Sub(time.Unix(e.ModTime, 0)))}
		if t.Title == "" {
			t.Title = e.Path
		}
		if i := strings.LastIndex(e.Path, "/"); i > 0 {
			t.Folder = e.Path[:i]
		}
		if base != "" {
			t.URL = base + "/n/" + escapePath(e.Path)
		}
		s.Tasks = append(s.Tasks, t)
	}
	return s
}

func digestText(d digestData) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n%s\n\n", d.Headline, d.Summary)
	for _, s := range d.Sections {
		fmt.Fprintf(&b, "%s (%d)\n", s.Label, s.Total)
		for _, t := range s.Tasks {
			fmt.Fprintf(&b, "  • %s\n", t.Title)
			meta := t.Age
			if t.Folder != "" {
				meta = t.Folder + " · " + meta
			}
			fmt.Fprintf(&b, "    %s\n", meta)
			if t.URL != "" {
				fmt.Fprintf(&b, "    %s\n", t.URL)
			}
		}
		if s.More > 0 {
			fmt.Fprintf(&b, "  + %d more", s.More)
			if s.URL != "" {
				fmt.Fprintf(&b, ": %s", s.URL)
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	if d.VaultURL != "" {
		fmt.Fprintf(&b, "Open your vault: %s\n\n", d.VaultURL)
	}
	fmt.Fprintf(&b, "-- \nSent by vellum every %s. Set VELLUM_NOTIFY=off to stop these emails.\n", d.Every)
	return b.String()
}

// escapePath percent-encodes each segment of a vault path for a /n/ link.
func escapePath(p string) string {
	parts := strings.Split(p, "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	return strings.Join(parts, "/")
}

// ago renders a duration as a coarse relative age.
func ago(d time.Duration) string {
	switch days := int(d.Hours() / 24); {
	case d < time.Hour:
		return "just now"
	case d < 24*time.Hour:
		return plural(int(d.Hours()), "hour", "hours") + " ago"
	case days == 1:
		return "yesterday"
	case days < 14:
		return fmt.Sprintf("%d days ago", days)
	case days < 60:
		return fmt.Sprintf("%d weeks ago", days/7)
	case days < 730:
		return fmt.Sprintf("%d months ago", days/30)
	default:
		return fmt.Sprintf("%d years ago", days/365)
	}
}

// humanDuration renders the digest window: "24 hours", "7 days", "hour".
func humanDuration(d time.Duration) string {
	switch {
	case d == time.Hour:
		return "hour"
	case d%(24*time.Hour) == 0 && d > 24*time.Hour:
		return plural(int(d/(24*time.Hour)), "day", "days")
	case d%time.Hour == 0:
		return plural(int(d/time.Hour), "hour", "hours")
	default:
		return d.String()
	}
}
