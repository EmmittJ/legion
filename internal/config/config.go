// Package config loads Legion's operator configuration from
// .legion/config.toml: the target repo, the vessel registry, and
// Archon tuning. See docs/architecture.md.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"
)

// DefaultPath is the config location relative to the operating repo root.
const DefaultPath = ".legion/config.toml"

// Duration wraps time.Duration for TOML strings like "5s" or "30m".
type Duration struct{ time.Duration }

// UnmarshalText implements encoding.TextUnmarshaler.
func (d *Duration) UnmarshalText(text []byte) error {
	v, err := time.ParseDuration(string(text))
	if err != nil {
		return err
	}
	d.Duration = v
	return nil
}

// Archon tunes the reconciler daemon.
type Archon struct {
	PollInterval Duration `toml:"poll_interval"`
	MaxVessels   int      `toml:"max_vessels"`
	BeadTimeout  Duration `toml:"bead_timeout"`
}

// Config is the parsed .legion/config.toml.
type Config struct {
	RepoURL       string            `toml:"repo_url"`
	DefaultVessel string            `toml:"default_vessel"`
	Vessels       map[string]string `toml:"vessels"`
	Archon        Archon            `toml:"archon"`
}

// Load reads and validates the config at path.
func Load(path string) (*Config, error) {
	var cfg Config
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	return &cfg, nil
}

// Find walks up from dir looking for DefaultPath, so commands work from
// anywhere inside the operating repo.
func Find(dir string) (string, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		p := filepath.Join(dir, filepath.FromSlash(DefaultPath))
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no %s found from %s upward (run lg init)", DefaultPath, dir)
		}
		dir = parent
	}
}

func (c *Config) applyDefaults() {
	if c.Archon.PollInterval.Duration == 0 {
		c.Archon.PollInterval.Duration = 5 * time.Second
	}
	if c.Archon.MaxVessels == 0 {
		c.Archon.MaxVessels = 3
	}
	if c.Archon.BeadTimeout.Duration == 0 {
		c.Archon.BeadTimeout.Duration = 30 * time.Minute
	}
}

func (c *Config) validate() error {
	if c.RepoURL == "" {
		return fmt.Errorf("repo_url is required")
	}
	if len(c.Vessels) == 0 {
		return fmt.Errorf("[vessels] registry must have at least one entry")
	}
	if c.DefaultVessel == "" {
		return fmt.Errorf("default_vessel is required")
	}
	if _, ok := c.Vessels[c.DefaultVessel]; !ok {
		return fmt.Errorf("default_vessel %q not in [vessels] registry", c.DefaultVessel)
	}
	return nil
}

// Image resolves a vessel name (or "" for the default) to a container image.
func (c *Config) Image(vesselName string) (name, image string, err error) {
	if vesselName == "" {
		vesselName = c.DefaultVessel
	}
	img, ok := c.Vessels[vesselName]
	if !ok {
		return "", "", fmt.Errorf("vessel %q not in [vessels] registry", vesselName)
	}
	return vesselName, img, nil
}
