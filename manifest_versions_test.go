package vellum

// These tests guard the version that server.json, the Claude Desktop bundle
// manifest and the Claude plugin manifests carry by hand. It lagged three releases behind once (1.9.0
// while v1.12.1 was out), so `task bump` rewrites it and the release
// workflow refuses a stable tag whose manifests do not match.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const manifestScript = "scripts/manifest-versions.sh"

// manifestFiles are the files whose version the release depends on, in the
// order the script lists them.
var manifestFiles = []string{
	"server.json",
	"mcpb/manifest.json",
	marketplaceFile,
	pluginFile,
}

// readmeFile pins the image in its deployment examples; bump rewrites those
// tags along with the manifests.
const readmeFile = "README.md"

// readmeImagePin finds an image tag pinned to a release in the README.
var readmeImagePin = regexp.MustCompile(`ghcr\.io/freema/vellum:(\d+\.\d+\.\d+)`)

const (
	marketplaceFile = ".claude-plugin/marketplace.json"
	pluginFile      = "plugins/claude/vellum/.claude-plugin/plugin.json"
)

// pinnedVersion finds the release in a package identifier: an image tag
// (ghcr.io/freema/vellum:1.2.3) or a release download URL (.../v1.2.3/x).
var pinnedVersion = regexp.MustCompile(`(?::|/releases/download/v)(\d+\.\d+\.\d+)(?:/|"|$)`)

