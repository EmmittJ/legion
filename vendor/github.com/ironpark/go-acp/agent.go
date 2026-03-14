package acp

import (
	"context"
	"encoding/json"
	"io"
)

// AgentSideConnection represents an agent-side connection to a client.
//
// This class provides the agent's view of an ACP connection. It handles
// incoming requests from the client and provides methods for the agent
// to communicate back to the client (implementing the Client interface).
//
// See protocol docs: [Agent](https://agentclientprotocol.com/protocol/overview#agent)
type AgentSideConnection struct {
	conn  *Connection
	agent Agent
}

// Verify that AgentSideConnection implements Client at compile time.
var _ Client = (*AgentSideConnection)(nil)

// NewAgentSideConnection creates a new agent-side connection to a client.
//
// Parameters:
//   - agent: The Agent implementation that will handle incoming client requests
//   - reader: The stream for receiving data from the client (typically stdin)
//   - writer: The stream for sending data to the client (typically stdout)
//
// See protocol docs: [Communication Model](https://agentclientprotocol.com/protocol/overview#communication-model)
func NewAgentSideConnection(agent Agent, reader io.Reader, writer io.Writer, opts ...ConnectionOption) *AgentSideConnection {
	asc := &AgentSideConnection{
		agent: agent,
	}

	handler := func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		return asc.handleIncomingMethod(ctx, method, params)
	}
	asc.conn = NewConnection(handler, reader, writer, opts...)

	return asc
}

// Client returns the Client interface for making requests to the client.
//
// The AgentSideConnection itself implements Client, so this returns self.
func (c *AgentSideConnection) Client() Client {
	return c
}

// Start starts the connection and begins processing messages.
func (c *AgentSideConnection) Start(ctx context.Context) error {
	return c.conn.Start(ctx)
}

// Close closes the connection gracefully.
func (c *AgentSideConnection) Close() error {
	return c.conn.Close()
}

// Done returns a channel that is closed when the connection is done.
func (c *AgentSideConnection) Done() <-chan struct{} {
	return c.conn.Done()
}

// --- Client interface implementation (outbound calls to client) ---

func (c *AgentSideConnection) SessionUpdate(ctx context.Context, params *SessionNotification) error {
	return c.conn.SendNotification(ctx, ClientMethods.SessionUpdate, params)
}

func (c *AgentSideConnection) RequestPermission(ctx context.Context, params *RequestPermissionRequest) (*RequestPermissionResponse, error) {
	return sendRequest[RequestPermissionResponse](ctx, c.conn, ClientMethods.SessionRequestPermission, params)
}

func (c *AgentSideConnection) ReadTextFile(ctx context.Context, params *ReadTextFileRequest) (*ReadTextFileResponse, error) {
	return sendRequest[ReadTextFileResponse](ctx, c.conn, ClientMethods.FSReadTextFile, params)
}

func (c *AgentSideConnection) WriteTextFile(ctx context.Context, params *WriteTextFileRequest) (*WriteTextFileResponse, error) {
	return sendRequest[WriteTextFileResponse](ctx, c.conn, ClientMethods.FSWriteTextFile, params)
}

func (c *AgentSideConnection) CreateTerminal(ctx context.Context, params *CreateTerminalRequest) (*CreateTerminalResponse, error) {
	return sendRequest[CreateTerminalResponse](ctx, c.conn, ClientMethods.TerminalCreate, params)
}

// CreateTerminalHandle executes a command in a new terminal and returns a TerminalHandle.
func (c *AgentSideConnection) CreateTerminalHandle(ctx context.Context, params *CreateTerminalRequest) (*TerminalHandle, error) {
	response, err := c.CreateTerminal(ctx, params)
	if err != nil {
		return nil, err
	}
	return NewTerminalHandle(response.TerminalID, params.SessionID, c), nil
}

func (c *AgentSideConnection) TerminalOutput(ctx context.Context, params *TerminalOutputRequest) (*TerminalOutputResponse, error) {
	return sendRequest[TerminalOutputResponse](ctx, c.conn, ClientMethods.TerminalOutput, params)
}

func (c *AgentSideConnection) ReleaseTerminal(ctx context.Context, params *ReleaseTerminalRequest) (*ReleaseTerminalResponse, error) {
	return sendRequest[ReleaseTerminalResponse](ctx, c.conn, ClientMethods.TerminalRelease, params)
}

func (c *AgentSideConnection) WaitForTerminalExit(ctx context.Context, params *WaitForTerminalExitRequest) (*WaitForTerminalExitResponse, error) {
	return sendRequest[WaitForTerminalExitResponse](ctx, c.conn, ClientMethods.TerminalWaitForExit, params)
}

