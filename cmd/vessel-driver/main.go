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
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/EmmittJ/legion/internal/config"
	"github.com/EmmittJ/legion/internal/telemetry"
)

// ---------------------------------------------------------------------------
// result — written to /workspace/result.json for the post-run hook to consume.
// ---------------------------------------------------------------------------

// vesselResult is the outcome record vessel-driver writes to /workspace/result.json
// on every exit path (success or error).  The post-run hook reads this file to
// decide whether to push, close the issue, or mark it failed.
type vesselResult struct {
	IssueID      string `json:"issue_id"`
	Status       string `json:"status"`                  // "success" | "error"
	Branch       string `json:"branch,omitempty"`        // set on success
	ErrorMessage string `json:"error_message,omitempty"` // set on error
}

// hermesResult is the classification result written to /workspace/result.json when
// running in hermes mode.  Hermes does not work on code; it classifies issues
// and emits a routing decision.
//
// Status is required so the dispatch loop's readACPResult() can proceed after
// the acp-session built-in completes.  hooks/hermes/post-run.sh reads .role
// and .issue_id; it ignores .status, so adding it here is backwards-compatible.
type hermesResult struct {
	Status  string `json:"status"`   // "success" | "error"
	IssueID string `json:"issue_id"` // needed by hermes post-run hook
	Role    string `json:"role"`     // "worker" | "hierophant" | "inquisitor"
}

// writeResult serialises r to <workspaceDir>/result.json.  Errors are logged but
// never fatal — we always want the process to exit with the correct code even
// if the write fails.
func writeResult(workspaceDir string, r vesselResult) {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		slog.Error("result marshal failed", "err", err)
		return
	}
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		slog.Error("mkdir workspace failed", "dir", workspaceDir, "err", err)
		return
	}
	if err := os.WriteFile(filepath.Join(workspaceDir, "result.json"), data, 0o644); err != nil {
		slog.Error("write result.json failed", "err", err)
	}
}

// writeHermesResult serialises h to <workspaceDir>/result.json for hermes mode.
func writeHermesResult(workspaceDir string, h hermesResult) {
	data, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		slog.Error("hermes result marshal failed", "err", err)
		return
	}
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		slog.Error("mkdir workspace failed", "dir", workspaceDir, "err", err)
		return
	}
	if err := os.WriteFile(filepath.Join(workspaceDir, "result.json"), data, 0o644); err != nil {
		slog.Error("write hermes result.json failed", "err", err)
	}
}

// ---------------------------------------------------------------------------
// issueContext — read from /workspace/.legion/context.json (written by pre-run).
// ---------------------------------------------------------------------------

// issueContext mirrors the fields vessel-driver needs from the Beads issue.
// The pre-run hook writes `bd show <id> --json` to /workspace/.legion/context.json;
// that output is a JSON array, so we parse the first element.
type issueContext struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

// readIssueContext reads <workspaceDir>/.legion/context.json written by the pre-run hook.
// On any read/parse failure it returns a minimal fallback so the ACP session can
// still start (the agent will see the issue ID at minimum).
func readIssueContext(workspaceDir, issueID string) (*issueContext, error) {
	data, err := os.ReadFile(filepath.Join(workspaceDir, ".legion", "context.json"))
	if err != nil {
		slog.Warn("context.json not found — using fallback prompt", "issue_id", issueID, "err", err)
		return &issueContext{ID: issueID, Title: "Work on issue " + issueID}, nil
	}
	// bd show --json emits a JSON array; parse the first element.
	var items []issueContext
	if jsonErr := json.Unmarshal(data, &items); jsonErr != nil {
		// Try single-object form as a fallback.
		var single issueContext
		if err2 := json.Unmarshal(data, &single); err2 != nil {
			return nil, fmt.Errorf("parse context.json: %w", jsonErr)
		}
		return &single, nil
	}
	if len(items) == 0 {
		return &issueContext{ID: issueID, Title: "Work on issue " + issueID}, nil
	}
	return &items[0], nil
}

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

	// For hermes mode: capture all agent message chunks to extract JSON decision
	captureOutput bool
	output        strings.Builder
}

