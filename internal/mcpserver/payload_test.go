package mcpserver

// Tool payload measurement (NSO-567): the baseline every "lean reads" change
// is judged against. A deterministic generated vault is served over the real
// MCP wire (in-memory transport, no network) and a handful of typical agent
// scenarios are replayed against it. For every tool call the harness counts
// the bytes of the result's structuredContent and of its text blocks
// separately, straight from the JSON-RPC response.
//
// Print the tables (Markdown, ready to paste into a PR or
// docs/payload-baseline.md) with:
//
//	go test -run TestToolPayload -v ./internal/mcpserver
//
// Without -v the test still runs every scenario and checks that the numbers
// are reproducible, so a broken scenario fails CI.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/freema/vellum/internal/vault"
)

// Note modification times are serialized in the local time zone (list_notes,
// read_note). Pinning the package's tests to UTC keeps the byte counts
// identical on a laptop and in CI. Done in init, before any goroutine
// exists, so it cannot race with time.Now.
func init() { time.Local = time.UTC }

// payloadModTime is the fixed mtime of every fixture file: equal mtimes also
// make recency tie-breaks in search deterministic.
var payloadModTime = time.Date(2026, time.January, 5, 9, 0, 0, 0, time.UTC)

// payloadNotes is the number of regular (short) fixture notes; four long
// notes come on top.
const payloadNotes = 300

// payloadFixture describes a generated vault and the facts the scenarios
// need about it.
type payloadFixture struct {
	root     string
	notes    int      // total number of .md files
	longNote string   // the 500+ line note the section/fix scenarios use
	longAll  []string // every long note
	section  string   // heading text of the section holding the target paragraph
	sentence string   // the sentence the small-fix scenario corrects
	fixed    string   // its corrected form
	related  []string // five related notes (same project folder)
}

// payloadRand is splitmix64: tiny, fast and, unlike math/rand, guaranteed
// to produce the same sequence on every Go version.
type payloadRand struct{ s uint64 }

func (r *payloadRand) next() uint64 {
	r.s += 0x9e3779b97f4a7c15
	z := r.s
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

func (r *payloadRand) intn(n int) int         { return int(r.next() % uint64(n)) }
func (r *payloadRand) pick(w []string) string { return w[r.intn(len(w))] }

// payloadArea is one folder family of the fixture: its topic tags and the
// subfolders its notes are spread over ("" is the folder itself).
type payloadArea struct {
	dir  string
	tags []string
	subs []string
}

var payloadAreas = []payloadArea{
	{"projects/rekonstrukce-koupelny", []string{"koupelna", "dlažba", "rozpočet"}, []string{"", "nabídky", "schůzky"}},
	{"projects/zahrada", []string{"zahrada", "rajčata", "sklizeň"}, []string{"", "výsadba", "deník/2026"}},
	{"projects/kniha", []string{"kniha", "kapitola", "postava"}, []string{"", "kapitoly", "postavy"}},
	{"projects/homelab", []string{"server", "nasazení", "zálohování"}, []string{"", "runbooky", "incidenty"}},
	{"areas/finance", []string{"faktura", "daně", "pojištění"}, []string{"", "2026"}},
	{"areas/zdraví", []string{"zdraví", "trénink", "běh"}, []string{""}},
	{"archive/2025/dovolená", []string{"dovolená", "chorvatsko", "cestování"}, []string{""}},
	{"inbox", []string{"nápad", "úkol", "otázka"}, []string{""}},
}

var payloadWords = []string{
	"a", "s", "pro", "na", "do", "který", "když", "protože", "během", "podle",
	"nový", "starý", "důležitý", "další", "první", "poslední", "týden", "měsíc",
	"rok", "plán", "seznam", "poznámka", "otázka", "odpověď", "řešení", "problém",
	"cena", "termín", "dodavatel", "materiál", "práce", "výsledek", "návrh",
	"změna", "kontrola", "přehled", "shrnutí", "harmonogram", "rozpočet", "úkol",
}

var payloadNouns = []string{
	"plán", "termín", "nabídka", "přehled", "poznámky", "seznam", "návrh",
	"kontrola", "shrnutí", "otázky", "změny", "výsledky",
}

// payloadLong are the 500+ line notes. The first one carries the target
// paragraph of the section and small-fix scenarios.
var payloadLong = []struct {
	path, title string
	area        int
}{
	{"projects/rekonstrukce-koupelny/specifikace-rekonstrukce.md", "Specifikace rekonstrukce koupelny", 0},
	{"projects/homelab/runbooky/obnova-ze-zálohy.md", "Obnova ze zálohy", 3},
	{"projects/kniha/kapitoly/rukopis-první-verze.md", "Rukopis, první verze", 2},
	{"areas/finance/2026/daňové-přiznání-podklady.md", "Daňové přiznání, podklady", 4},
}

const (
	payloadSection  = "7.3 Harmonogram pokládky dlažby"
	payloadSentence = "Dodavatel potvrdil, že dlažba dorazí 14. března a pokládka začne hned další den."
	payloadFixed    = "Dodavatel potvrdil, že dlažba dorazí 21. března a pokládka začne hned další den."
)

func capitalize(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(r)) + s[n:]
}

