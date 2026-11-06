package vellum

// These tests guard the Claude Desktop bundle (vellum.mcpb) and the MCP
// registry entry (server.json): the bundle needs a universal macOS binary
// that the archives must not pick up, the release must attach the bundle
// under a fixed name, and the registry entry must pass the registry's own
// rules. Publishing to the registry is opt-in, for stable tags only, and
// needs no secret, so a release never fails for lack of one.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
)

const (
	registryName   = "io.github.freema/vellum"
	registrySchema = "https://static.modelcontextprotocol.io/schemas/2025-12-11/server.schema.json"
	bundleName     = "vellum.mcpb"
	universalID    = "vellum-macos-universal"
)

type universalBinary struct {
	ID           string   `yaml:"id"`
	IDs          []string `yaml:"ids"`
	Replace      bool     `yaml:"replace"`
	NameTemplate string   `yaml:"name_template"`
	ModTimestamp string   `yaml:"mod_timestamp"`
}

func TestGoreleaserBuildsTheUniversalMacBinary(t *testing.T) {
	var c struct {
		Builds []struct {
			ID           string `yaml:"id"`
			Binary       string `yaml:"binary"`
			ModTimestamp string `yaml:"mod_timestamp"`
		} `yaml:"builds"`
		Universal []universalBinary `yaml:"universal_binaries"`
		Archives  []struct {
			IDs []string `yaml:"ids"`
		} `yaml:"archives"`
	}
	loadYAML(t, ".goreleaser.yaml", &c)
	if len(c.Universal) != 1 || len(c.Builds) != 1 {
		t.Fatalf("universal_binaries = %d, builds = %d; want one of each", len(c.Universal), len(c.Builds))
	}
	u, b := c.Universal[0], c.Builds[0]
	if u.ID != universalID {
		t.Errorf("universal binary id = %q, want %q (scripts/mcpb looks it up by that id)", u.ID, universalID)
	}
	if !slices.Equal(u.IDs, []string{b.ID}) {
		t.Errorf("universal binary merges %v, want the build %q", u.IDs, b.ID)
	}
	// replace: true would swap the two macOS archives for one
	// vellum_darwin_all.tar.gz and break every download link and the cask.
	if u.Replace {
		t.Error("universal_binaries.replace is true: the per-arch macOS archives would disappear")
	}
	if u.NameTemplate != b.Binary {
		t.Errorf("universal binary name = %q, want %q", u.NameTemplate, b.Binary)
	}
	// The bundle is dated by its binaries; the commit time makes a re-run
	// of the release pack the same bytes.
	if u.ModTimestamp != "{{ .CommitTimestamp }}" || b.ModTimestamp != "{{ .CommitTimestamp }}" {
		t.Errorf("mod_timestamp = %q (universal), %q (build), want {{ .CommitTimestamp }} for both", u.ModTimestamp, b.ModTimestamp)
	}
	for i, a := range c.Archives {
		if slices.Contains(a.IDs, u.ID) || len(a.IDs) == 0 {
			t.Errorf("archive %d takes ids %v: it must list the build explicitly and leave the universal binary out", i, a.IDs)
		}
	}

	src, err := os.ReadFile(filepath.Join("scripts", "mcpb", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), `universalID = "`+universalID+`"`) || !strings.Contains(string(src), `buildID = "`+b.ID+`"`) {
		t.Errorf("scripts/mcpb looks the binaries up under other ids than .goreleaser.yaml builds")
	}
}

// fullWorkflow is release.yml with the fields the bundle and registry jobs
// depend on.
type fullWorkflow struct {
	On struct {
		Push struct {
			Tags     []string `yaml:"tags"`
			Branches []string `yaml:"branches"`
		} `yaml:"push"`
	} `yaml:"on"`
	Jobs map[string]struct {
		If          string            `yaml:"if"`
		Needs       any               `yaml:"needs"`
		Permissions map[string]string `yaml:"permissions"`
		Env         map[string]string `yaml:"env"`
		Steps       []workflowStep    `yaml:"steps"`
	} `yaml:"jobs"`
}

func loadFullReleaseWorkflow(t *testing.T) (fullWorkflow, map[string]any) {
	t.Helper()
	var w fullWorkflow
	loadYAML(t, filepath.Join(".github", "workflows", "release.yml"), &w)
	var raw map[string]any
	loadYAML(t, filepath.Join(".github", "workflows", "release.yml"), &raw)
	return w, raw
}

