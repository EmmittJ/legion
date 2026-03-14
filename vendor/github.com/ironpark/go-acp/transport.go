package acp

import (
	"context"
	"fmt"
	"os/exec"
)

// SpawnAgent starts an agent process and returns a ClientSideConnection connected to it.
//
// The agent process communicates via stdin/stdout using newline-delimited JSON-RPC.
// The process is automatically killed when the context is cancelled or the connection is closed.
//
// Parameters:
//   - ctx: Context for process lifecycle management
//   - client: The Client implementation that will handle incoming agent requests
//   - command: The command to execute (e.g., "my-agent")
//   - args: Arguments to pass to the command
func SpawnAgent(ctx context.Context, client Client, command string, args ...string) (*ClientSideConnection, error) {
	cmd := exec.CommandContext(ctx, command, args...)

	agentStdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdin pipe: %w", err)
	}

	agentStdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start agent process: %w", err)
	}

	// Create the connection: write to agent's stdin, read from agent's stdout
	conn := NewClientSideConnection(client, agentStdin, agentStdout)

	// Wait for process exit in background and clean up.
	// This goroutine always terminates because cmd.Wait returns when the process exits.
	go func() {
		_ = cmd.Wait()
		// Process exited — close the connection so Done() fires
		conn.Close()
	}()

	return conn, nil
}