func (r *payloadRand) sentence(topics []string) string {
	n := 6 + r.intn(9)
	words := make([]string, n)
	for i := range words {
		if r.intn(4) == 0 {
			words[i] = r.pick(topics)
		} else {
			words[i] = r.pick(payloadWords)
		}
	}
	return capitalize(strings.Join(words, " ")) + "."
}

// writePayloadVault generates the fixture into root. Same input, same bytes:
// nothing depends on the clock, the map order or math/rand.
func writePayloadVault(t testing.TB, root string) payloadFixture {
	t.Helper()
	r := &payloadRand{s: 567}
	files := map[string]string{}

	// Pass 1: paths, so pass 2 can link to any note.
	type plan struct {
		path, stem, title string
		area              int
	}
	plans := make([]plan, payloadNotes)
	perArea := make([][]int, len(payloadAreas))
	for i := range plans {
		ai := i % len(payloadAreas)
		a := payloadAreas[ai]
		sub := a.subs[(i/len(payloadAreas))%len(a.subs)]
		topic := r.pick(a.tags)
		noun := r.pick(payloadNouns)
		stem := fmt.Sprintf("%s-%s-%03d", topic, noun, i)
		plans[i] = plan{
			path:  path.Join(a.dir, sub, stem+".md"),
			stem:  stem,
			title: fmt.Sprintf("%s %s %d", capitalize(topic), noun, i),
			area:  ai,
		}
		perArea[ai] = append(perArea[ai], i)
	}

	// Pass 2: regular notes, 12 to 40 body lines with headings, tags,
	// wikilinks and the odd inline #tag or task status.
	statuses := []string{vault.StatusBacklog, vault.StatusInProgress, vault.StatusDone}
	for i, p := range plans {
		a := payloadAreas[p.area]
		var b strings.Builder
		b.WriteString("---\n")
		fmt.Fprintf(&b, "title: %s\n", p.title)
		fmt.Fprintf(&b, "tags: [%s, %s]\n", a.tags[0], a.tags[1+r.intn(2)])
		if strings.HasPrefix(a.dir, "projects/") && i%9 == 0 {
			fmt.Fprintf(&b, "type: task\nstatus: %s\n", statuses[(i/9)%3])
		}
		b.WriteString("---\n")
		fmt.Fprintf(&b, "# %s\n\n", p.title)
		lines := 12 + r.intn(29)
		for l := 0; l < lines; l++ {
			switch {
			case l > 0 && l%10 == 0:
				fmt.Fprintf(&b, "\n## %s\n\n", capitalize(r.pick(payloadNouns)))
			case l%4 == 3:
				b.WriteString("\n")
			default:
				s := r.sentence(a.tags)
				if r.intn(12) == 0 {
					s += " #" + r.pick(a.tags)
				}
				b.WriteString(s + "\n")
			}
		}
		b.WriteString("\nSouvisí:")
		peers := perArea[p.area]
		for k := 0; k < 1+r.intn(3); k++ {
			peer := plans[peers[r.intn(len(peers))]]
			if peer.path != p.path {
				fmt.Fprintf(&b, " [[%s]]", peer.stem)
			}
		}
		b.WriteString("\n")
		files[p.path] = b.String()
	}

	// Long notes: 10 chapters x 4 subsections x 3 paragraphs (~600 lines).
	for li, ln := range payloadLong {
		a := payloadAreas[ln.area]
		var b strings.Builder
		fmt.Fprintf(&b, "---\ntitle: %s\ntags: [%s, %s]\n---\n# %s\n\n", ln.title, a.tags[0], a.tags[1], ln.title)
		b.WriteString(r.sentence(a.tags) + "\n" + r.sentence(a.tags) + "\n")
		for ch := 1; ch <= 10; ch++ {
			fmt.Fprintf(&b, "\n## %d. %s %s\n\n", ch, capitalize(r.pick(payloadNouns)), r.pick(a.tags))
			b.WriteString(r.sentence(a.tags) + "\n" + r.sentence(a.tags) + "\n")
			for sub := 1; sub <= 4; sub++ {
				heading := fmt.Sprintf("%d.%d %s %s", ch, sub, capitalize(r.pick(payloadNouns)), r.pick(a.tags))
				target := li == 0 && ch == 7 && sub == 3
				if target {
					heading = payloadSection
				}
				fmt.Fprintf(&b, "\n### %s\n", heading)
				for para := 0; para < 3; para++ {
					b.WriteString("\n")
					for l := 0; l < 3; l++ {
						if target && para == 1 && l == 1 {
							b.WriteString(payloadSentence + "\n")
							continue
						}
						b.WriteString(r.sentence(a.tags) + "\n")
					}
				}
			}
		}
		peers := perArea[ln.area]
		fmt.Fprintf(&b, "\nSouvisí: [[%s]] [[%s]]\n", plans[peers[0]].stem, plans[peers[1]].stem)
		files[ln.path] = b.String()
	}

	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		abs := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(files[p]), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(abs, payloadModTime, payloadModTime); err != nil {
			t.Fatal(err)
		}
	}

	fx := payloadFixture{
		root:     root,
		notes:    len(files),
		longNote: payloadLong[0].path,
		section:  payloadSection,
		sentence: payloadSentence,
		fixed:    payloadFixed,
	}
	for _, ln := range payloadLong {
		fx.longAll = append(fx.longAll, ln.path)
	}
	for _, i := range perArea[1][:5] {
		fx.related = append(fx.related, plans[i].path)
	}
	return fx
}

