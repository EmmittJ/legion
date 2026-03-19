package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	archoncfg "github.com/EmmittJ/legion/internal/config"
	"github.com/EmmittJ/legion/internal/telemetry"
)

// config holds all runtime configuration read from environment variables.
// Secrets, image path, and infrastructure coordinates live here.
// Timing and limits are in ArchonConfig (loaded from .legion/archon.toml).
type config struct {
	repoURL       string
	vesselImage   string
	githubToken   string
	vesselModel   string
	doltHost      string
	doltPort      string
	dockerNetwork string // Docker network vessel containers join (must reach dolt)
}

// entry tracks a running vessel container.
type entry struct {
	issueID         string
	issueTitle      string
	startedAt       time.Time
	roleName        string
	agentName       string
	originalIssueID string // root issue for rework chains; empty for non-rework workers
	reworkCount     int    // current rework iteration for this vessel; 0 for first-run workers
}

// tracker holds the set of vessel containers Archon is currently managing.
type tracker struct {
	mu   sync.Mutex
	runs map[string]entry // container name → entry
}

func (t *tracker) has(name string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, ok := t.runs[name]
	return ok
}

func (t *tracker) add(name, issueID, issueTitle, roleName, agentName, originalIssueID string, reworkCount int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.runs[name] = entry{
		issueID:         issueID,
		issueTitle:      issueTitle,
		startedAt:       time.Now(),
		roleName:        roleName,
		agentName:       agentName,
		originalIssueID: originalIssueID,
		reworkCount:     reworkCount,
	}
}

func (t *tracker) remove(name string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.runs, name)
}

// snapshot returns a copy of the current tracking map safe for ranging outside the lock.
func (t *tracker) snapshot() map[string]entry {
	t.mu.Lock()
	defer t.mu.Unlock()
	snap := make(map[string]entry, len(t.runs))
	for k, v := range t.runs {
		snap[k] = v
	}
	return snap
}

// addAt records a vessel with an explicit start time — used by reconcile to
// restore containers that were already running before this Archon process started.
// Reconciled containers get empty role/agent (ADR lg-dyh).
func (t *tracker) addAt(name, issueID string, startedAt time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.runs[name] = entry{issueID: issueID, startedAt: startedAt}
}

// count returns the total number of tracked vessels.
func (t *tracker) count() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.runs)
}

// countByRole returns the number of tracked vessels with the given role.
func (t *tracker) countByRole(role string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := 0
	for _, e := range t.runs {
		if e.roleName == role {
			n++
		}
	}
	return n
}

// countByAgent returns the number of tracked vessels with the given agent name.
func (t *tracker) countByAgent(agent string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := 0
	for _, e := range t.runs {
		if e.agentName == agent {
			n++
		}
	}
	return n
}

