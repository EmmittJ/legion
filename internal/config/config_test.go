package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

const sample = `
repo_url = "https://github.com/you/yourrepo"
default_vessel = "copilot"

[vessels]
copilot = "ghcr.io/emmittj/legion/vessel-copilot:latest"
claude  = "ghcr.io/emmittj/legion/vessel-claude:latest"

[archon]
poll_interval = "10s"
max_vessels = 2
bead_timeout = "45m"
`

func write(t *testing.T, dir, content string) string {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(DefaultPath))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoad(t *testing.T) {
	p := write(t, t.TempDir(), sample)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RepoURL != "https://github.com/you/yourrepo" || cfg.DefaultVessel != "copilot" {
		t.Errorf("bad config: %+v", cfg)
	}
	if cfg.Archon.PollInterval.Duration != 10*time.Second ||
		cfg.Archon.MaxVessels != 2 ||
		cfg.Archon.BeadTimeout.Duration != 45*time.Minute {
		t.Errorf("bad archon tuning: %+v", cfg.Archon)
	}
}

func TestDefaults(t *testing.T) {
	p := write(t, t.TempDir(), `
repo_url = "https://example.com/r.git"
default_vessel = "copilot"
[vessels]
copilot = "img:latest"
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Archon.PollInterval.Duration != 5*time.Second ||
		cfg.Archon.MaxVessels != 3 ||
		cfg.Archon.BeadTimeout.Duration != 30*time.Minute {
		t.Errorf("defaults not applied: %+v", cfg.Archon)
	}
}

func TestValidation(t *testing.T) {
	cases := map[string]string{
		"missing repo_url":       "default_vessel = \"x\"\n[vessels]\nx = \"i\"\n",
		"empty registry":         "repo_url = \"r\"\ndefault_vessel = \"x\"\n",
		"default not in vessels": "repo_url = \"r\"\ndefault_vessel = \"nope\"\n[vessels]\nx = \"i\"\n",
	}
	for name, content := range cases {
		if _, err := Load(write(t, t.TempDir(), content)); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func TestImageResolution(t *testing.T) {
	cfg, err := Load(write(t, t.TempDir(), sample))
	if err != nil {
		t.Fatal(err)
	}
	name, img, err := cfg.Image("")
	if err != nil || name != "copilot" || img != "ghcr.io/emmittj/legion/vessel-copilot:latest" {
		t.Errorf("default: (%q, %q, %v)", name, img, err)
	}
	name, img, err = cfg.Image("claude")
	if err != nil || name != "claude" || img != "ghcr.io/emmittj/legion/vessel-claude:latest" {
		t.Errorf("named: (%q, %q, %v)", name, img, err)
	}
	if _, _, err = cfg.Image("ghost"); err == nil {
		t.Error("unknown vessel: want error")
	}
}

func TestFindWalksUp(t *testing.T) {
	root := t.TempDir()
	write(t, root, sample)
	nested := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	p, err := Find(nested)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, filepath.FromSlash(DefaultPath))
	if p != want {
		t.Errorf("Find = %q, want %q", p, want)
	}
	if _, err := Find(filepath.Join(t.TempDir())); err == nil {
		t.Error("Find outside any repo: want error")
	}
}
