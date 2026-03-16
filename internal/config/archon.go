package config

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/BurntSushi/toml"
)

// ArchonRoleConfig defines the agent pool and dispatch behaviour for a single
// functional role. All fields are optional — Archon applies safe defaults.
type ArchonRoleConfig struct {
	Agents   []string `toml:"agents"`   // ordered agent pool; must not be empty when [roles] is set
	Limit    int      `toml:"limit"`    // concurrent vessel cap; 0 = inherit limits.max_global
	Strategy string   `toml:"strategy"` // "first" (default) | "round-robin"
}

// ArchonConfig is the top-level configuration for the Archon daemon.
// It is loaded from .legion/archon.toml (or LEGION_CONFIG_PATH) and
// overrideable via environment variables for 12-factor deployments.
type ArchonConfig struct {
	Daemon  ArchonDaemon                `toml:"daemon"`
	Limits  ArchonLimits                `toml:"limits"`
	Routing ArchonRouting               `toml:"routing"`
	Review  ArchonReview                `toml:"review"`
	Vessel  ArchonVessel                `toml:"vessel"`
	Hooks   ArchonHooks                 `toml:"hooks"`
	Hermes  ArchonHermes                `toml:"hermes"`
	Roles   map[string]ArchonRoleConfig `toml:"roles"`
}

// ArchonDaemon controls loop timing and per-vessel deadline.
type ArchonDaemon struct {
	PulseIntervalSeconds   int `toml:"pulse_interval_seconds"`
	WatcherIntervalSeconds int `toml:"watcher_interval_seconds"`
	VesselTimeoutSeconds   int `toml:"vessel_timeout_seconds"`
}

// PulseInterval converts PulseIntervalSeconds to a time.Duration.
func (d ArchonDaemon) PulseInterval() time.Duration {
	return time.Duration(d.PulseIntervalSeconds) * time.Second
}

// WatcherInterval converts WatcherIntervalSeconds to a time.Duration.
func (d ArchonDaemon) WatcherInterval() time.Duration {
	return time.Duration(d.WatcherIntervalSeconds) * time.Second
}

// VesselTimeout converts VesselTimeoutSeconds to a time.Duration.
func (d ArchonDaemon) VesselTimeout() time.Duration {
	return time.Duration(d.VesselTimeoutSeconds) * time.Second
}

// ArchonLimits caps concurrent vessel containers globally and per-role.
// Per-role limits live in Roles[name].Limit — not here.
type ArchonLimits struct {
	MaxGlobal int `toml:"max_global"` // 0 = unlimited
}

// ArchonRouting controls the dispatcher and default agent assignment.
type ArchonRouting struct {
	RouterAgent    string `toml:"router_agent"`
	DefaultRole    string `toml:"default_role"`
	MaxDispatch    int    `toml:"max_dispatch"`
	DispatcherMode string `toml:"dispatcher_mode"`
	DispatchLabel  string `toml:"dispatch_label"`
}

// ArchonReview controls automatic review-vessel creation after worker completion.
type ArchonReview struct {
	Enabled             bool `toml:"enabled"`
	MaxRework           int  `toml:"max_rework"`
	DeleteBranchOnMerge bool `toml:"delete_branch_on_merge"`
}

// ArchonVessel holds per-vessel defaults forwarded into containers.
type ArchonVessel struct {
	DefaultModel      string            `toml:"default_model"`
	ModelTiers        map[string]string `toml:"model_tiers"`
	RoleModelDefaults map[string]string `toml:"role_model_defaults"`
}

// ArchonHooks configures lifecycle event hooks (pre-start, post-stop, pre-pulse).
// Hooks are optional shell scripts that Archon invokes at specific points.
// Two-tier resolution: ImageHookDir (production, volume-mounted) then RepoHookDir (dev).
// PrePulseEnabled gates pre-pulse invocation (default false for performance).
type ArchonHooks struct {
	PrePulseEnabled bool   `toml:"pre_pulse_enabled"` // default false
	ImageHookDir    string `toml:"image_hook_dir"`    // default /etc/legion/hooks/archon
	RepoHookDir     string `toml:"repo_hook_dir"`     // default .legion/hooks/archon
}

// ArchonHermes configures the optional Hermes vessel service.
type ArchonHermes struct {
	Enabled        bool   `toml:"enabled"`
	Image          string `toml:"image"`
	TimeoutSeconds int    `toml:"timeout_seconds"`
}

// HermesTimeout converts TimeoutSeconds to a time.Duration.
func (h ArchonHermes) HermesTimeout() time.Duration {
	return time.Duration(h.TimeoutSeconds) * time.Second
}

