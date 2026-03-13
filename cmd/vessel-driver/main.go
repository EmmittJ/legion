package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/EmmittJ/legion/internal/acp"
	"github.com/EmmittJ/legion/internal/telemetry"
)

// TraceWriter writes structured execution traces to Beads issue notes.
// Each trace is timestamped and appended to preserve history.
type TraceWriter struct {
	issueID string
}

// NewTraceWriter creates a trace writer for the given issue.
func NewTraceWriter(issueID string) *TraceWriter {
	return &TraceWriter{issueID: issueID}
}

// Write appends a formatted trace event to the Beads issue.
// Format: [TIMESTAMP] <component>: <message>
func (tw *TraceWriter) Write(component, message string) error {
	timestamp := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	trace := fmt.Sprintf("[%s] %s: %s", timestamp, component, message)
	return runCmd("", "bd", "update", tw.issueID, "--append-notes", trace)
}

// WriteJSON appends a structured JSON trace event to the Beads issue.
// Useful for capturing rich context (ACP messages, git output, etc).
func (tw *TraceWriter) WriteJSON(component string, data map[string]any) error {
	timestamp := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	data["timestamp"] = timestamp
	data["component"] = component
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	trace := string(raw)
	return runCmd("", "bd", "update", tw.issueID, "--append-notes", trace)
}