// SessionUpdate is called for every streaming update Copilot sends while
// processing a prompt.  We log interesting events at info level and ignore
// the rest so container stdout stays readable.
func (c *vesselClient) SessionUpdate(ctx context.Context, params *acp.SessionNotification) error {
	acp.MatchSessionUpdate(&params.Update, acp.SessionUpdateMatcher[struct{}]{
		AgentMessageChunk: func(v acp.SessionUpdateAgentMessageChunk) struct{} {
			if text, ok := v.Content.AsText(); ok && text.Text != "" {
				slog.DebugContext(ctx, "copilot output", "text", text.Text)
				// For hermes mode, capture all output
				if c.captureOutput {
					c.mu.Lock()
					c.output.WriteString(text.Text)
					c.mu.Unlock()
				}
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

// ---------------------------------------------------------------------------
// vesselClient — implements acp.Client so Copilot can read/write files and
// run shell commands inside the vessel container.
// ---------------------------------------------------------------------------

// resolveACPCommand resolves an ACPSpec to a concrete exec arg slice.
// If modelOverride is non-empty (e.g. from LEGION_MODEL env var), it
// takes precedence over spec.Model.
func resolveACPCommand(spec config.ACPSpec, modelOverride, workspaceDir string) ([]string, error) {
	model := spec.Model
	if modelOverride != "" {
		model = modelOverride
	}

	switch spec.Backend {
	case "copilot":
		args := []string{"copilot", "--acp", "--stdio"}
		if model != "" {
			args = append(args, "--model", model)
		}
		agentFile := spec.AgentFile
		if agentFile != "" && !strings.Contains(agentFile, "/") {
			agentFile = workspaceDir + "/.github/agents/" + agentFile + ".agent.md"
		}
		if agentFile != "" {
			args = append(args, "--agent", agentFile)
		}
		return args, nil

	case "raw":
		if spec.AgentFile == "" {
			return nil, fmt.Errorf("acp_spec.backend=raw requires agent_file to be set")
		}
		return strings.Fields(spec.AgentFile), nil

	default:
		return nil, fmt.Errorf("acp_spec.backend %q is not supported", spec.Backend)
	}
}

// runWorkerACPSession runs the ACP session for worker / reviewer / hierophant /
// inquisitor roles.  On success it writes /workspace/result.json with
// STATUS=success.  On failure it returns an error; the dispatch loop's
// fatalFail handler then writes result.json with STATUS=error.
//
// The tracer is used only to create child spans; the root span lives in main().
func runWorkerACPSession(ctx context.Context, vc *config.VesselConfig, legionModel string, tracer trace.Tracer) error {
	issueID := vc.IssueID
	branch := vesselBranch(vc) // dispatch.go resolves the correct branch per role

	issue, err := readIssueContext(vc.WorkspaceDir, issueID)
	if err != nil {
		return fmt.Errorf("read issue context: %w", err)
	}

	// Agent identity pre-flight: if agent_name is set the agent file must exist
	// inside the cloned repo before we spend time starting Copilot.
	if vc.AgentName != "" {
		agentFile := vc.WorkspaceDir + "/.github/agents/" + vc.AgentName + ".agent.md"
		if _, statErr := os.Stat(agentFile); statErr != nil {
			return fmt.Errorf("agent file check: %w", statErr)
		}
	}

	// ── ACP initialize ────────────────────────────────────────────────────────
	// IMPORTANT: copilot --acp --stdio authenticates via GH_TOKEN.
	// The token MUST have the "copilot" OAuth scope.  A plain repo-scoped PAT
	// or Actions GITHUB_TOKEN will cause session/prompt to hang until the
	// VESSEL_TIMEOUT deadline fires.  Use a token from a Copilot-enabled
	// GitHub account with the copilot scope.
	_, acpInitSpan := tracer.Start(ctx, "legion.vessel.acp.initialize",
		trace.WithAttributes(attribute.String("model", vc.ACPSpec.Model)),
	)

	acpArgs, err := resolveACPCommand(vc.ACPSpec, legionModel, vc.WorkspaceDir)
	if err != nil {
		acpInitSpan.End()
		return fmt.Errorf("resolve ACP command: %w", err)
	}
	slog.InfoContext(ctx, "starting ACP session", "model", vc.ACPSpec.Model, "acp_args", acpArgs)

	// Spawn the ACP server with stderr forwarded to our container stderr for
	// debugging.  We use NewClientSideConnection directly (rather than
	// SpawnAgent) so we can control the cmd before it starts.
	acpCmd := exec.CommandContext(ctx, acpArgs[0], acpArgs[1:]...)
	acpStdin, err := acpCmd.StdinPipe()
	if err != nil {
		acpInitSpan.End()
		return fmt.Errorf("StdinPipe: %w", err)
	}
	acpStdout, err := acpCmd.StdoutPipe()
	if err != nil {
		acpInitSpan.End()
		return fmt.Errorf("StdoutPipe: %w", err)
	}
	acpCmd.Stderr = os.Stderr // copilot auth/debug output → container stderr

	if err := acpCmd.Start(); err != nil {
		acpInitSpan.End()
		return fmt.Errorf("acpCmd.Start: %w", err)
	}

	client := &vesselClient{
		workspace: vc.WorkspaceDir,
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
		return fmt.Errorf("Initialize: %w", err)
	}
	acpInitSpan.End()
	slog.InfoContext(ctx, "ACP handshake OK", "protocol_version", int(initResult.ProtocolVersion))

	// ── ACP session ───────────────────────────────────────────────────────────
	// New session — inject the Beads MCP server so Copilot can read and update
	// issues directly during its work.
	_, acpSessionSpan := tracer.Start(ctx, "legion.vessel.acp.session")
	sessionResult, err := conn.NewSession(ctx, &acp.NewSessionRequest{
		Cwd: vc.WorkspaceDir,
		MCPServers: []acp.MCPServer{
			acp.NewMCPServerStdio("beads", "bd", []string{"mcp"}, []acp.EnvVariable{}),
		},
	})
	if err != nil {
		acpSessionSpan.RecordError(err)
		acpSessionSpan.SetStatus(codes.Error, err.Error())
		acpSessionSpan.End()
		return fmt.Errorf("NewSession: %w", err)
	}
	sessionID := sessionResult.SessionID
	acpSessionSpan.End()
	slog.InfoContext(ctx, "ACP session ready", "session_id", string(sessionID))

	// Build prompt from issue context written by pre-run hook.
	promptContent := issue.Title
	if issue.Description != "" {
		promptContent += "\n\n" + issue.Description
	}

	_, acpPromptSpan := tracer.Start(ctx, "legion.vessel.acp.prompt",
		trace.WithAttributes(attribute.String("model", vc.ACPSpec.Model)),
	)

	// Determine prompt timeout — default 15 min, overrideable via VESSEL_TIMEOUT (seconds).
	// Archon always sets VESSEL_TIMEOUT explicitly; the 900-second hardcoded value is
	// a last-resort fallback for standalone or test runs only.
	timeoutSecs := 900
	if v := os.Getenv("VESSEL_TIMEOUT"); v != "" {
		if n, parseErr := strconv.Atoi(v); parseErr == nil {
			timeoutSecs = n
		}
	}
	promptCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSecs)*time.Second)
	defer cancel()

	// SessionUpdate notifications are delivered to client.SessionUpdate()
	// automatically by the SDK while conn.Prompt is blocking.
	promptResult, err := conn.Prompt(promptCtx, &acp.PromptRequest{
		SessionID: sessionID,
		Prompt: []acp.ContentBlock{
			acp.NewContentBlockText(promptContent),
		},
	})

	// Unify the stop reason string for the rest of the logic.
	var stopReason string
	if promptResult != nil {
		stopReason = string(promptResult.StopReason)
	}

	// Handle prompt completion or error.
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
		return fmt.Errorf("prompt: %w", promptErr)
	}
	acpPromptSpan.SetStatus(codes.Ok, "")
	acpPromptSpan.End()
	slog.InfoContext(ctx, "prompt complete", "stop_reason", stopReason)

	// Write success result for the post-run hook to consume.
	writeResult(vc.WorkspaceDir, vesselResult{
		IssueID: issueID,
		Status:  "success",
		Branch:  branch,
	})

	slog.InfoContext(ctx, "worker ACP session complete",
		"issue_id", issueID, "branch", branch, "stop_reason", stopReason)
	return nil
}

// runHermesACPSession runs the ACP session for the hermes (routing/classification)
// role.  On success it writes /workspace/result.json with STATUS=success and the
// routing decision in the "role" field.  On failure it returns an error.
func runHermesACPSession(ctx context.Context, vc *config.VesselConfig, legionModel string, tracer trace.Tracer) error {
	issueID := vc.IssueID

	// Read the issue context written by the hermes pre-run hook.
	issue, err := readIssueContext(vc.WorkspaceDir, issueID)
	if err != nil {
		return fmt.Errorf("read issue context: %w", err)
	}

	// ── ACP initialize ────────────────────────────────────────────────────────
	_, acpInitSpan := tracer.Start(ctx, "legion.vessel.acp.initialize",
		trace.WithAttributes(attribute.String("model", vc.ACPSpec.Model)),
	)

	acpArgs, err := resolveACPCommand(vc.ACPSpec, legionModel, vc.WorkspaceDir)
	if err != nil {
		acpInitSpan.End()
		return fmt.Errorf("resolve ACP command: %w", err)
	}
	slog.InfoContext(ctx, "starting hermes ACP session", "model", vc.ACPSpec.Model, "acp_args", acpArgs)

	acpCmd := exec.CommandContext(ctx, acpArgs[0], acpArgs[1:]...)
	acpStdin, err := acpCmd.StdinPipe()
	if err != nil {
		acpInitSpan.End()
		return fmt.Errorf("StdinPipe: %w", err)
	}
	acpStdout, err := acpCmd.StdoutPipe()
	if err != nil {
		acpInitSpan.End()
		return fmt.Errorf("StdoutPipe: %w", err)
	}
	acpCmd.Stderr = os.Stderr

	if err := acpCmd.Start(); err != nil {
		acpInitSpan.End()
		return fmt.Errorf("acpCmd.Start: %w", err)
	}

	client := &vesselClient{
		workspace:     vc.WorkspaceDir,
		terminals:     make(map[string]*terminalSession),
		captureOutput: true, // hermes captures output to extract the role decision
	}
	conn := acp.NewClientSideConnection(client, acpStdin, acpStdout)
	defer conn.Close()

	go func() {
		_ = acpCmd.Wait()
		conn.Close()
	}()

	go func() {
		if err := conn.Start(ctx); err != nil && !errors.Is(err, context.Canceled) {
			slog.ErrorContext(ctx, "acp connection error", "err", err)
		}
	}()
	runtime.Gosched()

	// Initialize ACP handshake.
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
		return fmt.Errorf("Initialize: %w", err)
	}
	acpInitSpan.End()
	slog.InfoContext(ctx, "hermes ACP handshake OK", "protocol_version", int(initResult.ProtocolVersion))

	// ── ACP session ───────────────────────────────────────────────────────────
	_, acpSessionSpan := tracer.Start(ctx, "legion.vessel.acp.session")
	sessionResult, err := conn.NewSession(ctx, &acp.NewSessionRequest{
		Cwd: vc.WorkspaceDir,
		MCPServers: []acp.MCPServer{
			acp.NewMCPServerStdio("beads", "bd", []string{"mcp"}, []acp.EnvVariable{}),
		},
	})
	if err != nil {
		acpSessionSpan.RecordError(err)
		acpSessionSpan.SetStatus(codes.Error, err.Error())
		acpSessionSpan.End()
		return fmt.Errorf("NewSession: %w", err)
	}
	sessionID := sessionResult.SessionID
	acpSessionSpan.End()
	slog.InfoContext(ctx, "hermes ACP session ready", "session_id", string(sessionID))

	// Build prompt from issue context.
	promptContent := issue.Title
	if issue.Description != "" {
		promptContent += "\n\n" + issue.Description
	}

	_, acpPromptSpan := tracer.Start(ctx, "legion.vessel.acp.prompt",
		trace.WithAttributes(attribute.String("model", vc.ACPSpec.Model)),
	)

	// Hermes classifies quickly; default timeout is shorter than worker.
	timeoutSecs := 60
	if v := os.Getenv("VESSEL_TIMEOUT"); v != "" {
		if n, parseErr := strconv.Atoi(v); parseErr == nil {
			timeoutSecs = n
		}
	}
	promptCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSecs)*time.Second)
	defer cancel()

	promptResult, err := conn.Prompt(promptCtx, &acp.PromptRequest{
		SessionID: sessionID,
		Prompt: []acp.ContentBlock{
			acp.NewContentBlockText(promptContent),
		},
	})

	var stopReason string
	if promptResult != nil {
		stopReason = string(promptResult.StopReason)
	}

	if err != nil || stopReason != "end_turn" {
		var promptErr error
		if err != nil {
			promptErr = err
			slog.ErrorContext(ctx, "hermes prompt error", "err", err)
		} else {
			promptErr = fmt.Errorf("stop reason: %s", stopReason)
			slog.ErrorContext(ctx, "hermes prompt stopped with error reason", "stop_reason", stopReason)
		}
		acpPromptSpan.RecordError(promptErr)
		acpPromptSpan.SetStatus(codes.Error, promptErr.Error())
		acpPromptSpan.End()
		return fmt.Errorf("hermes prompt: %w", promptErr)
	}
	acpPromptSpan.SetStatus(codes.Ok, "")
	acpPromptSpan.End()
	slog.InfoContext(ctx, "hermes prompt complete", "stop_reason", stopReason)

	// ── Extract role decision ─────────────────────────────────────────────────
	// Hermes outputs JSON; parse it to extract the role field.
	// May contain surrounding text, so we scan for the first { … } object.
	client.mu.Lock()
	output := client.output.String()
	client.mu.Unlock()

	slog.InfoContext(ctx, "hermes output captured", "output", output)

	role := "worker" // safe fallback
	var hermesOutput map[string]interface{}
	if err := json.Unmarshal([]byte(output), &hermesOutput); err == nil {
		if r, ok := hermesOutput["role"]; ok {
			if roleStr, ok := r.(string); ok {
				role = roleStr
			}
		}
	} else {
		// Try to find JSON in the output by searching for { ... }.
		start := strings.Index(output, "{")
		if start >= 0 {
			end := strings.LastIndex(output, "}")
			if end > start {
				jsonStr := output[start : end+1]
				if err := json.Unmarshal([]byte(jsonStr), &hermesOutput); err == nil {
					if r, ok := hermesOutput["role"]; ok {
						if roleStr, ok := r.(string); ok {
							role = roleStr
						}
					}
				}
			}
		}
	}

	// Validate the role — unknown roles default to "worker" to prevent dispatch errors.
	switch role {
	case "worker", "hierophant", "inquisitor":
		// valid
	default:
		slog.WarnContext(ctx, "hermes: unknown role, defaulting to worker", "role", role)
		role = "worker"
	}

	// Write the hermes result.  Status="success" is required so the dispatch
	// loop's readACPResult() can proceed; .role is read by hooks/hermes/post-run.sh.
	writeHermesResult(vc.WorkspaceDir, hermesResult{
		Status:  "success",
		IssueID: issueID,
		Role:    role,
	})

	slog.InfoContext(ctx, "hermes complete", "issue_id", issueID, "role", role)
	return nil
}

