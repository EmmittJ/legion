package main

// File: cmd/vessel-driver/dispatch.go
//
// Implements the vessel lifecycle hook dispatch loop.
//
// Discovers and invokes shell hook scripts across four hook tiers for each
// lifecycle event:
//
//	Tier 1: /hooks/common/<event>/
//	Tier 2: /hooks/<role>/<event>/
//	Tier 3: /workspace/.legion/hooks/common/<event>/   (skipped if absent)
//	Tier 4: /workspace/.legion/hooks/<role>/<event>/   (skipped if absent)
//
// Lifecycle order (events run in sequence):
//
//	pre-clone   → fatal on error
//	post-clone  → fatal on error
//	pre-acp     → fatal on error
//	acp-session → fatal on error; built-in ACP client used when no hooks found
//	post-acp    → NON-fatal; log warning and continue
//	pre-commit  → fatal on error
//	post-commit → fatal on error
//	on-error    → always ignored; run on any fatal failure
//
// writeResult is defined in main.go (same package) and is called by fatalFail.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/EmmittJ/legion/internal/config"
)

// Lifecycle event names. Using plain string constants (no distinct type) keeps
// them easy to use in log messages and path construction.
const (
	eventPreClone   = "pre-clone"
	eventPostClone  = "post-clone"
	eventPreACP     = "pre-acp"
	eventACPSession = "acp-session"
	eventPostACP    = "post-acp"
	eventPreCommit  = "pre-commit"
	eventPostCommit = "post-commit"
	eventOnError    = "on-error"
)

// acpResultJSON is the schema vessel-driver expects from /workspace/result.json
// after an acp-session (whether custom hook or built-in).
// Custom hooks MUST write this shape; the built-in writes it via writeResult /
// writeHermesResult (both emit a "status" field).
type acpResultJSON struct {
	Status       string `json:"status"`
	IssueID      string `json:"issue_id"`
	ErrorMessage string `json:"error_message"`
}

// ─── Hook discovery ──────────────────────────────────────────────────────────

// DiscoverHooks returns all executable *.sh files for the given lifecycle event
// and role, collected across the four hook tiers in tier order, alphabetically
// within each tier. Missing or unreadable directories are silently skipped.
//
// Exported for unit tests.
func DiscoverHooks(event, role string) []string {
	type tierSpec struct {
		dir       string
		mustExist bool // workspace tiers are silently skipped when absent
	}
	tiers := []tierSpec{
		{dir: "/hooks/common/" + event},
		{dir: "/hooks/" + role + "/" + event},
		{dir: "/workspace/.legion/hooks/common/" + event, mustExist: true},
		{dir: "/workspace/.legion/hooks/" + role + "/" + event, mustExist: true},
	}

	var all []string
	for _, t := range tiers {
		if t.mustExist {
			if _, err := os.Stat(t.dir); os.IsNotExist(err) {
				continue
			}
		}
		all = append(all, hooksInDir(t.dir)...)
	}
	return all
}

// hooksInDir lists executable *.sh files in dir, sorted alphabetically.
// Missing or unreadable directories are silently skipped (returns nil).
func hooksInDir(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		// Directory absent or unreadable — silent no-op per spec.
		return nil
	}
	var hooks []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sh") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		// Check any executable bit (owner, group, or other execute).
		// Non-executable .sh files are skipped silently.
		if info.Mode()&0o111 == 0 {
			slog.Debug("dispatch: skipping non-executable hook",
				"path", filepath.Join(dir, e.Name()))
			continue
		}
		hooks = append(hooks, filepath.Join(dir, e.Name()))
	}
	sort.Strings(hooks)
	return hooks
}

// ─── Environment helpers ─────────────────────────────────────────────────────

// setEnv returns a copy of env with key overridden to value, removing any
// pre-existing entries for that key to avoid ambiguity.
func setEnv(env []string, key, value string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env)+1)
	for _, e := range env {
		if !strings.HasPrefix(e, prefix) {
			out = append(out, e)
		}
	}
	return append(out, key+"="+value)
}

