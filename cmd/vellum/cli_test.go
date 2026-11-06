package main

import (
	"bytes"
	"errors"
	"flag"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/freema/vellum/internal/config"
)

func TestParseArgs(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want cliOptions
	}{
		{"http default", nil, cliOptions{}},
		{"http with -vault", []string{"-vault", "/v"}, cliOptions{vault: "/v"}},
		{"legacy stdio flag", []string{"-mcp-stdio"}, cliOptions{stdio: true}},
		{"legacy stdio double dash", []string{"--mcp-stdio"}, cliOptions{stdio: true}},
		{"legacy stdio with -vault", []string{"-mcp-stdio", "-vault", "/v"}, cliOptions{stdio: true, vault: "/v"}},
		{"subcommand", []string{"mcp", "/v"}, cliOptions{stdio: true, vault: "/v"}},
		{"subcommand without path", []string{"mcp"}, cliOptions{stdio: true}},
		{"subcommand -vault", []string{"mcp", "-vault", "/v"}, cliOptions{stdio: true, vault: "/v"}},
		{"flag before path", []string{"mcp", "-init", "/v"}, cliOptions{stdio: true, initVault: true, vault: "/v"}},
		{"flag after path", []string{"mcp", "/v", "-init"}, cliOptions{stdio: true, initVault: true, vault: "/v"}},
		{"same path twice", []string{"mcp", "-vault", "/v", "/v"}, cliOptions{stdio: true, vault: "/v"}},
		{"path after --", []string{"mcp", "--", "-odd"}, cliOptions{stdio: true, vault: "-odd"}},
		{"healthcheck", []string{"-healthcheck"}, cliOptions{healthcheck: true}},
		{"version", []string{"-version"}, cliOptions{version: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stderr bytes.Buffer
			got, err := parseArgs(tc.args, &stderr)
			if err != nil {
				t.Fatalf("parseArgs(%q): %v (stderr %q)", tc.args, err, stderr.String())
			}
			if got != tc.want {
				t.Fatalf("parseArgs(%q) = %+v, want %+v", tc.args, got, tc.want)
			}
		})
	}
}

func TestParseArgsErrors(t *testing.T) {
	cases := map[string][]string{
		"positional without mcp": {"/v"},
		"two paths":              {"mcp", "/a", "/b"},
		"path and other -vault":  {"mcp", "-vault", "/a", "/b"},
		"unknown flag":           {"-nope"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			var stderr bytes.Buffer
			if _, err := parseArgs(args, &stderr); err == nil {
				t.Fatalf("parseArgs(%q) succeeded, want an error", args)
			}
			if stderr.Len() == 0 {
				t.Fatal("a usage error must explain itself on stderr")
			}
		})
	}
}

// vellum -h describes both modes and the subcommand.
func TestUsageDescribesModes(t *testing.T) {
	var stderr bytes.Buffer
	_, err := parseArgs([]string{"-h"}, &stderr)
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("-h: err = %v, want flag.ErrHelp", err)
	}
	help := stderr.String()
	for _, want := range []string{
		"vellum mcp", "HTTP", "stdio", "-vault", "-init", "-mcp-stdio",
		"VELLUM_VAULT_PATH", "VELLUM_HOST", "127.0.0.1", "0.0.0.0",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("usage text lacks %q:\n%s", want, help)
		}
	}
}

func TestResolveVaultPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name     string
		arg, env string
		stdio    bool
		want     string
	}{
		{"argument wins over env", "/a", "/b", true, "/a"},
		{"env when no argument", "", "/b", true, "/b"},
		{"http default", "", "", false, filepath.Join(cwd, "vault")},
		{"tilde argument", "~/notes", "", true, filepath.Join(home, "notes")},
		{"tilde env", "", "~/notes", true, filepath.Join(home, "notes")},
		{"bare tilde", "~", "", true, home},
		{"relative made absolute", "rel/v", "", true, filepath.Join(cwd, "rel", "v")},
		{"tilde user untouched", "~bob/v", "", true, filepath.Join(cwd, "~bob", "v")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want, _ := filepath.Abs(filepath.FromSlash(tc.want))
			got, err := resolveVaultPath(tc.arg, tc.env, tc.stdio)
			if err != nil {
				t.Fatalf("resolveVaultPath: %v", err)
			}
			if got != want {
				t.Fatalf("resolveVaultPath(%q, %q) = %q, want %q", tc.arg, tc.env, got, want)
			}
		})
	}
}

// Without an argument and without VELLUM_VAULT_PATH, stdio mode must not
// fall back to a ./vault relative to wherever the MCP client started it.
func TestResolveVaultPathStdioNeedsPath(t *testing.T) {
	if _, err := resolveVaultPath("", "", true); !errors.Is(err, errNoVault) {
		t.Fatalf("err = %v, want errNoVault", err)
	}
}