// defaultArchonConfig returns a fully populated ArchonConfig with safe defaults.
func defaultArchonConfig() ArchonConfig {
	return ArchonConfig{
		Daemon: ArchonDaemon{
			PulseIntervalSeconds:   10,
			WatcherIntervalSeconds: 30,
			VesselTimeoutSeconds:   1800,
		},
		Limits: ArchonLimits{
			MaxGlobal: 5,
		},
		Routing: ArchonRouting{
			DefaultRole:    "worker",
			MaxDispatch:    3,
			DispatcherMode: "keyword",
			DispatchLabel:  "dispatch:auto",
		},
		Review: ArchonReview{
			Enabled:             true,
			MaxRework:           3,
			DeleteBranchOnMerge: true,
		},
		Vessel: ArchonVessel{
			DefaultModel: "",
			ModelTiers: map[string]string{
				"fast":     "claude-haiku-4.5",
				"standard": "claude-sonnet-4.6",
				"premium":  "claude-opus-4.6",
			},
			RoleModelDefaults: map[string]string{
				"worker":     "standard",
				"reviewer":   "premium",
				"dispatcher": "fast",
				"planner":    "standard",
			},
		},
		Hooks: ArchonHooks{
			PrePulseEnabled: false,
			ImageHookDir:    "/etc/legion/hooks/archon",
			RepoHookDir:     ".legion/hooks/archon",
		},
		Hermes: ArchonHermes{
			Enabled:        false,
			Image:          "",
			TimeoutSeconds: 30,
		},
		Roles: map[string]ArchonRoleConfig{
			"worker":   {Agents: []string{"wraith"}, Limit: 3, Strategy: "first"},
			"planner":  {Agents: []string{"hierophant"}, Limit: 1, Strategy: "first"},
			"reviewer": {Agents: []string{"inquisitor"}, Limit: 1, Strategy: "first"},
		},
	}
}

// LoadArchonConfig loads ArchonConfig using the following resolution order:
//
//  1. LEGION_CONFIG_PATH env var (if set)
//  2. /etc/legion/archon.toml   (production — volume mounted)
//  3. .legion/archon.toml       (local dev — relative to cwd)
//  4. Built-in defaults         (no file required)
//
// After file loading, environment variable overrides are applied and the
// resulting config is validated before returning.
func LoadArchonConfig() (ArchonConfig, error) {
	cfg := defaultArchonConfig()

	path := os.Getenv("LEGION_CONFIG_PATH")
	if path == "" {
		for _, candidate := range []string{"/etc/legion/archon.toml", ".legion/archon.toml"} {
			if _, err := os.Stat(candidate); err == nil {
				path = candidate
				break
			}
		}
	}

	if path != "" {
		if _, err := toml.DecodeFile(path, &cfg); err != nil {
			return cfg, fmt.Errorf("loading archon config from %s: %w", path, err)
		}
	}
	// path == "" → no config file found; defaults remain in effect (non-fatal).

	applyEnvOverrides(&cfg)

	if err := cfg.Validate(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// applyEnvOverrides applies 12-factor environment variable overrides on top of
// whatever was loaded from the config file. Malformed values are silently ignored
// so that a bad env var never prevents startup; operators will see unexpected
// behaviour and should validate their env.
func applyEnvOverrides(cfg *ArchonConfig) {
	if v := os.Getenv("LEGION_MAX_VESSELS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Limits.MaxGlobal = n
		}
	}
	if v := os.Getenv("LEGION_PULSE_INTERVAL"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Daemon.PulseIntervalSeconds = n
		}
	}
	if v := os.Getenv("LEGION_VESSEL_TIMEOUT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Daemon.VesselTimeoutSeconds = n
		}
	} else if v := os.Getenv("VESSEL_TIMEOUT"); v != "" {
		// Backward compat: VESSEL_TIMEOUT was the legacy env var name.
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Daemon.VesselTimeoutSeconds = n
		}
	}
	if v := os.Getenv("LEGION_ROUTER_AGENT"); v != "" {
		cfg.Routing.RouterAgent = v
	}
	if v := os.Getenv("LEGION_DEFAULT_ROLE"); v != "" {
		cfg.Routing.DefaultRole = v
	}
	if v := os.Getenv("LEGION_REVIEW_ENABLED"); v != "" {
		cfg.Review.Enabled = v == "true"
	}
	if v := os.Getenv("LEGION_MAX_REWORK"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Review.MaxRework = n
		}
	}
	if v := os.Getenv("LEGION_DISPATCHER_MODE"); v != "" {
		cfg.Routing.DispatcherMode = v
	}
	if v := os.Getenv("LEGION_MAX_DISPATCH"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Routing.MaxDispatch = n
		}
	}
	if v := os.Getenv("VESSEL_MODEL"); v != "" {
		cfg.Vessel.DefaultModel = v
	}
}

// Validate checks ArchonConfig for obviously invalid values.
func (c *ArchonConfig) Validate() error {
	if c.Daemon.PulseIntervalSeconds < 1 {
		return fmt.Errorf("daemon.pulse_interval_seconds must be >= 1")
	}
	if c.Limits.MaxGlobal < 0 {
		return fmt.Errorf("limits.max_global must be >= 0 (0 = unlimited)")
	}
	if c.Routing.MaxDispatch < 1 {
		return fmt.Errorf("routing.max_dispatch must be >= 1")
	}
	if len(c.Roles) > 0 {
		if _, ok := c.Roles[c.Routing.DefaultRole]; !ok {
			return fmt.Errorf("routing.default_role %q is not defined in [roles]; defined roles: %v",
				c.Routing.DefaultRole, sortedKeys(c.Roles))
		}
		validStrategies := map[string]bool{"": true, "first": true, "round-robin": true}
		for name, rc := range c.Roles {
			if len(rc.Agents) == 0 {
				return fmt.Errorf("roles.%s.agents must have at least one entry", name)
			}
			if !validStrategies[rc.Strategy] {
				return fmt.Errorf("roles.%s.strategy %q is invalid; valid values: \"first\", \"round-robin\"", name, rc.Strategy)
			}
			if rc.Limit < 0 {
				return fmt.Errorf("roles.%s.limit must be >= 0 (0 = inherit max_global)", name)
			}
		}
	}
	return nil
}

// sortedKeys returns the keys of m sorted alphabetically.
func sortedKeys(m map[string]ArchonRoleConfig) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
