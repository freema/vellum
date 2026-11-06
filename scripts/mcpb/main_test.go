package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"debug/pe"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// fakeServerEnv makes the test binary act as an MCP server over stdio, so
// listTools can be tested without building vellum.
const fakeServerEnv = "VELLUM_MCPB_FAKE_SERVER"

func TestMain(m *testing.M) {
	switch os.Getenv(fakeServerEnv) {
	case "tools":
		server := mcp.NewServer(&mcp.Implementation{Name: "fake", Version: "0"}, nil)
		type empty struct{}
		handler := func(context.Context, *mcp.CallToolRequest, empty) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{}, nil, nil
		}
		mcp.AddTool(server, &mcp.Tool{Name: "zeta_tool", Description: "Second tool. It has more to say."}, handler)
		mcp.AddTool(server, &mcp.Tool{Name: "alpha_tool", Description: "First tool: one line\nand a second line."}, handler)
		if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
			os.Exit(0) // the client hung up: that is how every session ends
		}
		os.Exit(0)
	case "fail":
		fmt.Fprintln(os.Stderr, "vellum: vault folder /nope does not exist")
		os.Exit(1)
	}
	os.Exit(m.Run())
}

// fakeServer starts the test binary as the fake server in the given mode.
func fakeServer(mode string) func(vault string) (*exec.Cmd, error) {
	return func(vault string) (*exec.Cmd, error) {
		if info, err := os.Stat(vault); err != nil || !info.IsDir() {
			return nil, fmt.Errorf("vault %s is not a folder", vault)
		}
		cmd := exec.Command(os.Args[0], "-test.run=^$")
		cmd.Env = append(os.Environ(), fakeServerEnv+"="+mode)
		return cmd, nil
	}
}

const committedManifest = "../../mcpb/manifest.json"

func readCommittedManifest(t *testing.T) ([]byte, manifest) {
	t.Helper()
	raw, err := os.ReadFile(committedManifest)
	if err != nil {
		t.Fatal(err)
	}
	m, err := parseManifest(raw)
	if err != nil {
		t.Fatalf("%s: %v", committedManifest, err)
	}
	return raw, m
}

// TestCommittedManifest pins what Claude Desktop does with the bundle: ask
// for a folder at install, then start `vellum mcp <folder>` from the binary
// for the current OS.
func TestCommittedManifest(t *testing.T) {
	raw, m := readCommittedManifest(t)
	if err := m.check(); err != nil {
		t.Fatalf("committed manifest fails the build check:\n%v", err)
	}
	if m.ManifestVersion != "0.3" {
		t.Errorf("manifest_version = %q, want 0.3", m.ManifestVersion)
	}

	var full struct {
		Description string `json:"description"`
		LongDesc    string `json:"long_description"`
		Author      struct {
			Name  string `json:"name"`
			Email string `json:"email"`
			URL   string `json:"url"`
		} `json:"author"`
		License    string `json:"license"`
		UserConfig map[string]struct {
			Title       string `json:"title"`
			Description string `json:"description"`
			Default     any    `json:"default"`
		} `json:"user_config"`
		Server struct {
			MCPConfig struct {
				Env map[string]string `json:"env"`
			} `json:"mcp_config"`
		} `json:"server"`
		Tools any `json:"tools"`
	}
	if err := json.Unmarshal(raw, &full); err != nil {
		t.Fatal(err)
	}
	vault := full.UserConfig[vaultKey]
	if vault.Description != "Folder with your markdown notes" {
		t.Errorf("user_config.%s.description = %q, want the install prompt text", vaultKey, vault.Description)
	}
	if vault.Title == "" {
		t.Errorf("user_config.%s has no title", vaultKey)
	}
	if vault.Default != nil {
		t.Errorf("user_config.%s.default = %v: the folder must be picked, never guessed", vaultKey, vault.Default)
	}
	if len(full.Server.MCPConfig.Env) != 0 {
		t.Errorf("mcp_config.env = %v: the vault goes in as the argument, nothing else is configured", full.Server.MCPConfig.Env)
	}
	if full.Tools != nil {
		t.Error("the template lists tools: they are read from the server at build time, a hand-kept list goes stale")
	}
	if full.License != "MIT" {
		t.Errorf("license = %q, want MIT (LICENSE)", full.License)
	}
	if full.Author.Name == "" || full.Author.Email != "" || full.Author.URL != "" {
		t.Errorf("author = %+v, want a name only (no personal contact details in the repo)", full.Author)
	}
	for field, text := range map[string]string{"description": full.Description, "long_description": full.LongDesc} {
		if strings.Contains(text, "—") {
			t.Errorf("%s contains an em-dash; user-facing text uses commas, colons or parentheses", field)
		}
	}

	// The icon is a PNG next to the manifest.
	icon, err := os.ReadFile(filepath.Join(filepath.Dir(committedManifest), m.Icon))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(icon, []byte("\x89PNG\r\n\x1a\n")) {
		t.Errorf("%s is not a PNG", m.Icon)
	}

	// The version follows the release like server.json does (task bump).
	var server struct {
		Version string `json:"version"`
	}
	b, err := os.ReadFile("../../server.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &server); err != nil {
		t.Fatal(err)
	}
	if m.Version != server.Version {
		t.Errorf("manifest version = %q, server.json = %q; run task bump", m.Version, server.Version)
	}
}

