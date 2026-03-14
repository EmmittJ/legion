package acp

//go:generate sh -c "cd internal/cmd/schema && go run . gen -config ../../../.schema.yaml"

import (
	"context"
	"encoding/json"
)

// Agent represents the interface that agents must implement to handle client requests.
//
// This interface defines the core methods that an agent must implement.
// Session lifecycle methods (NewSession, LoadSession, ListSessions) are handled
// automatically when a SessionStore is configured via [WithSessionStore].
// To override the default session management, implement the optional interfaces:
//   - [SessionCreator] for custom session/new handling
//   - [SessionLoader] for custom session/load handling
//   - [SessionLister] for custom session/list handling
//
// Other optional capabilities:
//   - [SessionForker] for session/fork (unstable)
//   - [SessionResumer] for session/resume (unstable)
//   - [SessionCloser] for session/close (unstable)
//   - [ModelSetter] for session/set_model (unstable)
//   - [ExtMethodHandler] for custom extension methods
//   - [ExtNotificationHandler] for custom extension notifications
//
// See protocol docs: [Agent](https://agentclientprotocol.com/protocol/overview#agent)
type Agent interface {
	// Initialize establishes the connection and negotiates capabilities.
	//
	// This is the first method called after connection establishment.
	// The agent should return its capabilities and supported protocol version.
	//
	// See protocol docs: [Initialization](https://agentclientprotocol.com/protocol/initialization)
	Initialize(ctx context.Context, params *InitializeRequest) (*InitializeResponse, error)

	// Authenticate handles user authentication using the specified method.
	//
	// Called when the client needs to authenticate the user with the agent.
	//
	// See protocol docs: [Authentication](https://agentclientprotocol.com/protocol/authentication)
	Authenticate(ctx context.Context, params *AuthenticateRequest) (*AuthenticateResponse, error)

	// SetSessionMode changes the current session mode.
	//
	// See protocol docs: [Session Modes](https://agentclientprotocol.com/protocol/session-modes)
	SetSessionMode(ctx context.Context, params *SetSessionModeRequest) (*SetSessionModeResponse, error)

	// SetSessionConfigOption updates a session configuration option.
	//
	// See protocol docs: [Session Config Options](https://agentclientprotocol.com/protocol/session-config-options)
	SetSessionConfigOption(ctx context.Context, params *SetSessionConfigOptionRequest) (*SetSessionConfigOptionResponse, error)

	// Prompt processes a user prompt and generates a response.
	//
	// This is the main method for handling user input and generating agent responses.
	//
	// See protocol docs: [User Message](https://agentclientprotocol.com/protocol/prompt-turn#1-user-message)
	Prompt(ctx context.Context, params *PromptRequest) (*PromptResponse, error)

	// Cancel cancels ongoing operations for a session.
	//
	// This is a notification method (no response expected).
	//
	// See protocol docs: [Cancellation](https://agentclientprotocol.com/protocol/prompt-turn#cancellation)
	Cancel(ctx context.Context, params *CancelNotification) error
}

// SessionCreator is an optional interface for agents that handle session creation.
//
// If not implemented and a SessionStore is configured via [WithSessionStore],
// session creation is handled automatically by the store.
type SessionCreator interface {
	NewSession(ctx context.Context, params *NewSessionRequest) (*NewSessionResponse, error)
}

// SessionLoader is an optional interface for agents that handle session loading.
//
// If not implemented and a SessionStore is configured via [WithSessionStore],
// session loading is handled automatically by the store.
type SessionLoader interface {
	LoadSession(ctx context.Context, params *LoadSessionRequest) (*LoadSessionResponse, error)
}

// SessionLister is an optional interface for agents that handle session listing.
//
// If not implemented and a SessionStore is configured via [WithSessionStore],
// session listing is handled automatically by the store.
type SessionLister interface {
	ListSessions(ctx context.Context, params *ListSessionsRequest) (*ListSessionsResponse, error)
}

// SessionForker is an optional interface for agents that support session forking (unstable).
//
// Implement this interface to allow clients to fork sessions.
type SessionForker interface {
	ForkSession(ctx context.Context, params *ForkSessionRequest) (*ForkSessionResponse, error)
}