// reconcile scans for vessel containers that were already running (e.g. from a
// previous Archon process) and adds them to the tracker so the watcher loop can
// time them out or clean them up.  It queries only running containers whose names
// match our naming prefix; containers that have already exited are ignored here
// and will be cleaned up by the watcher on its first tick if they are in the
// tracker, or left as stopped cruft otherwise.
func reconcile(ctx context.Context, t *tracker) {
	const prefix = "legion-vessel-"

	out, err := run("docker", "ps",
		"--filter", "name="+prefix,
		"--filter", "status=running",
		"--format", "{{.Names}}",
	)
	if err != nil {
		slog.WarnContext(ctx, "reconcile: could not list vessel containers", "err", err)
		return
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	count := 0
	for _, name := range lines {
		name = strings.TrimSpace(name)
		if name == "" || !strings.HasPrefix(name, prefix) {
			continue
		}
		if t.has(name) {
			continue
		}
		issueID := strings.TrimPrefix(name, prefix)
		startedAt := time.Now() // conservative fallback
		if tsOut, err := run("docker", "inspect", name, "--format", "{{.State.StartedAt}}"); err == nil {
			raw := strings.TrimSpace(string(tsOut))
			// Docker emits RFC3339Nano; fall back gracefully on parse failure.
			if ts, err := time.Parse(time.RFC3339Nano, raw); err == nil {
				startedAt = ts
			}
		}
		t.addAt(name, issueID, startedAt)
		slog.InfoContext(ctx, "reconcile: tracking pre-existing vessel",
			"container", name, "issue_id", issueID, "started_at", startedAt)
		count++
	}
	if count > 0 {
		slog.InfoContext(ctx, "reconcile: recovered vessels", "count", count)
	}
}

// obs bundles the OpenTelemetry instruments shared across the pulse and watcher loops.
type obs struct {
	tracer         trace.Tracer
	issuesSpawned  metric.Int64Counter
	vesselDuration metric.Float64Histogram
}

// The JSON output is a flat array — no wrapping "issue" key.
type issueItem struct {
	ID                 string   `json:"id"`
	Title              string   `json:"title"`
	Status             string   `json:"status"`
	Labels             []string `json:"labels"`
	Description        string   `json:"description"`
	AcceptanceCriteria string   `json:"acceptance_criteria"`
}

// isHermesClassifying returns true if the issue has a "hermes:classifying" label,
// indicating a Hermes classifier vessel is already running for this issue.
// Used by pulse to prevent double-spawn.
func isHermesClassifying(labels []string) bool {
	for _, l := range labels {
		if l == "hermes:classifying" {
			return true
		}
	}
	return false
}

// isInfraIssue returns true for issues that carry infrastructure labels
// (role definitions, agent definitions). Archon must never spawn vessels for these.
func isInfraIssue(iss issueItem) bool {
	for _, l := range iss.Labels {
		if l == "gt:role" || l == "gt:agent" {
			return true
		}
	}
	return false
}

// isEscalatedIssue returns true for issues that have been escalated to human review.
// Archon must never spawn vessels for escalated issues.
func isEscalatedIssue(iss issueItem) bool {
	for _, l := range iss.Labels {
		if l == "escalate:human" {
			return true
		}
	}
	return false
}

// agentLabel extracts the agent name from an issue's labels.
// Returns "" if no agent:<name> label is present.
func agentLabel(iss issueItem) string {
	for _, l := range iss.Labels {
		if strings.HasPrefix(l, "agent:") {
			return strings.TrimPrefix(l, "agent:")
		}
	}
	return ""
}

// roleLabel extracts the role label value from an issue's label slice.
// Labels are formatted as "key:value". Returns empty string if no role label found.
func roleLabel(labels []string) string {
	for _, l := range labels {
		if strings.HasPrefix(l, "role:") {
			return strings.TrimPrefix(l, "role:")
		}
	}
	return ""
}

// modelLabel extracts the model label value from an issue's label slice.
// Labels are formatted as "key:value". Returns empty string if no model label found.
func modelLabel(labels []string) string {
	for _, l := range labels {
		if strings.HasPrefix(l, "model:") {
			return strings.TrimPrefix(l, "model:")
		}
	}
	return ""
}

// reviewBranchLabel extracts the review branch from a "review-branch:<branch>"
// label. Returns "" when no such label is present.
func reviewBranchLabel(labels []string) string {
	for _, l := range labels {
		if strings.HasPrefix(l, "review-branch:") {
			return strings.TrimPrefix(l, "review-branch:")
		}
	}
	return ""
}

// discoveredFromLabel extracts the originating issue ID from a
// "discovered-from:<issueID>" label. Returns "" when absent.
func discoveredFromLabel(labels []string) string {
	for _, l := range labels {
		if strings.HasPrefix(l, "discovered-from:") {
			return strings.TrimPrefix(l, "discovered-from:")
		}
	}
	return ""
}

// reworkCountLabel parses "review-rework-count:N" → N. Returns 0 if absent or unparseable.
func reworkCountLabel(labels []string) int {
	for _, l := range labels {
		if after, ok := strings.CutPrefix(l, "review-rework-count:"); ok {
			n, err := strconv.Atoi(strings.TrimSpace(after))
			if err != nil {
				return 0
			}
			return n
		}
	}
	return 0
}

// originalIssueLabel parses "original-issue:<id>" → id. Returns "" if absent.
func originalIssueLabel(labels []string) string {
	for _, l := range labels {
		if after, ok := strings.CutPrefix(l, "original-issue:"); ok {
			return strings.TrimSpace(after)
		}
	}
	return ""
}

// workBranchLabel parses "work-branch:<branch>" → branch. Returns "" if absent.
func workBranchLabel(labels []string) string {
	for _, l := range labels {
		if after, ok := strings.CutPrefix(l, "work-branch:"); ok {
			return strings.TrimSpace(after)
		}
	}
	return ""
}

// resolveModel determines which model to use for a vessel by consulting, in order:
//  1. Explicit model: label on the issue — highest priority
//  2. Role-based default via ModelTiers
//  3. Configured DefaultModel
//  4. Hardcoded fallback ("claude-sonnet-4.6")
func resolveModel(cfg archoncfg.ArchonConfig, labels []string) string {
	if m := modelLabel(labels); m != "" {
		return m
	}
	role := roleLabel(labels)
	if tier, ok := cfg.Vessel.RoleModelDefaults[role]; ok {
		if model, ok := cfg.Vessel.ModelTiers[tier]; ok {
			return model
		}
	}
	if cfg.Vessel.DefaultModel != "" {
		return cfg.Vessel.DefaultModel
	}
	return "claude-sonnet-4.6"
}

// inferRole returns the role for an issue: explicit role label > default role from config.
func inferRole(iss issueItem, defaultRole string) string {
	if r := roleLabel(iss.Labels); r != "" {
		return r
	}
	return defaultRole
}

// resolveAgent selects an agent from the configured pool for the given role.
// Returns ("", false) when no pool is configured — caller must skip dispatch.
// Selection order:
//  1. Explicit agent: label on the issue — user override, bypasses pool.
//  2. First agent in cfg.Roles[roleName].Agents (MVP: "first" strategy).
func resolveAgent(cfg archoncfg.ArchonConfig, iss issueItem, roleName string) (string, bool) {
	if explicit := agentLabel(iss); explicit != "" {
		return explicit, true
	}
	if rc, ok := cfg.Roles[roleName]; ok && len(rc.Agents) > 0 {
		return rc.Agents[0], true
	}
	return "", false
}

type containerState struct {
	Status   string `json:"Status"`
	ExitCode int    `json:"ExitCode"`
}

func loadConfig() config {
	network := os.Getenv("DOCKER_NETWORK")
	if network == "" {
		network = "legion_legion-net"
	}
	return config{
		repoURL:       os.Getenv("REPO_URL"),
		vesselImage:   os.Getenv("VESSEL_IMAGE"),
		githubToken:   os.Getenv("GITHUB_TOKEN"),
		vesselModel:   os.Getenv("VESSEL_MODEL"),
		doltHost:      os.Getenv("DOLT_HOST"),
		doltPort:      os.Getenv("DOLT_PORT"),
		dockerNetwork: network,
	}
}

// isExecutable checks if a file exists and is executable.
// Uses os.Stat + mode bits only; returns false for directories.
func isExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	if info.IsDir() {
		return false
	}
	// Check execute bit for owner, group, or other.
	return (info.Mode() & 0o111) != 0
}

