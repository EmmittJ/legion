package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
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

// issueItem is the element returned by `bd ready --json` and `bd list --json`.
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
func markDone(issueID string) {
	if _, err := run("bd", "close", issueID, "--reason", "completed"); err != nil {
		log.Printf("ERROR: closing issue %s: %v", issueID, err)
	}
}

// markError marks an issue blocked in Beads and appends a reason note.
// "failed" is not a valid Beads status; blocked is the correct terminal-error state.
func markError(issueID, reason string) {
	if _, err := run("bd", "update", issueID, "--status=blocked", "--append-notes", reason); err != nil {
		log.Printf("ERROR: marking issue %s blocked: %v", issueID, err)
	}
}

func markBlocked(issueID, note string) {
	if _, err := run("bd", "update", issueID, "--status=blocked", "--append-notes", note); err != nil {
		log.Printf("ERROR: marking issue %s blocked: %v", issueID, err)
	}
}

func spawnVessel(cfg config, issueID, name string) error {
	_, err := run(
		"docker", "run",
		"--detach",
		"--name", name,
		"--network=legion-net",
		"-e", "ISSUE_ID="+issueID,
		"-e", "BEADS_DOLT_SERVER_HOST="+cfg.doltHost,
		"-e", "BEADS_DOLT_SERVER_PORT="+cfg.doltPort,
		"-e", "REPO_URL="+cfg.repoURL,
		"-e", "GITHUB_TOKEN="+cfg.githubToken,
		"-e", "VESSEL_MODEL="+cfg.vesselModel,
		cfg.vesselImage,
	)
	return err
}

// removeContainer removes a stopped container via `docker rm -f`. Best-effort:
// if the container is already gone or the call fails for any reason, the error
// is logged and swallowed — callers must not depend on this succeeding.
func removeContainer(name string) {
	if _, err := run("docker", "rm", "-f", name); err != nil {
		log.Printf("WARN: removing container %s: %v", name, err)
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
func pulse(cfg config, t *tracker) {
	issues, err := listReadyIssues()
	if err != nil {
		log.Printf("ERROR: pulse: %v", err)
		return
	}
	for _, iss := range issues {
		name := containerName(iss.ID)
		if t.has(name) {
			continue
		}
		if err := claimIssue(iss.ID); err != nil {
			// Another Archon instance may have already claimed it — skip silently.
			log.Printf("ERROR: claim %s (skipping): %v", iss.ID, err)
			continue
		}
		if err := spawnVessel(cfg, iss.ID, name); err != nil {
			log.Printf("ERROR: spawning vessel for %s: %v", iss.ID, err)
			markError(iss.ID, fmt.Sprintf("spawn failed: %v", err))
			continue
		}
		t.add(name, iss.ID)
		log.Printf("spawned %s for issue %s", name, iss.ID)
	}
}

// watch is a single execution of the watcher loop body.
func watch(cfg config, t *tracker) {
	for name, e := range t.snapshot() {
		state, err := inspectState(name)
		if err != nil {
			log.Printf("ERROR: watcher: %v", err)
			continue
		}
		switch {
		case state.Status == "exited" && state.ExitCode == 0:
			log.Printf("vessel %s exited cleanly (issue %s)", name, e.issueID)
			markDone(e.issueID)
			removeContainer(name)
			t.remove(name)

		case state.Status == "exited":
			log.Printf("vessel %s exited with code %d (issue %s)", name, state.ExitCode, e.issueID)
			markError(e.issueID, fmt.Sprintf("vessel exited with code %d", state.ExitCode))
			removeContainer(name)
			t.remove(name)

		case time.Since(e.startedAt) > cfg.timeout:
			log.Printf("vessel %s timed out after %v (issue %s)", name, cfg.timeout, e.issueID)
			markBlocked(e.issueID, "vessel timed out")
			if _, err := run("docker", "stop", name); err != nil {
				log.Printf("ERROR: stopping timed-out vessel %s: %v", name, err)
			}
			removeContainer(name)
			t.remove(name)
		}
	}
}

func pulseLoop(cfg config, t *tracker, done <-chan struct{}) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			pulse(cfg, t)
		}
	}
}

func watcherLoop(cfg config, t *tracker, done <-chan struct{}) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			watch(cfg, t)
		}
	}
}

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.LUTC)
	log.SetOutput(os.Stderr)

	cfg := loadConfig()
	t := &tracker{runs: make(map[string]entry)}

	done := make(chan struct{})
	go pulseLoop(cfg, t, done)
	go watcherLoop(cfg, t, done)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	log.Println("received shutdown signal, stopping")
	close(done)
	time.Sleep(500 * time.Millisecond)
}