var stableVersion = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)$`)

// manifestVersions reads every version field and every pinned package
// identifier straight from the JSON, independently of the script, keyed
// "file .path".
func manifestVersions(t *testing.T, root string) map[string]string {
	t.Helper()
	read := func(file string, into any) {
		b, err := os.ReadFile(filepath.Join(root, file))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(b, into); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
	}
	type versioned struct {
		Version string `json:"version"`
	}
	got := map[string]string{}

	var server struct {
		Version  string `json:"version"`
		Packages []struct {
			// The registry rejects a version field on an image package,
			// so it is optional here.
			Version    *string `json:"version"`
			Identifier string  `json:"identifier"`
		} `json:"packages"`
	}
	read("server.json", &server)
	got["server.json .version"] = server.Version
	for i, p := range server.Packages {
		if p.Version != nil {
			got[fmt.Sprintf("server.json .packages[%d].version", i)] = *p.Version
		}
		pinned := "(unpinned)"
		if m := pinnedVersion.FindStringSubmatch(p.Identifier); m != nil {
			pinned = m[1]
		}
		got[fmt.Sprintf("server.json .packages[%d].identifier", i)] = pinned
	}

	var bundle versioned
	read("mcpb/manifest.json", &bundle)
	got["mcpb/manifest.json .version"] = bundle.Version

	var market struct {
		Plugins []versioned `json:"plugins"`
	}
	read(marketplaceFile, &market)
	for i, p := range market.Plugins {
		got[fmt.Sprintf("%s .plugins[%d].version", marketplaceFile, i)] = p.Version
	}

	var plugin versioned
	read(pluginFile, &plugin)
	got[pluginFile+" .version"] = plugin.Version
	return got
}

func semver(t *testing.T, v string) [3]int {
	t.Helper()
	m := stableVersion.FindStringSubmatch(v)
	if m == nil {
		t.Fatalf("%q is not a stable x.y.z version", v)
	}
	var out [3]int
	for i := range out {
		out[i], _ = strconv.Atoi(m[i+1])
	}
	return out
}

func lessVersion(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// latestStableTag is the highest vX.Y.Z tag in the repository, or "" when
// git or the tags are not there (a shallow CI checkout has none).
func latestStableTag(t *testing.T) string {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		return ""
	}
	out, err := exec.Command(git, "tag", "--list", "v*").Output()
	if err != nil {
		return ""
	}
	best := ""
	for _, tag := range strings.Fields(string(out)) {
		v := strings.TrimPrefix(tag, "v")
		if !stableVersion.MatchString(v) {
			continue // pre-releases do not move the manifests
		}
		if best == "" || lessVersion(semver(t, best), semver(t, v)) {
			best = v
		}
	}
	return best
}

// requireScriptTools skips when the script cannot run here.
func requireScriptTools(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the manifest script is a bash script")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not available")
	}
	return bash
}

// manifestSandbox copies the script and the real manifests into a fresh
// directory with the repository's layout, so a test can rewrite them.
func manifestSandbox(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, f := range append([]string{manifestScript, readmeFile}, manifestFiles...) {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		dst := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func readFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, f := range manifestFiles {
		b, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			t.Fatal(err)
		}
		out[f] = string(b)
	}
	return out
}

func runScript(t *testing.T, bash, dir string, args ...string) (string, error) {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join(dir, manifestScript))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bash, append([]string{abs}, args...)...)
	// The script finds the manifests from its own location, not the cwd.
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "GITHUB_ACTIONS=")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestManifestVersionsAgreeAndKeepUpWithTheLatestTag(t *testing.T) {
	got := manifestVersions(t, ".")
	if len(got) < 7 {
		t.Fatalf("found %d version fields, want at least 7: %v", len(got), got)
	}
	want := got["server.json .version"]
	for field, v := range got {
		if v != want {
			t.Errorf("%s = %q, server.json .version = %q; run task bump", field, v, want)
		}
	}
	semver(t, want)

	tag := latestStableTag(t)
	if tag == "" {
		t.Log("no stable git tags here; skipped the comparison with the latest tag")
	} else if lessVersion(semver(t, want), semver(t, tag)) {
		t.Errorf("manifests say %s but v%s is already tagged; run task bump VERSION=%s", want, tag, tag)
	}

	// The script must agree with this reading of the files.
	bash := requireScriptTools(t)
	if out, err := runScript(t, bash, ".", "check", want); err != nil {
		t.Errorf("script check %s on the repository: %v\n%s", want, err, out)
	}
	out, err := runScript(t, bash, ".", "list")
	if err != nil {
		t.Fatalf("script list: %v\n%s", err, out)
	}
	for field := range got {
		file, path, _ := strings.Cut(field, " ")
		if !strings.Contains(out, file+"\t"+path+"\t"+want) {
			t.Errorf("script list misses %s, got:\n%s", field, out)
		}
	}
}

func TestManifestBumpRewritesOnlyTheVersions(t *testing.T) {
	bash := requireScriptTools(t)
	dir := manifestSandbox(t)
	before := readFiles(t, dir)

	if out, err := runScript(t, bash, dir, "bump", "9.8.7"); err != nil {
		t.Fatalf("bump 9.8.7: %v\n%s", err, out)
	}
	for field, v := range manifestVersions(t, dir) {
		if v != "9.8.7" {
			t.Errorf("after bump %s = %q, want 9.8.7", field, v)
		}
	}
	after := readFiles(t, dir)
	for _, f := range manifestFiles {
		old, cur := strings.Split(before[f], "\n"), strings.Split(after[f], "\n")
		if len(old) != len(cur) {
			t.Errorf("%s: bump changed the line count %d -> %d", f, len(old), len(cur))
			continue
		}
		changed := 0
		for i := range old {
			if old[i] == cur[i] {
				continue
			}
			changed++
			want := regexp.MustCompile(`"version": "[^"]*"`).ReplaceAllString(old[i], `"version": "9.8.7"`)
			if strings.Contains(old[i], `"identifier"`) {
				want = pinnedVersion.ReplaceAllStringFunc(old[i], func(m string) string {
					return strings.Replace(m, pinnedVersion.FindStringSubmatch(m)[1], "9.8.7", 1)
				})
			}
			if cur[i] != want {
				t.Errorf("%s line %d: bump changed %q to %q", f, i+1, old[i], cur[i])
			}
		}
		if changed == 0 {
			t.Errorf("%s: bump changed nothing", f)
		}
	}

	// The README changes only in its pinned image tags.
	oldReadme, err := os.ReadFile(readmeFile)
	if err != nil {
		t.Fatal(err)
	}
	newReadme, err := os.ReadFile(filepath.Join(dir, readmeFile))
	if err != nil {
		t.Fatal(err)
	}
	wantReadme := readmeImagePin.ReplaceAllString(string(oldReadme), "ghcr.io/freema/vellum:9.8.7")
	if string(newReadme) != wantReadme {
		t.Errorf("bump changed %s beyond its pinned image tags", readmeFile)
	}
	if !strings.Contains(string(newReadme), "ghcr.io/freema/vellum:9.8.7") {
		t.Errorf("bump left the image tags in %s alone", readmeFile)
	}

	t.Run("accepts a v prefix", func(t *testing.T) {
		if out, err := runScript(t, bash, dir, "bump", "v9.8.8"); err != nil {
			t.Fatalf("bump v9.8.8: %v\n%s", err, out)
		}
		if v := manifestVersions(t, dir)["server.json .version"]; v != "9.8.8" {
			t.Errorf("server.json .version = %q, want 9.8.8", v)
		}
	})

	t.Run("rejects anything but x.y.z", func(t *testing.T) {
		snapshot := readFiles(t, dir)
		for _, bad := range []string{"", "1.2", "1.2.3-beta.1", "v1.13.0-beta.1-4-gabcdef-dirty", "1.2.3/x", "1.2.3\"", "01x.2.3"} {
			if out, err := runScript(t, bash, dir, "bump", bad); err == nil {
				t.Errorf("bump %q succeeded:\n%s", bad, out)
			}
		}
		if got := readFiles(t, dir); fmt.Sprint(got) != fmt.Sprint(snapshot) {
			t.Error("a rejected bump changed the manifests")
		}
	})

	t.Run("refuses a version field it does not know", func(t *testing.T) {
		server := filepath.Join(dir, "server.json")
		b, err := os.ReadFile(server)
		if err != nil {
			t.Fatal(err)
		}
		// Another version key (a future package or field) that the
		// script's list does not cover must stop the bump, not be skipped.
		extra := strings.Replace(string(b), `"websiteUrl"`, `"_meta": {"version": "0.1"},`+"\n  "+`"websiteUrl"`, 1)
		if err := os.WriteFile(server, []byte(extra), 0o600); err != nil {
			t.Fatal(err)
		}
		snapshot := readFiles(t, dir)
		out, err := runScript(t, bash, dir, "bump", "9.9.9")
		if err == nil {
			t.Fatalf("bump succeeded with an unlisted version key:\n%s", out)
		}
		if !strings.Contains(out, "server.json") {
			t.Errorf("refusal does not name server.json:\n%s", out)
		}
		if got := readFiles(t, dir); fmt.Sprint(got) != fmt.Sprint(snapshot) {
			t.Error("a refused bump changed the manifests")
		}
	})

	t.Run("refuses a package identifier without a version", func(t *testing.T) {
		dir := manifestSandbox(t)
		server := filepath.Join(dir, "server.json")
		b, err := os.ReadFile(server)
		if err != nil {
			t.Fatal(err)
		}
		// An image without a tag would leave the registry entry pointing at
		// whatever :latest is when someone installs it.
		unpinned := regexp.MustCompile(`ghcr\.io/freema/vellum:[^"]*`).ReplaceAllString(string(b), "ghcr.io/freema/vellum")
		if unpinned == string(b) {
			t.Fatal("server.json has no ghcr.io/freema/vellum:<version> identifier")
		}
		if err := os.WriteFile(server, []byte(unpinned), 0o600); err != nil {
			t.Fatal(err)
		}
		snapshot := readFiles(t, dir)
		out, err := runScript(t, bash, dir, "bump", "9.9.9")
		if err == nil || !strings.Contains(out, "server.json has an identifier without a release version") {
			t.Fatalf("bump with an unpinned identifier: err %v, output:\n%s", err, out)
		}
		if got := readFiles(t, dir); fmt.Sprint(got) != fmt.Sprint(snapshot) {
			t.Error("a refused bump changed the manifests")
		}
		if out, err := runScript(t, bash, dir, "check", "9.9.9"); err == nil || !strings.Contains(out, ".packages[0].identifier is (unpinned)") {
			t.Errorf("check with an unpinned identifier: err %v, output:\n%s", err, out)
		}
	})
}

