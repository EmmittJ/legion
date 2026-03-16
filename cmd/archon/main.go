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
	"strings"
	"sync"
	"syscall"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
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
	issueID    string
	issueTitle string
	startedAt  time.Time
	roleName   string
	agentName  string
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

func (t *tracker) add(name, issueID, issueTitle, roleName, agentName string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.runs[name] = entry{
		issueID:    issueID,
		issueTitle: issueTitle,
		startedAt:  time.Now(),
		roleName:   roleName,
		agentName:  agentName,
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

// createReviewBead creates an Inquisitor review bead in Beads after a worker vessel
// exits cleanly. Best-effort: logs on failure, never crashes Archon.
func createReviewBead(ctx context.Context, issueID, issueTitle string) {
	_, err := run("bd", "create",
		"Review: "+issueTitle,
		"--description=Review output of vessel "+issueID+". Branch: vessel/"+issueID+".",
		"--add-label", "role:inquisitor",
		"--add-label", "discovered-from:"+issueID,
		"-t", "task",
		"-p", "1",
		"--json",
	)
	if err != nil {
		slog.ErrorContext(ctx, "creating review bead", "issue_id", issueID, "err", err)
	}
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
		AgentName:      agentName,
		ReviewEnabled:  acfg.Review.Enabled,
		MaxRework:      acfg.Review.MaxRework,
		DefaultRole:    acfg.Routing.DefaultRole,
		MaxDispatch:    acfg.Routing.MaxDispatch,
		DispatcherMode: acfg.Routing.DispatcherMode,
		RouterAgent:    acfg.Routing.RouterAgent,
	}
	vc.ApplyDefaults()
	vc.DeleteBranchOnMerge = acfg.Review.DeleteBranchOnMerge

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
		"-e", "GITHUB_TOKEN=" + cfg.githubToken,
		"-e", "DOLT_HOST=" + cfg.doltHost,
		"-e", "DOLT_PORT=" + cfg.doltPort,
		"-e", "ISSUE_TITLE=" + issueTitle,
		"-e", "ISSUE_DESCRIPTION=" + issueDescription,
		"-e", "ISSUE_AC=" + issueAC,
		"-e", "LEGION_MODEL=" + resolveModel(acfg, labels),
	}
	if ep := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"); ep != "" {
		args = append(args, "-e", "OTEL_EXPORTER_OTLP_ENDPOINT="+ep)
	}
	args = append(args, "-e", "OTEL_SERVICE_NAME=legion.vessel-driver")
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
// and agent would violate any configured limit (global, per-role, or per-agent).
func vesselLimitReached(t *tracker, acfg archoncfg.ArchonConfig, roleName, agentName string) bool {
	if acfg.Limits.MaxGlobal > 0 && t.count() >= acfg.Limits.MaxGlobal {
		return true
	}
	if roleName != "" {
		if cap, ok := acfg.Limits.ByRole[roleName]; ok && t.countByRole(roleName) >= cap {
			return true
		}
	}
	if agentName != "" {
		if cap, ok := acfg.Limits.ByAgent[agentName]; ok && t.countByAgent(agentName) >= cap {
			return true
		}
	}
	return false
}