// archonHookEnv constructs the explicit environment variable list for hook execution.
// Returns ~9 LEGION_* and BEADS_DOLT_* variables (no os.Environ() passthrough).
func archonHookEnv(cfg archoncfg.ArchonConfig, runtime config) []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"LEGION_REPO_URL=" + runtime.repoURL,
		"LEGION_VESSEL_IMAGE=" + runtime.vesselImage,
		"LEGION_VESSEL_MODEL=" + runtime.vesselModel,
		"LEGION_DOCKER_NETWORK=" + runtime.dockerNetwork,
		"BEADS_DOLT_HOST=" + runtime.doltHost,
		"BEADS_DOLT_PORT=" + runtime.doltPort,
		"LEGION_PULSE_INTERVAL=" + fmt.Sprintf("%d", cfg.Daemon.PulseIntervalSeconds),
		"LEGION_WATCHER_INTERVAL=" + fmt.Sprintf("%d", cfg.Daemon.WatcherIntervalSeconds),
	}
}

// runArchonHook invokes a lifecycle hook script if it exists and is executable.
// Two-tier search: ImageHookDir (production) then RepoHookDir (dev).
// Returns nil if hook not found; wrapped error on failure.
func runArchonHook(ctx context.Context, event string, cfg archoncfg.ArchonConfig, runtime config) error {
	candidates := []string{
		fmt.Sprintf("%s/%s.sh", cfg.Hooks.ImageHookDir, event),
		fmt.Sprintf("%s/%s.sh", cfg.Hooks.RepoHookDir, event),
	}

	var hookPath string
	for _, candidate := range candidates {
		if isExecutable(candidate) {
			hookPath = candidate
			break
		}
	}

	if hookPath == "" {
		// Hook not found — not an error, just skip.
		return nil
	}

	cmd := exec.CommandContext(ctx, hookPath)
	cmd.Env = archonHookEnv(cfg, runtime)
	var stderr strings.Builder
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg == "" {
			errMsg = err.Error()
		}
		return fmt.Errorf("hook %s failed: %s", event, errMsg)
	}

	return nil
}

// containerName returns a deterministic Docker container name for the given issue ID.
// Non-alphanumeric characters (other than hyphens) are replaced with hyphens.
func containerName(issueID string) string {
	sanitized := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' {
			return r
		}
		return '-'
	}, issueID)
	return "legion-vessel-" + sanitized
}

// hermesContainerName returns a deterministic Docker container name for a Hermes
// classifier vessel for the given issue ID.
func hermesContainerName(issueID string) string {
	sanitized := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' {
			return r
		}
		return '-'
	}, issueID)
	return "legion-hermes-" + sanitized
}