func TestManifestCheckCatchesMistakes(t *testing.T) {
	raw, _ := readCommittedManifest(t)
	edit := func(t *testing.T, f func(map[string]any)) manifest {
		t.Helper()
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		f(doc)
		b, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		m, err := parseManifest(b)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	server := func(doc map[string]any) map[string]any { return doc["server"].(map[string]any) }
	cfg := func(doc map[string]any) map[string]any { return server(doc)["mcp_config"].(map[string]any) }
	overrides := func(doc map[string]any) map[string]any { return cfg(doc)["platform_overrides"].(map[string]any) }
	vault := func(doc map[string]any) map[string]any {
		return doc["user_config"].(map[string]any)[vaultKey].(map[string]any)
	}

	cases := map[string]struct {
		edit func(map[string]any)
		want string
	}{
		"node server":       {func(d map[string]any) { server(d)["type"] = "node" }, "server.type"},
		"no vault argument": {func(d map[string]any) { cfg(d)["args"] = []any{"mcp"} }, "args"},
		"http mode":         {func(d map[string]any) { cfg(d)["args"] = []any{"${user_config.vault_path}"} }, "args"},
		"wrong windows binary": {func(d map[string]any) {
			overrides(d)["win32"] = map[string]any{"command": "${__dirname}/server/vellum"}
		}, "win32"},
		"relative macOS command": {func(d map[string]any) {
			overrides(d)["darwin"] = map[string]any{"command": "server/vellum"}
		}, "darwin"},
		"linux without a binary": {func(d map[string]any) {
			d["compatibility"] = map[string]any{"platforms": []any{"darwin", "win32", "linux"}}
		}, "compatibility.platforms"},
		"linux override": {func(d map[string]any) {
			overrides(d)["linux"] = map[string]any{"command": "${__dirname}/server/vellum"}
		}, "linux"},
		"vault as text":              {func(d map[string]any) { vault(d)["type"] = "string" }, "required directory"},
		"vault optional":             {func(d map[string]any) { vault(d)["required"] = false }, "required directory"},
		"no vault config":            {func(d map[string]any) { delete(d, "user_config") }, "user_config.vault_path is missing"},
		"entry point elsewhere":      {func(d map[string]any) { server(d)["entry_point"] = "vellum" }, "entry_point"},
		"remote icon":                {func(d map[string]any) { d["icon"] = "https://example.com/icon.png" }, "icon"},
		"command outside the bundle": {func(d map[string]any) { cfg(d)["command"] = "/usr/local/bin/vellum" }, "command"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := edit(t, tc.edit).check()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("check() = %v, want an error about %s", err, tc.want)
			}
		})
	}
}

// fatHeader builds the start of a universal Mach-O with the given CPU types.
func fatHeader(cpus ...uint32) []byte {
	var b bytes.Buffer
	_ = binary.Write(&b, binary.BigEndian, []uint32{0xcafebabe, uint32(len(cpus))})
	for i, cpu := range cpus {
		_ = binary.Write(&b, binary.BigEndian, []uint32{cpu, 0, uint32(4096 * (i + 1)), 16, 12})
	}
	return b.Bytes()
}