var exactNpmPin = regexp.MustCompile(`^@anthropic-ai/mcpb@\d+\.\d+\.\d+$`)

func TestReleaseWorkflowShipsTheMCPBundle(t *testing.T) {
	job := loadReleaseWorkflow(t).Jobs["release"]
	index := func(pred func(workflowStep) bool) int { return slices.IndexFunc(job.Steps, pred) }

	grAt := index(func(s workflowStep) bool { return strings.HasPrefix(s.Uses, "goreleaser/goreleaser-action@") })
	buildAt := index(func(s workflowStep) bool { return s.Name == "Build the MCP bundle" })
	uploadAt := index(func(s workflowStep) bool { return s.Name == "Attach the MCP bundle to the release" })
	attestAt := index(func(s workflowStep) bool {
		return strings.HasPrefix(s.Uses, "actions/attest@") && s.With["subject-path"] == "./dist/"+bundleName
	})
	if buildAt < 0 || uploadAt < 0 || attestAt < 0 {
		t.Fatalf("release job lacks a bundle step: build %d, upload %d, attest %d", buildAt, uploadAt, attestAt)
	}
	// The bundle packs the binaries GoReleaser built, into the release it
	// created.
	if grAt > buildAt || buildAt > uploadAt || buildAt > attestAt {
		t.Errorf("step order: goreleaser %d, build %d, upload %d, attest %d", grAt, buildAt, uploadAt, attestAt)
	}

	build := job.Steps[buildAt]
	for _, want := range []string{
		"go run ./scripts/mcpb -dist dist -out dist/" + bundleName,
		`npx --yes "$MCPB_CLI" validate dist/mcpb/manifest.json`,
	} {
		if !strings.Contains(build.Run, want) {
			t.Errorf("bundle build step lacks %q:\n%s", want, build.Run)
		}
	}
	pin := build.Env["MCPB_CLI"]
	if !exactNpmPin.MatchString(pin) {
		t.Errorf("MCPB_CLI = %q, want an exact @anthropic-ai/mcpb@x.y.z pin", pin)
	}

	upload := job.Steps[uploadAt]
	if fields := strings.Fields(upload.Run); !slices.Equal(fields, []string{"gh", "release", "upload", `"$GITHUB_REF_NAME"`, "dist/" + bundleName, "--clobber"}) {
		t.Errorf("upload step runs %q, want gh release upload \"$GITHUB_REF_NAME\" dist/%s --clobber", upload.Run, bundleName)
	}
	if upload.Env["GH_TOKEN"] != "${{ secrets.GITHUB_TOKEN }}" {
		t.Errorf("upload step GH_TOKEN = %q, want the workflow's own token", upload.Env["GH_TOKEN"])
	}

	// One pin for the CLI everywhere it runs.
	var ci workflow
	loadYAML(t, filepath.Join(".github", "workflows", "ci.yml"), &ci)
	validate, ok := stepNamed(ci.Jobs["release-config"], "Validate the MCP bundle manifest")
	if !ok || validate.Env["MCPB_CLI"] != pin || !strings.Contains(validate.Run, "validate mcpb/manifest.json") {
		t.Errorf("ci.yml must validate mcpb/manifest.json with %s, got %+v", pin, validate)
	}
	var tf struct {
		Vars  map[string]any `yaml:"vars"`
		Tasks map[string]struct {
			Deps []string `yaml:"deps"`
			Cmds []string `yaml:"cmds"`
		} `yaml:"tasks"`
	}
	loadYAML(t, "Taskfile.yml", &tf)
	if tf.Vars["MCPB_CLI"] != pin {
		t.Errorf("Taskfile MCPB_CLI = %v, release.yml pins %q; keep them equal", tf.Vars["MCPB_CLI"], pin)
	}
	task, ok := tf.Tasks["mcpb"]
	if !ok {
		t.Fatal("Taskfile.yml has no mcpb task")
	}
	if !slices.Equal(task.Deps, []string{"release-snapshot"}) {
		t.Errorf("task mcpb deps = %v, want [release-snapshot]: the bundle is packed from its dist/", task.Deps)
	}
	cmds := strings.Join(task.Cmds, "\n")
	for _, want := range []string{"go run ./scripts/mcpb", "npx --yes {{.MCPB_CLI}} validate dist/mcpb/manifest.json"} {
		if !strings.Contains(cmds, want) {
			t.Errorf("task mcpb lacks %q:\n%s", want, cmds)
		}
	}
}