func main() {
	// Read required env vars first — these fatal before any spans exist,
	// which is acceptable: there is nothing meaningful to trace yet.
	issueID := requireEnv("ISSUE_ID")
	repoURL := requireEnv("REPO_URL")
	githubToken := requireEnv("GITHUB_TOKEN")
	model := os.Getenv("VESSEL_MODEL")

	// Configure git credential store so the token never appears in remote URLs
	// or log output.  Must happen before any git operation.
	if err := setupGitCredentials(githubToken); err != nil {
		slog.Error("git credential setup failed", "err", err)
		os.Exit(1)
	}

	// Initialize the local Beads Dolt database from the GitHub git remote.
	// This replaces the shared DOLT_DSN model: each vessel carries its own bd
	// instance seeded from origin.  Must happen before any `bd` command.
	if err := initBeads(repoURL); err != nil {
		slog.Error("beads init failed", "err", err)
		os.Exit(1)
	}

	ctx := context.Background()

	// Trace writer for appending execution events to Beads issue notes.
	tw := NewTraceWriter(issueID)

	// Initialize telemetry. Non-fatal: a noop tracer/meter is returned on
	// failure so the rest of the binary continues without distributed tracing.
	tracer, _, _, shutdown, err := telemetry.Setup(ctx, "legion.vessel-driver")
	if err != nil {
		slog.Error("telemetry setup failed", "err", err)
		// non-fatal — continue
	}
	// IMPORTANT: vessel-driver is short-lived. This defer is the only
	// mechanism that flushes buffered spans to Jaeger before exit.
	// os.Exit bypasses defers, so the die() helper below calls shutdown
	// explicitly on every fatal error path.
	defer func() { _ = shutdown(ctx) }()

	// Root span covering the entire vessel lifecycle.
	ctx, rootSpan := tracer.Start(ctx, "legion.vessel.run",
		trace.WithAttributes(
			attribute.String("issue.id", issueID),
			attribute.String("repo.url", repoURL),
		),
	)
	defer rootSpan.End()

	// die records the error on the root span, flushes telemetry, and exits 1.
	// It must be called instead of log.Fatalf/os.Exit on every fatal path
	// reached after this point, because os.Exit bypasses all defers.
	die := func(msg string, fatalErr error) {
		rootSpan.RecordError(fatalErr)
		rootSpan.SetStatus(codes.Error, msg)
		rootSpan.End()
		slog.ErrorContext(ctx, msg, "err", fatalErr)
		_ = shutdown(ctx)
		os.Exit(1)
	}

	// Step 2: Read issue from Beads.
	_, beadsReadSpan := tracer.Start(ctx, "legion.vessel.beads.read",
		trace.WithAttributes(attribute.String("issue.id", issueID)),
	)
	issue, err := bdShow(issueID)
	if err != nil {
		beadsReadSpan.RecordError(err)
		beadsReadSpan.SetStatus(codes.Error, err.Error())
		beadsReadSpan.End()
		die(fmt.Sprintf("bd show %s failed", issueID), err)
	}
	beadsReadSpan.End()

	// Step 3: Clone the repo.
	_, cloneSpan := tracer.Start(ctx, "legion.vessel.git.clone",
		trace.WithAttributes(attribute.String("repo.url", repoURL)),
	)
	if err := runCmd("", "git", "clone", repoURL, "/workspace"); err != nil {
		cloneSpan.RecordError(err)
		cloneSpan.SetStatus(codes.Error, err.Error())
		cloneSpan.End()
		_ = tw.Write("GIT", fmt.Sprintf("clone failed: %v", err))
		markFailed(issueID, "git clone failed")
		die("git clone failed", err)
	}
	cloneSpan.End()
	_ = tw.Write("GIT", fmt.Sprintf("cloned %s to /workspace", repoURL))

	// Step 4: Checkout branch.
	branch := "legion/" + issueID
	_, checkoutSpan := tracer.Start(ctx, "legion.vessel.git.checkout",
		trace.WithAttributes(attribute.String("git.branch", branch)),
	)
	if err := runCmd("/workspace", "git", "checkout", "-b", branch); err != nil {
		checkoutSpan.RecordError(err)
		checkoutSpan.SetStatus(codes.Error, err.Error())
		checkoutSpan.End()
		_ = tw.Write("GIT", fmt.Sprintf("checkout failed: %v", err))
		markFailed(issueID, "checkout failed")
		die("git checkout failed", err)
	}
	checkoutSpan.End()
	_ = tw.Write("GIT", fmt.Sprintf("checked out branch %s", branch))

	// Steps 5+6: Start ACP server and perform protocol handshake.
	_, acpInitSpan := tracer.Start(ctx, "legion.vessel.acp.initialize",
		trace.WithAttributes(attribute.String("model", model)),
	)
	client, err := acp.New(ctx, model)
	if err != nil {
		acpInitSpan.RecordError(err)
		acpInitSpan.SetStatus(codes.Error, err.Error())
		acpInitSpan.End()
		_ = tw.Write("ACP", fmt.Sprintf("start failed: %v", err))
		markFailed(issueID, "ACP start failed")
		die("acp.New failed", err)
	}
	defer client.Close()

	protocolVersion, err := client.Initialize()
	if err != nil {
		acpInitSpan.RecordError(err)
		acpInitSpan.SetStatus(codes.Error, err.Error())
		acpInitSpan.End()
		_ = tw.Write("ACP", fmt.Sprintf("initialize handshake failed: %v", err))
		markFailed(issueID, "ACP error")
		die("acp.Initialize failed", err)
	}
	acpInitSpan.End()
	slog.InfoContext(ctx, "ACP handshake OK", "protocol_version", protocolVersion)
	_ = tw.WriteJSON("ACP", map[string]any{
		"event": "initialize",
		"protocol_version": protocolVersion,
		"status": "ok",
	})

	// Step 7: New session.
	_, acpSessionSpan := tracer.Start(ctx, "legion.vessel.acp.session")
	sessionID, err := client.NewSession("/workspace")
	if err != nil {
		acpSessionSpan.RecordError(err)
		acpSessionSpan.SetStatus(codes.Error, err.Error())
		acpSessionSpan.End()
		_ = tw.Write("ACP", fmt.Sprintf("new session failed: %v", err))
		markFailed(issueID, "ACP error")
		die("acp.NewSession failed", err)
	}
	acpSessionSpan.End()
	slog.InfoContext(ctx, "ACP session ready", "session_id", sessionID)
	_ = tw.WriteJSON("ACP", map[string]any{
		"event": "session/new",
		"session_id": sessionID,
		"cwd": "/workspace",
		"status": "ready",
	})

	// Step 8: Prompt with issue content.
	promptContent := issue.Title + "\n\n" + issue.Description

	_, acpPromptSpan := tracer.Start(ctx, "legion.vessel.acp.prompt",
		trace.WithAttributes(attribute.String("model", model)),
	)

	// Write the prompt to Beads for visibility.
	_ = tw.WriteJSON("ACP", map[string]any{
		"event": "prompt/request",
		"user_message": promptContent,
		"session_id": sessionID,
	})

	onUpdate := func(update map[string]any) {
		raw, marshalErr := json.Marshal(update)
		if marshalErr != nil {
			return
		}
		note := string(raw)
		// Best-effort — don't fail the whole operation if a trace write fails.
		_ = runCmd("", "bd", "update", issueID, "--append-notes", note)

		// Also record as a span event for trace visibility in Jaeger.
		acpPromptSpan.AddEvent("acp.update", trace.WithAttributes(
			attribute.String("type", fmt.Sprintf("%v", update["type"])),
		))
	}

	// Determine prompt timeout — default 45 min, overrideable via VESSEL_TIMEOUT (seconds).
	timeoutSecs := 2700
	if v := os.Getenv("VESSEL_TIMEOUT"); v != "" {
		if n, parseErr := strconv.Atoi(v); parseErr == nil {
			timeoutSecs = n
		}
	}
	promptCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSecs)*time.Second)
	defer cancel()
	stopReason, err := client.Prompt(promptCtx, sessionID, promptContent, onUpdate)

	// Step 9/10: Handle prompt completion.
	if err != nil || stopReason == "error" {
		var promptErr error
		if err != nil {
			promptErr = err
			slog.ErrorContext(ctx, "prompt error", "err", err)
		} else {
			promptErr = fmt.Errorf("stop reason: %s", stopReason)
			slog.ErrorContext(ctx, "prompt stopped with error reason", "stop_reason", stopReason)
		}
		acpPromptSpan.RecordError(promptErr)
		acpPromptSpan.SetStatus(codes.Error, promptErr.Error())
		acpPromptSpan.End()
		_ = tw.WriteJSON("ACP", map[string]any{
			"event": "prompt/error",
			"error": promptErr.Error(),
			"stop_reason": stopReason,
		})
		markFailed(issueID, "ACP error")
		die("prompt failed", promptErr)
	}
	acpPromptSpan.SetStatus(codes.Ok, "")
	acpPromptSpan.End()
	slog.InfoContext(ctx, "prompt complete", "stop_reason", stopReason)
	_ = tw.WriteJSON("ACP", map[string]any{
		"event": "prompt/response",
		"stop_reason": stopReason,
		"status": "ok",
	})

	// Steps 9a–9c: git add + commit + push.
	_, pushSpan := tracer.Start(ctx, "legion.vessel.git.push",
		trace.WithAttributes(attribute.String("git.branch", branch)),
	)

	// Step 9a: git add -A.
	if err := runCmd("/workspace", "git", "add", "-A"); err != nil {
		pushSpan.RecordError(err)
		pushSpan.SetStatus(codes.Error, err.Error())
		pushSpan.End()
		_ = tw.Write("GIT", fmt.Sprintf("add failed: %v", err))
		markFailed(issueID, "git add failed")
		die("git add failed", err)
	}
	_ = tw.Write("GIT", "staged all changes")

	// Step 9b: git commit.
	commitMsg := fmt.Sprintf("feat(%s): %s", issueID, issue.Title)
	if err := runCmd("/workspace",
		"git",
		"-c", "user.email=vessel@legion",
		"-c", "user.name=Vessel",
		"commit", "-m", commitMsg,
	); err != nil {
		pushSpan.RecordError(err)
		pushSpan.SetStatus(codes.Error, err.Error())
		pushSpan.End()
		_ = tw.Write("GIT", fmt.Sprintf("commit failed: %v", err))
		markFailed(issueID, "git commit failed")
		die("git commit failed", err)
	}
	_ = tw.Write("GIT", fmt.Sprintf("committed: %s", commitMsg))

	// Step 9c: git push.
	// gh auth setup-git already wired the credential helper, so the clone URL
	// is used as-is — no token injection into the remote URL needed.
	if err := runCmd("/workspace", "git", "push", "origin", branch); err != nil {
		pushSpan.RecordError(err)
		pushSpan.SetStatus(codes.Error, err.Error())
		pushSpan.End()
		_ = tw.Write("GIT", fmt.Sprintf("push failed: %v", err))
		markFailed(issueID, "git push failed")
		die("git push failed", err)
	}
	pushSpan.End()
	_ = tw.Write("GIT", fmt.Sprintf("pushed branch %s to origin", branch))

	// Step 9d: close the issue.
	_, beadsCloseSpan := tracer.Start(ctx, "legion.vessel.beads.close",
		trace.WithAttributes(attribute.String("issue.id", issueID)),
	)
	
	// Write final success status
	_ = tw.WriteJSON("VESSEL", map[string]any{
		"event": "completion",
		"status": "success",
		"branch": branch,
		"stop_reason": stopReason,
		"message": "vessel-driver execution completed successfully",
	})
	
	if err := runCmd("", "bd", "close", issueID, "--reason", "completed"); err != nil {
		beadsCloseSpan.RecordError(err)
		beadsCloseSpan.SetStatus(codes.Error, err.Error())
		slog.WarnContext(ctx, "bd close failed — issue may need manual close", "issue_id", issueID, "err", err)
	}
	beadsCloseSpan.End()

	// Mark root span successful before deferred End() fires.
	rootSpan.SetStatus(codes.Ok, "")
	rootSpan.SetAttributes(
		attribute.String("git.branch", branch),
		attribute.String("stop_reason", stopReason),
	)

	slog.InfoContext(ctx, "vessel complete", "issue_id", issueID, "branch", branch)
	// rootSpan.End() and shutdown(ctx) called by defer — spans flushed to Jaeger.
}