func TestTaskBumpRunsTheScript(t *testing.T) {
	b, err := os.ReadFile("Taskfile.yml")
	if err != nil {
		t.Fatal(err)
	}
	var tf struct {
		Tasks map[string]struct {
			Cmds []string `yaml:"cmds"`
		} `yaml:"tasks"`
	}
	if err := yaml.Unmarshal(b, &tf); err != nil {
		t.Fatalf("Taskfile.yml: %v", err)
	}
	bump, ok := tf.Tasks["bump"]
	if !ok {
		t.Fatal("Taskfile.yml has no bump task")
	}
	if cmds := strings.Join(bump.Cmds, "\n"); !strings.Contains(cmds, manifestScript+` bump "{{.VERSION}}"`) {
		t.Errorf("task bump must run %s bump \"{{.VERSION}}\", got:\n%s", manifestScript, cmds)
	}
}

// TestReleaseWorkflowChecksManifestVersions runs the workflow's check step
// as written: a stable tag with a manifest behind fails and names the file,
// a pre-release tag passes untouched, and no image is built before it.
func TestReleaseWorkflowChecksManifestVersions(t *testing.T) {
	w := loadReleaseWorkflow(t)
	job, ok := w.Jobs["manifests"]
	if !ok {
		t.Fatal("release.yml has no manifests job")
	}
	if needs, _ := w.Jobs["image"].Needs.(string); needs != "manifests" {
		t.Errorf("image job needs = %v, want manifests: the image must not build for a tag with stale manifests", w.Jobs["image"].Needs)
	}
	step, ok := stepNamed(job, "Check manifest versions")
	if !ok {
		t.Fatal(`manifests job has no "Check manifest versions" step`)
	}
	if _, ok := stepUsing(job, "actions/checkout"); !ok {
		t.Error("manifests job does not check out the repository")
	}

	bash := requireScriptTools(t)
	current := manifestVersions(t, ".")["server.json .version"]

	run := func(t *testing.T, dir, tag string, env ...string) (string, error) {
		t.Helper()
		cmd := exec.Command(bash, "-e", "-c", step.Run)
		cmd.Dir = dir
		cmd.Env = append(append(os.Environ(), "GITHUB_REF_NAME="+tag, "GITHUB_ACTIONS="), env...)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	t.Run("matching stable tag passes", func(t *testing.T) {
		if out, err := run(t, manifestSandbox(t), "v"+current); err != nil {
			t.Errorf("v%s: %v\n%s", current, err, out)
		}
	})

	t.Run("stale manifests fail naming every file", func(t *testing.T) {
		out, err := run(t, manifestSandbox(t), "v99.0.0")
		if err == nil {
			t.Fatalf("v99.0.0 passed with %s manifests:\n%s", current, out)
		}
		for _, f := range manifestFiles {
			if !strings.Contains(out, f+" is out of date") {
				t.Errorf("output does not name %s:\n%s", f, out)
			}
		}
		if !strings.Contains(out, "task bump VERSION=99.0.0") {
			t.Errorf("output does not say how to fix it:\n%s", out)
		}
	})

	t.Run("one file behind is the one named", func(t *testing.T) {
		dir := manifestSandbox(t)
		if out, err := runScript(t, bash, dir, "bump", "99.0.0"); err != nil {
			t.Fatalf("bump: %v\n%s", err, out)
		}
		plugin := filepath.Join(dir, pluginFile)
		b, _ := os.ReadFile(plugin)
		if err := os.WriteFile(plugin, []byte(strings.Replace(string(b), "99.0.0", "1.9.0", 1)), 0o600); err != nil {
			t.Fatal(err)
		}
		out, err := run(t, dir, "v99.0.0")
		if err == nil {
			t.Fatalf("v99.0.0 passed with a stale plugin.json:\n%s", out)
		}
		if !strings.Contains(out, pluginFile+" is out of date: .version is 1.9.0") {
			t.Errorf("output does not name plugin.json:\n%s", out)
		}
		for _, f := range manifestFiles {
			if f != pluginFile && strings.Contains(out, f+" is out of date") {
				t.Errorf("output blames %s, which is current:\n%s", f, out)
			}
		}
	})

	t.Run("a stale package identifier is named", func(t *testing.T) {
		dir := manifestSandbox(t)
		if out, err := runScript(t, bash, dir, "bump", "99.0.0"); err != nil {
			t.Fatalf("bump: %v\n%s", err, out)
		}
		server := filepath.Join(dir, "server.json")
		b, _ := os.ReadFile(server)
		stale := strings.Replace(string(b), "/releases/download/v99.0.0/", "/releases/download/v1.9.0/", 1)
		if stale == string(b) {
			t.Fatal("server.json has no release download identifier")
		}
		if err := os.WriteFile(server, []byte(stale), 0o600); err != nil {
			t.Fatal(err)
		}
		out, err := run(t, dir, "v99.0.0")
		if err == nil {
			t.Fatalf("v99.0.0 passed with a bundle URL of v1.9.0:\n%s", out)
		}
		if !regexp.MustCompile(`server\.json is out of date: \.packages\[\d\]\.identifier is 1\.9\.0`).MatchString(out) {
			t.Errorf("output does not name the identifier:\n%s", out)
		}
	})

	t.Run("annotates the file on GitHub", func(t *testing.T) {
		out, _ := run(t, manifestSandbox(t), "v99.0.0", "GITHUB_ACTIONS=true")
		if !strings.Contains(out, "::error file=server.json::") {
			t.Errorf("no ::error annotation for server.json:\n%s", out)
		}
	})

	t.Run("pre-release tag skips the check", func(t *testing.T) {
		dir := manifestSandbox(t)
		before := readFiles(t, dir)
		if out, err := run(t, dir, "v99.0.0-beta.1"); err != nil {
			t.Errorf("v99.0.0-beta.1 failed: %v\n%s", err, out)
		}
		if fmt.Sprint(readFiles(t, dir)) != fmt.Sprint(before) {
			t.Error("the check changed the manifests")
		}
	})
}

// TestReadmeImagePinsMatchTheManifests keeps the README's deployment
// examples on the release the manifests carry: they once pinned 1.9.0
// for three releases. task bump rewrites them together.
func TestReadmeImagePinsMatchTheManifests(t *testing.T) {
	want := manifestVersions(t, ".")["server.json .version"]
	b, err := os.ReadFile(readmeFile)
	if err != nil {
		t.Fatal(err)
	}
	pins := readmeImagePin.FindAllStringSubmatch(string(b), -1)
	if len(pins) == 0 {
		t.Fatalf("%s pins no image version; the deployment examples should", readmeFile)
	}
	for _, m := range pins {
		if m[1] != want {
			t.Errorf("%s pins %s, the manifests carry %s; run task bump VERSION=%s", readmeFile, m[0], want, want)
		}
	}
}