// ---- wire recording ----

// payloadRecorder keeps the raw JSON-RPC result of the latest response the
// client received. Calls are sequential, so "latest" is the call's result.
type payloadRecorder struct {
	mu   sync.Mutex
	last json.RawMessage
}

func (p *payloadRecorder) set(raw json.RawMessage) {
	p.mu.Lock()
	p.last = append(json.RawMessage(nil), raw...)
	p.mu.Unlock()
}

func (p *payloadRecorder) take() json.RawMessage {
	p.mu.Lock()
	defer p.mu.Unlock()
	raw := p.last
	p.last = nil
	return raw
}

type recordingTransport struct {
	mcp.Transport
	rec *payloadRecorder
}

func (t recordingTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	c, err := t.Transport.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return recordingConn{Connection: c, rec: t.rec}, nil
}

type recordingConn struct {
	mcp.Connection
	rec *payloadRecorder
}

func (c recordingConn) Read(ctx context.Context) (jsonrpc.Message, error) {
	msg, err := c.Connection.Read(ctx)
	if resp, ok := msg.(*jsonrpc.Response); ok && err == nil {
		c.rec.set(resp.Result)
	}
	return msg, err
}

// ---- scenarios ----

// payloadStats are byte counts summed over calls. Args is the JSON of the
// call arguments (what the model has to write); Structured and Text are the
// result's structuredContent and text blocks (what comes back).
type payloadStats struct {
	Calls      int
	Args       int
	Structured int
	Text       int
}

