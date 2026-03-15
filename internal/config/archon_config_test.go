package config

import (
	"testing"

	"github.com/BurntSushi/toml"
)

// canonicalModelTiers is the source-of-truth for default tier values.
var canonicalModelTiers = map[string]string{
	"fast":     "claude-haiku-4.5",
	"standard": "claude-sonnet-4.6",
	"premium":  "claude-opus-4.6",
}

// canonicalRoleModelDefaults is the source-of-truth for default role values.
var canonicalRoleModelDefaults = map[string]string{
	"worker":     "standard",
	"reviewer":   "premium",
	"dispatcher": "fast",
	"planner":    "standard",
}

// TestDefaultArchonConfig_MapsPopulated verifies that defaultArchonConfig
// populates both maps with the canonical values.
func TestDefaultArchonConfig_MapsPopulated(t *testing.T) {
	cfg := defaultArchonConfig()

	if len(cfg.Vessel.ModelTiers) != len(canonicalModelTiers) {
		t.Fatalf("ModelTiers: want %d entries, got %d", len(canonicalModelTiers), len(cfg.Vessel.ModelTiers))
	}
	for tier, model := range canonicalModelTiers {
		if got := cfg.Vessel.ModelTiers[tier]; got != model {
			t.Errorf("ModelTiers[%q]: want %q, got %q", tier, model, got)
		}
	}

	if len(cfg.Vessel.RoleModelDefaults) != len(canonicalRoleModelDefaults) {
		t.Fatalf("RoleModelDefaults: want %d entries, got %d", len(canonicalRoleModelDefaults), len(cfg.Vessel.RoleModelDefaults))
	}
	for role, tier := range canonicalRoleModelDefaults {
		if got := cfg.Vessel.RoleModelDefaults[role]; got != tier {
			t.Errorf("RoleModelDefaults[%q]: want %q, got %q", role, tier, got)
		}
	}
}

// TestArchonConfig_TOMLRoundTrip verifies that TOML containing
// [vessel.model_tiers] and [vessel.role_model_defaults] sections is decoded
// correctly into ArchonConfig.
func TestArchonConfig_TOMLRoundTrip(t *testing.T) {
	const doc = `
[vessel]
default_model = "my-model"

[vessel.model_tiers]
fast     = "claude-haiku-4.5"
standard = "claude-sonnet-4.6"
premium  = "claude-opus-4.6"

[vessel.role_model_defaults]
worker     = "standard"
reviewer   = "premium"
dispatcher = "fast"
planner    = "standard"
`
	var cfg ArchonConfig
	if _, err := toml.Decode(doc, &cfg); err != nil {
		t.Fatalf("toml.Decode: %v", err)
	}

	if cfg.Vessel.DefaultModel != "my-model" {
		t.Errorf("DefaultModel: want %q, got %q", "my-model", cfg.Vessel.DefaultModel)
	}

	for tier, model := range canonicalModelTiers {
		if got := cfg.Vessel.ModelTiers[tier]; got != model {
			t.Errorf("ModelTiers[%q]: want %q, got %q", tier, model, got)
		}
	}

	for role, tier := range canonicalRoleModelDefaults {
		if got := cfg.Vessel.RoleModelDefaults[role]; got != tier {
			t.Errorf("RoleModelDefaults[%q]: want %q, got %q", role, tier, got)
		}
	}
}

// TestArchonConfig_TOMLOmittedSections verifies that when TOML omits the map
// sections entirely, the defaults loaded via defaultArchonConfig remain in
// effect (i.e., a partial TOML decode does not zero out pre-populated maps).
func TestArchonConfig_TOMLOmittedSections(t *testing.T) {
	const doc = `
[daemon]
pulse_interval_seconds = 5
`
	// Start from defaults, then decode minimal TOML on top — same flow as
	// LoadArchonConfig.
	cfg := defaultArchonConfig()
	if _, err := toml.Decode(doc, &cfg); err != nil {
		t.Fatalf("toml.Decode: %v", err)
	}

	if len(cfg.Vessel.ModelTiers) != len(canonicalModelTiers) {
		t.Fatalf("ModelTiers after partial decode: want %d entries, got %d",
			len(canonicalModelTiers), len(cfg.Vessel.ModelTiers))
	}
	for tier, model := range canonicalModelTiers {
		if got := cfg.Vessel.ModelTiers[tier]; got != model {
			t.Errorf("ModelTiers[%q]: want %q, got %q", tier, model, got)
		}
	}

	if len(cfg.Vessel.RoleModelDefaults) != len(canonicalRoleModelDefaults) {
		t.Fatalf("RoleModelDefaults after partial decode: want %d entries, got %d",
			len(canonicalRoleModelDefaults), len(cfg.Vessel.RoleModelDefaults))
	}
	for role, tier := range canonicalRoleModelDefaults {
		if got := cfg.Vessel.RoleModelDefaults[role]; got != tier {
			t.Errorf("RoleModelDefaults[%q]: want %q, got %q", role, tier, got)
		}
	}

	// Also confirm the decoded field took effect.
	if cfg.Daemon.PulseIntervalSeconds != 5 {
		t.Errorf("PulseIntervalSeconds: want 5, got %d", cfg.Daemon.PulseIntervalSeconds)
	}
}