// baseHookEnv constructs the base environment exported to every hook subprocess.
// It inherits the full process environment and then explicitly sets/overrides
// the vars the spec requires, ensuring hooks always see canonical values.
func baseHookEnv(vc *config.VesselConfig, branch string) []string {
	env := os.Environ()
	for _, kv := range []struct{ k, v string }{
		{"LEGION_ROLE", vc.RoleName},
		{"ISSUE_ID", vc.IssueID},
		{"REPO_URL", vc.RepoURL},
		{"BRANCH", branch},
		// Pass-through: use the inherited values, but set explicitly so
		// subprocesses always see them even if the parent env is sparse.
		{"LEGION_CONFIG_JSON", os.Getenv("LEGION_CONFIG_JSON")},
		{"GITHUB_TOKEN", os.Getenv("GITHUB_TOKEN")},
		{"BEADS_DIR", "/workspace/.beads"},
		{"BEADS_DOLT_SERVER_HOST", os.Getenv("BEADS_DOLT_SERVER_HOST")},
		{"BEADS_DOLT_SERVER_PORT", os.Getenv("BEADS_DOLT_SERVER_PORT")},
		{"BEADS_DOLT_SERVER_USER", "root"},
	} {
		env = setEnv(env, kv.k, kv.v)
	}
	return env
}

// enrichedEnv extends base env with STATUS and ERROR_MSG for post-acp stages.
func enrichedEnv(base []string, status, errorMsg string) []string {
	e := setEnv(base, "STATUS", status)
	return setEnv(e, "ERROR_MSG", errorMsg)
}

// ─── Hook execution ──────────────────────────────────────────────────────────

// runHook executes a single hook script as a subprocess with the provided env.
// stdout and stderr are forwarded to the container's stdout/stderr.
func runHook(ctx context.Context, path string, env []string) error {
	cmd := exec.CommandContext(ctx, path)
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	slog.InfoContext(ctx, "dispatch: running hook", "path", path)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("hook %s: %w", filepath.Base(path), err)
	}
	return nil
}

// runStage runs each hook in the list in order, returning on the first error.
func runStage(ctx context.Context, hooks []string, env []string) error {
	for _, h := range hooks {
		if err := runHook(ctx, h, env); err != nil {
			return err
		}
	}
	return nil
}

// runOnError discovers and runs all on-error hooks, ignoring every return value.
func runOnError(ctx context.Context, role string, env []string) {
	for _, h := range DiscoverHooks(eventOnError, role) {
		if err := runHook(ctx, h, env); err != nil {
			slog.WarnContext(ctx, "dispatch: on-error hook failed (ignored)",
				"hook", h, "err", err)
		}
	}
}

// ─── result.json reader ──────────────────────────────────────────────────────

// readACPResult reads /workspace/result.json and returns the status and any
// error message. If the file is absent, unreadable, or missing the status field,
// it returns ("error", <descriptive message>) so the caller can treat it as a
// fatal acp-session failure per spec.
func readACPResult() (status, errorMsg string) {
	data, err := os.ReadFile("/workspace/result.json")
	if err != nil {
		return "error", "acp-session did not produce result.json"
	}
	var r acpResultJSON
	if jsonErr := json.Unmarshal(data, &r); jsonErr != nil {
		return "error", "acp-session result.json unparseable: " + jsonErr.Error()
	}
	if r.Status == "" {
		return "error", "acp-session result.json missing status field"
	}
	return r.Status, r.ErrorMessage
}

// ─── Branch resolution ───────────────────────────────────────────────────────

// vesselBranch returns the branch name for a given VesselConfig:
//   - worker                      → "vessel/<issueID>"
//   - reviewer/hierophant/inquisitor → vc.ReviewBranch (may be empty)
//   - hermes                      → "" (no branch; hermes does not clone)
//   - anything else               → "vessel/<issueID>" (safe default)
func vesselBranch(vc *config.VesselConfig) string {
	switch vc.RoleName {
	case "worker":
		return "vessel/" + vc.IssueID
	case "reviewer", "hierophant", "inquisitor":
		return vc.ReviewBranch
	default:
		// hermes and unknown roles → no branch.
		return ""
	}
}

// ─── Main dispatch loop ──────────────────────────────────────────────────────