func (s *payloadStats) add(o payloadStats) {
	s.Calls += o.Calls
	s.Args += o.Args
	s.Structured += o.Structured
	s.Text += o.Text
}

// Result is everything the server sends back: both copies.
func (s payloadStats) Result() int { return s.Structured + s.Text }

type payloadRun struct {
	t       *testing.T
	session *mcp.ClientSession
	rec     *payloadRecorder
	deps    Deps
	fx      payloadFixture
	stats   payloadStats
	byTool  map[string]*payloadStats
}

// call invokes a tool, records its byte counts and decodes the structured
// result (or, if a tool returns none, its first text block) into out.
func (r *payloadRun) call(name string, args map[string]any, out any) {
	r.t.Helper()
	argJSON, err := json.Marshal(args)
	if err != nil {
		r.t.Fatal(err)
	}
	r.rec.take()
	res, err := r.session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		r.t.Fatalf("CallTool(%s): %v", name, err)
	}
	var wire struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StructuredContent json.RawMessage `json:"structuredContent"`
	}
	raw := r.rec.take()
	if err := json.Unmarshal(raw, &wire); err != nil {
		r.t.Fatalf("decode %s wire result: %v", name, err)
	}
	if res.IsError {
		r.t.Fatalf("tool %s returned error: %s", name, raw)
	}
	st := payloadStats{Calls: 1, Args: len(argJSON), Structured: len(wire.StructuredContent)}
	firstText := ""
	for _, c := range wire.Content {
		if c.Type == "text" {
			st.Text += len(c.Text)
			if firstText == "" {
				firstText = c.Text
			}
		}
	}
	r.stats.add(st)
	if r.byTool[name] == nil {
		r.byTool[name] = &payloadStats{}
	}
	r.byTool[name].add(st)

	if out == nil {
		return
	}
	src := []byte(wire.StructuredContent)
	if len(src) == 0 {
		src = []byte(firstText)
	}
	if err := json.Unmarshal(src, out); err != nil {
		r.t.Fatalf("decode %s output: %v", name, err)
	}
}

type payloadNote struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Hash    string `json:"hash"`
}

// sectionBody returns the lines under the heading whose text is section, up
// to the next heading of any level: the leaf section patch_note replaces.
func sectionBody(body, section string) (string, bool) {
	lines := strings.Split(body, "\n")
	for i, l := range lines {
		if strings.TrimSpace(strings.TrimLeft(l, "#")) != section || !strings.HasPrefix(l, "#") {
			continue
		}
		end := len(lines)
		for j := i + 1; j < len(lines); j++ {
			if strings.HasPrefix(lines[j], "#") {
				end = j
				break
			}
		}
		return strings.Join(lines[i+1:end], "\n"), true
	}
	return "", false
}

// payloadScenario is one agent task as a sequence of tool calls.
type payloadScenario struct {
	name, desc string
	run        func(r *payloadRun)
}

