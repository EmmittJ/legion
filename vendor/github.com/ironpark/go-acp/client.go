package acp

import (
	"context"
	"encoding/json"
	"io"
)

// ClientSideConnection represents a client-side connection to an agent.
//
// This class provides the client's view of an ACP connection, allowing
// clients to send requests to agents and handle incoming agent requests.
//
// See protocol docs: [Client](https://agentclientprotocol.com/protocol/overview#client)
type ClientSideConnection struct {
	conn *Connection
	impl Client
}

// NewClientSideConnection creates a new client-side connection to an agent.
//
// Parameters:
//   - client: The Client implementation that will handle incoming agent requests
//   - reader: The stream for receiving data from the agent (typically agent's stdout)
//   - writer: The stream for sending data to the agent (typically agent's stdin)
//
// See protocol docs: [Communication Model](https://agentclientprotocol.com/protocol/overview#communication-model)
func NewClientSideConnection(client Client, writer io.Writer, reader io.Reader, opts ...ConnectionOption) *ClientSideConnection {
	csc := &ClientSideConnection{
		impl: client,
	}

	handler := func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		return csc.handleIncomingMethod(ctx, method, params)
	}
	csc.conn = NewConnection(handler, reader, writer, opts...)

	return csc
}

// Start begins processing JSON-RPC messages.
func (c *ClientSideConnection) Start(ctx context.Context) error {
	return c.conn.Start(ctx)
}

// Close closes the connection gracefully.
func (c *ClientSideConnection) Close() error {
	return c.conn.Close()
}

// Done returns a channel that is closed when the connection is done.
func (c *ClientSideConnection) Done() <-chan struct{} {
	return c.conn.Done()
}

// --- Outbound agent-calling methods ---

func (c *ClientSideConnection) Initialize(ctx context.Context, params *InitializeRequest) (*InitializeResponse, error) {
	return sendRequest[InitializeResponse](ctx, c.conn, AgentMethods.Initialize, params)
}

func (c *ClientSideConnection) Authenticate(ctx context.Context, params *AuthenticateRequest) (*AuthenticateResponse, error) {
	return sendRequest[AuthenticateResponse](ctx, c.conn, AgentMethods.Authenticate, params)
}

func (c *ClientSideConnection) NewSession(ctx context.Context, params *NewSessionRequest) (*NewSessionResponse, error) {
	return sendRequest[NewSessionResponse](ctx, c.conn, AgentMethods.SessionNew, params)
}

func (c *ClientSideConnection) LoadSession(ctx context.Context, params *LoadSessionRequest) (*LoadSessionResponse, error) {
	return sendRequest[LoadSessionResponse](ctx, c.conn, AgentMethods.SessionLoad, params)
}

func (c *ClientSideConnection) ListSessions(ctx context.Context, params *ListSessionsRequest) (*ListSessionsResponse, error) {
	return sendRequest[ListSessionsResponse](ctx, c.conn, AgentMethods.SessionList, params)
}

func (c *ClientSideConnection) SetSessionMode(ctx context.Context, params *SetSessionModeRequest) (*SetSessionModeResponse, error) {
	return sendRequest[SetSessionModeResponse](ctx, c.conn, AgentMethods.SessionSetMode, params)
}

func (c *ClientSideConnection) SetSessionConfigOption(ctx context.Context, params *SetSessionConfigOptionRequest) (*SetSessionConfigOptionResponse, error) {
	return sendRequest[SetSessionConfigOptionResponse](ctx, c.conn, AgentMethods.SessionSetConfigOption, params)
}

func (c *ClientSideConnection) Prompt(ctx context.Context, params *PromptRequest) (*PromptResponse, error) {
	return sendRequest[PromptResponse](ctx, c.conn, AgentMethods.SessionPrompt, params)
}

func (c *ClientSideConnection) Cancel(ctx context.Context, params *CancelNotification) error {
	return c.conn.SendNotification(ctx, AgentMethods.SessionCancel, params)
}

// --- Unstable outbound agent-calling methods ---

func (c *ClientSideConnection) ForkSession(ctx context.Context, params *ForkSessionRequest) (*ForkSessionResponse, error) {
	return sendRequest[ForkSessionResponse](ctx, c.conn, AgentMethodsUnstable.SessionFork, params)
}

func (c *ClientSideConnection) ResumeSession(ctx context.Context, params *ResumeSessionRequest) (*ResumeSessionResponse, error) {
	return sendRequest[ResumeSessionResponse](ctx, c.conn, AgentMethodsUnstable.SessionResume, params)
}

func (c *ClientSideConnection) CloseSession(ctx context.Context, params *CloseSessionRequest) (*CloseSessionResponse, error) {
	return sendRequest[CloseSessionResponse](ctx, c.conn, AgentMethodsUnstable.SessionClose, params)
}

func (c *ClientSideConnection) SetSessionModel(ctx context.Context, params *SetSessionModelRequest) (*SetSessionModelResponse, error) {
	return sendRequest[SetSessionModelResponse](ctx, c.conn, AgentMethodsUnstable.SessionSetModel, params)
}

// --- Extension methods ---

func (c *ClientSideConnection) ExtMethod(ctx context.Context, method string, params any) (json.RawMessage, error) {
	return c.conn.SendRequest(ctx, method, params)
}

func (c *ClientSideConnection) ExtNotification(ctx context.Context, method string, params any) error {
	return c.conn.SendNotification(ctx, method, params)
}

// --- Incoming request handler ---

func (c *ClientSideConnection) handleIncomingMethod(ctx context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case ClientMethods.SessionUpdate:
		return unmarshalAndCallVoid(ctx, params, c.impl.SessionUpdate)
	case ClientMethods.SessionRequestPermission:
		return unmarshalAndCall(ctx, params, c.impl.RequestPermission)
	case ClientMethods.FSReadTextFile:
		return unmarshalAndCall(ctx, params, c.impl.ReadTextFile)
	case ClientMethods.FSWriteTextFile:
		return unmarshalAndCall(ctx, params, c.impl.WriteTextFile)
	case ClientMethods.TerminalCreate:
		return unmarshalAndCall(ctx, params, c.impl.CreateTerminal)
	case ClientMethods.TerminalOutput:
		return unmarshalAndCall(ctx, params, c.impl.TerminalOutput)
	case ClientMethods.TerminalRelease:
		return unmarshalAndCall(ctx, params, c.impl.ReleaseTerminal)
	case ClientMethods.TerminalWaitForExit:
		return unmarshalAndCall(ctx, params, c.impl.WaitForTerminalExit)
	case ClientMethods.TerminalKill:
		return unmarshalAndCall(ctx, params, c.impl.KillTerminalCommand)
	default:
		if handler, ok := c.impl.(ExtMethodHandler); ok {
			return handler.ExtMethod(ctx, method, params)
		}
		return nil, ErrMethodNotFound(method)
	}
}
