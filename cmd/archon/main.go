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

	"github.com/EmmittJ/legion/internal/telemetry"
)

// config holds all runtime configuration read from environment variables.
type config struct {
	doltHost    string
	doltPort    string
	repoURL     string
	vesselImage string
	githubToken string
	vesselModel string
	timeout     time.Duration
}

// entry tracks a running vessel container.
type entry struct {
	issueID   string
	startedAt time.Time
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

func (t *tracker) add(name, issueID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.runs[name] = entry{issueID: issueID, startedAt: time.Now()}
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

// obs bundles the OpenTelemetry instruments shared across the pulse and watcher loops.
type obs struct {
	tracer         trace.Tracer
	issuesSpawned  metric.Int64Counter
	vesselDuration metric.Float64Histogram
}


// The JSON output is a flat array — no wrapping "issue" key.
type issueItem struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

type containerState struct {
	Status   string `json:"Status"`
	ExitCode int    `json:"ExitCode"`
}

func loadConfig() config {
	timeout := 3600 * time.Second
	if v := os.Getenv("ARCHON_TIMEOUT"); v != "" {
		var secs int
		if _, err := fmt.Sscanf(v, "%d", &secs); err == nil && secs > 0 {
			timeout = time.Duration(secs) * time.Second
		}
	}
	return config{
		doltHost:    os.Getenv("BEADS_DOLT_SERVER_HOST"),
		doltPort:    os.Getenv("BEADS_DOLT_SERVER_PORT"),
		repoURL:     os.Getenv("REPO_URL"),
		vesselImage: os.Getenv("VESSEL_IMAGE"),
		githubToken: os.Getenv("GITHUB_TOKEN"),
		vesselModel: os.Getenv("VESSEL_MODEL"),
		timeout:     timeout,
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

func listReadyIssues() ([]issueItem, error) {
	out, err := run("bd", "ready", "--json")
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

// markError marks an issue blocked in Beads and appends a reason note.
// "failed" is not a valid Beads status; blocked is the correct terminal-error state.
func markError(ctx context.Context, issueID, reason string) {
	if _, err := run("bd", "update", issueID, "--status=blocked", "--append-notes", reason); err != nil {
		slog.ErrorContext(ctx, "marking issue blocked", "issue_id", issueID, "err", err)
	}
}

func markBlocked(ctx context.Context, issueID, note string) {
	if _, err := run("bd", "update", issueID, "--status=blocked", "--append-notes", note); err != nil {
		slog.ErrorContext(ctx, "marking issue blocked", "issue_id", issueID, "err", err)
	}
}

func spawnVessel(ctx context.Context, cfg config, issueID, name string, o *obs) error {
	ctx, span := o.tracer.Start(ctx, "legion.archon.vessel.spawn",
		trace.WithAttributes(
			attribute.String("issue.id", issueID),
			attribute.String("container.name", name),
		),
	)
	defer span.End()

	_, err := run(
		"docker", "run",
		"--detach",
		"--name", name,
		"--network=legion_legion-net",
		"-e", "ISSUE_ID="+issueID,
		"-e", "BEADS_DOLT_SERVER_HOST="+cfg.doltHost,
		"-e", "BEADS_DOLT_SERVER_PORT="+cfg.doltPort,
		"-e", "REPO_URL="+cfg.repoURL,
		"-e", "GITHUB_TOKEN="+cfg.githubToken,
		"-e", "VESSEL_MODEL="+cfg.vesselModel,
		cfg.vesselImage,
	)
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

// pulse is a single execution of the pulse loop body.
func pulse(ctx context.Context, cfg config, t *tracker, o *obs) {
	ctx, span := o.tracer.Start(ctx, "legion.archon.pulse")
	defer span.End()

	issues, err := listReadyIssues()
	if err != nil {
		slog.ErrorContext(ctx, "pulse: listing ready issues", "err", err)
		span.RecordError(err)
		return
	}

	spawned := 0
	for _, iss := range issues {
		name := containerName(iss.ID)
		if t.has(name) {
			continue
		}
		if err := claimIssue(iss.ID); err != nil {
			// Another Archon instance may have already claimed it — skip silently.
			slog.ErrorContext(ctx, "claim issue (skipping)", "issue_id", iss.ID, "err", err)
			continue
		}
		if err := spawnVessel(ctx, cfg, iss.ID, name, o); err != nil {
			slog.ErrorContext(ctx, "spawning vessel", "issue_id", iss.ID, "err", err)
			markError(ctx, iss.ID, fmt.Sprintf("spawn failed: %v", err))
			continue
		}
		t.add(name, iss.ID)
		slog.InfoContext(ctx, "spawned vessel", "container", name, "issue_id", iss.ID)
		spawned++
	}

	span.SetAttributes(
		attribute.Int("ready_count", len(issues)),
		attribute.Int("spawned_count", spawned),
	)
}

// watch is a single execution of the watcher loop body.
func watch(ctx context.Context, cfg config, t *tracker, o *obs) {
	ctx, span := o.tracer.Start(ctx, "legion.archon.watcher.tick")
	defer span.End()

	for name, e := range t.snapshot() {
		state, err := inspectState(name)
		if err != nil {
			slog.ErrorContext(ctx, "watcher: inspect failed", "container", name, "err", err)
			span.RecordError(err)
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
			removeContainer(ctx, name)
			t.remove(name)

		case state.Status == "exited":
			slog.WarnContext(ctx, "vessel exited with error",
				"container", name, "exit_code", state.ExitCode, "issue_id", e.issueID)
			span.AddEvent("vessel.exit", trace.WithAttributes(
				attribute.String("issue.id", e.issueID),
				attribute.Int("exit_code", state.ExitCode),
			))
			o.vesselDuration.Record(ctx, time.Since(e.startedAt).Seconds())
			markError(ctx, e.issueID, fmt.Sprintf("vessel exited with code %d", state.ExitCode))
			removeContainer(ctx, name)
			t.remove(name)

		case time.Since(e.startedAt) > cfg.timeout:
			slog.WarnContext(ctx, "vessel timed out",
				"container", name, "timeout", cfg.timeout, "issue_id", e.issueID)
			span.AddEvent("vessel.timeout", trace.WithAttributes(
				attribute.String("issue.id", e.issueID),
			))
			markBlocked(ctx, e.issueID, "vessel timed out")
			if _, err := run("docker", "stop", name); err != nil {
				slog.ErrorContext(ctx, "stopping timed-out vessel", "container", name, "err", err)
			}
			removeContainer(ctx, name)
			t.remove(name)
		}
	}
}

func pulseLoop(ctx context.Context, cfg config, t *tracker, o *obs) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pulse(ctx, cfg, t, o)
		}
	}
}

func watcherLoop(ctx context.Context, cfg config, t *tracker, o *obs) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			watch(ctx, cfg, t, o)
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
	t := &tracker{runs: make(map[string]entry)}

	slog.Info("archon starting", "pulse_interval", "5s", "watcher_interval", "10s")

	go pulseLoop(ctx, cfg, t, o)
	go watcherLoop(ctx, cfg, t, o)

	<-ctx.Done()
	slog.Info("received shutdown signal, stopping")
	time.Sleep(500 * time.Millisecond)
}