func TestEnsureVaultDir(t *testing.T) {
	dir := t.TempDir()

	if err := ensureVaultDir(dir, false); err != nil {
		t.Fatalf("existing folder: %v", err)
	}

	missing := filepath.Join(dir, "missing", "vault")
	err := ensureVaultDir(missing, false)
	if err == nil || !strings.Contains(err.Error(), missing) {
		t.Fatalf("missing folder: err = %v, want one naming %s", err, missing)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "missing")); !os.IsNotExist(statErr) {
		t.Fatal("a refused vault path must leave nothing on disk")
	}

	if err := ensureVaultDir(missing, true); err != nil {
		t.Fatalf("create: %v", err)
	}
	if info, err := os.Stat(missing); err != nil || !info.IsDir() {
		t.Fatalf("create did not make the folder: %v", err)
	}

	file := filepath.Join(dir, "note.md")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ensureVaultDir(file, true); err == nil {
		t.Fatal("a file is not a vault folder")
	}
}

func TestListenAddr(t *testing.T) {
	cases := map[string]string{
		"127.0.0.1": "127.0.0.1:8080",
		"0.0.0.0":   "0.0.0.0:8080",
		"::1":       "[::1]:8080",
		"[::1]":     "[::1]:8080",
		"localhost": "localhost:8080",
	}
	for host, want := range cases {
		if got := listenAddr(config.Config{Host: host, Port: "8080"}); got != want {
			t.Errorf("listenAddr(%q) = %q, want %q", host, got, want)
		}
	}
}

func TestHealthcheckURL(t *testing.T) {
	cases := map[string]string{
		"127.0.0.1": "http://127.0.0.1:8080/healthz",
		"0.0.0.0":   "http://127.0.0.1:8080/healthz",
		"::":        "http://127.0.0.1:8080/healthz",
		"":          "http://127.0.0.1:8080/healthz",
		"::1":       "http://[::1]:8080/healthz",
		"10.0.0.5":  "http://10.0.0.5:8080/healthz",
		"localhost": "http://localhost:8080/healthz",
	}
	for host, want := range cases {
		if got := healthcheckURL(config.Config{Host: host, Port: "8080"}); got != want {
			t.Errorf("healthcheckURL(%q) = %q, want %q", host, got, want)
		}
	}
}

// The default listener accepts connections from its own machine only; a
// connection to the machine's network address is refused. VELLUM_HOST=0.0.0.0
// restores listening on every interface.
func TestListenDefaultIsLoopbackOnly(t *testing.T) {
	t.Setenv("VELLUM_HOST", "")
	t.Setenv("PORT", "0")
	cfg := config.Load()

	ln, err := listen(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	go acceptAll(ln)

	tcp := ln.Addr().(*net.TCPAddr)
	if !tcp.IP.Equal(net.IPv4(127, 0, 0, 1)) || !isLoopback(ln.Addr()) {
		t.Fatalf("default listener bound to %s, want 127.0.0.1", ln.Addr())
	}
	port := tcp.Port
	if err := dial(net.IPv4(127, 0, 0, 1), port); err != nil {
		t.Fatalf("same machine must connect: %v", err)
	}
	if ip := networkIP(t); ip != nil {
		if err := dial(ip, port); err == nil {
			t.Fatalf("connection via the network address %s succeeded; the default must be loopback only", ip)
		}
	}
}

func TestListenAllInterfaces(t *testing.T) {
	t.Setenv("VELLUM_HOST", "0.0.0.0")
	t.Setenv("PORT", "0")
	cfg := config.Load()

	ln, err := listen(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	go acceptAll(ln)

	if got, want := boundAddr(cfg, ln), "0.0.0.0:"+strconv.Itoa(ln.Addr().(*net.TCPAddr).Port); got != want {
		t.Errorf("boundAddr = %q, want %q", got, want)
	}
	if isLoopback(ln.Addr()) {
		t.Fatalf("VELLUM_HOST=0.0.0.0 listener %s reported as loopback", ln.Addr())
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := dial(net.IPv4(127, 0, 0, 1), port); err != nil {
		t.Fatalf("loopback must connect: %v", err)
	}
	if ip := networkIP(t); ip != nil {
		if err := dial(ip, port); err != nil {
			t.Fatalf("connection via the network address %s failed: %v", ip, err)
		}
	}
}

func acceptAll(ln net.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		_ = c.Close()
	}
}

func dial(ip net.IP, port int) error {
	c, err := net.DialTimeout("tcp", net.JoinHostPort(ip.String(), strconv.Itoa(port)), time.Second)
	if err != nil {
		return err
	}
	return c.Close()
}

// networkIP returns an IPv4 address of this machine that is not loopback,
// standing in for "another machine on the network". Tests that need one
// skip that part when the host has none (e.g. an isolated sandbox).
func networkIP(t *testing.T) net.IP {
	t.Helper()
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		t.Logf("no interface addresses: %v", err)
		return nil
	}
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip := ipnet.IP.To4()
		if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
			continue
		}
		return ip
	}
	t.Log("no non-loopback IPv4 address; skipping the network-side check")
	return nil
}