// RunDispatch executes the full vessel lifecycle in event order.
//
// acpBuiltIn is invoked as the acp-session implementation when no hook files
// are found for that event. It MUST write /workspace/result.json on success.
//
// Returns 0 on success, 1 on any fatal error.
func RunDispatch(ctx context.Context, vc *config.VesselConfig, acpBuiltIn func(context.Context) error) int {
	role := vc.RoleName
	branch := vesselBranch(vc)
	base := baseHookEnv(vc, branch)

	// fatalFail: run on-error hooks, write result.json with STATUS=error, return 1.
	// Caller must return this value immediately after calling fatalFail.
	fatalFail := func(msg string) int {
		slog.ErrorContext(ctx, "dispatch: fatal lifecycle error", "msg", msg)
		runOnError(ctx, role, base)
		writeResult(vesselResult{
			IssueID:      vc.IssueID,
			Status:       "error",
			ErrorMessage: msg,
		})
		return 1
	}

	// ── pre-clone ─────────────────────────────────────────────────────────────
	slog.InfoContext(ctx, "dispatch: stage pre-clone")
	if err := runStage(ctx, DiscoverHooks(eventPreClone, role), base); err != nil {
		return fatalFail("pre-clone: " + err.Error())
	}

	// ── post-clone ────────────────────────────────────────────────────────────
	slog.InfoContext(ctx, "dispatch: stage post-clone")
	if err := runStage(ctx, DiscoverHooks(eventPostClone, role), base); err != nil {
		return fatalFail("post-clone: " + err.Error())
	}

	// ── pre-acp ───────────────────────────────────────────────────────────────
	slog.InfoContext(ctx, "dispatch: stage pre-acp")
	if err := runStage(ctx, DiscoverHooks(eventPreACP, role), base); err != nil {
		return fatalFail("pre-acp: " + err.Error())
	}

	// ── acp-session ───────────────────────────────────────────────────────────
	slog.InfoContext(ctx, "dispatch: stage acp-session")
	acpHooks := DiscoverHooks(eventACPSession, role)

	var acpStatus, acpErrMsg string

	if len(acpHooks) == 0 {
		// No custom hooks found → run the built-in ACP client.
		slog.InfoContext(ctx, "dispatch: acp-session — no hooks found, using built-in ACP client")
		if err := acpBuiltIn(ctx); err != nil {
			return fatalFail("acp-session built-in: " + err.Error())
		}
		// Built-in MUST have written /workspace/result.json.
		acpStatus, acpErrMsg = readACPResult()
	} else {
		// Custom hooks found → run them; they are responsible for writing result.json.
		slog.InfoContext(ctx, "dispatch: acp-session — custom hooks found",
			"count", len(acpHooks))
		if err := runStage(ctx, acpHooks, base); err != nil {
			return fatalFail("acp-session: " + err.Error())
		}
		acpStatus, acpErrMsg = readACPResult()
	}

	// acp-session STATUS=error → run on-error hooks and exit.
	if acpStatus == "error" {
		slog.ErrorContext(ctx, "dispatch: acp-session reported error",
			"error_msg", acpErrMsg)
		runOnError(ctx, role, enrichedEnv(base, acpStatus, acpErrMsg))
		// result.json already written (by built-in or by custom hook); just exit.
		return 1
	}

	// Enrich the environment with STATUS/ERROR_MSG for post-acp stages.
	postBase := enrichedEnv(base, acpStatus, acpErrMsg)

	// ── post-acp (NON-fatal) ──────────────────────────────────────────────────
	slog.InfoContext(ctx, "dispatch: stage post-acp")
	for _, h := range DiscoverHooks(eventPostACP, role) {
		if err := runHook(ctx, h, postBase); err != nil {
			// Non-fatal: log warning and continue.
			slog.WarnContext(ctx, "dispatch: post-acp hook failed (non-fatal — continuing)",
				"hook", h, "err", err)
		}
	}

	// ── pre-commit ────────────────────────────────────────────────────────────
	slog.InfoContext(ctx, "dispatch: stage pre-commit")
	if err := runStage(ctx, DiscoverHooks(eventPreCommit, role), postBase); err != nil {
		return fatalFail("pre-commit: " + err.Error())
	}

	// ── post-commit ───────────────────────────────────────────────────────────
	slog.InfoContext(ctx, "dispatch: stage post-commit")
	if err := runStage(ctx, DiscoverHooks(eventPostCommit, role), postBase); err != nil {
		return fatalFail("post-commit: " + err.Error())
	}

	return 0
}
