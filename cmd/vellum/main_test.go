package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// runMainEnv makes the test binary act as the vellum binary, so these tests
// exercise the real main(): flags, exit codes and what reaches stdout.
const runMainEnv = "VELLUM_TEST_RUN_MAIN"

func TestMain(m *testing.M) {
	if os.Getenv(runMainEnv) == "1" {
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// vellumCmd runs main() in a child process with a clean vellum environment:
// nothing from the developer's shell (VELLUM_*, PORT, AUTH_ENABLED, ...)
// leaks in, and HOME points at a temp dir.
func vellumCmd(t *testing.T, env []string, args ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], args...)
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		switch {
		case strings.HasPrefix(key, "VELLUM_"), strings.HasPrefix(key, "SENTRY_"),
			strings.HasPrefix(key, "SMTP_"), key == "PORT", key == "AUTH_ENABLED",
			key == "HOME", key == "USERPROFILE":
			continue
		}
		cmd.Env = append(cmd.Env, kv)
	}
	cmd.Env = append(cmd.Env, runMainEnv+"=1", "HOME="+t.TempDir())
	cmd.Env = append(cmd.Env, env...)
	cmd.Dir = t.TempDir()
	return cmd
}

func exitCode(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		return 0
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		t.Fatalf("run: %v", err)
	}
	return exit.ExitCode()
}