// peFile builds the smallest PE image debug/pe accepts, for one machine:
// a DOS stub pointing at the PE header and an empty PE32+ optional header.
func peFile(machine uint16) []byte {
	b := make([]byte, 0x40)
	copy(b, "MZ")
	binary.LittleEndian.PutUint32(b[0x3c:], 0x40)
	b = append(b, "PE\x00\x00"...)
	var hdr bytes.Buffer
	_ = binary.Write(&hdr, binary.LittleEndian, pe.FileHeader{
		Machine:              machine,
		SizeOfOptionalHeader: uint16(binary.Size(pe.OptionalHeader64{})),
		Characteristics:      pe.IMAGE_FILE_EXECUTABLE_IMAGE | pe.IMAGE_FILE_LARGE_ADDRESS_AWARE,
	})
	_ = binary.Write(&hdr, binary.LittleEndian, pe.OptionalHeader64{Magic: 0x20b, NumberOfRvaAndSizes: 16})
	return append(b, hdr.Bytes()...)
}

func writeFile(t *testing.T, dir, name string, data []byte, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, mode); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCheckUniversal(t *testing.T) {
	dir := t.TempDir()
	const arm64, x86 = 0x0100000c, 0x01000007
	if err := checkUniversal(writeFile(t, dir, "both", fatHeader(x86, arm64), 0o755)); err != nil {
		t.Errorf("x86_64+arm64: %v", err)
	}
	for name, data := range map[string][]byte{
		"arm64 only":   fatHeader(arm64),
		"x86_64 only":  fatHeader(x86),
		"thin binary":  {0xcf, 0xfa, 0xed, 0xfe, 0x0c, 0x00, 0x00, 0x01},
		"windows file": peFile(0x8664),
		"empty fat":    fatHeader(),
		"truncated":    fatHeader(x86, arm64)[:20],
	} {
		if err := checkUniversal(writeFile(t, dir, strings.ReplaceAll(name, " ", "_"), data, 0o755)); err == nil {
			t.Errorf("%s passed as a universal binary", name)
		}
	}
}

func TestCheckWindowsAMD64(t *testing.T) {
	dir := t.TempDir()
	if err := checkWindowsAMD64(writeFile(t, dir, "amd64.exe", peFile(0x8664), 0o755)); err != nil {
		t.Errorf("amd64: %v", err)
	}
	if err := checkWindowsAMD64(writeFile(t, dir, "arm64.exe", peFile(0xaa64), 0o755)); err == nil {
		t.Error("an arm64 executable passed as amd64")
	}
	if err := checkWindowsAMD64(writeFile(t, dir, "mac", fatHeader(1, 2), 0o755)); err == nil {
		t.Error("a Mach-O passed as a Windows executable")
	}
}

