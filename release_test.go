package vellum

// These tests guard the release setup (.goreleaser.yaml, the release
// workflow and the Taskfile) against edits that would quietly break a tag:
// a renamed archive breaks every releases/latest/download link, a dropped
// build tag ships binaries without the web UI, and a second "create the
// release" step makes the tag's job fail halfway through.

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type goreleaserConfig struct {
	Version int `yaml:"version"`
	Before  struct {
		Hooks []string `yaml:"hooks"`
	} `yaml:"before"`
	Builds []struct {
		ID      string   `yaml:"id"`
		Main    string   `yaml:"main"`
		Binary  string   `yaml:"binary"`
		Env     []string `yaml:"env"`
		Flags   []string `yaml:"flags"`
		Tags    []string `yaml:"tags"`
		Ldflags []string `yaml:"ldflags"`
		Goos    []string `yaml:"goos"`
		Goarch  []string `yaml:"goarch"`
		Ignore  []any    `yaml:"ignore"`
	} `yaml:"builds"`
	Archives []struct {
		NameTemplate    string   `yaml:"name_template"`
		Formats         []string `yaml:"formats"`
		FormatOverrides []struct {
			Goos    string   `yaml:"goos"`
			Formats []string `yaml:"formats"`
		} `yaml:"format_overrides"`
	} `yaml:"archives"`
	Checksum struct {
		NameTemplate string `yaml:"name_template"`
	} `yaml:"checksum"`
	Changelog struct {
		Disable any `yaml:"disable"`
	} `yaml:"changelog"`
	Release struct {
		Prerelease string `yaml:"prerelease"`
		Disable    any    `yaml:"disable"`
	} `yaml:"release"`
	Dockers   []any `yaml:"dockers"`
	DockersV2 []any `yaml:"dockers_v2"`
}

type workflowStep struct {
	Name string            `yaml:"name"`
	Uses string            `yaml:"uses"`
	Run  string            `yaml:"run"`
	With map[string]string `yaml:"with"`
	Env  map[string]string `yaml:"env"`
}

type workflowJob struct {
	Needs       any               `yaml:"needs"`
	Permissions map[string]string `yaml:"permissions"`
	Steps       []workflowStep    `yaml:"steps"`
}

type workflow struct {
	Jobs map[string]workflowJob `yaml:"jobs"`
}

func loadYAML(t *testing.T, path string, into any) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(b, into); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

func loadGoreleaser(t *testing.T) goreleaserConfig {
	t.Helper()
	var c goreleaserConfig
	loadYAML(t, ".goreleaser.yaml", &c)
	return c
}

func loadReleaseWorkflow(t *testing.T) workflow {
	t.Helper()
	var w workflow
	loadYAML(t, filepath.Join(".github", "workflows", "release.yml"), &w)
	return w
}

// stepUsing returns the job's step that runs the given action (any version).
func stepUsing(job workflowJob, action string) (workflowStep, bool) {
	for _, s := range job.Steps {
		if strings.HasPrefix(s.Uses, action+"@") {
			return s, true
		}
	}
	return workflowStep{}, false
}

func stepNamed(job workflowJob, name string) (workflowStep, bool) {
	for _, s := range job.Steps {
		if s.Name == name {
			return s, true
		}
	}
	return workflowStep{}, false
}