func TestRegistryPublishingIsOptInAndStableOnly(t *testing.T) {
	w, raw := loadFullReleaseWorkflow(t)
	// Only a pushed v* tag starts this workflow: no branch, no manual run.
	if on, _ := raw["on"].(map[string]any); len(on) != 1 || !slices.Equal(w.On.Push.Tags, []string{"v*"}) || len(w.On.Push.Branches) != 0 {
		t.Fatalf("release.yml triggers = %v, want only push of v* tags", raw["on"])
	}

	job, ok := w.Jobs["registry"]
	if !ok {
		t.Fatal("release.yml has no registry job")
	}
	if needs, _ := job.Needs.(string); needs != "release" {
		t.Errorf("registry job needs = %v, want release: the bundle must be attached first", job.Needs)
	}
	cond := strings.Join(strings.Fields(job.If), " ")
	for _, want := range []string{
		"github.repository == 'freema/vellum'",
		"startsWith(github.ref, 'refs/tags/v')",
		"!contains(github.ref_name, '-')",
		"vars.MCP_REGISTRY_PUBLISH == 'true'",
	} {
		if !strings.Contains(cond, want) {
			t.Errorf("registry job condition lacks %s:\n%s", want, cond)
		}
	}
	if strings.Contains(cond, "||") {
		t.Errorf("registry job condition has an ||, which could run it outside a stable opt-in tag:\n%s", cond)
	}
	want := map[string]string{"contents": "read", "id-token": "write"}
	if !maps.Equal(job.Permissions, want) {
		t.Errorf("registry job permissions = %v, want %v", job.Permissions, want)
	}
	if !exactVersion.MatchString(job.Env["MCP_PUBLISHER_VERSION"]) {
		t.Errorf("MCP_PUBLISHER_VERSION = %q, want an exact vX.Y.Z pin", job.Env["MCP_PUBLISHER_VERSION"])
	}

	var script strings.Builder
	for _, s := range job.Steps {
		script.WriteString(s.Run + "\n")
		for k, v := range s.Env {
			if strings.Contains(v, "secrets.") && v != "${{ secrets.GITHUB_TOKEN }}" {
				t.Errorf("step %q needs %s=%s: publishing must work without any secret (GitHub OIDC)", s.Name, k, v)
			}
		}
	}
	all := script.String()
	for _, want := range []string{
		"gh release download \"$GITHUB_REF_NAME\" --pattern " + bundleName,
		"cosign verify-blob",
		`"$RUNNER_TEMP/mcp-publisher" validate "$RUNNER_TEMP/server.json"`,
		`"$RUNNER_TEMP/mcp-publisher" login github-oidc`,
		`"$RUNNER_TEMP/mcp-publisher" publish "$RUNNER_TEMP/server.json"`,
	} {
		if !strings.Contains(all, want) {
			t.Errorf("registry job lacks %q", want)
		}
	}
	validateAt, publishAt := strings.Index(all, "mcp-publisher\" validate"), strings.Index(all, "mcp-publisher\" publish")
	if validateAt > publishAt {
		t.Error("registry job publishes before it validates")
	}
	if strings.Contains(all, "releases/latest") {
		t.Error("registry job downloads a mutable releases/latest asset")
	}

	// No other job touches the registry.
	for name, j := range w.Jobs {
		if name == "registry" {
			continue
		}
		for _, s := range j.Steps {
			if strings.Contains(s.Run, "mcp-publisher") {
				t.Errorf("job %s step %q runs mcp-publisher outside the gated registry job", name, s.Name)
			}
		}
	}
}

// serverJSON is the part of server.json the registry validates.
type serverJSON struct {
	Schema      string `json:"$schema"`
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Version     string `json:"version"`
	Packages    []struct {
		RegistryType    string  `json:"registryType"`
		RegistryBaseURL string  `json:"registryBaseUrl"`
		Identifier      string  `json:"identifier"`
		Version         *string `json:"version"`
		FileSHA256      *string `json:"fileSha256"`
		Transport       struct {
			Type string `json:"type"`
			URL  string `json:"url"`
		} `json:"transport"`
		EnvironmentVariables []struct {
			Name string `json:"name"`
		} `json:"environmentVariables"`
	} `json:"packages"`
}

func readServerJSON(t *testing.T, file string) serverJSON {
	t.Helper()
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var s serverJSON
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatalf("%s: %v", file, err)
	}
	return s
}