// payloadScenarios are the typical agent tasks, each expressed with today's
// tools. Since NSO-569 the long-note scenarios read only the part they need
// (outline, section), since NSO-572 orient walks the folder counts instead
// of listing every note; docs/payload-baseline.md has the numbers from
// before. Order matters only for small-fix, which edits the long note and
// therefore runs last.
var payloadScenarios = []payloadScenario{
	{"find-and-read", "search_notes on a two-word query, then read_note on the top hit", func(r *payloadRun) {
		var found searchNotesOut
		r.call("search_notes", map[string]any{"query": "dlažba rozpočet"}, &found)
		if len(found.Results) == 0 {
			r.t.Fatal("find-and-read: search returned nothing")
		}
		r.call("read_note", map[string]any{"path": found.Results[0].Path}, nil)
	}},
	{"long-note-section", "one paragraph out of a 600-line note: read_note outline, then read_note of that section", func(r *payloadRun) {
		var outline struct {
			Headings []string `json:"headings"`
		}
		r.call("read_note", map[string]any{"path": r.fx.longNote, "outline": true}, &outline)
		section := ""
		for _, h := range outline.Headings {
			if strings.HasSuffix(h, "# "+r.fx.section) {
				section = r.fx.section
			}
		}
		if section == "" {
			r.t.Fatalf("long-note-section: %q not in the outline %v", r.fx.section, outline.Headings)
		}
		var note payloadNote
		r.call("read_note", map[string]any{"path": r.fx.longNote, "section": section}, &note)
		if !strings.Contains(note.Content, r.fx.sentence) {
			r.t.Fatal("long-note-section: target paragraph missing")
		}
	}},
	{"orient", "learn the vault structure: list_notes of the root (folders with note counts), then of its largest folder (before NSO-572: one recursive list_notes of every note)", func(r *payloadRun) {
		var root listNotesOut
		r.call("list_notes", map[string]any{}, &root)
		sum, largest := len(root.Notes), vault.DirCount{}
		for _, d := range root.Dirs {
			sum += d.Notes
			if d.Notes > largest.Notes {
				largest = d
			}
		}
		if sum != r.fx.notes || largest.Path == "" {
			r.t.Fatalf("orient: root lists %d notes in %+v, want %d", sum, root.Dirs, r.fx.notes)
		}
		var sub listNotesOut
		r.call("list_notes", map[string]any{"dir": largest.Path}, &sub)
		if len(sub.Dirs) == 0 {
			r.t.Fatalf("orient: %s shows no subfolders", largest.Path)
		}
	}},
	{"read-five-related", "five notes of one project in one read_notes call (before NSO-571: five read_note calls)", func(r *payloadRun) {
		var out readNotesOut
		r.call("read_notes", map[string]any{"paths": r.fx.related}, &out)
		if out.Summary.Read != len(r.fx.related) {
			r.t.Fatalf("read-five-related: read %d of %d notes: %+v", out.Summary.Read, len(r.fx.related), out.Summary)
		}
	}},
	{"where-does-it-go", "suggest_location for a new note", func(r *payloadRun) {
		var out suggestLocationOut
		r.call("suggest_location", map[string]any{
			"content": "---\ntags: [zahrada, rajčata]\n---\n# Odrůdy rajčat na příští rok\n\nSeznam odrůd, které chci vyzkoušet.\n",
		}, &out)
		if len(out.Suggestions) == 0 || !strings.HasPrefix(out.Suggestions[0].Dir, "projects/zahrada") {
			r.t.Fatalf("where-does-it-go: unexpected suggestions %+v", out.Suggestions)
		}
	}},
	{"small-fix", "change one sentence in the long note: read_note of its section, then replace_in_note of that sentence (before NSO-574: patch_note of the whole section)", func(r *payloadRun) {
		before, err := r.deps.Vault.Read(r.fx.longNote)
		if err != nil {
			r.t.Fatal(err)
		}
		var note payloadNote
		r.call("read_note", map[string]any{"path": r.fx.longNote, "section": r.fx.section}, &note)
		if !strings.Contains(note.Content, r.fx.sentence) {
			r.t.Fatalf("small-fix: section %q with the target sentence not found", r.fx.section)
		}
		var out replaceInNoteOut
		r.call("replace_in_note", map[string]any{
			"path":          r.fx.longNote,
			"old_text":      r.fx.sentence,
			"new_text":      r.fx.fixed,
			"expected_hash": note.Hash,
		}, &out)
		after, err := r.deps.Vault.Read(r.fx.longNote)
		if err != nil {
			r.t.Fatal(err)
		}
		if want := strings.Replace(before.Content, r.fx.sentence, r.fx.fixed, 1); after.Content != want {
			r.t.Fatal("small-fix: the note changed beyond the one sentence")
		}
		if out.Hash != after.Hash || !strings.Contains(out.Excerpt, r.fx.fixed) {
			r.t.Fatalf("small-fix: replace_in_note answered %+v", out)
		}
	}},
}

