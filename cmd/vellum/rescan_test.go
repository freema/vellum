package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// stdioPeer is a running `vellum mcp <vault>` child driven over JSON-RPC.
type stdioPeer struct {
	t      *testing.T
	stdin  io.WriteCloser
	lines  chan string
	stderr *lockedBuffer
	nextID int
}

// lockedBuffer is a bytes.Buffer the child's stderr copier and the test
// can share.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func startPeer(t *testing.T, vaultDir string, env ...string) *stdioPeer {
	t.Helper()
	cmd := vellumCmd(t, env, "mcp", vaultDir)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	p := &stdioPeer{t: t, stdin: stdin, lines: make(chan string, 64), stderr: &lockedBuffer{}, nextID: 1}
	cmd.Stderr = p.stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	})
	go func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
		for sc.Scan() {
			p.lines <- sc.Text()
		}
		close(p.lines)
	}()

	p.request("initialize", `{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"0"}}`)
	p.send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	return p
}

func (p *stdioPeer) send(msg string) {
	p.t.Helper()
	if _, err := io.WriteString(p.stdin, msg+"\n"); err != nil {
		p.t.Fatalf("write %s: %v", msg, err)
	}
}

// request sends one JSON-RPC request and returns its result, skipping
// notifications and anything else on the way.
func (p *stdioPeer) request(method, params string) json.RawMessage {
	p.t.Helper()
	p.nextID++
	id := p.nextID
	p.send(fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":%q,"params":%s}`, id, method, params))
	deadline := time.After(15 * time.Second)
	for {
		select {
		case line, ok := <-p.lines:
			if !ok {
				p.t.Fatalf("%s: vellum exited; stderr:\n%s", method, p.stderr.String())
			}
			var msg struct {
				ID     *int            `json:"id"`
				Result json.RawMessage `json:"result"`
				Error  json.RawMessage `json:"error"`
			}
			if err := json.Unmarshal([]byte(line), &msg); err != nil {
				p.t.Fatalf("stdout line is not JSON-RPC: %q", line)
			}
			if msg.ID == nil || *msg.ID != id {
				continue
			}
			if len(msg.Error) > 0 {
				p.t.Fatalf("%s failed: %s", method, msg.Error)
			}
			return msg.Result
		case <-deadline:
			p.t.Fatalf("%s: no response; stderr:\n%s", method, p.stderr.String())
		}
	}
}

func (p *stdioPeer) callTool(name string, args map[string]any) json.RawMessage {
	p.t.Helper()
	raw, err := json.Marshal(map[string]any{"name": name, "arguments": args})
	if err != nil {
		p.t.Fatal(err)
	}
	return p.request("tools/call", string(raw))
}

func (p *stdioPeer) searchPaths(query string) []string {
	p.t.Helper()
	var res struct {
		StructuredContent struct {
			Results []struct{ Path string } `json:"results"`
		} `json:"structuredContent"`
	}
	if err := json.Unmarshal(p.callTool("search_notes", map[string]any{"query": query}), &res); err != nil {
		p.t.Fatal(err)
	}
	var out []string
	for _, r := range res.StructuredContent.Results {
		out = append(out, r.Path)
	}
	return out
}

// NSO-562 acceptance: Claude Code and Claude Desktop each start their own
// stdio process over one vault. A note written through one is found by the
// other's search once the rescan interval has passed, without a restart.
func TestTwoStdioProcessesSeeEachOthersWrites(t *testing.T) {
	vaultDir := t.TempDir()
	writeNote(t, vaultDir, "seed.md", "# Seed\n")
	const every = 300 * time.Millisecond
	env := "VELLUM_RESCAN=" + every.String()
	a := startPeer(t, vaultDir, env)
	b := startPeer(t, vaultDir, env)

	if got := b.searchPaths("wombat"); len(got) != 0 {
		t.Fatalf("b finds %v before anything was written", got)
	}
	a.callTool("write_note", map[string]any{
		"path": "inbox/from-a.md", "content": "# From A\n\nThe wombat note, #shared\n",
	})
	if got := a.searchPaths("wombat"); len(got) != 1 {
		t.Fatalf("the writer does not find its own note: %v", got)
	}

	time.Sleep(every + every/2)
	if got := b.searchPaths("wombat"); strings.Join(got, ",") != "inbox/from-a.md" {
		t.Fatalf("b's search after the interval = %v, want inbox/from-a.md; b's log:\n%s", got, b.stderr.String())
	}
}

// VELLUM_RESCAN=off keeps today's behaviour: the second process sees the
// note only after a restart.
func TestStdioRescanOff(t *testing.T) {
	vaultDir := t.TempDir()
	writeNote(t, vaultDir, "seed.md", "# Seed\n")
	a := startPeer(t, vaultDir, "VELLUM_RESCAN=50ms")
	b := startPeer(t, vaultDir, "VELLUM_RESCAN=off")

	a.callTool("write_note", map[string]any{"path": "inbox/x.md", "content": "# X\n\nplatypus\n"})
	time.Sleep(200 * time.Millisecond)
	if got := b.searchPaths("platypus"); len(got) != 0 {
		t.Fatalf("rescan off, yet b found %v", got)
	}
	if !strings.Contains(b.stderr.String(), "rescan is off") {
		t.Errorf("startup log should say the rescan is off:\n%s", b.stderr.String())
	}
}
