package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// ACPSpec describes the abstract intent for an ACP session.
// vessel-driver resolves this to a concrete exec command at runtime.
type ACPSpec struct {
	Transport string `json:"transport"`            // Required. "stdio" only for MVP.
	Backend   string `json:"backend"`              // Required. "copilot" | "raw"
	Model     string `json:"model,omitempty"`      // Optional. Overridden by LEGION_MODEL at runtime.
	AgentFile string `json:"agent_file,omitempty"` // Optional. Bare name → vessel-driver expands to
	// /workspace/.github/agents/<name>.agent.md
}

// VesselConfig is the complete, validated configuration for a single vessel
// container run. Archon assembles it at spawn time; vessel-driver and hooks
// consume it. Secrets (GITHUB_TOKEN, DOLT_HOST/PORT, OTEL_*) are NOT here —
// they remain as separate env vars.
//
// Delivered via LEGION_CONFIG_JSON env var (marshaled JSON string).
// Test override: set LEGION_CONFIG_FILE to a path containing the JSON.
type VesselConfig struct {
	// ── Core identity (all required) ─────────────────────────────────────────
	IssueID  string  `json:"issue_id"`  // e.g. "lg-abc"
	RoleName string  `json:"role_name"` // "worker"|"reviewer"|"dispatcher"|"planner"
	RepoURL  string  `json:"repo_url"`  // git clone URL
	ACPSpec  ACPSpec `json:"acp_spec"`

	// ── Agent overlay (optional) ──────────────────────────────────────────────
	AgentName string `json:"agent_name,omitempty"` // e.g. "baal"; kept for informational use

	// ── Review workflow ───────────────────────────────────────────────────────
	ReviewEnabled        bool `json:"review_enabled"`          // default: false
	MaxRework            int  `json:"max_rework"`              // default: 3
	DeleteBranchOnMerge  bool `json:"delete_branch_on_merge"`  // default: true (see ApplyDefaults)
	DeleteBranchOnReject bool `json:"delete_branch_on_reject"` // default: false

	// ── Dispatcher config ─────────────────────────────────────────────────────
	RouterAgent    string `json:"router_agent,omitempty"` // agent that fills dispatcher role
	DefaultRole    string `json:"default_role"`           // default: "worker"
	MaxDispatch    int    `json:"max_dispatch"`           // default: 3
	DispatcherMode string `json:"dispatcher_mode"`        // "keyword"|"llm"; default: "keyword"

	// ── Reviewer/rework vessels (required when RoleName == "reviewer") ────────
	ReviewBranch        string `json:"review_branch,omitempty"`
	ReviewWorkIssue     string `json:"review_work_issue,omitempty"`
	ReviewOriginalIssue string `json:"review_original_issue,omitempty"`
	ReviewReworkCount   int    `json:"review_rework_count,omitempty"`

	// unexported sentinel — tracks whether DeleteBranchOnMerge was explicitly set in JSON
	deleteBranchOnMergeExplicit bool
}

// ApplyDefaults sets fields to their logical defaults when not explicitly provided.
func (c *VesselConfig) ApplyDefaults() {
	if c.MaxRework <= 0 {
		c.MaxRework = 3
	}
	if c.MaxDispatch <= 0 {
		c.MaxDispatch = 3
	}
	if c.DefaultRole == "" {
		c.DefaultRole = "worker"
	}
	if c.DispatcherMode == "" {
		c.DispatcherMode = "keyword"
	}
	if !c.deleteBranchOnMergeExplicit {
		c.DeleteBranchOnMerge = true // default: delete on merge
	}
}

// UnmarshalJSON implements json.Unmarshaler to track explicit bool fields.
func (c *VesselConfig) UnmarshalJSON(data []byte) error {
	type Alias VesselConfig
	aux := struct {
		DeleteBranchOnMerge *bool `json:"delete_branch_on_merge"`
		*Alias
	}{Alias: (*Alias)(c)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if aux.DeleteBranchOnMerge != nil {
		c.DeleteBranchOnMerge = *aux.DeleteBranchOnMerge
		c.deleteBranchOnMergeExplicit = true
	}
	return nil
}

// Validate returns an error if required fields are missing or inconsistent.
// Call after ApplyDefaults().
func (c *VesselConfig) Validate() error {
	var errs []string
	if c.IssueID == "" {
		errs = append(errs, "issue_id required")
	}
	if c.RoleName == "" {
		errs = append(errs, "role_name required")
	}
	if c.RepoURL == "" {
		errs = append(errs, "repo_url required")
	}
	if c.ACPSpec.Transport == "" {
		return fmt.Errorf("acp_spec.transport is required")
	}
	if c.ACPSpec.Backend == "" {
		return fmt.Errorf("acp_spec.backend is required")
	}
	if c.MaxRework < 1 {
		errs = append(errs, "max_rework must be >= 1")
	}
	if c.MaxDispatch < 1 {
		errs = append(errs, "max_dispatch must be >= 1")
	}
	if c.RoleName == "reviewer" {
		if c.ReviewBranch == "" {
			errs = append(errs, "review_branch required for reviewer role")
		}
		if c.ReviewWorkIssue == "" {
			errs = append(errs, "review_work_issue required for reviewer role")
		}
		if c.ReviewOriginalIssue == "" {
			errs = append(errs, "review_original_issue required for reviewer role")
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("VesselConfig invalid: %s", strings.Join(errs, "; "))
	}
	return nil
}

// Load reads VesselConfig from LEGION_CONFIG_JSON env var.
// If LEGION_CONFIG_FILE is set, reads from that path instead (testing only).
func Load() (*VesselConfig, error) {
	var data []byte
	if path := os.Getenv("LEGION_CONFIG_FILE"); path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading LEGION_CONFIG_FILE: %w", err)
		}
		data = b
	} else {
		raw := os.Getenv("LEGION_CONFIG_JSON")
		if raw == "" {
			return nil, fmt.Errorf("LEGION_CONFIG_JSON not set and LEGION_CONFIG_FILE not set")
		}
		data = []byte(raw)
	}
	var cfg VesselConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing VesselConfig: %w", err)
	}
	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}