// requireEnv returns the value of an env var or exits 1.
// Called before telemetry is initialised, so plain slog (no context) is used.
func requireEnv(name string) string {
	v := os.Getenv(name)
	if v == "" {
		slog.Error("required env var not set", "name", name)
		os.Exit(1)
	}
	return v
}

// issueCore holds the fields of a Beads issue nested inside the "issue" envelope.
type issueCore struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

// issueDetails is the envelope returned by `bd show <id> --json`.
// bdShow calls `bd show <id> --json` and parses the result.
// bd show returns a single-element flat array: [{"id":"...","title":"...",...}]
func bdShow(id string) (*issueCore, error) {
	out, err := execOutput("", "bd", "show", id, "--json")
	if err != nil {
		return nil, err
	}
	var items []issueCore
	if err := json.Unmarshal(out, &items); err != nil {
		return nil, fmt.Errorf("bd show: parse JSON: %w", err)
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("bd show: empty result")
	}
	return &items[0], nil
}

// markFailed marks an issue blocked with a "failed" label and appends a reason note.
// Beads uses "blocked" as the terminal-error status; the "failed" label distinguishes
// error-exits from genuine dependency blocks.
func markFailed(issueID, reason string) {
	if err := runCmd("", "bd", "update", issueID, "--status=blocked", "--add-label", "failed", "--append-notes="+reason); err != nil {
		slog.Warn("could not mark issue failed", "issue_id", issueID, "err", err)
	}
}

