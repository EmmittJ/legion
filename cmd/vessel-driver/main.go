package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	acp "github.com/ironpark/go-acp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/EmmittJ/legion/internal/config"
	"github.com/EmmittJ/legion/internal/telemetry"
)

// ---------------------------------------------------------------------------
// vesselClient — implements acp.Client so Copilot can read/write files and
// run shell commands inside the vessel container.
// ---------------------------------------------------------------------------

// terminalSession holds a running child process and buffers its combined output.
type terminalSession struct {
	cmd      *exec.Cmd
	mu       sync.Mutex
	outBuf   bytes.Buffer
	done     chan struct{}
	exitCode *int64
	signal   string
}

// Write implements io.Writer so terminalSession can be set as cmd.Stdout/Stderr.
func (s *terminalSession) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.outBuf.Write(p)
}

// output returns the current buffered combined stdout+stderr.
func (s *terminalSession) output() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.outBuf.String()
}

// vesselClient implements acp.Client.  It runs inside a Docker container so
// all file paths are relative to /workspace and all permissions are
// auto-approved — there is no interactive user to ask.
type vesselClient struct {
	workspace string
	mu        sync.Mutex
	terminals map[string]*terminalSession
}

// SessionUpdate is called for every streaming update Copilot sends while
// processing a prompt.  We log interesting events at info level and ignore
// the rest so container stdout stays readable.
func (c *vesselClient) SessionUpdate(ctx context.Context, params *acp.SessionNotification) error {
	acp.MatchSessionUpdate(&params.Update, acp.SessionUpdateMatcher[struct{}]{
		AgentMessageChunk: func(v acp.SessionUpdateAgentMessageChunk) struct{} {
			if text, ok := v.Content.AsText(); ok && text.Text != "" {
				slog.DebugContext(ctx, "copilot output", "text", text.Text)
			}
			return struct{}{}
		},
		ToolCall: func(v acp.SessionUpdateToolCall) struct{} {
			status := ""
			if v.Status != nil {
				status = string(*v.Status)
			}
			slog.InfoContext(ctx, "copilot tool call", "title", v.Title, "status", status)
			return struct{}{}
		},
		ToolCallUpdate: func(v acp.SessionUpdateToolCallUpdate) struct{} {
			status := ""
			if v.Status != nil {
				status = string(*v.Status)
			}
			slog.InfoContext(ctx, "copilot tool update", "id", string(v.ToolCallID), "status", status)
			return struct{}{}
		},
		Default: func() struct{} { return struct{}{} },
	})
	return nil
}

// RequestPermission auto-approves every tool call.  The vessel is a
// short-lived, sandboxed Docker container — there is no user to prompt and
// every action is intentional.
func (c *vesselClient) RequestPermission(ctx context.Context, params *acp.RequestPermissionRequest) (*acp.RequestPermissionResponse, error) {
	slog.InfoContext(ctx, "copilot permission request (auto-approve)", "title", params.ToolCall.Title)
	if len(params.Options) == 0 {
		return &acp.RequestPermissionResponse{}, nil
	}
	// Select the first option (typically "allow once").
	return &acp.RequestPermissionResponse{
		Outcome: acp.NewRequestPermissionOutcomeSelected(params.Options[0].OptionID),
	}, nil
}

// ReadTextFile reads a workspace-relative path and returns its contents.
func (c *vesselClient) ReadTextFile(ctx context.Context, params *acp.ReadTextFileRequest) (*acp.ReadTextFileResponse, error) {
	path := filepath.Join(c.workspace, params.Path)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("ReadTextFile %s: %w", params.Path, err)
	}
	slog.InfoContext(ctx, "copilot read file", "path", params.Path, "bytes", len(data))
	return &acp.ReadTextFileResponse{Content: string(data)}, nil
}

// WriteTextFile writes content to a workspace-relative path, creating
// intermediate directories as needed.
func (c *vesselClient) WriteTextFile(ctx context.Context, params *acp.WriteTextFileRequest) (*acp.WriteTextFileResponse, error) {
	path := filepath.Join(c.workspace, params.Path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("WriteTextFile mkdir %s: %w", params.Path, err)
	}
	if err := os.WriteFile(path, []byte(params.Content), 0o644); err != nil {
		return nil, fmt.Errorf("WriteTextFile %s: %w", params.Path, err)
	}
	slog.InfoContext(ctx, "copilot wrote file", "path", params.Path, "bytes", len(params.Content))
	return &acp.WriteTextFileResponse{}, nil
}