// `vellum mcp /does/not/exist` exits 1 with one sentence naming the path on
// stderr, nothing on stdout, and creates nothing.
func TestStdioMissingVault(t *testing.T) {
	parent := t.TempDir()
	missing := filepath.Join(parent, "nope", "vault")

	var stdout, stderr bytes.Buffer
	cmd := vellumCmd(t, nil, "mcp", missing)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if code := exitCode(t, cmd.Run()); code != 1 {
		t.Fatalf("exit code %d, want 1 (stderr %q)", code, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout must stay clean for JSON-RPC, got %q", stdout.String())
	}
	msg := strings.TrimRight(stderr.String(), "\n")
	if strings.Contains(msg, "\n") || !strings.Contains(msg, missing) {
		t.Fatalf("stderr = %q, want one line naming %s", msg, missing)
	}
	if _, err := os.Stat(filepath.Join(parent, "nope")); !os.IsNotExist(err) {
		t.Fatal("a missing vault must not be created without -init")
	}
}

// Without an argument and VELLUM_VAULT_PATH, stdio mode refuses to guess.
func TestStdioNoVaultGiven(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cmd := vellumCmd(t, nil, "mcp")
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if code := exitCode(t, cmd.Run()); code != 1 {
		t.Fatalf("exit code %d, want 1 (stderr %q)", code, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want nothing", stdout.String())
	}
	if !strings.Contains(stderr.String(), "VELLUM_VAULT_PATH") {
		t.Fatalf("stderr = %q, want a hint how to give the vault", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(cmd.Dir, "vault")); !os.IsNotExist(err) {
		t.Fatal("stdio mode must not create ./vault")
	}
}

// A usage error is reported on stderr, never on stdout.
func TestStdioUsageErrorKeepsStdoutClean(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cmd := vellumCmd(t, nil, "mcp", "/a", "/b")
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if code := exitCode(t, cmd.Run()); code == 0 {
		t.Fatal("two vault paths must fail")
	}
	if stdout.Len() != 0 || stderr.Len() == 0 {
		t.Fatalf("stdout %q / stderr %q: errors belong on stderr", stdout.String(), stderr.String())
	}
}

// `vellum mcp ~/notes` opens $HOME/notes whatever the working directory,
// and stdout carries nothing but JSON-RPC.
func TestStdioSubcommandTildePath(t *testing.T) {
	home := t.TempDir()
	vaultDir := filepath.Join(home, "notes")
	writeNote(t, vaultDir, "hello.md", "# Hello\n")

	cmd := vellumCmd(t, []string{"HOME=" + home, "USERPROFILE=" + home}, "mcp", "~/notes")
	stderr := mcpSession(t, cmd)

	wantRoot, err := filepath.EvalSymlinks(vaultDir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "vault="+wantRoot) || !strings.Contains(stderr, "notes=1") {
		t.Fatalf("startup log must name the absolute vault and its note count, got:\n%s", stderr)
	}
}

// The old spelling keeps working: -mcp-stdio with VELLUM_VAULT_PATH.
func TestStdioLegacyFlagWithEnv(t *testing.T) {
	vaultDir := t.TempDir()
	writeNote(t, vaultDir, "a.md", "a\n")
	writeNote(t, vaultDir, "b.md", "b\n")

	cmd := vellumCmd(t, []string{"VELLUM_VAULT_PATH=" + vaultDir}, "-mcp-stdio")
	stderr := mcpSession(t, cmd)
	if !strings.Contains(stderr, "notes=2") {
		t.Fatalf("expected the env vault with 2 notes, log:\n%s", stderr)
	}
}

// -init creates a missing vault (with the default structure).
func TestStdioInitCreatesVault(t *testing.T) {
	vaultDir := filepath.Join(t.TempDir(), "new")
	cmd := vellumCmd(t, nil, "mcp", vaultDir, "-init")
	mcpSession(t, cmd)
	if _, err := os.Stat(filepath.Join(vaultDir, "inbox")); err != nil {
		t.Fatalf("-init should create the vault and its structure: %v", err)
	}
}

// mcpSession speaks a minimal MCP exchange over the child's stdio and fails
// if any stdout line is not a JSON-RPC message. It returns the stderr log.
func mcpSession(t *testing.T, cmd *exec.Cmd) string {
	t.Helper()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	send := func(msg string) {
		if _, err := io.WriteString(stdin, msg+"\n"); err != nil {
			t.Fatalf("write %s: %v", msg, err)
		}
	}
	send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}`)
	send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	send(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)

	type result struct {
		lines []string
		err   error
	}
	done := make(chan result, 1)
	go func() {
		var r result
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
		for sc.Scan() {
			line := sc.Text()
			r.lines = append(r.lines, line)
			if strings.Contains(line, `"id":2`) {
				_ = stdin.Close()
			}
		}
		r.err = sc.Err()
		done <- r
	}()

	var r result
	select {
	case r = <-done:
	case <-time.After(15 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("no tools/list response; stderr:\n%s", stderr.String())
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("vellum mcp exited with %v; stderr:\n%s", err, stderr.String())
	}
	if r.err != nil {
		t.Fatal(r.err)
	}

	sawTools := false
	for _, line := range r.lines {
		var msg struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Result  json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal([]byte(line), &msg); err != nil || msg.JSONRPC != "2.0" {
			t.Fatalf("stdout line is not a JSON-RPC message: %q", line)
		}
		if string(msg.ID) == "2" && len(msg.Result) > 0 {
			sawTools = true
		}
	}
	if !sawTools {
		t.Fatalf("no tools/list result on stdout: %q", r.lines)
	}
	return stderr.String()
}

func writeNote(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Without VELLUM_HOST the HTTP server listens on 127.0.0.1 only, without the
// loud auth warning; -healthcheck reaches it.
func TestHTTPDefaultLoopback(t *testing.T) {
	port := freePort(t)
	stderr := runHTTP(t, nil, port, func() {
		if ip := networkIP(t); ip != nil {
			if err := dial(ip, port); err == nil {
				t.Errorf("default server accepted a connection on the network address %s", ip)
			}
		}
	})
	if !strings.Contains(stderr, "addr=127.0.0.1:"+strconv.Itoa(port)) {
		t.Errorf("startup log must show the loopback listen address:\n%s", stderr)
	}
	if strings.Contains(stderr, "AUTH IS DISABLED") {
		t.Errorf("loopback-only server should not raise the network warning:\n%s", stderr)
	}
}

// VELLUM_HOST=0.0.0.0 restores listening on every interface (the Docker
// image's setting); -healthcheck still works and the warning names the address.
func TestHTTPAllInterfaces(t *testing.T) {
	port := freePort(t)
	stderr := runHTTP(t, []string{"VELLUM_HOST=0.0.0.0"}, port, func() {
		if ip := networkIP(t); ip != nil {
			if err := dial(ip, port); err != nil {
				t.Errorf("VELLUM_HOST=0.0.0.0 server refused the network address %s: %v", ip, err)
			}
		}
	})
	if !strings.Contains(stderr, "AUTH IS DISABLED") || !strings.Contains(stderr, "0.0.0.0:"+strconv.Itoa(port)) {
		t.Errorf("a network-facing server without auth must warn and name its address:\n%s", stderr)
	}
}

// runHTTP starts vellum in HTTP mode, waits until -healthcheck passes, runs
// check, stops the server and returns its log.
func runHTTP(t *testing.T, env []string, port int, check func()) string {
	t.Helper()
	vaultDir := t.TempDir()
	env = append(env, "PORT="+strconv.Itoa(port), "AUTH_ENABLED=false")
	cmd := vellumCmd(t, env, "-vault", vaultDir)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	stop := func() {
		_ = cmd.Process.Signal(os.Interrupt)
		select {
		case <-exited:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			<-exited
		}
	}

	healthy := false
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); {
		hc := vellumCmd(t, env, "-healthcheck")
		if hc.Run() == nil {
			healthy = true
			break
		}
		select {
		case err := <-exited:
			t.Fatalf("server exited early (%v):\n%s", err, stderr.String())
		case <-time.After(100 * time.Millisecond):
		}
	}
	if !healthy {
		stop()
		t.Fatalf("-healthcheck never passed:\n%s", stderr.String())
	}
	check()
	stop()
	return stderr.String()
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	return ln.Addr().(*net.TCPAddr).Port
}
