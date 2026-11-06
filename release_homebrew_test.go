package vellum

// These tests guard the Homebrew cask (homebrew_casks in .goreleaser.yaml):
// `brew install freema/tap/vellum` depends on the tap coordinates, a beta tag
// must not reach `brew upgrade`, a release must not fail while the TAP_TOKEN
// secret does not exist yet, and the unsigned binary only starts on macOS
// once the quarantine flag is gone.

import (
	"slices"
	"strings"
	"testing"
	"text/template"
)

type homebrewCask struct {
	Name       string   `yaml:"name"`
	IDs        []string `yaml:"ids"`
	Binaries   []string `yaml:"binaries"`
	Directory  string   `yaml:"directory"`
	Homepage   string   `yaml:"homepage"`
	SkipUpload string   `yaml:"skip_upload"`
	Repository struct {
		Owner string `yaml:"owner"`
		Name  string `yaml:"name"`
		Token string `yaml:"token"`
	} `yaml:"repository"`
	Hooks struct {
		Post struct {
			InstallSteps string `yaml:"install_steps"`
			Install      string `yaml:"install"`
		} `yaml:"post"`
	} `yaml:"hooks"`
}

func loadHomebrewCasks(t *testing.T) (casks []homebrewCask, brews []any) {
	t.Helper()
	var c struct {
		Casks []homebrewCask `yaml:"homebrew_casks"`
		Brews []any          `yaml:"brews"`
	}
	loadYAML(t, ".goreleaser.yaml", &c)
	return c.Casks, c.Brews
}

func loadHomebrewCask(t *testing.T) homebrewCask {
	t.Helper()
	casks, brews := loadHomebrewCasks(t)
	if len(brews) != 0 {
		t.Errorf("brews (formula) is set: it is deprecated since GoReleaser 2.10, use homebrew_casks")
	}
	if len(casks) != 1 {
		t.Fatalf("homebrew_casks = %d, want exactly one", len(casks))
	}
	return casks[0]
}

func TestHomebrewCaskTargetsTheTap(t *testing.T) {
	cask := loadHomebrewCask(t)
	// `brew install freema/tap/vellum` resolves to the repo
	// freema/homebrew-tap and the file Casks/vellum.rb in it.
	if cask.Repository.Owner != "freema" || cask.Repository.Name != "homebrew-tap" {
		t.Errorf("cask repository = %s/%s, want freema/homebrew-tap", cask.Repository.Owner, cask.Repository.Name)
	}
	if cask.Name != "vellum" {
		t.Errorf("cask name = %q, want vellum (brew install freema/tap/vellum)", cask.Name)
	}
	if cask.Directory != "" && cask.Directory != "Casks" {
		t.Errorf("cask directory = %q, want the default Casks", cask.Directory)
	}
	// The release's own GITHUB_TOKEN cannot push to another repository.
	if cask.Repository.Token != "{{ .Env.TAP_TOKEN }}" {
		t.Errorf("cask repository token = %q, want {{ .Env.TAP_TOKEN }}", cask.Repository.Token)
	}
	if cask.Homepage == "" {
		t.Error("cask has no homepage")
	}

	c := loadGoreleaser(t)
	if len(c.Builds) != 1 {
		t.Fatalf("builds = %d, want exactly one", len(c.Builds))
	}
	b := c.Builds[0]
	if !slices.Contains(cask.IDs, b.ID) {
		t.Errorf("cask ids %v do not include the build %q: the cask would find no archives", cask.IDs, b.ID)
	}
	if !slices.Equal(cask.Binaries, []string{b.Binary}) {
		t.Errorf("cask binaries = %v, want [%s]", cask.Binaries, b.Binary)
	}
	if !slices.Contains(b.Goos, "darwin") {
		t.Errorf("build goos %v has no darwin: the cask would have nothing to install on macOS", b.Goos)
	}
}

// renderSkipUpload evaluates skip_upload the way GoReleaser does for the
// functions it uses: isEnvSet is true only for a variable that is set and
// not empty. An unknown function or field fails the parse, so the test also
// catches a template this stub cannot vouch for.
func renderSkipUpload(t *testing.T, tmpl string, env map[string]string) string {
	t.Helper()
	tp, err := template.New("skip_upload").
		Option("missingkey=error").
		Funcs(template.FuncMap{
			"isEnvSet": func(name string) bool { return env[name] != "" },
		}).
		Parse(tmpl)
	if err != nil {
		t.Fatalf("skip_upload %q: %v", tmpl, err)
	}
	var out strings.Builder
	if err := tp.Execute(&out, map[string]any{"Env": env}); err != nil {
		t.Fatalf("skip_upload %q: %v", tmpl, err)
	}
	return strings.TrimSpace(out.String())
}

func TestHomebrewCaskUploadRules(t *testing.T) {
	skip := loadHomebrewCask(t).SkipUpload
	for _, tc := range []struct {
		name string
		env  map[string]string
		want string
	}{
		// "auto" is GoReleaser's "skip for a pre-release": a beta tag
		// (v1.13.0-beta.1) leaves the tap alone, a stable tag updates it.
		{"token set", map[string]string{"TAP_TOKEN": "github_pat_x"}, "auto"},
		// GitHub Actions passes a secret that does not exist as an empty
		// string; the release must go on without touching the tap.
		{"secret missing", map[string]string{"TAP_TOKEN": ""}, "true"},
		{"variable unset", map[string]string{}, "true"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := renderSkipUpload(t, skip, tc.env); got != tc.want {
				t.Errorf("skip_upload %q with %v = %q, want %q", skip, tc.env, got, tc.want)
			}
		})
	}
}

func TestHomebrewCaskLiftsQuarantine(t *testing.T) {
	cask := loadHomebrewCask(t)
	post := cask.Hooks.Post
	// The raw-Ruby `install` hook is deprecated in GoReleaser 2.20, and
	// `goreleaser check` in CI fails on deprecated properties.
	if post.Install != "" {
		t.Errorf("hooks.post.install is deprecated, use install_steps")
	}
	steps := post.InstallSteps
	for _, want := range []string{
		"on_macos do",
		`run "/usr/bin/xattr"`,
		`"-dr"`,
		`"com.apple.quarantine"`,
		// The binary the cask links, inside the staged archive.
		`"{{ .StagedPath }}/` + loadGoreleaser(t).Builds[0].Binary + `"`,
	} {
		if !strings.Contains(steps, want) {
			t.Errorf("hooks.post.install_steps lack %s; Gatekeeper would refuse the unsigned binary:\n%s", want, steps)
		}
	}
}

func TestReleaseWorkflowPassesTheTapToken(t *testing.T) {
	gr, ok := stepUsing(loadReleaseWorkflow(t).Jobs["release"], "goreleaser/goreleaser-action")
	if !ok {
		t.Fatal("release job does not run goreleaser/goreleaser-action")
	}
	if gr.Env["TAP_TOKEN"] != "${{ secrets.TAP_TOKEN }}" {
		t.Errorf("GoReleaser step env TAP_TOKEN = %q, want ${{ secrets.TAP_TOKEN }}", gr.Env["TAP_TOKEN"])
	}
	// The tap token must not leak into the jobs that do not need it.
	for name, job := range loadReleaseWorkflow(t).Jobs {
		for _, s := range job.Steps {
			if name == "release" && s.Uses == gr.Uses {
				continue
			}
			for k, v := range s.Env {
				if strings.Contains(v, "secrets.TAP_TOKEN") {
					t.Errorf("job %s step %q passes TAP_TOKEN as %s; only the GoReleaser step needs it", name, s.Name, k)
				}
			}
		}
	}
}
