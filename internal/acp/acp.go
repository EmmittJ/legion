// Package acp is Legion's thin session layer over the official ACP Go SDK
// (github.com/coder/acp-go-sdk, ACP v1). The animus uses it to possess a
// vessel: start the harness, open a session with Legion's MCP tools
// injected, and drive prompt turns. See ADR-0002.
package acp

import (
	"context"
	"fmt"
	"io"
	"os/exec"

	acp "github.com/coder/acp-go-sdk"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// UpdateHandler receives streamed session updates (message chunks, tool
// calls, plans) during a prompt turn.
type UpdateHandler func(ctx context.Context, n acp.SessionNotification)

// Config describes how to start a harness session.
type Config struct {
	// Command is the harness argv (e.g. ["copilot", "--stdio"]). Ignored
	// when streams are injected for tests.
	Command []string
	// Cwd is the session working directory — the cloned target repo.
	Cwd string
	// McpServers are injected at session/new (Legion's animus tools).
	McpServers []acp.McpServer
	// OnUpdate observes streamed updates. Optional.
	OnUpdate UpdateHandler
}

// Session is a live ACP session with a harness.
type Session struct {
	conn *acp.ClientSideConnection
	id   acp.SessionId
	cmd  *exec.Cmd
	done func() error
}

func tracer() trace.Tracer { return otel.Tracer("legion/internal/acp") }

func end(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
}

// Start launches the harness process and performs the ACP handshake:
// initialize → session/new (with MCP servers injected).
func Start(ctx context.Context, cfg Config) (*Session, error) {
	if len(cfg.Command) == 0 {
		return nil, fmt.Errorf("acp: empty harness command")
	}
	cmd := exec.CommandContext(ctx, cfg.Command[0], cfg.Command[1:]...)
	cmd.Dir = cfg.Cwd
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("acp: start harness %q: %w", cfg.Command[0], err)
	}
	s, err := connect(ctx, cfg, stdin, stdout)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, err
	}
	s.cmd = cmd
	return s, nil
}

// connect performs the handshake over arbitrary streams. Split from Start
// so tests can drive a fake harness over in-memory pipes.
func connect(ctx context.Context, cfg Config, peerInput io.Writer, peerOutput io.Reader) (*Session, error) {
	ctx, span := tracer().Start(ctx, "acp.session")
	defer func() { span.End() }()

	conn := acp.NewClientSideConnection(&headlessClient{onUpdate: cfg.OnUpdate}, peerInput, peerOutput)

	if _, err := conn.Initialize(ctx, acp.InitializeRequest{
		ProtocolVersion: acp.ProtocolVersionNumber,
		ClientInfo:      &acp.Implementation{Name: "legion-animus"},
	}); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, fmt.Errorf("acp: initialize: %w", err)
	}

	mcp := cfg.McpServers
	if mcp == nil {
		mcp = []acp.McpServer{}
	}
	res, err := conn.NewSession(ctx, acp.NewSessionRequest{
		Cwd:        cfg.Cwd,
		McpServers: mcp,
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, fmt.Errorf("acp: session/new: %w", err)
	}
	span.SetAttributes(attribute.String("acp.session_id", string(res.SessionId)))

	return &Session{conn: conn, id: res.SessionId}, nil
}

// ID returns the ACP session ID.
func (s *Session) ID() string { return string(s.id) }

// Prompt sends one prompt turn and blocks until the harness stops.
func (s *Session) Prompt(ctx context.Context, text string) (acp.StopReason, error) {
	ctx, span := tracer().Start(ctx, "acp.turn", trace.WithAttributes(
		attribute.String("acp.session_id", string(s.id)),
	))
	res, err := s.conn.Prompt(ctx, acp.PromptRequest{
		SessionId: s.id,
		Prompt:    []acp.ContentBlock{acp.TextBlock(text)},
	})
	if err != nil {
		end(span, err)
		return "", fmt.Errorf("acp: prompt: %w", err)
	}
	span.SetAttributes(attribute.String("acp.stop_reason", string(res.StopReason)))
	end(span, nil)
	return res.StopReason, nil
}

// Close tears down the harness process.
func (s *Session) Close() error {
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
		return s.cmd.Wait()
	}
	if s.done != nil {
		return s.done()
	}
	return nil
}

// headlessClient implements the ACP Client interface for unattended
// operation: permissions are auto-granted (the vessel is the sandbox),
// updates stream to the handler, and fs/terminal capabilities are not
// advertised so those methods reject.
type headlessClient struct {
	onUpdate UpdateHandler
}

var _ acp.Client = (*headlessClient)(nil)

// RequestPermission grants the most permissive allow option offered.
// The vessel container is the security boundary, not the permission prompt.
func (h *headlessClient) RequestPermission(ctx context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	var pick *acp.PermissionOption
	for i := range params.Options {
		o := &params.Options[i]
		switch o.Kind {
		case acp.PermissionOptionKindAllowAlways:
			pick = o
		case acp.PermissionOptionKindAllowOnce:
			if pick == nil {
				pick = o
			}
		}
	}
	if pick == nil {
		return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeCancelled()}, nil
	}
	return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeSelected(pick.OptionId)}, nil
}

func (h *headlessClient) SessionUpdate(ctx context.Context, params acp.SessionNotification) error {
	if h.onUpdate != nil {
		h.onUpdate(ctx, params)
	}
	return nil
}

func (h *headlessClient) ReadTextFile(context.Context, acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	return acp.ReadTextFileResponse{}, acp.NewMethodNotFound("fs/read_text_file")
}

func (h *headlessClient) WriteTextFile(context.Context, acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	return acp.WriteTextFileResponse{}, acp.NewMethodNotFound("fs/write_text_file")
}

func (h *headlessClient) CreateTerminal(context.Context, acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	return acp.CreateTerminalResponse{}, acp.NewMethodNotFound("terminal/create")
}

func (h *headlessClient) KillTerminal(context.Context, acp.KillTerminalRequest) (acp.KillTerminalResponse, error) {
	return acp.KillTerminalResponse{}, acp.NewMethodNotFound("terminal/kill")
}

func (h *headlessClient) TerminalOutput(context.Context, acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return acp.TerminalOutputResponse{}, acp.NewMethodNotFound("terminal/output")
}

func (h *headlessClient) ReleaseTerminal(context.Context, acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return acp.ReleaseTerminalResponse{}, acp.NewMethodNotFound("terminal/release")
}

func (h *headlessClient) WaitForTerminalExit(context.Context, acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	return acp.WaitForTerminalExitResponse{}, acp.NewMethodNotFound("terminal/wait_for_exit")
}

// StdioMcpServer builds a stdio-transport MCP server entry for
// session/new — how the animus injects Legion's bead tools.
func StdioMcpServer(name, command string, args []string, env map[string]string) acp.McpServer {
	vars := make([]acp.EnvVariable, 0, len(env))
	for k, v := range env {
		vars = append(vars, acp.EnvVariable{Name: k, Value: v})
	}
	return acp.McpServer{Stdio: &acp.McpServerStdio{
		Name:    name,
		Command: command,
		Args:    args,
		Env:     vars,
	}}
}