// CreateTerminal spawns the requested command and begins buffering its output.
// The command runs as a background goroutine; TerminalOutput polls the buffer
// and WaitForTerminalExit blocks until the process exits.
func (c *vesselClient) CreateTerminal(ctx context.Context, params *acp.CreateTerminalRequest) (*acp.CreateTerminalResponse, error) {
	id := fmt.Sprintf("term-%d", time.Now().UnixNano())

	cmd := exec.CommandContext(ctx, params.Command, params.Args...)

	// Working directory: use the param if set, else fall back to workspace.
	cwd := params.Cwd
	if cwd == "" {
		cwd = c.workspace
	}
	cmd.Dir = cwd

	// Inherit the container environment and layer any caller-supplied vars.
	cmd.Env = os.Environ()
	for _, e := range params.Env {
		cmd.Env = append(cmd.Env, e.Name+"="+e.Value)
	}

	sess := &terminalSession{
		cmd:  cmd,
		done: make(chan struct{}),
	}
	// Both stdout and stderr feed the same buffer so Copilot sees interleaved output.
	cmd.Stdout = sess
	cmd.Stderr = sess

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("CreateTerminal %q: %w", params.Command, err)
	}
	slog.InfoContext(ctx, "copilot terminal created", "id", id, "command", params.Command, "args", params.Args, "cwd", cwd)

	go func() {
		waitErr := cmd.Wait()
		var code int64
		if waitErr != nil {
			var exitErr *exec.ExitError
			if errors.As(waitErr, &exitErr) {
				code = int64(exitErr.ExitCode())
				sess.signal = exitErr.ProcessState.String()
			} else {
				code = -1
			}
		}
		sess.mu.Lock()
		sess.exitCode = &code
		sess.mu.Unlock()
		close(sess.done)
		slog.InfoContext(ctx, "copilot terminal exited", "id", id, "exit_code", code)
	}()

	c.mu.Lock()
	c.terminals[id] = sess
	c.mu.Unlock()

	return &acp.CreateTerminalResponse{TerminalID: id}, nil
}

// TerminalOutput returns the current buffered output of a terminal.
// The terminal may still be running; Copilot polls this while waiting.
func (c *vesselClient) TerminalOutput(ctx context.Context, params *acp.TerminalOutputRequest) (*acp.TerminalOutputResponse, error) {
	c.mu.Lock()
	sess, ok := c.terminals[params.TerminalID]
	c.mu.Unlock()
	if !ok {
		return &acp.TerminalOutputResponse{Output: ""}, nil
	}
	return &acp.TerminalOutputResponse{Output: sess.output()}, nil
}

