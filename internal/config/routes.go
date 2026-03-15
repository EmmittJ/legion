package config

import (
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// RoutesConfig holds the parsed content of .legion/routes.toml.
type RoutesConfig struct {
	Rules []DispatchRule `toml:"rule"`
}

// DispatchRule is a single routing rule: if the issue title matches Pattern,
// route to Role. Model optionally pins a model tier or literal model ID.
type DispatchRule struct {
	Pattern string `toml:"pattern"`
	Role    string `toml:"role"`
	Model   string `toml:"model"` // optional; empty means use role default
}

// LoadRoutesConfig loads .legion/routes.toml from the given root directory.
// Returns an empty RoutesConfig (no rules) if the file does not exist.
func LoadRoutesConfig(root string) (RoutesConfig, error) {
	path := filepath.Join(root, ".legion", "routes.toml")
	var cfg RoutesConfig
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return cfg, nil
	}
	_, err := toml.DecodeFile(path, &cfg)
	return cfg, err
}
