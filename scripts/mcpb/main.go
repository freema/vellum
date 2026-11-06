// Command mcpb packs dist/vellum.mcpb, the MCP bundle that Claude Desktop
// installs with a double click, from the binaries GoReleaser left in dist/.
//
//	go run ./scripts/mcpb [-dist dist] [-out dist/vellum.mcpb] [-version x.y.z]
//
// The bundle holds mcpb/manifest.json (with the release version and the
// server's tool list filled in), the icon, the license, the universal macOS
// binary and the Windows amd64 binary. An MCPB manifest can pick a command
// per operating system but not per CPU, so macOS gets one binary with both
// architectures and Windows on ARM runs the amd64 build under emulation.
// Claude Desktop does not run on Linux, so no Linux binary goes in.
//
// The archive is deterministic: entries are sorted and stamped with the
// binaries' modification time, which GoReleaser sets to the commit time. A
// re-run of the release job therefore uploads the same bytes, and the
// fileSha256 that the MCP registry lists keeps matching the download.
//
// Nothing is published here: the release workflow attaches the file to the
// GitHub Release, and `task mcpb` builds it locally from a snapshot.
package main

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"context"
	"crypto/sha256"
	"debug/pe"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// buildID is the GoReleaser build of the vellum binary (.goreleaser.yaml).
	buildID = "vellum"
	// universalID is the universal_binaries entry that merges the two macOS
	// builds into one file.
	universalID = "vellum-macos-universal"

	// dirnameVar is what the MCPB host replaces with the bundle's folder.
	dirnameVar = "${__dirname}/"
	// vaultKey is the user_config entry that asks for the notes folder.
	vaultKey = "vault_path"
)

// bundleBinaries maps an MCPB platform to the binary's path in the bundle.
// The manifest's platform_overrides must point at exactly these files.
var bundleBinaries = map[string]string{
	"darwin": "server/vellum",
	"win32":  "server/vellum.exe",
}

func main() {
	dist := flag.String("dist", "dist", "GoReleaser output folder (artifacts.json, metadata.json)")
	out := flag.String("out", "", "bundle to write (default <dist>/vellum.mcpb)")
	manifestPath := flag.String("manifest", filepath.Join("mcpb", "manifest.json"), "manifest template")
	license := flag.String("license", "LICENSE", "license file to ship in the bundle")
	version := flag.String("version", "", "bundle version (default: the version in <dist>/metadata.json)")
	flag.Parse()
	if *out == "" {
		*out = filepath.Join(*dist, "vellum.mcpb")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	sum, size, err := build(ctx, options{
		dist:     *dist,
		out:      *out,
		manifest: *manifestPath,
		license:  *license,
		version:  *version,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "mcpb:", err)
		os.Exit(1)
	}
	fmt.Printf("%s  %s (%d bytes)\n", sum, *out, size)
}

type options struct {
	dist, out, manifest, license, version string
	// toolsCommand starts the server that the tool list is read from; nil
	// runs the dist binary built for this machine. Tests swap in a fake.
	toolsCommand func(vault string) (*exec.Cmd, error)
}

// build stages the bundle into <dist>/mcpb, packs it into opts.out and
// returns the archive's SHA-256 and size.
func build(ctx context.Context, opts options) (string, int64, error) {
	artifacts, err := loadArtifacts(filepath.Join(opts.dist, "artifacts.json"))
	if err != nil {
		return "", 0, err
	}
	version := opts.version
	if version == "" {
		if version, err = distVersion(filepath.Join(opts.dist, "metadata.json")); err != nil {
			return "", 0, err
		}
	}
	version = strings.TrimPrefix(version, "v")

	raw, err := os.ReadFile(opts.manifest)
	if err != nil {
		return "", 0, err
	}
	m, err := parseManifest(raw)
	if err != nil {
		return "", 0, fmt.Errorf("%s: %w", opts.manifest, err)
	}
	if err := m.check(); err != nil {
		return "", 0, fmt.Errorf("%s: %w", opts.manifest, err)
	}

	darwin, err := artifacts.binary("darwin", "all", universalID)
	if err != nil {
		return "", 0, err
	}
	if err := checkUniversal(darwin); err != nil {
		return "", 0, err
	}
	windows, err := artifacts.binary("windows", "amd64", buildID)
	if err != nil {
		return "", 0, err
	}
	if err := checkWindowsAMD64(windows); err != nil {
		return "", 0, err
	}

	command := opts.toolsCommand
	if command == nil {
		host, err := artifacts.binary(runtime.GOOS, runtime.GOARCH, buildID)
		if err != nil {
			return "", 0, fmt.Errorf("the tool list is read from the binary built for this machine: %w", err)
		}
		command = func(vault string) (*exec.Cmd, error) { return serverCommand(host, vault), nil }
	}
	tools, err := listTools(ctx, command)
	if err != nil {
		return "", 0, fmt.Errorf("list the server's tools: %w", err)
	}
	manifest, err := renderManifest(raw, version, tools)
	if err != nil {
		return "", 0, err
	}

	files := []bundleFile{
		{name: "manifest.json", data: manifest, mode: 0o644},
		{name: m.Icon, src: filepath.Join(filepath.Dir(opts.manifest), filepath.FromSlash(m.Icon)), mode: 0o644},
		{name: "LICENSE", src: opts.license, mode: 0o644},
		{name: bundleBinaries["darwin"], src: darwin, mode: 0o755},
		{name: bundleBinaries["win32"], src: windows, mode: 0o755},
	}
	// The binaries carry GoReleaser's commit timestamp (mod_timestamp), so
	// they date the whole archive.
	stamp, err := latestModTime(darwin, windows)
	if err != nil {
		return "", 0, err
	}
	staging := filepath.Join(opts.dist, "mcpb")
	if err := stage(staging, files); err != nil {
		return "", 0, err
	}
	return pack(opts.out, files, stamp)
}

// artifact is one entry of GoReleaser's dist/artifacts.json.
type artifact struct {
	Path   string `json:"path"`
	Goos   string `json:"goos"`
	Goarch string `json:"goarch"`
	Type   string `json:"type"`
	Extra  struct {
		ID string `json:"ID"`
	} `json:"extra"`
}

type artifactList []artifact

func loadArtifacts(file string) (artifactList, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("%w (run GoReleaser first: task release-snapshot)", err)
	}
	var list artifactList
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	return list, nil
}