func TestGoreleaserBuildsEveryPlatformWithTheUI(t *testing.T) {
	c := loadGoreleaser(t)
	if c.Version != 2 {
		t.Errorf("config schema version = %d, want 2", c.Version)
	}
	if len(c.Builds) != 1 {
		t.Fatalf("builds = %d, want exactly one", len(c.Builds))
	}
	b := c.Builds[0]
	if b.Main != "./cmd/vellum" || b.Binary != "vellum" {
		t.Errorf("build main/binary = %q/%q, want ./cmd/vellum/vellum", b.Main, b.Binary)
	}
	if !slices.Contains(b.Env, "CGO_ENABLED=0") {
		t.Errorf("build env %v must turn cgo off: the binary has to stay static", b.Env)
	}
	if !slices.Contains(b.Tags, "embedspa") {
		t.Errorf("build tags %v lack embedspa: the binaries would ship without the web UI", b.Tags)
	}
	if !slices.Contains(b.Flags, "-trimpath") {
		t.Errorf("build flags %v lack -trimpath", b.Flags)
	}
	ld := strings.Join(b.Ldflags, " ")
	for _, want := range []string{"-s", "-w", "-X main.version={{ .Tag }}"} {
		if !strings.Contains(ld, want) {
			t.Errorf("ldflags %q lack %q", ld, want)
		}
	}
	slices.Sort(b.Goos)
	slices.Sort(b.Goarch)
	if !slices.Equal(b.Goos, []string{"darwin", "linux", "windows"}) {
		t.Errorf("goos = %v, want darwin, linux, windows", b.Goos)
	}
	if !slices.Equal(b.Goarch, []string{"amd64", "arm64"}) {
		t.Errorf("goarch = %v, want amd64, arm64", b.Goarch)
	}
	if len(b.Ignore) != 0 {
		t.Errorf("build ignores %v: every os/arch pair should ship", b.Ignore)
	}

	// The embed reads web/dist, so the SPA must be built before any binary.
	hooks := strings.Join(c.Before.Hooks, "\n")
	ci := strings.Index(hooks, "npm --prefix web ci")
	build := strings.Index(hooks, "npm --prefix web run build")
	if ci < 0 || build < 0 || ci > build {
		t.Errorf("before.hooks must run `npm --prefix web ci` and then `npm --prefix web run build`, got:\n%s", hooks)
	}
}

func TestGoreleaserArchiveNamesStayStable(t *testing.T) {
	c := loadGoreleaser(t)
	if len(c.Archives) != 1 {
		t.Fatalf("archives = %d, want exactly one", len(c.Archives))
	}
	a := c.Archives[0]
	// releases/latest/download/<name> only works when <name> is the same
	// for every release, so no version may leak into it.
	if a.NameTemplate != "{{ .ProjectName }}_{{ .Os }}_{{ .Arch }}" {
		t.Errorf("archive name_template = %q, want {{ .ProjectName }}_{{ .Os }}_{{ .Arch }}", a.NameTemplate)
	}
	for _, v := range []string{".Version", ".Tag", ".Major", ".Minor", ".Patch", ".ShortCommit"} {
		if strings.Contains(a.NameTemplate, v) {
			t.Errorf("archive name_template %q contains %s", a.NameTemplate, v)
		}
	}
	if !slices.Equal(a.Formats, []string{"tar.gz"}) {
		t.Errorf("archive formats = %v, want [tar.gz]", a.Formats)
	}
	var windows []string
	for _, o := range a.FormatOverrides {
		if o.Goos == "windows" {
			windows = o.Formats
		}
	}
	if !slices.Equal(windows, []string{"zip"}) {
		t.Errorf("windows archive formats = %v, want [zip]", windows)
	}
	if c.Checksum.NameTemplate != "checksums.txt" {
		t.Errorf("checksum name = %q, want checksums.txt (actions/attest reads it)", c.Checksum.NameTemplate)
	}
}

func TestGoreleaserReleaseSettings(t *testing.T) {
	c := loadGoreleaser(t)
	if c.Release.Prerelease != "auto" {
		t.Errorf("release.prerelease = %q, want auto so a beta tag stays a pre-release", c.Release.Prerelease)
	}
	if c.Release.Disable != nil {
		t.Errorf("release.disable is set: GoReleaser is what creates the GitHub Release")
	}
	// The changelog step is the one that loads --release-notes; disabling
	// it would publish the release with an empty body.
	if c.Changelog.Disable != nil {
		t.Errorf("changelog.disable is set: the CHANGELOG.md notes would be dropped")
	}
	if len(c.Dockers) != 0 || len(c.DockersV2) != 0 {
		t.Errorf("GoReleaser must not build images: the workflow's image job does")
	}
}

var exactVersion = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)