var (
	githubReleaseAsset = regexp.MustCompile(`^/([a-zA-Z0-9]([a-zA-Z0-9\-]{0,37}[a-zA-Z0-9])?)/([a-zA-Z0-9._\-]+)/releases/download/([^/]+)/([^/]+)$`)
	urlTemplateVar     = regexp.MustCompile(`\{([^}]+)\}`)
	sha256Hex          = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

// registryProblems applies the MCP registry's publish-time rules
// (modelcontextprotocol/registry, internal/validators: validators.go,
// registries/oci.go, registries/mcpb.go) that a file can be checked against
// offline. publishing is true for the file the workflow publishes, which must
// carry the bundle's hash; the committed file must not.
func registryProblems(s serverJSON, publishing bool) []string {
	var p []string
	add := func(format string, a ...any) { p = append(p, fmt.Sprintf(format, a...)) }
	if s.Schema != registrySchema {
		add("$schema = %q, want the registry's current %s", s.Schema, registrySchema)
	}
	if s.Name != registryName {
		add("name = %q, want %s", s.Name, registryName)
	}
	if n := len([]rune(s.Description)); n == 0 || n > 100 {
		add("description is %d characters, the registry allows 1 to 100", n)
	}
	if n := len([]rune(s.Title)); n > 100 {
		add("title is %d characters, the registry allows at most 100", n)
	}
	types := map[string]int{}
	for i, pkg := range s.Packages {
		types[pkg.RegistryType]++
		if strings.ContainsAny(pkg.Identifier, " \t\n") {
			add("packages[%d].identifier has spaces", i)
		}
		switch pkg.RegistryType {
		case "oci":
			if pkg.RegistryBaseURL != "" || pkg.Version != nil || pkg.FileSHA256 != nil {
				add("packages[%d]: an oci package must not have registryBaseUrl, version or fileSha256", i)
			}
			if want := "ghcr.io/freema/vellum:" + s.Version; pkg.Identifier != want {
				add("packages[%d].identifier = %q, want %q", i, pkg.Identifier, want)
			}
		case "mcpb":
			u, err := url.Parse(pkg.Identifier)
			if err != nil || u.Scheme != "https" || u.Host != "github.com" || !githubReleaseAsset.MatchString(u.Path) {
				add("packages[%d].identifier = %q is not an https GitHub release asset", i, pkg.Identifier)
			}
			if !strings.Contains(strings.ToLower(pkg.Identifier), "mcp") {
				add("packages[%d].identifier must contain \"mcp\"", i)
			}
			if want := "https://github.com/freema/vellum/releases/download/v" + s.Version + "/" + bundleName; pkg.Identifier != want {
				add("packages[%d].identifier = %q, want %q", i, pkg.Identifier, want)
			}
			if pkg.RegistryBaseURL != "" {
				add("packages[%d]: an mcpb package must not have registryBaseUrl", i)
			}
			if pkg.Version != nil && *pkg.Version != s.Version {
				add("packages[%d].version = %q, server version %q", i, *pkg.Version, s.Version)
			}
			switch {
			case publishing && (pkg.FileSHA256 == nil || !sha256Hex.MatchString(*pkg.FileSHA256)):
				add("packages[%d].fileSha256 is missing or malformed: the registry requires it for mcpb", i)
			case !publishing && pkg.FileSHA256 != nil:
				add("packages[%d].fileSha256 is committed: the release workflow fills it in from the built bundle, a committed one goes stale", i)
			}
		default:
			add("packages[%d].registryType = %q, want oci or mcpb", i, pkg.RegistryType)
		}

		vars := map[string]bool{}
		for _, e := range pkg.EnvironmentVariables {
			vars[e.Name] = true
		}
		switch pkg.Transport.Type {
		case "stdio":
			if pkg.Transport.URL != "" {
				add("packages[%d]: a stdio transport has no url", i)
			}
		case "streamable-http", "sse":
			u, err := url.Parse(urlTemplateVar.ReplaceAllString(pkg.Transport.URL, "x"))
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				add("packages[%d].transport.url = %q is not a URL", i, pkg.Transport.URL)
			}
			for _, m := range urlTemplateVar.FindAllStringSubmatch(pkg.Transport.URL, -1) {
				if !vars[m[1]] {
					add("packages[%d].transport.url uses {%s}, which is no variable of the package", i, m[1])
				}
			}
		default:
			add("packages[%d].transport.type = %q", i, pkg.Transport.Type)
		}
	}
	if types["oci"] != 1 || types["mcpb"] != 1 {
		add("packages = %v, want one oci and one mcpb", types)
	}
	return p
}