// WaitForTerminalExit blocks until the terminal's process exits and returns
// its exit code.
func (c *vesselClient) WaitForTerminalExit(ctx context.Context, params *acp.WaitForTerminalExitRequest) (*acp.WaitForTerminalExitResponse, error) {
	c.mu.Lock()
	sess, ok := c.terminals[params.TerminalID]
	c.mu.Unlock()
	if !ok {
		// Terminal already released — treat as clean exit.
		zero := int64(0)
		return &acp.WaitForTerminalExitResponse{ExitCode: &zero}, nil
	}
	select {
	case <-sess.done:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	resp := &acp.WaitForTerminalExitResponse{}
	sess.mu.Lock()
	resp.ExitCode = sess.exitCode
	resp.Signal = sess.signal
	sess.mu.Unlock()
	return resp, nil
}

// ReleaseTerminal kills the process (if still running) and removes the session.
func (c *vesselClient) ReleaseTerminal(ctx context.Context, params *acp.ReleaseTerminalRequest) (*acp.ReleaseTerminalResponse, error) {
	c.mu.Lock()
	sess, ok := c.terminals[params.TerminalID]
	if ok {
		delete(c.terminals, params.TerminalID)
	}
	c.mu.Unlock()

	if ok && sess.cmd.Process != nil {
		select {
		case <-sess.done:
			// already exited — nothing to kill
		default:
			_ = sess.cmd.Process.Kill()
		}
	}
	return &acp.ReleaseTerminalResponse{}, nil
}

// KillTerminalCommand kills the process but leaves the session entry intact
// so Copilot can still call TerminalOutput/WaitForTerminalExit.
func (c *vesselClient) KillTerminalCommand(ctx context.Context, params *acp.KillTerminalRequest) (*acp.KillTerminalResponse, error) {
	c.mu.Lock()
	sess, ok := c.terminals[params.TerminalID]
	c.mu.Unlock()

	if ok && sess.cmd.Process != nil {
		select {
		case <-sess.done:
			// already exited
		default:
			_ = sess.cmd.Process.Kill()
		}
	}
	return &acp.KillTerminalResponse{}, nil
}

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
	// Load structured config from LEGION_CONFIG_JSON (or LEGION_CONFIG_FILE for tests).
	// Secrets (GITHUB_TOKEN, DOLT_HOST/PORT, OTEL_*) remain as individual env vars.
	vc, err := config.Load()
	if err != nil {
		slog.Error("config load failed", "err", err)
		os.Exit(1)
	}
	issueID := vc.IssueID
	repoURL := vc.RepoURL
	agentName := vc.AgentName

	// Secrets — stay as individual env vars, not in LEGION_CONFIG_JSON.
	githubToken := requireEnv("GITHUB_TOKEN")
	doltHost := requireEnv("DOLT_HOST")
	doltPort := requireEnv("DOLT_PORT")

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

	// Agent identity check: if LEGION_AGENT is set the agent file must exist
	// inside the cloned repo before we spend time starting Copilot.
	if agentName != "" {
		agentFile := "/workspace/.github/agents/" + agentName + ".agent.md"
		if _, statErr := os.Stat(agentFile); statErr != nil {
			reason := "agent file not found: " + agentName
			if !os.IsNotExist(statErr) {
				reason = "agent file unreadable: " + statErr.Error()
			}
			markFailed(issueID, reason)
			die("agent file check failed", statErr)
		}
	}

	// Steps 5+6: Start ACP server and perform protocol handshake.
	// IMPORTANT: copilot --acp --stdio authenticates via GH_TOKEN (set above by
	// setupGitCredentials).  The token MUST have the "copilot" OAuth scope.
	// A plain repo-scoped PAT or Actions GITHUB_TOKEN will cause session/prompt
	// to hang until the 300 s deadline fires with "context deadline exceeded".
	// Use a token from a Copilot-enabled GitHub account with the copilot scope.
	_, acpInitSpan := tracer.Start(ctx, "legion.vessel.acp.initialize",
		trace.WithAttributes(attribute.String("model", vc.Model)),
	)
	slog.InfoContext(ctx, "starting ACP session", "model", vc.Model, "acp_command", vc.ACPCommand)

	// Split ACP command — already validated non-empty and newline-free by config.Load().
	acpParts := strings.Fields(vc.ACPCommand)
	if len(acpParts) == 0 {
		die("acp_command is empty after splitting", errors.New("empty ACP command"))
	}

	// Spawn the ACP server with stderr forwarded to our container stderr for
	// debugging.  We use NewClientSideConnection directly (rather than
	// SpawnAgent) so we can control the cmd before it starts.
	acpCmd := exec.CommandContext(ctx, acpParts[0], acpParts[1:]...)
	acpStdin, err := acpCmd.StdinPipe()
	if err != nil {
		acpInitSpan.RecordError(err)
		acpInitSpan.SetStatus(codes.Error, err.Error())
		acpInitSpan.End()
		markFailed(issueID, "ACP start failed")
		die("acpCmd.StdinPipe failed", err)
	}
	acpStdout, err := acpCmd.StdoutPipe()
	if err != nil {
		acpInitSpan.RecordError(err)
		acpInitSpan.SetStatus(codes.Error, err.Error())
		acpInitSpan.End()
		markFailed(issueID, "ACP start failed")
		die("acpCmd.StdoutPipe failed", err)
	}
	acpCmd.Stderr = os.Stderr // copilot auth/debug output → container stderr

	if err := acpCmd.Start(); err != nil {
		acpInitSpan.RecordError(err)
		acpInitSpan.SetStatus(codes.Error, err.Error())
		acpInitSpan.End()
		_ = tw.Write("ACP", fmt.Sprintf("start failed: %v", err))
		markFailed(issueID, "ACP start failed")
		die("acpCmd.Start failed", err)
	}

	client := &vesselClient{
		workspace: "/workspace",
		terminals: make(map[string]*terminalSession),
	}
	conn := acp.NewClientSideConnection(client, acpStdin, acpStdout)
	defer conn.Close()

	// Close the connection when the copilot process exits so Done() fires.
	go func() {
		_ = acpCmd.Wait()
		conn.Close()
	}()

	// Start the JSON-RPC read/write loops in the background.
	// conn.Start blocks until the connection is closed or ctx is cancelled.
	// IMPORTANT: Start() sets c.ctx on its first line; we must yield to let it
	// run before calling Initialize() — otherwise SendRequest panics on nil c.ctx.
	go func() {
		if err := conn.Start(ctx); err != nil && !errors.Is(err, context.Canceled) {
			slog.ErrorContext(ctx, "acp connection error", "err", err)
		}
	}()
	runtime.Gosched() // yield so Start goroutine initialises c.ctx before we proceed

	// Initialize — negotiate protocol version and advertise our capabilities.
	initResult, err := conn.Initialize(ctx, &acp.InitializeRequest{
		ProtocolVersion: acp.ProtocolVersion(acp.CurrentProtocolVersion),
		ClientCapabilities: &acp.ClientCapabilities{
			FS: &acp.FileSystemCapabilities{
				ReadTextFile:  true,
				WriteTextFile: true,
			},
			Terminal: true,
		},
	})
	if err != nil {
		acpInitSpan.RecordError(err)
		acpInitSpan.SetStatus(codes.Error, err.Error())
		acpInitSpan.End()
		_ = tw.Write("ACP", fmt.Sprintf("initialize handshake failed: %v", err))
		markFailed(issueID, "ACP error")
		die("conn.Initialize failed", err)
	}
	acpInitSpan.End()
	slog.InfoContext(ctx, "ACP handshake OK", "protocol_version", int(initResult.ProtocolVersion))
	_ = tw.WriteJSON("ACP", map[string]any{
		"event":            "initialize",
		"protocol_version": int(initResult.ProtocolVersion),
		"status":           "ok",
	})

	// Step 7: New session — inject the Beads MCP server so Copilot can read
	// and update issues directly during its work.
	_, acpSessionSpan := tracer.Start(ctx, "legion.vessel.acp.session")
	sessionResult, err := conn.NewSession(ctx, &acp.NewSessionRequest{
		Cwd: "/workspace",
		MCPServers: []acp.MCPServer{
			acp.NewMCPServerStdio("beads", "bd", []string{"mcp"}, []acp.EnvVariable{}),
		},
	})
	if err != nil {
		acpSessionSpan.RecordError(err)
		acpSessionSpan.SetStatus(codes.Error, err.Error())
		acpSessionSpan.End()
		_ = tw.Write("ACP", fmt.Sprintf("new session failed: %v", err))
		markFailed(issueID, "ACP error")
		die("conn.NewSession failed", err)
	}
	sessionID := sessionResult.SessionID
	acpSessionSpan.End()
	slog.InfoContext(ctx, "ACP session ready", "session_id", string(sessionID))
	_ = tw.WriteJSON("ACP", map[string]any{
		"event":      "session/new",
		"session_id": string(sessionID),
		"cwd":        "/workspace",
		"status":     "ready",
	})

	// Step 8: Prompt with issue content.
	promptContent := issue.Title + "\n\n" + issue.Description

	_, acpPromptSpan := tracer.Start(ctx, "legion.vessel.acp.prompt",
		trace.WithAttributes(attribute.String("model", vc.Model)),
	)

	// Write the prompt to Beads for visibility.
	_ = tw.WriteJSON("ACP", map[string]any{
		"event":        "prompt/request",
		"user_message": promptContent,
		"session_id":   string(sessionID),
	})

	// Determine prompt timeout — default 5 min, overrideable via VESSEL_TIMEOUT (seconds).
	timeoutSecs := 300
	if v := os.Getenv("VESSEL_TIMEOUT"); v != "" {
		if n, parseErr := strconv.Atoi(v); parseErr == nil {
			timeoutSecs = n
		}
	}
	promptCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSecs)*time.Second)
	defer cancel()

	// SessionUpdate notifications are delivered to vc.SessionUpdate()
	// automatically by the SDK while conn.Prompt is blocking.
	promptResult, err := conn.Prompt(promptCtx, &acp.PromptRequest{
		SessionID: sessionID,
		Prompt: []acp.ContentBlock{
			acp.NewContentBlockText(promptContent),
		},
	})

	// Unify the stop reason string for the rest of the main() logic.
	var stopReason string
	if promptResult != nil {
		stopReason = string(promptResult.StopReason)
	}

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

// issueMode returns "review" or "work" based on issue labels.
// Used in Phase 2 (lg-b47) to dispatch Inquisitor review sessions.
func issueMode(labels []string) string {
	for _, l := range labels {
		if l == "type:review" {
			return "review"
		}
	}
	return "work"
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