// SessionResumer is an optional interface for agents that support session resuming (unstable).
//
// Implement this interface to allow clients to resume sessions without replaying history.
type SessionResumer interface {
	ResumeSession(ctx context.Context, params *ResumeSessionRequest) (*ResumeSessionResponse, error)
}

// SessionCloser is an optional interface for agents that support session closing (unstable).
//
// Implement this interface to allow clients to explicitly close sessions.
type SessionCloser interface {
	CloseSession(ctx context.Context, params *CloseSessionRequest) (*CloseSessionResponse, error)
}

// ModelSetter is an optional interface for agents that support model selection (unstable).
//
// Implement this interface to allow clients to change the model during a session.
type ModelSetter interface {
	SetSessionModel(ctx context.Context, params *SetSessionModelRequest) (*SetSessionModelResponse, error)
}

// ExtMethodHandler is an optional interface for handling custom extension methods.
//
// Implement this interface on Agent or Client to handle custom protocol extension
// methods. Extension methods should be prefixed with an underscore (e.g., "_myMethod").
type ExtMethodHandler interface {
	ExtMethod(ctx context.Context, method string, params json.RawMessage) (any, error)
}

// ExtNotificationHandler is an optional interface for handling custom extension notifications.
//
// Implement this interface on Agent or Client to handle custom protocol extension
// notifications. Extension methods should be prefixed with an underscore (e.g., "_myNotification").
type ExtNotificationHandler interface {
	ExtNotification(ctx context.Context, method string, params json.RawMessage) error
}

// Client represents the interface for communicating with the client from an agent.
//
// This interface provides methods that agents can use to request services
// from the client, such as file system access and permission requests.
//
// See protocol docs: [Client](https://agentclientprotocol.com/protocol/overview#client)
type Client interface {
	// SessionUpdate sends a session update notification to the client.
	//
	// Used to stream real-time progress and results during prompt processing.
	//
	// See protocol docs: [Agent Reports Output](https://agentclientprotocol.com/protocol/prompt-turn#3-agent-reports-output)
	SessionUpdate(ctx context.Context, params *SessionNotification) error

	// RequestPermission requests user permission for a tool call operation.
	//
	// Called when the agent needs user authorization before executing
	// a potentially sensitive operation.
	//
	// See protocol docs: [Requesting Permission](https://agentclientprotocol.com/protocol/tool-calls#requesting-permission)
	RequestPermission(ctx context.Context, params *RequestPermissionRequest) (*RequestPermissionResponse, error)

	// ReadTextFile reads content from a text file in the client's file system.
	//
	// Only available if the client supports the fs.readTextFile capability.
	//
	// See protocol docs: [FileSystem](https://agentclientprotocol.com/protocol/initialization#filesystem)
	ReadTextFile(ctx context.Context, params *ReadTextFileRequest) (*ReadTextFileResponse, error)

	// WriteTextFile writes content to a text file in the client's file system.
	//
	// Only available if the client supports the fs.writeTextFile capability.
	//
	// See protocol docs: [FileSystem](https://agentclientprotocol.com/protocol/initialization#filesystem)
	WriteTextFile(ctx context.Context, params *WriteTextFileRequest) (*WriteTextFileResponse, error)

	// CreateTerminal creates a new terminal session.
	//
	// Only available if the client supports the terminal capability.
	CreateTerminal(ctx context.Context, params *CreateTerminalRequest) (*CreateTerminalResponse, error)

	// TerminalOutput gets the current output and status of a terminal.
	TerminalOutput(ctx context.Context, params *TerminalOutputRequest) (*TerminalOutputResponse, error)

	// ReleaseTerminal releases a terminal and frees its resources.
	ReleaseTerminal(ctx context.Context, params *ReleaseTerminalRequest) (*ReleaseTerminalResponse, error)

	// WaitForTerminalExit waits for a terminal command to exit.
	WaitForTerminalExit(ctx context.Context, params *WaitForTerminalExitRequest) (*WaitForTerminalExitResponse, error)

	// KillTerminalCommand kills a terminal command without releasing the terminal.
	KillTerminalCommand(ctx context.Context, params *KillTerminalRequest) (*KillTerminalResponse, error)
}