// run executes a command and returns its stdout. Stderr is captured and included
// in the error message on failure.
func run(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	if err != nil {
		return stdout, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout, nil
}

func listReadyIssues(dispatchLabel string) ([]issueItem, error) {
	args := []string{"ready", "--json"}
	if dispatchLabel != "" {
		args = append(args, "--label", dispatchLabel)
	}
	out, err := run("bd", args...)
	if err != nil {
		return nil, fmt.Errorf("listing ready issues: %w", err)
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" || trimmed == "null" {
		return nil, nil
	}
	var items []issueItem
	if err := json.Unmarshal([]byte(trimmed), &items); err != nil {
		return nil, fmt.Errorf("parsing bd ready output: %w", err)
	}
	return items, nil
}

func claimIssue(issueID string) error {
	_, err := run("bd", "update", issueID, "--status=in_progress", "--assignee=wraith")
	return err
}

// markDone closes an issue in Beads with reason "completed" — correct terminal state for a clean vessel exit.
func markDone(ctx context.Context, issueID string) {
	if _, err := run("bd", "close", issueID, "--reason", "completed"); err != nil {
		slog.ErrorContext(ctx, "closing issue", "issue_id", issueID, "err", err)
	}
}

// markError marks an issue blocked with a "failed" label and appends a reason note.
// "blocked" is the terminal-error status in Beads; "failed" label distinguishes
// error-exits from genuine dependency blocks or timeouts.
func markError(ctx context.Context, issueID, reason string) {
	if _, err := run("bd", "update", issueID, "--status=blocked", "--add-label", "failed", "--append-notes", reason); err != nil {
		slog.ErrorContext(ctx, "marking issue blocked", "issue_id", issueID, "err", err)
	}
}

func markBlocked(ctx context.Context, issueID, note string) {
	if _, err := run("bd", "update", issueID, "--status=blocked", "--append-notes", note); err != nil {
		slog.ErrorContext(ctx, "marking issue blocked", "issue_id", issueID, "err", err)
	}
}

// applyDefaultRole adds a "role:<defaultRole>" label to the issue in Beads.
// Called by watchHermes when a classifier vessel fails or times out so the
// pulse loop picks up the issue on the next tick with the fallback role.
func applyDefaultRole(ctx context.Context, defaultRole, issueID string) {
	if _, err := run("bd", "update", issueID, "--add-label", "role:"+defaultRole); err != nil {
		slog.ErrorContext(ctx, "applyDefaultRole: failed to add default role label",
			"issue_id", issueID, "role", defaultRole, "err", err)
	}
}

// fetchIssueRole queries Beads for the current role label on an issue.
// Returns "" when the call fails or no role:* label is present.
// Used by watchHermes to emit a structured "role assigned" log after Hermes exits.
func fetchIssueRole(issueID string) string {
	out, err := run("bd", "show", issueID, "--json")
	if err != nil {
		return ""
	}
	var iss issueItem
	if jerr := json.Unmarshal([]byte(strings.TrimSpace(string(out))), &iss); jerr != nil {
		return ""
	}
	return roleLabel(iss.Labels)
}

// removeLabel removes a label from an issue in Beads. Best-effort: logs on
// failure but never crashes Archon.
func removeLabel(ctx context.Context, label, issueID string) {
	if _, err := run("bd", "update", issueID, "--remove-label", label); err != nil {
		slog.ErrorContext(ctx, "removeLabel: failed",
			"issue_id", issueID, "label", label, "err", err)
	}
}

// exitLabel maps a vessel-driver exit code to a Beads label string.
// Codes are defined in the vessel-driver boundary ADR (lg-fzl):
//
//	0 = end_turn (success)
//	1 = acp-internal error
//	2 = refusal
//	3 = max_tokens
//	4 = timeout
func exitLabel(code int) string {
	switch code {
	case 2:
		return "failed:refusal"
	case 3:
		return "failed:tokens"
	case 4:
		return "failed:timeout"
	default: // 1 or unknown non-zero
		return "failed:internal"
	}
}

// markBlockedWithLabel marks an issue blocked and attaches a specific failure label.
// Best-effort: logs on failure, never crashes Archon.
func markBlockedWithLabel(ctx context.Context, issueID, label string) {
	if _, err := run("bd", "update", issueID, "--status=blocked", "--add-label", label); err != nil {
		slog.ErrorContext(ctx, "marking issue blocked", "issue_id", issueID, "label", label, "err", err)
	}
}

// createReviewBead creates a reviewer role bead in Beads after a worker vessel
// exits cleanly. Best-effort: logs on failure, never crashes Archon.
func createReviewBead(ctx context.Context, issueID, issueTitle, originalIssueID string, reworkCount int) {
	labels := fmt.Sprintf("role:reviewer,discovered-from:%s,review-branch:vessel/%s,review-rework-count:%d,original-issue:%s,dispatch:auto",
		issueID, issueID, reworkCount, originalIssueID)
	out, err := run("bd", "create",
		"Review: "+issueTitle,
		"--description=Review output of vessel "+issueID+". Branch: vessel/"+issueID+".",
		"--labels", labels,
		"-t", "task",
		"-p", "1",
		"--json",
	)
	if err != nil {
		slog.ErrorContext(ctx, "creating review bead", "issue_id", issueID, "err", err)
		return
	}
	var created issueItem
	reviewID := ""
	if jerr := json.Unmarshal([]byte(strings.TrimSpace(string(out))), &created); jerr == nil {
		reviewID = created.ID
	}
	slog.InfoContext(ctx, "review bead created", "issue_id", issueID, "review_id", reviewID)
}

func spawnVessel(ctx context.Context, cfg config, acfg archoncfg.ArchonConfig, issueID, name, roleName, agentName, issueTitle, issueDescription, issueAC string, labels []string, o *obs) error {
	ctx, span := o.tracer.Start(ctx, "legion.archon.vessel.spawn",
		trace.WithAttributes(
			attribute.String("issue.id", issueID),
			attribute.String("container.name", name),
			attribute.String("vessel.role", roleName),
			attribute.String("vessel.agent", agentName),
		),
	)
	defer span.End()

	// Build VesselConfig — the single structured config delivered to the vessel.
	// Secrets (GITHUB_TOKEN, DOLT_*) remain as separate env vars.
	vc := archoncfg.VesselConfig{
		IssueID:  issueID,
		RoleName: roleName,
		RepoURL:  cfg.repoURL,
		ACPSpec: archoncfg.ACPSpec{
			Transport: "stdio",
			Backend:   "copilot",
			Model:     resolveModel(acfg, labels),
			AgentFile: agentName,
		},
		AgentName:            agentName,
		ReviewEnabled:        acfg.Review.Enabled,
		MaxRework:            acfg.Review.MaxRework,
		DefaultRole:          acfg.Routing.DefaultRole,
		MaxDispatch:          acfg.Routing.MaxDispatch,
		DispatcherMode:       acfg.Routing.DispatcherMode,
		RouterAgent:          acfg.Routing.RouterAgent,
		PromptTimeoutSeconds: acfg.Daemon.PromptTimeoutSeconds,
	}
	vc.ApplyDefaults()
	vc.DeleteBranchOnMerge = acfg.Review.DeleteBranchOnMerge

	// Populate reviewer-specific fields so VesselConfig.Validate() passes and
	// vessel-driver hooks have the branch available via LEGION_REVIEW_BRANCH
	// (lg-ldl). The branch is encoded as a label by createReviewBead.
	if roleName == "reviewer" {
		vc.ReviewBranch = reviewBranchLabel(labels)
		vc.ReviewWorkIssue = issueID
		vc.ReviewOriginalIssue = originalIssueLabel(labels)
		vc.ReviewReworkCount = reworkCountLabel(labels)
	}

	if roleName == "worker" {
		vc.WorkBranch = workBranchLabel(labels)
	}

	if err := vc.Validate(); err != nil {
		return fmt.Errorf("VesselConfig precondition failed for %s: %w", issueID, err)
	}

	vcJSON, err := json.Marshal(vc)
	if err != nil {
		return fmt.Errorf("marshaling VesselConfig: %w", err)
	}

	args := []string{
		"run",
		"--detach",
		"--name", name,
		"--network=" + cfg.dockerNetwork,
		"--add-host=host.docker.internal:host-gateway",
		"-e", "LEGION_CONFIG_JSON=" + string(vcJSON),
		"-e", "LEGION_ISSUE_ID=" + issueID,
		"-e", "LEGION_ROLE=" + roleName,
		"-e", "GITHUB_TOKEN=" + cfg.githubToken,
		"-e", "GH_TOKEN=" + cfg.githubToken,
		"-e", "DOLT_HOST=" + cfg.doltHost,
		"-e", "DOLT_PORT=" + cfg.doltPort,
		"-e", "ISSUE_TITLE=" + issueTitle,
		"-e", "ISSUE_DESCRIPTION=" + issueDescription,
		"-e", "ISSUE_AC=" + issueAC,
		"-e", "LEGION_MODEL=" + resolveModel(acfg, labels),
		// VESSEL_TIMEOUT: explicit ACP prompt deadline so vessel-driver's hardcoded
		// fallback (900 s) is never relied upon in production.
		"-e", "VESSEL_TIMEOUT=" + strconv.Itoa(vc.PromptTimeoutSeconds),
		// LEGION_REVIEW_BRANCH: required by the reviewer pre-acp hook (lg-ldl).
		// Empty for non-reviewer roles — harmless; hook only checks it for reviewer.
		"-e", "LEGION_REVIEW_BRANCH=" + vc.ReviewBranch,
		// LEGION_WORK_BRANCH: non-empty for rework workers; empty otherwise.
		"-e", "LEGION_WORK_BRANCH=" + vc.WorkBranch,
	}
	if ep := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"); ep != "" {
		args = append(args, "-e", "OTEL_EXPORTER_OTLP_ENDPOINT="+ep)
	}
	args = append(args, "-e", "OTEL_SERVICE_NAME=legion.vessel-driver")

	// Propagate the spawn span's trace context into the vessel container using
	// the W3C traceparent format (lg-4zv). vessel-driver reads this env var on
	// startup and creates its root span as a child, linking the two traces.
	prop := propagation.TraceContext{}
	carrier := propagation.MapCarrier{}
	prop.Inject(ctx, carrier)
	if tp := carrier.Get("traceparent"); tp != "" {
		args = append(args, "-e", "TRACEPARENT="+tp)
	}

	args = append(args, cfg.vesselImage)

	_, err = run("docker", args...)
	if err != nil {
		span.RecordError(err)
		return err
	}
	o.issuesSpawned.Add(ctx, 1)
	return nil
}