// pulse is a single execution of the pulse loop body.
func pulse(ctx context.Context, cfg config, acfg archoncfg.ArchonConfig, t *tracker, o *obs) {
	ctx, span := o.tracer.Start(ctx, "legion.archon.pulse")
	defer span.End()

	issues, err := listReadyIssues(acfg.Routing.DispatchLabel)
	if err != nil {
		slog.ErrorContext(ctx, "pulse: listing ready issues", "err", err)
		span.RecordError(err)
		return
	}
	slog.DebugContext(ctx, "pulse: bd ready result", "issues", len(issues))

	spawned := 0
	for _, iss := range issues {
		if isInfraIssue(iss) {
			slog.DebugContext(ctx, "pulse: skipping infra issue", "issue_id", iss.ID, "labels", iss.Labels)
			continue
		}
		if isEscalatedIssue(iss) {
			slog.InfoContext(ctx, "pulse: skipping escalated issue — human intervention required",
				"issue_id", iss.ID, "labels", iss.Labels)
			continue
		}
		name := containerName(iss.ID)
		if t.has(name) {
			continue
		}
		agent := agentLabel(iss)
		roleName := inferRole(iss, acfg.Routing.DefaultRole)
		if vesselLimitReached(t, acfg, roleName, agent) {
			slog.DebugContext(ctx, "pulse: vessel limit reached", "role", roleName, "agent", agent)
			continue
		}
		if err := claimIssue(iss.ID); err != nil {
			// Another Archon instance may have already claimed it — skip silently.
			slog.ErrorContext(ctx, "claim issue (skipping)", "issue_id", iss.ID, "err", err)
			continue
		}
		if err := spawnVessel(ctx, cfg, acfg, iss.ID, name, roleName, agent, iss.Title, iss.Description, iss.AcceptanceCriteria, iss.Labels, o); err != nil {
			slog.ErrorContext(ctx, "spawning vessel", "issue_id", iss.ID, "err", err)
			markError(ctx, iss.ID, fmt.Sprintf("spawn failed: %v", err))
			continue
		}
		t.add(name, iss.ID, iss.Title, roleName, agent)
		slog.InfoContext(ctx, "spawned vessel", "container", name, "issue_id", iss.ID, "agent", agent, "role", roleName)
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

	for name, e := range t.snapshot() {
		state, err := inspectState(name)
		if err != nil {
			errStr := err.Error()
			if strings.Contains(errStr, "No such object") || strings.Contains(errStr, "No such container") {
				// Container was auto-removed (--rm) before we could inspect it.
				// Treat as terminal: evict from tracker and mark the issue failed
				// so the pulse loop can spawn a replacement.
				slog.WarnContext(ctx, "watcher: container already gone, evicting",
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
				slog.ErrorContext(ctx, "watcher: inspect failed", "container", name, "err", err)
				span.RecordError(err)
			}
			continue
		}
		switch {
		case state.Status == "exited" && state.ExitCode == 0:
			slog.InfoContext(ctx, "vessel exited cleanly", "container", name, "issue_id", e.issueID)
			span.AddEvent("vessel.exit", trace.WithAttributes(
				attribute.String("issue.id", e.issueID),
				attribute.Int("exit_code", 0),
			))
			o.vesselDuration.Record(ctx, time.Since(e.startedAt).Seconds())
			markDone(ctx, e.issueID)
			if acfg.Review.Enabled && e.issueTitle != "" {
				createReviewBead(ctx, e.issueID, e.issueTitle)
			}
			removeContainer(ctx, name)
			t.remove(name)
			delete(lastHeartbeat, name)

		case state.Status == "exited":
			label := exitLabel(state.ExitCode)
			slog.WarnContext(ctx, "vessel exited with error",
				"container", name, "exit_code", state.ExitCode, "issue_id", e.issueID, "label", label)
			span.AddEvent("vessel.exit", trace.WithAttributes(
				attribute.String("issue.id", e.issueID),
				attribute.Int("exit_code", state.ExitCode),
			))
			o.vesselDuration.Record(ctx, time.Since(e.startedAt).Seconds())
			markBlockedWithLabel(ctx, e.issueID, label)
			removeContainer(ctx, name)
			t.remove(name)
			delete(lastHeartbeat, name)

		case time.Since(e.startedAt) > acfg.Daemon.VesselTimeout():
			slog.WarnContext(ctx, "vessel timed out",
				"container", name, "timeout", acfg.Daemon.VesselTimeout(), "issue_id", e.issueID)
			span.AddEvent("vessel.timeout", trace.WithAttributes(
				attribute.String("issue.id", e.issueID),
			))
			markBlocked(ctx, e.issueID, "vessel timed out")
			if _, err := run("docker", "stop", name); err != nil {
				slog.ErrorContext(ctx, "stopping timed-out vessel", "container", name, "err", err)
			}
			removeContainer(ctx, name)
			t.remove(name)
			delete(lastHeartbeat, name)

		default:
			// Container is still running. Emit a heartbeat log at most once every 30 s.
			if time.Since(lastHeartbeat[name]) >= 30*time.Second {
				slog.InfoContext(ctx, "vessel still running",
					"container", name,
					"issue_id", e.issueID,
					"elapsed", time.Since(e.startedAt).Round(time.Second),
				)
				lastHeartbeat[name] = time.Now()
			}
		}
	}
}

func pulseLoop(ctx context.Context, cfg config, acfg archoncfg.ArchonConfig, t *tracker, o *obs) {
	ticker := time.NewTicker(acfg.Daemon.PulseInterval())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pulse(ctx, cfg, acfg, t, o)
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

	slog.Info("archon starting",
		"pulse_interval", acfg.Daemon.PulseInterval(),
		"watcher_interval", acfg.Daemon.WatcherInterval(),
		"vessel_timeout", acfg.Daemon.VesselTimeout(),
		"max_global", acfg.Limits.MaxGlobal,
	)

	// Recover any vessel containers that outlived a previous Archon process so the
	// watcher loop can time them out or clean them up without re-spawning.
	reconcile(ctx, t)

	go pulseLoop(ctx, cfg, acfg, t, o)
	go watcherLoop(ctx, cfg, acfg, t, o)

	<-ctx.Done()
	slog.Info("received shutdown signal, stopping")
	time.Sleep(500 * time.Millisecond)
}
