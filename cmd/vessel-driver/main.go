package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
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

func main() {
	// Read required env vars first — these fatal before any spans exist,
	// which is acceptable: there is nothing meaningful to trace yet.
	issueID := requireEnv("ISSUE_ID")
	repoURL := requireEnv("REPO_URL")
	githubToken := requireEnv("GITHUB_TOKEN")
	_ = requireEnv("DOLT_DSN") // validated at startup; used implicitly by bd CLI
	model := os.Getenv("VESSEL_MODEL")

	ctx := context.Background()

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
		markFailed(issueID, "git clone failed")
		die("git clone failed", err)
	}
	cloneSpan.End()

	// Step 4: Checkout branch.
	branch := "legion/" + issueID
	_, checkoutSpan := tracer.Start(ctx, "legion.vessel.git.checkout",
		trace.WithAttributes(attribute.String("git.branch", branch)),
	)
	if err := runCmd("/workspace", "git", "checkout", "-b", branch); err != nil {
		checkoutSpan.RecordError(err)
		checkoutSpan.SetStatus(codes.Error, err.Error())
		checkoutSpan.End()
		markFailed(issueID, "checkout failed")
		die("git checkout failed", err)
	}
	checkoutSpan.End()

	// Steps 5+6: Start ACP server and perform protocol handshake.
	_, acpInitSpan := tracer.Start(ctx, "legion.vessel.acp.initialize",
		trace.WithAttributes(attribute.String("model", model)),
	)
	client, err := acp.New(ctx, model)
	if err != nil {
		acpInitSpan.RecordError(err)
		acpInitSpan.SetStatus(codes.Error, err.Error())
		acpInitSpan.End()
		markFailed(issueID, "ACP start failed")
		die("acp.New failed", err)
	}
	defer client.Close()

	protocolVersion, err := client.Initialize()
	if err != nil {
		acpInitSpan.RecordError(err)
		acpInitSpan.SetStatus(codes.Error, err.Error())
		acpInitSpan.End()
		markFailed(issueID, "ACP error")
		die("acp.Initialize failed", err)
	}
	acpInitSpan.End()
	slog.InfoContext(ctx, "ACP handshake OK", "protocol_version", protocolVersion)

	// Step 7: New session.
	_, acpSessionSpan := tracer.Start(ctx, "legion.vessel.acp.session")
	sessionID, err := client.NewSession("/workspace")
	if err != nil {
		acpSessionSpan.RecordError(err)
		acpSessionSpan.SetStatus(codes.Error, err.Error())
		acpSessionSpan.End()
		markFailed(issueID, "ACP error")
		die("acp.NewSession failed", err)
	}
	acpSessionSpan.End()
	slog.InfoContext(ctx, "ACP session ready", "session_id", sessionID)

	// Step 8: Prompt with issue content.
	promptContent := issue.Title + "\n\n" + issue.Description

	_, acpPromptSpan := tracer.Start(ctx, "legion.vessel.acp.prompt",
		trace.WithAttributes(attribute.String("model", model)),
	)

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
		markFailed(issueID, "ACP error")
		die("prompt failed", promptErr)
	}
	acpPromptSpan.SetStatus(codes.Ok, "")
	acpPromptSpan.End()
	slog.InfoContext(ctx, "prompt complete", "stop_reason", stopReason)

	// Steps 9a–9c: git add + commit + push.
	_, pushSpan := tracer.Start(ctx, "legion.vessel.git.push",
		trace.WithAttributes(attribute.String("git.branch", branch)),
	)

	// Step 9a: git add -A.
	if err := runCmd("/workspace", "git", "add", "-A"); err != nil {
		pushSpan.RecordError(err)
		pushSpan.SetStatus(codes.Error, err.Error())
		pushSpan.End()
		markFailed(issueID, "git add failed")
		die("git add failed", err)
	}

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
		markFailed(issueID, "git commit failed")
		die("git commit failed", err)
	}

	// Step 9c: git push.
	// Inject token into the origin remote URL so the push authenticates without
	// exposing the credential as a command-line argument (visible in `ps aux`).
	pushURL, err := buildPushURL(repoURL, githubToken)
	if err != nil {
		pushSpan.RecordError(err)
		pushSpan.SetStatus(codes.Error, err.Error())
		pushSpan.End()
		markFailed(issueID, "git push failed")
		die("build push URL failed", err)
	}
	if err := runCmd("/workspace", "git", "remote", "set-url", "origin", pushURL); err != nil {
		pushSpan.RecordError(err)
		pushSpan.SetStatus(codes.Error, err.Error())
		pushSpan.End()
		markFailed(issueID, "git push failed")
		die("git remote set-url failed", err)
	}
	if err := runCmd("/workspace", "git", "push", "origin", branch); err != nil {
		pushSpan.RecordError(err)
		pushSpan.SetStatus(codes.Error, err.Error())
		pushSpan.End()
		markFailed(issueID, "git push failed")
		die("git push failed", err)
	}
	pushSpan.End()

	// Step 9d: close the issue.
	_, beadsCloseSpan := tracer.Start(ctx, "legion.vessel.beads.close",
		trace.WithAttributes(attribute.String("issue.id", issueID)),
	)
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
type issueDetails struct {
	Issue issueCore `json:"issue"`
}

// bdShow calls `bd show <id> --json` and parses the result.
func bdShow(id string) (*issueCore, error) {
	out, err := execOutput("", "bd", "show", id, "--json")
	if err != nil {
		return nil, err
	}
	var env issueDetails
	if err := json.Unmarshal(out, &env); err != nil {
		return nil, fmt.Errorf("bd show: parse JSON: %w", err)
	}
	return &env.Issue, nil
}

// markFailed marks an issue as blocked with a reason. Errors are logged but not fatal.
func markFailed(issueID, reason string) {
	if err := runCmd("", "bd", "update", issueID, "--status=blocked", "--append-notes="+reason); err != nil {
		slog.Warn("could not mark issue blocked", "issue_id", issueID, "err", err)
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

// buildPushURL injects the GitHub token into the repo URL.
// Handles both https://github.com/owner/repo and git@github.com:owner/repo formats.
func buildPushURL(repoURL, token string) (string, error) {
	// Convert SSH format to HTTPS.
	httpsURL := repoURL
	if strings.HasPrefix(repoURL, "git@github.com:") {
		path := strings.TrimPrefix(repoURL, "git@github.com:")
		httpsURL = "https://github.com/" + path
	}

	// Ensure .git suffix for consistency.
	if !strings.HasSuffix(httpsURL, ".git") {
		httpsURL += ".git"
	}

	u, err := url.Parse(httpsURL)
	if err != nil {
		return "", fmt.Errorf("parse repo URL %q: %w", httpsURL, err)
	}
	if u.Host == "" {
		return "", fmt.Errorf("repo URL %q has no host", httpsURL)
	}

	u.User = url.UserPassword("x-access-token", token)
	return u.String(), nil
}