// binary finds a built binary by platform and build id. Matching on the
// metadata rather than on dist/ folder names survives GoReleaser changing
// its layout (the _v1 and _v8.0 suffixes, for one).
func (l artifactList) binary(goos, goarch, id string) (string, error) {
	for _, a := range l {
		if a.Type == "Binary" && a.Goos == goos && a.Goarch == goarch && a.Extra.ID == id {
			return a.Path, nil
		}
	}
	return "", fmt.Errorf("artifacts.json has no %s/%s binary from build %q", goos, goarch, id)
}

func distVersion(file string) (string, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return "", err
	}
	var meta struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(b, &meta); err != nil {
		return "", fmt.Errorf("%s: %w", file, err)
	}
	if meta.Version == "" {
		return "", fmt.Errorf("%s has no version", file)
	}
	return meta.Version, nil
}

// manifest is the part of the MCPB manifest this tool relies on.
type manifest struct {
	ManifestVersion string `json:"manifest_version"`
	Name            string `json:"name"`
	Version         string `json:"version"`
	Icon            string `json:"icon"`
	Server          struct {
		Type       string `json:"type"`
		EntryPoint string `json:"entry_point"`
		MCPConfig  struct {
			Command           string   `json:"command"`
			Args              []string `json:"args"`
			PlatformOverrides map[string]struct {
				Command string `json:"command"`
			} `json:"platform_overrides"`
		} `json:"mcp_config"`
	} `json:"server"`
	UserConfig map[string]struct {
		Type     string `json:"type"`
		Title    string `json:"title"`
		Required bool   `json:"required"`
	} `json:"user_config"`
	Compatibility struct {
		Platforms []string `json:"platforms"`
	} `json:"compatibility"`
}

func parseManifest(raw []byte) (manifest, error) {
	var m manifest
	err := json.Unmarshal(raw, &m)
	return m, err
}