// payloadRow is one line of the report.
type payloadRow struct {
	Name, Desc string
	payloadStats
}

// payloadReport is the outcome of one full replay.
type payloadReport struct {
	Scenarios []payloadRow
	Tools     []payloadRow
}

// runPayload generates a fresh vault, serves it and replays every scenario.
func runPayload(t *testing.T) payloadReport {
	t.Helper()
	return runPayloadWith(t, nil)
}

// runPayloadWith is runPayload on the fixture plus extra notes (path ->
// content), such as folder description notes (NSO-573).
func runPayloadWith(t *testing.T, extra map[string]string) payloadReport {
	t.Helper()
	return runPayloadScenarios(t, extra, payloadScenarios)
}

// runPayloadScenarios replays the given scenarios, in order, on a fresh
// fixture plus extra notes.
func runPayloadScenarios(t *testing.T, extra map[string]string, scenarios []payloadScenario) payloadReport {
	t.Helper()
	fx := writePayloadVault(t, t.TempDir())
	for p, c := range extra {
		abs := filepath.Join(fx.root, filepath.FromSlash(p))
		if err := os.WriteFile(abs, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(abs, payloadModTime, payloadModTime); err != nil {
			t.Fatal(err)
		}
		fx.notes++
	}
	v, err := vault.New(fx.root)
	if err != nil {
		t.Fatal(err)
	}
	ix := vault.NewIndex(v)
	if err := ix.Build(); err != nil {
		t.Fatal(err)
	}
	deps := Deps{
		Vault:     v,
		Index:     ix,
		Searcher:  vault.NewScanSearcher(v, ix),
		Structure: vault.DefaultStructure(),
		Version:   "test",
		Curator:   true, // suggest_location
	}
	server := New(deps)

	st, ct := mcp.NewInMemoryTransports()
	ctx := context.Background()
	serverSession, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = serverSession.Close() }()
	rec := &payloadRecorder{}
	client := mcp.NewClient(&mcp.Implementation{Name: "payload-client", Version: "0"}, nil)
	session, err := client.Connect(ctx, recordingTransport{Transport: ct, rec: rec}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()

	run := &payloadRun{t: t, session: session, rec: rec, deps: deps, fx: fx, byTool: map[string]*payloadStats{}}
	var rep payloadReport
	for _, sc := range scenarios {
		run.stats = payloadStats{}
		sc.run(run)
		rep.Scenarios = append(rep.Scenarios, payloadRow{Name: sc.name, Desc: sc.desc, payloadStats: run.stats})
	}
	for name, s := range run.byTool {
		rep.Tools = append(rep.Tools, payloadRow{Name: name, payloadStats: *s})
	}
	sort.Slice(rep.Tools, func(i, j int) bool { return rep.Tools[i].Name < rep.Tools[j].Name })
	return rep
}

// markdownTable renders rows plus a total line. Tokens are bytes / 4, a
// rough orientation only (Czech text with diacritics tokenizes worse).
func markdownTable(first string, rows []payloadRow) string {
	var b strings.Builder
	fmt.Fprintf(&b, "| %s | Calls | Args B | structuredContent B | Text B | Result B | ~Tokens (Result/4) |\n", first)
	b.WriteString("| --- | ---: | ---: | ---: | ---: | ---: | ---: |\n")
	var total payloadStats
	for _, r := range rows {
		total.add(r.payloadStats)
		fmt.Fprintf(&b, "| %s | %d | %d | %d | %d | %d | %d |\n",
			r.Name, r.Calls, r.Args, r.Structured, r.Text, r.Result(), r.Result()/4)
	}
	fmt.Fprintf(&b, "| **total** | %d | %d | %d | %d | %d | %d |\n",
		total.Calls, total.Args, total.Structured, total.Text, total.Result(), total.Result()/4)
	return b.String()
}

// TestToolPayload replays the scenarios twice on independently generated
// vaults, requires identical numbers and, with -v, prints the report.
func TestToolPayload(t *testing.T) {
	first := runPayload(t)
	if second := runPayload(t); !reflect.DeepEqual(first, second) {
		t.Fatalf("payload numbers are not reproducible:\n%+v\n%+v", first, second)
	}
	for _, r := range first.Scenarios {
		if r.Calls == 0 || r.Result() == 0 {
			t.Errorf("scenario %s measured nothing: %+v", r.Name, r.payloadStats)
		}
	}
	if !testing.Verbose() {
		return
	}
	fmt.Printf("\nTool payload per scenario (generated vault, %d notes):\n\n", payloadNotes+len(payloadLong))
	fmt.Print(markdownTable("Scenario", first.Scenarios))
	fmt.Print("\nScenarios:\n\n")
	for _, r := range first.Scenarios {
		fmt.Printf("- %s: %s\n", r.Name, r.Desc)
	}
	fmt.Print("\nTool payload per tool (all scenarios):\n\n")
	fmt.Print(markdownTable("Tool", first.Tools))
	fmt.Println()
}

// TestPayloadFixture pins the properties the measurement relies on: a few
// hundred notes, 500+ line notes with headings, nested folders, Czech
// diacritics, tags and resolving wikilinks, generated byte-for-byte the same.
func TestPayloadFixture(t *testing.T) {
	digest := func(root string) string {
		h := sha256.New()
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, p)
			_, _ = fmt.Fprintf(h, "%s\x00%s\x00", filepath.ToSlash(rel), data)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return hex.EncodeToString(h.Sum(nil))
	}

	fx := writePayloadVault(t, t.TempDir())
	again := writePayloadVault(t, t.TempDir())
	if digest(fx.root) != digest(again.root) {
		t.Fatal("fixture generation is not deterministic")
	}
	if !reflect.DeepEqual(fx.related, again.related) || fx.notes != again.notes {
		t.Fatal("fixture metadata is not deterministic")
	}

	v, err := vault.New(fx.root)
	if err != nil {
		t.Fatal(err)
	}
	ix := vault.NewIndex(v)
	if err := ix.Build(); err != nil {
		t.Fatal(err)
	}
	if ix.Len() != fx.notes || fx.notes < 200 {
		t.Errorf("indexed %d notes, generated %d, want a few hundred", ix.Len(), fx.notes)
	}

	for _, p := range fx.longAll {
		note, err := v.Read(p)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Count(note.Content, "\n")
		headings := 0
		for _, l := range strings.Split(note.Body, "\n") {
			if strings.HasPrefix(l, "#") {
				headings++
			}
		}
		if lines < 500 || headings < 40 {
			t.Errorf("%s: %d lines, %d headings; want 500+ lines with headings", p, lines, headings)
		}
	}

	deep, accented, linked, tagged := false, false, 0, 0
	for _, e := range ix.All() {
		if strings.Count(e.Path, "/") >= 3 {
			deep = true
		}
		if strings.ContainsAny(e.Path, "áčďéěíňóřšťúůýž") {
			accented = true
		}
		if len(ix.Backlinks(e.Path)) > 0 {
			linked++
		}
		if len(e.Tags) > 0 {
			tagged++
		}
	}
	if !deep || !accented {
		t.Errorf("want nested folders (%v) and diacritics in paths (%v)", deep, accented)
	}
	if linked < fx.notes/4 || tagged != fx.notes {
		t.Errorf("linked notes %d, tagged %d of %d", linked, tagged, fx.notes)
	}
	if len(fx.related) != 5 {
		t.Errorf("related = %v, want five notes", fx.related)
	}
	if tasks := ix.ListTasks("", ""); len(tasks) == 0 {
		t.Error("fixture has no task notes")
	}
}