// removeContainer removes a stopped container via `docker rm -f`. Best-effort:
// if the container is already gone or the call fails for any reason, the error
// is logged and swallowed — callers must not depend on this succeeding.
func removeContainer(ctx context.Context, name string) {
	if _, err := run("docker", "rm", "-f", name); err != nil {
		slog.WarnContext(ctx, "removing container", "container", name, "err", err)
	}
}

// spawnHermes spawns a Hermes classifier vessel for the given issue.
// It labels the issue "hermes:classifying" in Beads before starting the container
// so any concurrent Archon pulse tick does not re-dispatch.  On spawn failure the
// label is removed so the issue remains eligible for retry.
func spawnHermes(ctx context.Context, iss issueItem, cfg config, acfg archoncfg.ArchonConfig, ht *tracker) error {
	if acfg.Hermes.Image == "" {
		return fmt.Errorf("spawnHermes: hermes.image is not configured")
	}
	name := hermesContainerName(iss.ID)

	// Apply the classifying label *before* spawning to prevent double-dispatch.
	if _, err := run("bd", "update", iss.ID, "--add-label", "hermes:classifying"); err != nil {
		return fmt.Errorf("spawnHermes: labeling issue %s: %w", iss.ID, err)
	}

	// Build a minimal VesselConfig so the Hermes vessel can read its identity
	// via LEGION_CONFIG_JSON using the standard vessel-driver Load() path.
	vc := archoncfg.VesselConfig{
		IssueID:  iss.ID,
		RoleName: "hermes",
		RepoURL:  cfg.repoURL,
		ACPSpec: archoncfg.ACPSpec{
			Transport: "stdio",
			Backend:   "copilot",
		},
	}
	vc.ApplyDefaults()

	vcJSON, err := json.Marshal(vc)
	if err != nil {
		removeLabel(ctx, "hermes:classifying", iss.ID)
		return fmt.Errorf("spawnHermes: marshaling VesselConfig: %w", err)
	}

	args := []string{
		"run", "--detach",
		"--name", name,
		"--network=" + cfg.dockerNetwork,
		"--add-host=host.docker.internal:host-gateway",
		"-e", "LEGION_CONFIG_JSON=" + string(vcJSON),
		"-e", "ISSUE_TITLE=" + iss.Title,
		"-e", "ISSUE_DESCRIPTION=" + iss.Description,
		"-e", "DOLT_HOST=" + cfg.doltHost,
		"-e", "DOLT_PORT=" + cfg.doltPort,
		acfg.Hermes.Image,
	}

	if _, err := run("docker", args...); err != nil {
		// Spawn failed — roll back the label so the issue isn't permanently stuck.
		removeLabel(ctx, "hermes:classifying", iss.ID)
		return fmt.Errorf("spawnHermes: docker run: %w", err)
	}

	ht.add(name, iss.ID, iss.Title, "hermes", "", "", 0)
	slog.InfoContext(ctx, "spawned hermes", "container", name, "issue_id", iss.ID)
	return nil
}

func inspectState(name string) (containerState, error) {
	out, err := run("docker", "inspect", name, "--format", "{{json .State}}")
	if err != nil {
		return containerState{}, fmt.Errorf("inspecting %s: %w", name, err)
	}
	var state containerState
	if err := json.Unmarshal(out, &state); err != nil {
		return containerState{}, fmt.Errorf("parsing inspect output for %s: %w", name, err)
	}
	return state, nil
}