func TestServerJSONPassesTheRegistryRules(t *testing.T) {
	s := readServerJSON(t, "server.json")
	for _, p := range registryProblems(s, false) {
		t.Error(p)
	}
	for _, pkg := range s.Packages {
		if pkg.RegistryType == "mcpb" && pkg.Transport.Type != "stdio" {
			t.Errorf("the bundle runs `vellum mcp <folder>`, a stdio server; transport = %q", pkg.Transport.Type)
		}
	}
}

// TestRegistryFillStep runs the workflow's "Fill in server.json" step as
// written: it adds the hash of the downloaded bundle to the committed file
// and refuses a server.json whose bundle URL is not this tag's.
func TestRegistryFillStep(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the workflow step is a bash script")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	for _, tool := range []string{"jq", "shasum"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not available", tool)
		}
	}
	w, _ := loadFullReleaseWorkflow(t)
	var step workflowStep
	for _, s := range w.Jobs["registry"].Steps {
		if s.Name == "Fill in server.json" {
			step = s
		}
	}
	if step.Run == "" {
		t.Fatal(`registry job has no "Fill in server.json" step`)
	}
	committed, err := os.ReadFile("server.json")
	if err != nil {
		t.Fatal(err)
	}
	version := readServerJSON(t, "server.json").Version

	run := func(t *testing.T, tag string) (string, []byte, error) {
		t.Helper()
		dir, temp := t.TempDir(), t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "server.json"), committed, 0o600); err != nil {
			t.Fatal(err)
		}
		bundle := []byte("PK\x03\x04 stand-in for " + tag)
		if err := os.WriteFile(filepath.Join(temp, bundleName), bundle, 0o600); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(bash, "-e", "-o", "pipefail", "-c", step.Run)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GITHUB_REPOSITORY=freema/vellum", "GITHUB_REF_NAME="+tag, "RUNNER_TEMP="+temp)
		out, err := cmd.CombinedOutput()
		sum := sha256.Sum256(bundle)
		if err != nil {
			return hex.EncodeToString(sum[:]), out, err
		}
		if after, _ := os.ReadFile(filepath.Join(dir, "server.json")); string(after) != string(committed) {
			t.Error("the step changed the checked-out server.json")
		}
		return hex.EncodeToString(sum[:]), out, nil
	}

	t.Run("matching tag", func(t *testing.T) {
		sum, out, err := run(t, "v"+version)
		if err != nil {
			t.Fatalf("v%s: %v\n%s", version, err, out)
		}
		// The step prints the file it wrote; read it back from there.
		filled := filepath.Join(t.TempDir(), "server.json")
		start := strings.Index(string(out), "{")
		if start < 0 {
			t.Fatalf("step printed no JSON:\n%s", out)
		}
		if err := os.WriteFile(filled, out[start:], 0o600); err != nil {
			t.Fatal(err)
		}
		s := readServerJSON(t, filled)
		for _, p := range registryProblems(s, true) {
			t.Error(p)
		}
		for _, pkg := range s.Packages {
			switch pkg.RegistryType {
			case "mcpb":
				if pkg.FileSHA256 == nil || *pkg.FileSHA256 != sum {
					t.Errorf("mcpb fileSha256 = %v, want the downloaded bundle's %s", pkg.FileSHA256, sum)
				}
			default:
				if pkg.FileSHA256 != nil {
					t.Errorf("%s package got a fileSha256", pkg.RegistryType)
				}
			}
		}
	})

	t.Run("other tag", func(t *testing.T) {
		_, out, err := run(t, "v99.0.0")
		if err == nil {
			t.Fatalf("v99.0.0 filled a server.json whose bundle URL names v%s:\n%s", version, out)
		}
		if !strings.Contains(string(out), "needs exactly one mcpb package") {
			t.Errorf("the refusal does not say why:\n%s", out)
		}
	})
}

func TestDockerImageCarriesTheRegistryLabel(t *testing.T) {
	b, err := os.ReadFile("Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	// The label must be in the final stage, the image that is pushed.
	last := strings.LastIndex(src, "\nFROM ")
	if last < 0 {
		t.Fatal("Dockerfile has no FROM")
	}
	want := `LABEL io.modelcontextprotocol.server.name="` + readServerJSON(t, "server.json").Name + `"`
	if !strings.Contains(src[last:], want) {
		t.Errorf("the final Dockerfile stage lacks %s; the registry rejects the oci package without it", want)
	}
}