func TestReleaseWorkflowUsesPinnedGoreleaser(t *testing.T) {
	w := loadReleaseWorkflow(t)
	job, ok := w.Jobs["release"]
	if !ok {
		t.Fatal("release.yml has no release job")
	}
	if needs, _ := job.Needs.(string); needs != "image" {
		t.Errorf("release job needs = %v, want image: a failed image must leave no release", job.Needs)
	}
	for _, s := range job.Steps {
		if strings.Contains(s.Run, "gh release create") {
			t.Errorf("step %q still creates the release itself; only GoReleaser may", s.Name)
		}
	}

	checkout, ok := stepUsing(job, "actions/checkout")
	if !ok || checkout.With["fetch-depth"] != "0" {
		t.Errorf("checkout needs fetch-depth: 0 so GoReleaser sees the tags, got %v", checkout.With)
	}
	if _, ok := stepUsing(job, "actions/setup-go"); !ok {
		t.Error("release job does not set up Go")
	}
	node, ok := stepUsing(job, "actions/setup-node")
	if !ok || node.With["node-version"] != "22" {
		t.Errorf("release job needs Node 22 for the SPA build, got %v", node.With)
	}

	gr, ok := stepUsing(job, "goreleaser/goreleaser-action")
	if !ok {
		t.Fatal("release job does not run goreleaser/goreleaser-action")
	}
	if !exactVersion.MatchString(gr.With["version"]) {
		t.Errorf("GoReleaser version = %q, want an exact vX.Y.Z pin (latest broke CI once already)", gr.With["version"])
	}
	args := strings.Fields(gr.With["args"])
	if len(args) == 0 || args[0] != "release" || !slices.Contains(args, "--clean") {
		t.Errorf("GoReleaser args = %q, want release --clean ...", gr.With["args"])
	}
	if slices.Contains(args, "--snapshot") || slices.Contains(args, "--skip=publish") {
		t.Errorf("GoReleaser args = %q would not publish the release", gr.With["args"])
	}
	// The changelog step writes the notes outside the checkout.
	if !strings.Contains(gr.With["args"], "--release-notes ${{ runner.temp }}/notes.md") {
		t.Errorf("GoReleaser args = %q, want --release-notes ${{ runner.temp }}/notes.md", gr.With["args"])
	}
	if gr.Env["GITHUB_TOKEN"] == "" {
		t.Error("GoReleaser step has no GITHUB_TOKEN")
	}

	// CI runs `goreleaser check` with the same version on every PR.
	var ci workflow
	loadYAML(t, filepath.Join(".github", "workflows", "ci.yml"), &ci)
	check, ok := stepUsing(ci.Jobs["release-config"], "goreleaser/goreleaser-action")
	if !ok || check.With["args"] != "check" || check.With["version"] != gr.With["version"] {
		t.Errorf("ci.yml release-config must run `check` with GoReleaser %s, got %v", gr.With["version"], check.With)
	}

	// The pin in the Taskfile is what `task release-snapshot` runs locally.
	taskfile, err := os.ReadFile("Taskfile.yml")
	if err != nil {
		t.Fatal(err)
	}
	var tf struct {
		Vars  map[string]any `yaml:"vars"`
		Tasks map[string]struct {
			Cmds []string `yaml:"cmds"`
		} `yaml:"tasks"`
	}
	if err := yaml.Unmarshal(taskfile, &tf); err != nil {
		t.Fatalf("Taskfile.yml: %v", err)
	}
	if got := tf.Vars["GORELEASER_VERSION"]; got != gr.With["version"] {
		t.Errorf("Taskfile GORELEASER_VERSION = %v, release.yml pins %q; keep them equal", got, gr.With["version"])
	}
	snap, ok := tf.Tasks["release-snapshot"]
	cmds := strings.Join(snap.Cmds, "\n")
	if !ok || !strings.Contains(cmds, "release --snapshot --clean") {
		t.Errorf("task release-snapshot must run `goreleaser release --snapshot --clean`, got:\n%s", cmds)
	}
}

func TestReleaseWorkflowAttestsTheArchives(t *testing.T) {
	job := loadReleaseWorkflow(t).Jobs["release"]
	for _, p := range []string{"id-token", "attestations", "contents"} {
		if job.Permissions[p] != "write" {
			t.Errorf("release job permission %s = %q, want write", p, job.Permissions[p])
		}
	}
	attest, ok := stepUsing(job, "actions/attest")
	if !ok {
		t.Fatal("release job has no actions/attest step")
	}
	if attest.With["subject-checksums"] != "./dist/checksums.txt" {
		t.Errorf("attest subject-checksums = %q, want ./dist/checksums.txt", attest.With["subject-checksums"])
	}
	attestAt := slices.IndexFunc(job.Steps, func(s workflowStep) bool { return s.Uses == attest.Uses })
	grAt := slices.IndexFunc(job.Steps, func(s workflowStep) bool {
		return strings.HasPrefix(s.Uses, "goreleaser/goreleaser-action@")
	})
	if attestAt < grAt {
		t.Error("attest runs before GoReleaser has written dist/checksums.txt")
	}
}

