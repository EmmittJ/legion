package acp

import (
	"context"
)

// terminalClient is the subset of Client needed by TerminalHandle.
type terminalClient interface {
	TerminalOutput(ctx context.Context, params *TerminalOutputRequest) (*TerminalOutputResponse, error)
	WaitForTerminalExit(ctx context.Context, params *WaitForTerminalExitRequest) (*WaitForTerminalExitResponse, error)
	KillTerminalCommand(ctx context.Context, params *KillTerminalRequest) (*KillTerminalResponse, error)
	ReleaseTerminal(ctx context.Context, params *ReleaseTerminalRequest) (*ReleaseTerminalResponse, error)
}

// TerminalHandle represents a handle to a terminal session.
//
// This handle provides methods to interact with a terminal session
// created via CreateTerminal. It mirrors the TypeScript TerminalHandle
// implementation for consistent API across languages.
//
// The handle supports resource management patterns - always call Release()
// when done with the terminal to free resources.
//
// Note: This is an unstable feature and may be removed or changed.
type TerminalHandle struct {
	ID        string
	sessionID SessionID
	client    terminalClient
}

// NewTerminalHandle creates a new terminal handle.
func NewTerminalHandle(id string, sessionID SessionID, client terminalClient) *TerminalHandle {
	return &TerminalHandle{
		ID:        id,
		sessionID: sessionID,
		client:    client,
	}
}

// CurrentOutput gets the current terminal output without waiting for the command to exit.
func (t *TerminalHandle) CurrentOutput(ctx context.Context) (*TerminalOutputResponse, error) {
	return t.client.TerminalOutput(ctx, &TerminalOutputRequest{
		SessionID:  t.sessionID,
		TerminalID: t.ID,
	})
}

// WaitForExit waits for the terminal command to complete and returns its exit status.
func (t *TerminalHandle) WaitForExit(ctx context.Context) (*WaitForTerminalExitResponse, error) {
	return t.client.WaitForTerminalExit(ctx, &WaitForTerminalExitRequest{
		SessionID:  t.sessionID,
		TerminalID: t.ID,
	})
}

// Kill kills the terminal command without releasing the terminal.
func (t *TerminalHandle) Kill(ctx context.Context) error {
	_, err := t.client.KillTerminalCommand(ctx, &KillTerminalRequest{
		SessionID:  t.sessionID,
		TerminalID: t.ID,
	})
	return err
}

// Release releases the terminal and frees all associated resources.
//
// If the command is still running, it will be killed.
// After release, the terminal ID becomes invalid.
//
// **Important:** Always call this method when done with the terminal.
func (t *TerminalHandle) Release(ctx context.Context) error {
	_, err := t.client.ReleaseTerminal(ctx, &ReleaseTerminalRequest{
		SessionID:  t.sessionID,
		TerminalID: t.ID,
	})
	return err
}