// vesselLimitReached returns true if spawning a new vessel for the given role
// would violate any configured limit (global or per-role).
func vesselLimitReached(t *tracker, acfg archoncfg.ArchonConfig, roleName string) bool {
	if acfg.Limits.MaxGlobal > 0 && t.count() >= acfg.Limits.MaxGlobal {
		return true
	}
	if rc, ok := acfg.Roles[roleName]; ok && rc.Limit > 0 {
		if t.countByRole(roleName) >= rc.Limit {
			return true
		}
	}
	return false
}

// pulse is a single execution of the pulse loop body.
func pulse(ctx context.Context, cfg config, acfg archoncfg.ArchonConfig, t *tracker, ht *tracker, o *obs) {
	ctx, span := o.tracer.Start(ctx, "legion.archon.pulse")
	defer span.End()

	// Inject trace_id so Loki log lines link back to the Tempo trace (lg-030).
	logger := slog.Default()
	if sc := span.SpanContext(); sc.IsValid() {
		logger = logger.With("trace_id", sc.TraceID().String())
	}

	// Invoke pre-pulse hook if enabled (gated by flag, zero stat calls when disabled).
	if acfg.Hooks.PrePulseEnabled {
		if err := runArchonHook(ctx, "pre-pulse", acfg, cfg); err != nil {
			logger.WarnContext(ctx, "pre-pulse hook failed (continuing)", "err", err)
		}
	}

	issues, err := listReadyIssues(acfg.Routing.DispatchLabel)
	if err != nil {
		logger.ErrorContext(ctx, "pulse: listing ready issues", "err", err)
		span.RecordError(err)
		return
	}
	logger.DebugContext(ctx, "pulse: bd ready result", "issues", len(issues))

	spawned := 0
	for _, iss := range issues {
		if isInfraIssue(iss) {
			logger.DebugContext(ctx, "pulse: skipping infra issue", "issue_id", iss.ID, "labels", iss.Labels)
			continue
		}
		if isEscalatedIssue(iss) {
			logger.InfoContext(ctx, "pulse: skipping escalated issue — human intervention required",
				"issue_id", iss.ID, "labels", iss.Labels)
			continue
		}
		name := containerName(iss.ID)
		if t.has(name) {
			continue
		}

		// ── Hermes dispatch gate ────────────────────────────────────────────────
		// When Hermes is enabled and the issue has no role:* label, route it to a
		// Hermes classifier vessel first.  The classifier applies a role:* label
		// and exits; the next pulse tick then dispatches a regular vessel normally.
		if acfg.Hermes.Enabled {
			if isHermesClassifying(iss.Labels) {
				// Hermes is already running for this issue — wait for the watcher.
				logger.DebugContext(ctx, "pulse: hermes classifying, waiting",
					"issue_id", iss.ID)
				continue
			}
			hermesName := hermesContainerName(iss.ID)
			if ht.has(hermesName) {
				// Container tracked but label may not have propagated yet — skip.
				continue
			}
			if roleLabel(iss.Labels) == "" {
				// No role assigned yet — dispatch to Hermes for classification.
				if err := spawnHermes(ctx, iss, cfg, acfg, ht); err != nil {
					logger.ErrorContext(ctx, "pulse: spawning hermes", "issue_id", iss.ID, "err", err)
				}
				continue
			}
			// Issue has a role:* label (Hermes already classified it) — fall through
			// to the normal vessel dispatch path below.
		}
		// ── Normal vessel dispatch ──────────────────────────────────────────────

		roleName := inferRole(iss, acfg.Routing.DefaultRole)
		agent, ok := resolveAgent(acfg, iss, roleName)
		if !ok {
			logger.WarnContext(ctx, "pulse: no agent pool configured for role — skipping",
				"role", roleName, "issue_id", iss.ID)
			continue
		}
		if vesselLimitReached(t, acfg, roleName) {
			logger.DebugContext(ctx, "pulse: vessel limit reached", "role", roleName, "agent", agent)
			continue
		}
		if err := claimIssue(iss.ID); err != nil {
			// Another Archon instance may have already claimed it — skip silently.
			logger.ErrorContext(ctx, "claim issue (skipping)", "issue_id", iss.ID, "err", err)
			continue
		}
		logger.InfoContext(ctx, "issue assigned", "issue_id", iss.ID, "role", roleName, "agent", agent)
		if err := spawnVessel(ctx, cfg, acfg, iss.ID, name, roleName, agent, iss.Title, iss.Description, iss.AcceptanceCriteria, iss.Labels, o); err != nil {
			logger.ErrorContext(ctx, "spawning vessel", "issue_id", iss.ID, "err", err)
			markError(ctx, iss.ID, fmt.Sprintf("spawn failed: %v", err))
			continue
		}
		t.add(name, iss.ID, iss.Title, roleName, agent, originalIssueLabel(iss.Labels), reworkCountLabel(iss.Labels))
		logger.InfoContext(ctx, "spawned vessel", "container", name, "issue_id", iss.ID, "agent", agent, "role", roleName)
		spawned++
	}

	span.SetAttributes(
		attribute.Int("ready_count", len(issues)),
		attribute.Int("spawned_count", spawned),
	)
}