func TestReleaseWorkflowImageJobKeepsLatestStable(t *testing.T) {
	job, ok := loadReleaseWorkflow(t).Jobs["image"]
	if !ok {
		t.Fatal("release.yml has no image job")
	}
	meta, ok := stepUsing(job, "docker/metadata-action")
	if !ok {
		t.Fatal("image job has no docker/metadata-action step")
	}
	if !strings.Contains(meta.With["tags"], "type=raw,value=latest,enable=${{ !contains(github.ref_name, '-') }}") {
		t.Errorf("image tags no longer keep pre-releases off :latest:\n%s", meta.With["tags"])
	}
}

// TestReleaseNotesExtraction runs the workflow's changelog step as written
// and checks the file GoReleaser is pointed at: a stable tag gets its own
// CHANGELOG section, a beta the section of the version it leads up to.
func TestReleaseNotesExtraction(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the workflow step is a bash script")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	step, ok := stepNamed(loadReleaseWorkflow(t).Jobs["release"], "Extract changelog section")
	if !ok {
		t.Fatal(`release job has no "Extract changelog section" step`)
	}

	repo, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	changelog := "# Changelog\n\n## [Unreleased]\n\n## [2.1.0] — 2030-01-02\n\n### Added\n\n- The new thing.\n\n" +
		"## [2.0.0] — 2030-01-01\n\n- The old thing.\n"

	run := func(t *testing.T, tag string) string {
		t.Helper()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "CHANGELOG.md"), []byte(changelog), 0o600); err != nil {
			t.Fatal(err)
		}
		temp := t.TempDir()
		cmd := exec.Command(bash, "-e", "-c", step.Run)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GITHUB_REF_NAME="+tag, "RUNNER_TEMP="+temp)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("changelog step for %s: %v\n%s", tag, err, out)
		}
		// Nothing may be left in the checkout: GoReleaser treats any
		// untracked file as a dirty tree and refuses to release.
		entries, _ := os.ReadDir(dir)
		if len(entries) != 1 {
			t.Errorf("changelog step left files in the checkout: %v", entries)
		}
		notes, err := os.ReadFile(filepath.Join(temp, "notes.md"))
		if err != nil {
			t.Fatalf("notes for %s: %v", tag, err)
		}
		return string(notes)
	}

	t.Run("stable", func(t *testing.T) {
		notes := run(t, "v2.1.0")
		if !strings.Contains(notes, "The new thing.") || strings.Contains(notes, "The old thing.") {
			t.Errorf("v2.1.0 notes = %q, want only the 2.1.0 section", notes)
		}
	})
	t.Run("beta carries its target version", func(t *testing.T) {
		if notes := run(t, "v2.1.0-beta.1"); !strings.Contains(notes, "The new thing.") {
			t.Errorf("v2.1.0-beta.1 notes = %q, want the 2.1.0 section", notes)
		}
	})
	t.Run("missing section", func(t *testing.T) {
		if notes := run(t, "v9.9.9"); !strings.Contains(notes, "No changelog section found for v9.9.9") {
			t.Errorf("v9.9.9 notes = %q, want the placeholder", notes)
		}
	})
	t.Run("real changelog has the latest stable section", func(t *testing.T) {
		b, err := os.ReadFile(filepath.Join(repo, "CHANGELOG.md"))
		if err != nil {
			t.Fatal(err)
		}
		m := regexp.MustCompile(`(?m)^## \[(\d+\.\d+\.\d+)\]`).FindSubmatch(b)
		if m == nil {
			t.Skip("CHANGELOG.md has no released section yet")
		}
		changelog = string(b)
		if notes := run(t, "v"+string(m[1])); strings.Contains(notes, "No changelog section found") {
			t.Errorf("no notes extracted from CHANGELOG.md for v%s", m[1])
		}
	})
}