func main() {
	// Load structured config from LEGION_CONFIG_JSON (or LEGION_CONFIG_FILE for tests).
	// Secrets (GITHUB_TOKEN, DOLT_HOST/PORT, OTEL_*) remain as individual env vars
	// consumed by hooks — vessel-driver does not read them directly.
	vc, err := config.Load()
	if err != nil {
		slog.Error("config load failed", "err", err)
		// writeResult is safe to call here even though telemetry is not yet
		// initialised.  issueID is unknown at this point so we leave it empty.
		// WorkspaceDir defaults to /workspace before config is loaded.
		writeResult("/workspace", vesselResult{Status: "error", ErrorMessage: "config load: " + err.Error()})
		os.Exit(1)
	}

	issueID := vc.IssueID
	legionModel := os.Getenv("LEGION_MODEL") // overrides acp_spec.model at runtime if set

	ctx := context.Background()

	// If Archon injected a W3C traceparent env var, extract the parent span
	// context before telemetry.Setup so tracer.Start creates a child span that
	// links the vessel trace to Archon's dispatch trace in Tempo.
	if tp := os.Getenv("TRACEPARENT"); tp != "" {
		carrier := propagation.MapCarrier{"traceparent": tp}
		ctx = propagation.TraceContext{}.Extract(ctx, carrier)
	}

	// Initialize telemetry. Non-fatal: a noop tracer/meter is returned on
	// failure so the rest of the binary continues without distributed tracing.
	tracer, _, _, shutdown, err := telemetry.Setup(ctx, "legion.vessel-driver")
	if err != nil {
		slog.Error("telemetry setup failed", "err", err)
		// non-fatal — continue with noop tracer
	}

	// Log after telemetry.Setup so the JSON handler is active.
	if tp := os.Getenv("TRACEPARENT"); tp != "" {
		slog.InfoContext(ctx, "extracted parent trace context", "traceparent", tp)
	}

	// Root span covering the entire vessel lifecycle.
	ctx, rootSpan := tracer.Start(ctx, "legion.vessel.run",
		trace.WithAttributes(
			attribute.String("issue.id", issueID),
			attribute.String("repo.url", vc.RepoURL),
		),
	)

	// Build the acp-session built-in closure for this role.
	// The closure captures tracer and legionModel so the ACP functions have
	// access to them without threading them through the dispatch loop.
	var acpBuiltIn func(context.Context) error
	if vc.RoleName == "hermes" {
		acpBuiltIn = func(runCtx context.Context) error {
			return runHermesACPSession(runCtx, vc, legionModel, tracer)
		}
	} else {
		acpBuiltIn = func(runCtx context.Context) error {
			return runWorkerACPSession(runCtx, vc, legionModel, tracer)
		}
	}

	exitCode := RunDispatch(ctx, vc, acpBuiltIn)

	// Set root span status and end it before telemetry flush.
	if exitCode == 0 {
		rootSpan.SetStatus(codes.Ok, "")
	} else {
		rootSpan.SetStatus(codes.Error, "dispatch failed")
	}
	rootSpan.End()

	// IMPORTANT: vessel-driver is short-lived.  This shutdown call is the only
	// mechanism that flushes buffered spans before the process exits.
	_ = shutdown(ctx)
	os.Exit(exitCode)
}