// runCmd runs a command in dir, logging stderr on failure.
func runCmd(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Stdout = os.Stdout
	stderr := &strings.Builder{}
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			return fmt.Errorf("%s %v: %w\nstderr: %s", name, args, err, stderr.String())
		}
		return fmt.Errorf("%s %v: %w", name, args, err)
	}
	return nil
}

// execOutput runs a command and captures stdout.
func execOutput(dir, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	stderr := &strings.Builder{}
	cmd.Stderr = stderr
	out, err := cmd.Output()
	if err != nil {
		if stderr.Len() > 0 {
			return nil, fmt.Errorf("%s %v: %w\nstderr: %s", name, args, err, stderr.String())
		}
		return nil, fmt.Errorf("%s %v: %w", name, args, err)
	}
	return out, nil
}

// setupGitCredentials configures git's credential helper via `gh auth setup-git`
// so that the token never appears in remote URLs or log output.
// GH_TOKEN is set in-process so the gh invocation picks it up automatically.
func setupGitCredentials(token string) error {
	if err := os.Setenv("GH_TOKEN", token); err != nil {
		return fmt.Errorf("set GH_TOKEN: %w", err)
	}
	cmd := exec.CommandContext(context.Background(), "gh", "auth", "setup-git")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("gh auth setup-git: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// initBeads bootstraps the local Beads Dolt database inside the vessel container.
// Each vessel runs its own bd instance; there is no shared Dolt SQL server.
//
// Steps:
//  1. If /app/.beads/metadata.json already exists the database is already
//     initialised (e.g. a cached layer) — skip silently.
//  2. bd init --quiet  — creates the local Dolt database.
//  3. bd dolt remote add origin <git+https remote>  — wires the GitHub remote.
//  4. bd dolt pull  — syncs current issues from GitHub (warn-only; a fresh init
//     with no upstream history is acceptable).
//
// The git+https remote URL is derived from repoURL by:
//   - Stripping a trailing ".git" suffix (if present), then re-appending it to
//     normalise the form.
//   - Replacing the "https://" scheme prefix with "git+https://".
//
// git auth is already set up by setupGitCredentials before this is called.
func initBeads(repoURL string) error {
	const metadataPath = "/app/.beads/metadata.json"
	if _, err := os.Stat(metadataPath); err == nil {
		slog.Info("beads already initialised — skipping init", "path", metadataPath)
		return nil
	}

	// Derive the git+https remote URL.
	// e.g. https://github.com/EmmittJ/legion.git  →  git+https://github.com/EmmittJ/legion.git
	remote := strings.TrimSuffix(repoURL, ".git") + ".git"
	remote = "git+" + remote // prepend git+ to whatever scheme is present
	// Guard: only rewrite https:// — if the URL already starts with git+https:// we'd
	// double-prefix it.  Strip the erroneous double-prefix if it happened.
	remote = strings.ReplaceAll(remote, "git+git+", "git+")

	// Step 2: bd init
	if err := runCmd("/app", "bd", "init", "--quiet"); err != nil {
		return fmt.Errorf("bd init: %w", err)
	}

	// Step 3: wire the remote
	if err := runCmd("/app", "bd", "dolt", "remote", "add", "origin", remote); err != nil {
		return fmt.Errorf("bd dolt remote add origin: %w", err)
	}

	// Step 4: pull current issues (warn-only — an empty upstream is fine)
	if err := runCmd("/app", "bd", "dolt", "pull"); err != nil {
		slog.Warn("bd dolt pull failed — continuing with empty local db", "err", err)
	}

	slog.Info("beads initialised", "remote", remote)
	return nil
}