func (c *AgentSideConnection) KillTerminalCommand(ctx context.Context, params *KillTerminalRequest) (*KillTerminalResponse, error) {
	return sendRequest[KillTerminalResponse](ctx, c.conn, ClientMethods.TerminalKill, params)
}

// ExtMethod sends a custom extension method request to the client.
func (c *AgentSideConnection) ExtMethod(ctx context.Context, method string, params any) (json.RawMessage, error) {
	return c.conn.SendRequest(ctx, method, params)
}

// ExtNotification sends a custom extension notification to the client.
func (c *AgentSideConnection) ExtNotification(ctx context.Context, method string, params any) error {
	return c.conn.SendNotification(ctx, method, params)
}

// --- Incoming request handler ---

// unmarshalAndCall is a generic helper that unmarshals JSON params and calls a handler.
func unmarshalAndCall[T any, R any](ctx context.Context, params json.RawMessage, fn func(context.Context, *T) (*R, error)) (any, error) {
	var req T
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, ErrInvalidParams(nil, err.Error())
	}
	return fn(ctx, &req)
}

// unmarshalAndCallVoid is like unmarshalAndCall but for handlers that return only error.
func unmarshalAndCallVoid[T any](ctx context.Context, params json.RawMessage, fn func(context.Context, *T) error) (any, error) {
	var req T
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, ErrInvalidParams(nil, err.Error())
	}
	return nil, fn(ctx, &req)
}

func (c *AgentSideConnection) handleIncomingMethod(ctx context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case AgentMethods.Initialize:
		return unmarshalAndCall(ctx, params, c.agent.Initialize)
	case AgentMethods.Authenticate:
		return unmarshalAndCall(ctx, params, c.agent.Authenticate)
	case AgentMethods.SessionNew:
		if creator, ok := c.agent.(SessionCreator); ok {
			return unmarshalAndCall(ctx, params, creator.NewSession)
		}
		if c.conn.sessionStore != nil {
			return unmarshalAndCall(ctx, params, c.conn.sessionStore.handleNewSession)
		}
		return nil, ErrMethodNotFound(method)
	case AgentMethods.SessionLoad:
		if loader, ok := c.agent.(SessionLoader); ok {
			return unmarshalAndCall(ctx, params, loader.LoadSession)
		}
		if c.conn.sessionStore != nil {
			return unmarshalAndCall(ctx, params, c.conn.sessionStore.handleLoadSession)
		}
		return nil, ErrMethodNotFound(method)
	case AgentMethods.SessionList:
		if lister, ok := c.agent.(SessionLister); ok {
			return unmarshalAndCall(ctx, params, lister.ListSessions)
		}
		if c.conn.sessionStore != nil {
			return unmarshalAndCall(ctx, params, c.conn.sessionStore.handleListSessions)
		}
		return nil, ErrMethodNotFound(method)
	case AgentMethods.SessionSetMode:
		return unmarshalAndCall(ctx, params, c.agent.SetSessionMode)
	case AgentMethods.SessionSetConfigOption:
		return unmarshalAndCall(ctx, params, c.agent.SetSessionConfigOption)
	case AgentMethods.SessionPrompt:
		return unmarshalAndCall(ctx, params, c.agent.Prompt)
	case AgentMethods.SessionCancel:
		return unmarshalAndCallVoid(ctx, params, c.agent.Cancel)

	// Unstable agent methods — dispatched via optional interfaces
	case AgentMethodsUnstable.SessionFork:
		if forker, ok := c.agent.(SessionForker); ok {
			return unmarshalAndCall(ctx, params, forker.ForkSession)
		}
		return nil, ErrMethodNotFound(method)
	case AgentMethodsUnstable.SessionResume:
		if resumer, ok := c.agent.(SessionResumer); ok {
			return unmarshalAndCall(ctx, params, resumer.ResumeSession)
		}
		return nil, ErrMethodNotFound(method)
	case AgentMethodsUnstable.SessionClose:
		if closer, ok := c.agent.(SessionCloser); ok {
			return unmarshalAndCall(ctx, params, closer.CloseSession)
		}
		return nil, ErrMethodNotFound(method)
	case AgentMethodsUnstable.SessionSetModel:
		if setter, ok := c.agent.(ModelSetter); ok {
			return unmarshalAndCall(ctx, params, setter.SetSessionModel)
		}
		return nil, ErrMethodNotFound(method)

	default:
		if handler, ok := c.agent.(ExtMethodHandler); ok {
			return handler.ExtMethod(ctx, method, params)
		}
		return nil, ErrMethodNotFound(method)
	}
}