// watch is a single execution of the watcher loop body.
// lastHeartbeat tracks when each container last emitted a "still running" log;
// it is owned by watcherLoop and persists across ticks.
func watch(ctx context.Context, cfg config, acfg archoncfg.ArchonConfig, t *tracker, o *obs, lastHeartbeat map[string]time.Time) {
	ctx, span := o.tracer.Start(ctx, "legion.archon.watcher.tick")
	defer span.End()

	// Inject trace_id so Loki log lines link back to the Tempo trace (lg-030).
	logger := slog.Default()
	if sc := span.SpanContext(); sc.IsValid() {
		logger = logger.With("trace_id", sc.TraceID().String())
	}

	for name, e := range t.snapshot() {
		state, err := inspectState(name)
		if err != nil {
			errStr := err.Error()
			if strings.Contains(errStr, "No such object") || strings.Contains(errStr, "No such container") {
				// Container was auto-removed (--rm) before we could inspect it.
				// Treat as terminal: evict from tracker and mark the issue failed
				// so the pulse loop can spawn a replacement.
				logger.WarnContext(ctx, "watcher: container already gone, evicting",
					"container", name, "issue_id", e.issueID, "err", err)
				span.AddEvent("vessel.gone", trace.WithAttributes(
					attribute.String("issue.id", e.issueID),
					attribute.String("container.name", name),
				))
				markError(ctx, e.issueID, "vessel container removed before inspection")
				t.remove(name)
				delete(lastHeartbeat, name)
			} else {
				// Transient error (daemon not responding, etc.) — retry next tick.
				logger.ErrorContext(ctx, "watcher: inspect failed", "container", name, "err", err)
				span.RecordError(err)
			}
			continue
		}
		switch {
		case state.Status == "exited" && state.ExitCode == 0:
			logger.InfoContext(ctx, "vessel exited cleanly", "container", name, "issue_id", e.issueID)
			span.AddEvent("vessel.exit", trace.WithAttributes(
				attribute.String("issue.id", e.issueID),
				attribute.Int("exit_code", 0),
			))
			o.vesselDuration.Record(ctx, time.Since(e.startedAt).Seconds())
			markDone(ctx, e.issueID)
			logger.InfoContext(ctx, "issue closed", "issue_id", e.issueID, "container", name)
			if acfg.Review.Enabled && e.issueTitle != "" && e.roleName == "worker" {
				original := e.originalIssueID
				if original == "" {
					original = e.issueID // first-run: worker issue IS the original
				}
				createReviewBead(ctx, e.issueID, e.issueTitle, original, e.reworkCount)
			}
			removeContainer(ctx, name)
			t.remove(name)
			delete(lastHeartbeat, name)

		case state.Status == "exited":
			label := exitLabel(state.ExitCode)
			logger.WarnContext(ctx, "vessel exited with error",
				"container", name, "exit_code", state.ExitCode, "issue_id", e.issueID, "label", label)
			span.AddEvent("vessel.exit", trace.WithAttributes(
				attribute.String("issue.id", e.issueID),
				attribute.Int("exit_code", state.ExitCode),
			))
			o.vesselDuration.Record(ctx, time.Since(e.startedAt).Seconds())
			markBlockedWithLabel(ctx, e.issueID, label)
			logger.WarnContext(ctx, "issue blocked", "issue_id", e.issueID, "container", name, "exit_code", state.ExitCode, "label", label)
			removeContainer(ctx, name)
			t.remove(name)
			delete(lastHeartbeat, name)

		case time.Since(e.startedAt) > acfg.Daemon.VesselTimeout():
			logger.WarnContext(ctx, "vessel timed out",
				"container", name, "timeout", acfg.Daemon.VesselTimeout(), "issue_id", e.issueID)
			span.AddEvent("vessel.timeout", trace.WithAttributes(
				attribute.String("issue.id", e.issueID),
			))
			markBlocked(ctx, e.issueID, "vessel timed out")
			if _, err := run("docker", "stop", name); err != nil {
				logger.ErrorContext(ctx, "stopping timed-out vessel", "container", name, "err", err)
			}
			removeContainer(ctx, name)
			t.remove(name)
			delete(lastHeartbeat, name)

		default:
			// Container is still running. Emit a heartbeat log at most once every 30 s.
			if time.Since(lastHeartbeat[name]) >= 30*time.Second {
				logger.InfoContext(ctx, "vessel still running",
					"container", name,
					"issue_id", e.issueID,
					"elapsed", time.Since(e.startedAt).Round(time.Second),
				)
				lastHeartbeat[name] = time.Now()
			}
		}
	}
}

func pulseLoop(ctx context.Context, cfg config, acfg archoncfg.ArchonConfig, t *tracker, ht *tracker, o *obs) {
	ticker := time.NewTicker(acfg.Daemon.PulseInterval())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pulse(ctx, cfg, acfg, t, ht, o)
		}
	}
}

func watcherLoop(ctx context.Context, cfg config, acfg archoncfg.ArchonConfig, t *tracker, o *obs) {
	ticker := time.NewTicker(acfg.Daemon.WatcherInterval())
	defer ticker.Stop()
	lastHeartbeat := make(map[string]time.Time)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			watch(ctx, cfg, acfg, t, o, lastHeartbeat)
		}
	}
}