// check makes sure the manifest starts the binaries this tool puts in the
// bundle, the way `vellum mcp <folder>` expects to be started. A typo here
// would only show up as an extension that fails to start after install.
func (m manifest) check() error {
	var errs []error
	fail := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }

	if m.Name != "vellum" {
		fail("name = %q, want vellum", m.Name)
	}
	if m.Server.Type != "binary" {
		fail("server.type = %q, want binary", m.Server.Type)
	}
	if m.Icon == "" || path.IsAbs(m.Icon) || strings.Contains(m.Icon, "..") || strings.Contains(m.Icon, "://") {
		fail("icon = %q, want a file next to the manifest", m.Icon)
	}

	platforms := slices.Sorted(maps.Keys(bundleBinaries))
	got := slices.Clone(m.Compatibility.Platforms)
	slices.Sort(got)
	if !slices.Equal(got, platforms) {
		fail("compatibility.platforms = %v, want %v: the bundle carries binaries for exactly those", m.Compatibility.Platforms, platforms)
	}
	cfg := m.Server.MCPConfig
	for _, p := range platforms {
		want := dirnameVar + bundleBinaries[p]
		if got := cfg.PlatformOverrides[p].Command; got != want {
			fail("server.mcp_config.platform_overrides.%s.command = %q, want %q", p, got, want)
		}
	}
	for p := range cfg.PlatformOverrides {
		if _, ok := bundleBinaries[p]; !ok {
			fail("server.mcp_config.platform_overrides has %q, for which the bundle has no binary", p)
		}
	}
	if !isBundleBinary(strings.TrimPrefix(cfg.Command, dirnameVar)) || !strings.HasPrefix(cfg.Command, dirnameVar) {
		fail("server.mcp_config.command = %q, want one of the bundled binaries under %s", cfg.Command, dirnameVar)
	}
	if !isBundleBinary(m.Server.EntryPoint) {
		fail("server.entry_point = %q, want one of the bundled binaries", m.Server.EntryPoint)
	}

	vaultArg := "${user_config." + vaultKey + "}"
	if !slices.Equal(cfg.Args, []string{"mcp", vaultArg}) {
		fail("server.mcp_config.args = %q, want [mcp %s]", cfg.Args, vaultArg)
	}
	vault, ok := m.UserConfig[vaultKey]
	switch {
	case !ok:
		fail("user_config.%s is missing", vaultKey)
	case vault.Type != "directory" || !vault.Required:
		fail("user_config.%s must be a required directory, got type %q required %v", vaultKey, vault.Type, vault.Required)
	}
	return errors.Join(errs...)
}

func isBundleBinary(name string) bool {
	for _, b := range bundleBinaries {
		if name == b {
			return true
		}
	}
	return false
}