func TestSummary(t *testing.T) {
	for in, want := range map[string]string{
		"Delete a note.":                         "Delete a note.",
		"Read a note. Returns the hash.":         "Read a note.",
		"Search (title > tag > path). Ranked.":   "Search (title > tag > path).",
		"First line\nsecond line":                "First line",
		"  padded.  ":                            "padded.",
		"":                                       "",
		"Move or rename a note. Backlinks stay.": "Move or rename a note.",
	} {
		if got := summary(in); got != want {
			t.Errorf("summary(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestServerCommandStartsStdioWithoutLocalSettings(t *testing.T) {
	t.Setenv("VELLUM_CURATOR", "on")
	t.Setenv("VELLUM_SHARING", "on")
	cmd := serverCommand("/path/to/vellum", "/path/to/vault")
	if want := []string{"/path/to/vellum", "mcp", "/path/to/vault"}; !slices.Equal(cmd.Args, want) {
		t.Errorf("args = %q, want %q", cmd.Args, want)
	}
	if cmd.Env == nil {
		t.Fatal("the command inherits the whole environment")
	}
	for _, kv := range cmd.Env {
		if strings.HasPrefix(kv, "VELLUM_") {
			t.Errorf("env carries %s: the tool list would depend on the build machine", kv)
		}
	}
}

func TestListTools(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	tools, err := listTools(ctx, fakeServer("tools"))
	if err != nil {
		t.Fatal(err)
	}
	want := []toolEntry{
		{Name: "alpha_tool", Description: "First tool: one line"},
		{Name: "zeta_tool", Description: "Second tool."},
	}
	if !slices.Equal(tools, want) {
		t.Errorf("tools = %+v, want %+v", tools, want)
	}

	_, err = listTools(ctx, fakeServer("fail"))
	if err == nil || !strings.Contains(err.Error(), "vault folder /nope does not exist") {
		t.Errorf("a server that exits at start: err = %v, want its stderr in the error", err)
	}
}

// fakeDist lays out a GoReleaser dist/ folder with stand-in binaries.
func fakeDist(t *testing.T, stamp time.Time) string {
	t.Helper()
	dist := t.TempDir()
	universal := writeFile(t, dist, "vellum-macos-universal_darwin_all/vellum", fatHeader(0x01000007, 0x0100000c), 0o755)
	windows := writeFile(t, dist, "vellum_windows_amd64_v1/vellum.exe", peFile(0x8664), 0o755)
	arm := writeFile(t, dist, "vellum_windows_arm64_v8.0/vellum.exe", peFile(0xaa64), 0o755)
	for _, f := range []string{universal, windows, arm} {
		if err := os.Chtimes(f, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	art := func(path, goos, goarch, id string) map[string]any {
		return map[string]any{"name": filepath.Base(path), "path": path, "goos": goos, "goarch": goarch, "type": "Binary", "extra": map[string]any{"ID": id}}
	}
	artifacts := []map[string]any{
		art(windows, "windows", "amd64", buildID),
		art(arm, "windows", "arm64", buildID),
		art(universal, "darwin", "all", universalID),
		{"name": "vellum_windows_amd64.zip", "path": filepath.Join(dist, "vellum_windows_amd64.zip"), "goos": "windows", "goarch": "amd64", "type": "Archive", "extra": map[string]any{"ID": buildID}},
	}
	b, _ := json.Marshal(artifacts)
	writeFile(t, dist, "artifacts.json", b, 0o644)
	writeFile(t, dist, "metadata.json", []byte(`{"project_name":"vellum","tag":"v1.2.3-beta.1","version":"1.2.3-beta.1"}`), 0o644)
	return dist
}

func testOptions(t *testing.T, dist string) options {
	t.Helper()
	license := writeFile(t, t.TempDir(), "LICENSE", []byte("MIT License\n"), 0o644)
	return options{
		dist:         dist,
		out:          filepath.Join(dist, "vellum.mcpb"),
		manifest:     committedManifest,
		license:      license,
		toolsCommand: fakeServer("tools"),
	}
}

func TestBuildPacksAnInstallableBundle(t *testing.T) {
	stamp := time.Date(2026, 8, 20, 15, 13, 21, 0, time.UTC)
	dist := fakeDist(t, stamp)
	opts := testOptions(t, dist)

	sum, size, err := build(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(opts.out)
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(data)) != size {
		t.Errorf("reported size %d, file has %d bytes", size, len(data))
	}
	if h := sha256.Sum256(data); hex.EncodeToString(h[:]) != sum {
		t.Errorf("reported sha256 %s does not match the file", sum)
	}

	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	modes := map[string]os.FileMode{}
	contents := map[string][]byte{}
	for _, f := range zr.File {
		modes[f.Name] = f.Mode()
		if !f.Modified.Equal(stamp) {
			t.Errorf("%s is dated %v, want the binaries' commit time %v", f.Name, f.Modified, stamp)
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		contents[f.Name], _ = io.ReadAll(rc)
		_ = rc.Close()
	}
	want := map[string]os.FileMode{
		"LICENSE":           0o644,
		"icon.png":          0o644,
		"manifest.json":     0o644,
		"server/vellum":     0o755,
		"server/vellum.exe": 0o755,
	}
	if fmt.Sprint(modes) != fmt.Sprint(want) {
		t.Errorf("bundle entries = %v, want %v", modes, want)
	}
	if !bytes.Equal(contents["server/vellum"], fatHeader(0x01000007, 0x0100000c)) {
		t.Error("server/vellum is not the universal binary")
	}
	if !bytes.Equal(contents["server/vellum.exe"], peFile(0x8664)) {
		t.Error("server/vellum.exe is not the amd64 build")
	}

	var m struct {
		Version string      `json:"version"`
		Tools   []toolEntry `json:"tools"`
		Server  struct {
			Type string `json:"type"`
		} `json:"server"`
	}
	if err := json.Unmarshal(contents["manifest.json"], &m); err != nil {
		t.Fatalf("bundled manifest: %v", err)
	}
	if m.Version != "1.2.3-beta.1" {
		t.Errorf("bundled version = %q, want dist/metadata.json's 1.2.3-beta.1", m.Version)
	}
	if len(m.Tools) != 2 || m.Tools[0].Name != "alpha_tool" {
		t.Errorf("bundled tools = %+v, want the server's two tools", m.Tools)
	}
	if m.Server.Type != "binary" {
		t.Errorf("bundled manifest lost its fields: server.type = %q", m.Server.Type)
	}

	// The staged tree is the bundle, unpacked, for `mcpb validate`.
	staged, err := os.ReadFile(filepath.Join(dist, "mcpb", "manifest.json"))
	if err != nil || !bytes.Equal(staged, contents["manifest.json"]) {
		t.Errorf("dist/mcpb/manifest.json differs from the bundled one (%v)", err)
	}
	if info, err := os.Stat(filepath.Join(dist, "mcpb", "server", "vellum")); err != nil || info.Mode().Perm() != 0o755 {
		t.Errorf("staged binary is not executable: %v %v", info, err)
	}

	t.Run("deterministic", func(t *testing.T) {
		again := opts
		again.out = filepath.Join(t.TempDir(), "again.mcpb")
		sum2, _, err := build(context.Background(), again)
		if err != nil {
			t.Fatal(err)
		}
		if sum2 != sum {
			t.Errorf("a second build gave %s, the first %s: a re-run release would change the registry's hash", sum2, sum)
		}
	})

	t.Run("version flag wins and loses its v", func(t *testing.T) {
		v := opts
		v.version = "v9.9.9"
		v.out = filepath.Join(t.TempDir(), "v.mcpb")
		if _, _, err := build(context.Background(), v); err != nil {
			t.Fatal(err)
		}
		b, _ := os.ReadFile(filepath.Join(dist, "mcpb", "manifest.json"))
		if !bytes.Contains(b, []byte(`"version": "9.9.9"`)) {
			t.Errorf("manifest version not 9.9.9:\n%s", b)
		}
	})
}

func TestBuildRefusesWrongBinaries(t *testing.T) {
	stamp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	cases := map[string]struct {
		edit func(t *testing.T, dist string)
		want string
	}{
		"no universal build": {func(t *testing.T, dist string) {
			rewriteArtifacts(t, dist, func(a []map[string]any) []map[string]any {
				return slices.DeleteFunc(a, func(x map[string]any) bool { return x["goarch"] == "all" })
			})
		}, "darwin/all"},
		"single-architecture macOS binary": {func(t *testing.T, dist string) {
			writeFile(t, dist, "vellum-macos-universal_darwin_all/vellum", fatHeader(0x0100000c), 0o755)
		}, "x86_64"},
		"no windows amd64 build": {func(t *testing.T, dist string) {
			rewriteArtifacts(t, dist, func(a []map[string]any) []map[string]any {
				return slices.DeleteFunc(a, func(x map[string]any) bool {
					return x["goos"] == "windows" && x["goarch"] == "amd64" && x["type"] == "Binary"
				})
			})
		}, "windows/amd64"},
		"no GoReleaser run": {func(t *testing.T, dist string) {
			_ = os.Remove(filepath.Join(dist, "artifacts.json"))
		}, "release-snapshot"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dist := fakeDist(t, stamp)
			tc.edit(t, dist)
			opts := testOptions(t, dist)
			_, _, err := build(context.Background(), opts)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("build() = %v, want an error mentioning %s", err, tc.want)
			}
			if _, statErr := os.Stat(opts.out); statErr == nil {
				t.Error("a refused build still wrote the bundle")
			}
		})
	}
}

func rewriteArtifacts(t *testing.T, dist string, f func([]map[string]any) []map[string]any) {
	t.Helper()
	p := filepath.Join(dist, "artifacts.json")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var a []map[string]any
	if err := json.Unmarshal(b, &a); err != nil {
		t.Fatal(err)
	}
	b, _ = json.Marshal(f(a))
	writeFile(t, dist, "artifacts.json", b, 0o644)
}