// watchHermes is a single tick of the Hermes container watcher.
// On clean exit (0): remove container and tracker entry; Hermes applied role:*
// label so the next pulse picks the issue up normally.
// On error exit or timeout: apply the configured default role, remove the
// "hermes:classifying" label, and clean up so the issue is eligible for normal dispatch.
func watchHermes(ctx context.Context, acfg archoncfg.ArchonConfig, ht *tracker) {
	for name, e := range ht.snapshot() {
		state, err := inspectState(name)
		if err != nil {
			errStr := err.Error()
			if strings.Contains(errStr, "No such object") || strings.Contains(errStr, "No such container") {
				// Container is gone before we could inspect it — apply default role
				// so the issue is not permanently stuck in the classifying state.
				slog.WarnContext(ctx, "watchHermes: container gone before inspection, applying default role",
					"container", name, "issue_id", e.issueID)
				applyDefaultRole(ctx, acfg.Routing.DefaultRole, e.issueID)
				removeLabel(ctx, "hermes:classifying", e.issueID)
				ht.remove(name)
			} else {
				// Transient error — retry on next tick.
				slog.ErrorContext(ctx, "watchHermes: inspect failed", "container", name, "err", err)
			}
			continue
		}

		switch {
		case state.Status == "exited" && state.ExitCode == 0:
			// Hermes classified the issue and applied a role:* label.
			// Remove the classifying label defensively — idempotent if the vessel
			// already removed it, but prevents permanent stall if it did not.
			slog.InfoContext(ctx, "hermes exited cleanly", "container", name, "issue_id", e.issueID)
			removeLabel(ctx, "hermes:classifying", e.issueID)
			role := fetchIssueRole(e.issueID)
			slog.InfoContext(ctx, "role assigned", "issue_id", e.issueID, "role", role)
			removeContainer(ctx, name)
			ht.remove(name)

		case state.Status == "exited":
			// Hermes failed — fall back to the default role so the issue makes progress.
			slog.WarnContext(ctx, "hermes exited with error",
				"container", name, "exit_code", state.ExitCode, "issue_id", e.issueID)
			applyDefaultRole(ctx, acfg.Routing.DefaultRole, e.issueID)
			removeLabel(ctx, "hermes:classifying", e.issueID)
			removeContainer(ctx, name)
			ht.remove(name)

		case acfg.Hermes.HermesTimeout() > 0 && time.Since(e.startedAt) > acfg.Hermes.HermesTimeout():
			// Hermes ran past its deadline — stop it and fall back to default role.
			slog.WarnContext(ctx, "hermes timed out",
				"container", name, "timeout", acfg.Hermes.HermesTimeout(), "issue_id", e.issueID)
			applyDefaultRole(ctx, acfg.Routing.DefaultRole, e.issueID)
			removeLabel(ctx, "hermes:classifying", e.issueID)
			if _, err := run("docker", "stop", name); err != nil {
				slog.ErrorContext(ctx, "watchHermes: stopping timed-out container",
					"container", name, "err", err)
			}
			removeContainer(ctx, name)
			ht.remove(name)

		default:
			slog.DebugContext(ctx, "hermes still running",
				"container", name,
				"issue_id", e.issueID,
				"elapsed", time.Since(e.startedAt).Round(time.Second),
			)
		}
	}
}

// hermesWatcherLoop runs watchHermes on the watcher interval until ctx is cancelled.
func hermesWatcherLoop(ctx context.Context, acfg archoncfg.ArchonConfig, ht *tracker) {
	ticker := time.NewTicker(acfg.Daemon.WatcherInterval())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			watchHermes(ctx, acfg, ht)
		}
	}
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	tracer, meter, metricsMux, shutdown, err := telemetry.Setup(ctx, "legion.archon")
	if err != nil {
		slog.Error("telemetry setup failed", "err", err)
		// Non-fatal: continue without full telemetry.
	}
	defer shutdown(ctx)

	// Metric instruments — safe to call on a noop meter if Setup partially failed.
	issuesSpawned, _ := meter.Int64Counter("legion.issues.spawned",
		metric.WithDescription("Total issues spawned as vessels"))
	vesselDuration, _ := meter.Float64Histogram("legion.vessel.duration_seconds",
		metric.WithDescription("Vessel container lifetime in seconds"))

	o := &obs{
		tracer:         tracer,
		issuesSpawned:  issuesSpawned,
		vesselDuration: vesselDuration,
	}

	// /metrics HTTP server — only started when Prometheus exporter is healthy.
	if metricsMux != nil {
		go func() {
			srv := &http.Server{Addr: ":2112", Handler: metricsMux}
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				slog.Error("metrics server error", "err", err)
			}
		}()
	}

	cfg := loadConfig()
	acfg, err := archoncfg.LoadArchonConfig()
	if err != nil {
		slog.Error("failed to load archon config", "err", err)
		os.Exit(1)
	}
	t := &tracker{runs: make(map[string]entry)}
	ht := &tracker{runs: make(map[string]entry)} // hermes classifier tracker

	slog.Info("archon starting",
		"pulse_interval", acfg.Daemon.PulseInterval(),
		"watcher_interval", acfg.Daemon.WatcherInterval(),
		"vessel_timeout", acfg.Daemon.VesselTimeout(),
		"max_global", acfg.Limits.MaxGlobal,
	)

	// Invoke pre-start hook. Failure is fatal — Archon does not continue.
	if err := runArchonHook(ctx, "pre-start", acfg, cfg); err != nil {
		slog.Error("pre-start hook failed", "err", err)
		os.Exit(1)
	}

	// Deferred post-stop cleanup: runs with context.Background() + 30s timeout
	// (NOT the signal context, which is already cancelled at this point).
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if err := runArchonHook(cleanupCtx, "post-stop", acfg, cfg); err != nil {
			slog.Warn("post-stop hook failed (continuing exit)", "err", err)
		}
	}()

	// Recover any vessel containers that outlived a previous Archon process so the
	// watcher loop can time them out or clean them up without re-spawning.
	reconcile(ctx, t)

	go pulseLoop(ctx, cfg, acfg, t, ht, o)
	go watcherLoop(ctx, cfg, acfg, t, o)
	if acfg.Hermes.Enabled {
		go hermesWatcherLoop(ctx, acfg, ht)
	}

	<-ctx.Done()
	slog.Info("received shutdown signal, stopping")
	time.Sleep(500 * time.Millisecond)
}