// renderManifest returns the template with the release version and the
// tool list set. Every other field is kept as written.
func renderManifest(raw []byte, version string, tools []toolEntry) ([]byte, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	v, err := json.Marshal(version)
	if err != nil {
		return nil, err
	}
	t, err := json.Marshal(tools)
	if err != nil {
		return nil, err
	}
	fields["version"] = v
	fields["tools"] = t
	out, err := json.MarshalIndent(fields, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// toolEntry is a manifest `tools` item. The schema allows nothing else.
type toolEntry struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// serverCommand starts the vellum binary the way the bundle does, over
// stdio, with an environment free of any VELLUM_* settings of the machine
// that builds the bundle: the tool list must be what a fresh install shows.
func serverCommand(bin, vault string) *exec.Cmd {
	cmd := exec.Command(bin, "mcp", vault)
	cmd.Env = []string{"HOME=" + vault, "TMPDIR=" + os.TempDir()}
	return cmd
}

// listTools asks a running server for its tools, so the manifest can never
// advertise a tool the server no longer has.
func listTools(ctx context.Context, command func(vault string) (*exec.Cmd, error)) ([]toolEntry, error) {
	vault, err := os.MkdirTemp("", "vellum-mcpb-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(vault) }()
	cmd, err := command(vault)
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	client := mcp.NewClient(&mcp.Implementation{Name: "vellum-mcpb", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		return nil, withStderr(err, &stderr)
	}
	defer func() { _ = session.Close() }()

	var tools []toolEntry
	for tool, err := range session.Tools(ctx, nil) {
		if err != nil {
			return nil, withStderr(err, &stderr)
		}
		tools = append(tools, toolEntry{Name: tool.Name, Description: summary(tool.Description)})
	}
	if len(tools) == 0 {
		return nil, withStderr(errors.New("the server lists no tools"), &stderr)
	}
	slices.SortFunc(tools, func(a, b toolEntry) int { return strings.Compare(a.Name, b.Name) })
	return tools, nil
}

func withStderr(err error, stderr *bytes.Buffer) error {
	if s := strings.TrimSpace(stderr.String()); s != "" {
		return fmt.Errorf("%w\nserver stderr:\n%s", err, s)
	}
	return err
}

// summary shortens a tool description to its first sentence: Claude Desktop
// lists the tools before install, and the full descriptions are written for
// the model, not for that list.
func summary(desc string) string {
	s, _, _ := strings.Cut(strings.TrimSpace(desc), "\n")
	if i := strings.Index(s, ". "); i >= 0 {
		s = s[:i+1]
	}
	return strings.TrimSpace(s)
}

// checkUniversal fails unless file is a universal Mach-O with an arm64 and
// an x86_64 slice. A single-architecture binary would pack and install
// fine, then fail to start on half of all Macs.
func checkUniversal(file string) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	const (
		fatMagic   = 0xcafebabe
		fatMagic64 = 0xcafebabf
		cpuX86_64  = 0x01000007
		cpuARM64   = 0x0100000c
	)
	var header struct{ Magic, Count uint32 }
	if err := binary.Read(f, binary.BigEndian, &header); err != nil {
		return fmt.Errorf("%s: %w", file, err)
	}
	entrySize := int64(20) // cputype, cpusubtype, offset, size, align
	if header.Magic == fatMagic64 {
		entrySize = 32
	} else if header.Magic != fatMagic {
		return fmt.Errorf("%s is not a universal macOS binary (magic %#x)", file, header.Magic)
	}
	if header.Count == 0 || header.Count > 16 {
		return fmt.Errorf("%s: implausible universal binary with %d slices", file, header.Count)
	}
	found := map[uint32]bool{}
	entry := make([]byte, entrySize)
	for i := range int64(header.Count) {
		if _, err := f.ReadAt(entry, 8+i*entrySize); err != nil {
			return fmt.Errorf("%s: %w", file, err)
		}
		found[binary.BigEndian.Uint32(entry)] = true
	}
	for cpu, name := range map[uint32]string{cpuARM64: "arm64", cpuX86_64: "x86_64"} {
		if !found[cpu] {
			return fmt.Errorf("%s has no %s slice: Macs with that CPU could not run it", file, name)
		}
	}
	return nil
}

// checkWindowsAMD64 fails unless file is a Windows amd64 executable.
func checkWindowsAMD64(file string) error {
	f, err := pe.Open(file)
	if err != nil {
		return fmt.Errorf("%s is not a Windows executable: %w", file, err)
	}
	defer func() { _ = f.Close() }()
	if f.Machine != pe.IMAGE_FILE_MACHINE_AMD64 {
		return fmt.Errorf("%s is built for machine %#x, want amd64", file, f.Machine)
	}
	return nil
}

func latestModTime(files ...string) (time.Time, error) {
	var latest time.Time
	for _, f := range files {
		info, err := os.Stat(f)
		if err != nil {
			return time.Time{}, err
		}
		if info.ModTime().After(latest) {
			latest = info.ModTime()
		}
	}
	return latest.UTC().Truncate(time.Second), nil
}

// bundleFile is one entry of the bundle: either data or a file to copy.
type bundleFile struct {
	name string // slash-separated path inside the bundle
	src  string
	data []byte
	mode os.FileMode
}

// stage writes the bundle's tree into dir, replacing what was there, so it
// can be inspected or checked with `mcpb validate` before shipping. Each
// entry then points at its staged copy, which is what gets packed.
func stage(dir string, files []bundleFile) error {
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	for i, f := range files {
		dst := filepath.Join(dir, filepath.FromSlash(f.name))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		data := f.data
		if data == nil {
			var err error
			if data, err = os.ReadFile(f.src); err != nil {
				return err
			}
		}
		if err := os.WriteFile(dst, data, f.mode); err != nil {
			return err
		}
		// WriteFile honours the umask; the bundle must not.
		if err := os.Chmod(dst, f.mode); err != nil {
			return err
		}
		files[i].src, files[i].data = dst, nil
	}
	return nil
}

// pack zips the staged files into out and returns its SHA-256 and size.
func pack(out string, files []bundleFile, stamp time.Time) (string, int64, error) {
	sorted := slices.Clone(files)
	slices.SortFunc(sorted, func(a, b bundleFile) int { return strings.Compare(a.name, b.name) })

	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return "", 0, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(out), ".vellum-mcpb-*")
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()

	h := sha256.New()
	zw := zip.NewWriter(io.MultiWriter(tmp, h))
	zw.RegisterCompressor(zip.Deflate, func(w io.Writer) (io.WriteCloser, error) {
		return flate.NewWriter(w, flate.BestCompression)
	})
	for _, f := range sorted {
		hdr := &zip.FileHeader{Name: f.name, Method: zip.Deflate, Modified: stamp}
		// Unix permissions in the archive: the host must be able to start
		// the binaries straight after unpacking.
		hdr.SetMode(f.mode)
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			_ = tmp.Close()
			return "", 0, err
		}
		src, err := os.Open(f.src)
		if err != nil {
			_ = tmp.Close()
			return "", 0, err
		}
		_, err = io.Copy(w, src)
		_ = src.Close()
		if err != nil {
			_ = tmp.Close()
			return "", 0, err
		}
	}
	if err := zw.Close(); err != nil {
		_ = tmp.Close()
		return "", 0, err
	}
	info, err := tmp.Stat()
	if err != nil {
		_ = tmp.Close()
		return "", 0, err
	}
	if err := tmp.Close(); err != nil {
		return "", 0, err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return "", 0, err
	}
	if err := os.Rename(tmp.Name(), out); err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), info.Size(), nil
}
