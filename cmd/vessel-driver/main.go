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
	return runCmd("/workspace", "bd", "update", tw.issueID, "--append-notes", trace)
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
	return runCmd("/workspace", "bd", "update", tw.issueID, "--append-notes", trace)
}

func main() {
	// Read required env vars first — these fatal before any spans exist,
	// which is acceptable: there is nothing meaningful to trace yet.
	issueID := requireEnv("ISSUE_ID")
	repoURL := requireEnv("REPO_URL")
	githubToken := requireEnv("GITHUB_TOKEN")
	doltHost := requireEnv("DOLT_HOST")
	doltPort := requireEnv("DOLT_PORT")
	model := os.Getenv("VESSEL_MODEL")

	// Configure git credential store so the token never appears in remote URLs
	// or log output.  Must happen before any git operation.
	if err := setupGitCredentials(githubToken); err != nil {
		slog.Error("git credential setup failed", "err", err)
		os.Exit(1)
	}

	// Clone the repo first so that /workspace/.beads/ exists with config.yaml
	// and metadata.json — bd needs these files to resolve project context.
	if err := runCmd("", "git", "clone", repoURL, "/workspace"); err != nil {
		slog.Error("git clone failed", "err", err)
		os.Exit(1)
	}

	// Point bd at the host's persistent Dolt SQL server instead of running a
	// local DB. BEADS_DIR must be set before any bd call so it finds the
	// committed config.yaml and metadata.json from the clone.
	for _, kv := range []struct{ key, val string }{
		{"BEADS_DIR", "/workspace/.beads"},
		{"BEADS_DOLT_SERVER_HOST", doltHost},
		{"BEADS_DOLT_SERVER_PORT", doltPort},
		{"BEADS_DOLT_SERVER_USER", "root"},
	} {
		if err := os.Setenv(kv.key, kv.val); err != nil {
			slog.Error("failed to set env var", "key", kv.key, "err", err)
			os.Exit(1)
		}
	}
	slog.Info("beads connected", "host", doltHost, "port", doltPort, "issue", issueID)

	// bd show requires .beads/dolt/ to exist as a local Dolt workspace directory.
	// This directory is gitignored and never present in the clone.  bd init
	// creates it; the internal bd dolt pull will warn "no common ancestor" —
	// that is non-fatal and expected (no Dolt data branch lives in git).
	if err := runCmd("/workspace", "bd", "init"); err != nil {
		slog.Warn("bd init — local workspace init may warn", "err", err)
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
	issue, err := bdShow("/workspace", issueID)
	if err != nil {
		beadsReadSpan.RecordError(err)
		beadsReadSpan.SetStatus(codes.Error, err.Error())
		beadsReadSpan.End()
		die(fmt.Sprintf("bd show %s failed", issueID), err)
	}
	beadsReadSpan.End()

	// Step 3: Checkout branch.
	branch := "legion/" + issueID
	_, checkoutSpan := tracer.Start(ctx, "legion.vessel.git.checkout",
		trace.WithAttributes(attribute.String("git.branch", branch)),
	)
	// Try to create the branch fresh; if it already exists locally or on the remote
	// (e.g. a prior vessel run), fall back to switching to the existing branch.
	checkoutErr := runCmd("/workspace", "git", "checkout", "-b", branch)
	if checkoutErr != nil {
		// Branch already exists — switch to it instead.
		if switchErr := runCmd("/workspace", "git", "checkout", branch); switchErr != nil {
			// Neither create nor switch worked; report the original create error.
			checkoutSpan.RecordError(checkoutErr)
			checkoutSpan.SetStatus(codes.Error, checkoutErr.Error())
			checkoutSpan.End()
			_ = tw.Write("GIT", fmt.Sprintf("checkout failed: %v", checkoutErr))
			markFailed(issueID, "checkout failed")
			die("git checkout failed", checkoutErr)
		}
		slog.InfoContext(ctx, "git branch already exists, switched to existing branch", "branch", branch)
	}
	checkoutSpan.End()
	_ = tw.Write("GIT", fmt.Sprintf("checked out branch %s", branch))

	// Steps 5+6: Start ACP server and perform protocol handshake.
	// IMPORTANT: copilot --acp --stdio authenticates via GH_TOKEN (set above by
	// setupGitCredentials).  The token MUST have the "copilot" OAuth scope.
	// A plain repo-scoped PAT or Actions GITHUB_TOKEN will cause session/prompt
	// to hang until the 300 s deadline fires with "context deadline exceeded".
	// Use a token from a Copilot-enabled GitHub account with the copilot scope.
	_, acpInitSpan := tracer.Start(ctx, "legion.vessel.acp.initialize",
		trace.WithAttributes(attribute.String("model", model)),
	)
	slog.InfoContext(ctx, "starting ACP session", "model", model)
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

	protocolVersion, capabilities, err := client.Initialize()
	if err != nil {
		acpInitSpan.RecordError(err)
		acpInitSpan.SetStatus(codes.Error, err.Error())
		acpInitSpan.End()
		_ = tw.Write("ACP", fmt.Sprintf("initialize handshake failed: %v", err))
		markFailed(issueID, "ACP error")
		die("acp.Initialize failed", err)
	}
	acpInitSpan.End()
	slog.InfoContext(ctx, "ACP handshake OK", "protocol_version", protocolVersion, "capabilities_count", len(capabilities))
	_ = tw.WriteJSON("ACP", map[string]any{
		"event":            "initialize",
		"protocol_version": protocolVersion,
		"status":           "ok",
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
		"event":      "session/new",
		"session_id": sessionID,
		"cwd":        "/workspace",
		"status":     "ready",
	})

	// Step 8: Prompt with issue content.
	promptContent := issue.Title + "\n\n" + issue.Description

	_, acpPromptSpan := tracer.Start(ctx, "legion.vessel.acp.prompt",
		trace.WithAttributes(attribute.String("model", model)),
	)

	// Write the prompt to Beads for visibility.
	_ = tw.WriteJSON("ACP", map[string]any{
		"event":        "prompt/request",
		"user_message": promptContent,
		"session_id":   sessionID,
	})

	onUpdate := func(update map[string]any) {
		// Record as a span event for trace visibility in Grafana/Tempo.
		// Don't write every token to Beads — too chatty; final result is written at completion.
		acpPromptSpan.AddEvent("acp.update", trace.WithAttributes(
			attribute.String("type", fmt.Sprintf("%v", update["type"])),
		))
		// Log token streaming so we can distinguish "Copilot is working" from "token hung".
		slog.InfoContext(ctx, "acp update", "update_type", fmt.Sprintf("%v", update["type"]))
	}

	// Determine prompt timeout — default 5 min, overrideable via VESSEL_TIMEOUT (seconds).
	timeoutSecs := 300
	if v := os.Getenv("VESSEL_TIMEOUT"); v != "" {
		if n, parseErr := strconv.Atoi(v); parseErr == nil {
			timeoutSecs = n
		}
	}
	promptCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSecs)*time.Second)
	defer cancel()
	stopReason, err := client.Prompt(promptCtx, sessionID, promptContent, onUpdate)

	// Step 9/10: Handle prompt completion.
	if err != nil || stopReason != "end_turn" {
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
			"event":       "prompt/error",
			"error":       promptErr.Error(),
			"stop_reason": stopReason,
		})
		markFailed(issueID, "ACP error")
		die("prompt failed", promptErr)
	}
	acpPromptSpan.SetStatus(codes.Ok, "")
	acpPromptSpan.End()
	slog.InfoContext(ctx, "prompt complete", "stop_reason", stopReason)
	_ = tw.WriteJSON("ACP", map[string]any{
		"event":       "prompt/response",
		"stop_reason": stopReason,
		"status":      "ok",
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
		"event":       "completion",
		"status":      "success",
		"branch":      branch,
		"stop_reason": stopReason,
		"message":     "vessel-driver execution completed successfully",
	})

	if err := runCmd("/workspace", "bd", "close", issueID, "--reason", "completed"); err != nil {
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

// bdShow calls `bd show <id> --json` from dir and parses the result.
// bd show returns a single-element flat array: [{"id":"...","title":"...",...}]
func bdShow(dir, id string) (*issueCore, error) {
	out, err := execOutput(dir, "bd", "show", id, "--json")
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
	if err := runCmd("/workspace", "bd", "update", issueID, "--status=blocked", "--add-label", "failed", "--append-notes="+reason); err != nil {
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
