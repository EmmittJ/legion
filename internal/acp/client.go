package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultRequestTimeout = 30 * time.Second
	acpProtocolVersion    = 1
)

type Client struct {
	ctx     context.Context
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	scanner *bufio.Scanner

	mu       sync.Mutex
	pending  map[int]chan json.RawMessage
	notifyCh chan notification

	nextID atomic.Int64
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

type rpcNotification struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int            `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type notification struct {
	method string
	params json.RawMessage
}

// New starts the ACP server process and begins reading its stdout.
// If model is non-empty, it is passed to the server via --model.
func New(ctx context.Context, model string) (*Client, error) {
	args := []string{"--acp", "--stdio"}
	if model != "" {
		args = append(args, "--model", model)
	}
	cmd := exec.CommandContext(ctx, "copilot", args...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("acp: stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("acp: stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("acp: start copilot: %w", err)
	}

	c := &Client{
		ctx:      ctx,
		cmd:      cmd,
		stdin:    stdin,
		scanner:  bufio.NewScanner(stdout),
		pending:  make(map[int]chan json.RawMessage),
		notifyCh: make(chan notification, 64),
	}

	go c.readLoop()

	return c, nil
}

// readLoop reads NDJSON lines from copilot stdout and dispatches them.
func (c *Client) readLoop() {
	for c.scanner.Scan() {
		line := c.scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var envelope struct {
			ID *int `json:"id"`
		}
		if err := json.Unmarshal(line, &envelope); err != nil {
			continue
		}

		if envelope.ID == nil {
			// Notification — no id field.
			var notif rpcNotification
			if err := json.Unmarshal(line, &notif); err != nil {
				continue
			}
			select {
			case c.notifyCh <- notification{method: notif.Method, params: notif.Params}:
			default:
				// Drop if buffer full — caller is not draining fast enough.
			}
			continue
		}

		// Response — matched by id.
		var resp rpcResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			continue
		}

		c.mu.Lock()
		ch, ok := c.pending[*resp.ID]
		if ok {
			delete(c.pending, *resp.ID)
		}
		c.mu.Unlock()

		if ok {
			rawLine := make([]byte, len(line))
			copy(rawLine, line)
			ch <- rawLine
		}
	}
}

// send writes a JSON-RPC request to the copilot process stdin.
func (c *Client) send(id int, method string, params any) error {
	req := rpcRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}
	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("acp: marshal request: %w", err)
	}
	data = append(data, '\n')

	_, err = c.stdin.Write(data)
	if err != nil {
		return fmt.Errorf("acp: write request: %w", err)
	}
	return nil
}

// call sends a request and waits for a matching response, respecting timeout.
func (c *Client) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := int(c.nextID.Add(1))
	ch := make(chan json.RawMessage, 1)

	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()

	if err := c.send(id, method, params); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, err
	}

	select {
	case raw := <-ch:
		var resp rpcResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			return nil, fmt.Errorf("acp: unmarshal response: %w", err)
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("acp: rpc error %d: %s", resp.Error.Code, resp.Error.Message)
		}
		return resp.Result, nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("acp: %s timed out: %w", method, ctx.Err())
	}
}

// Initialize sends InitializeRequest and returns the protocol version and server capabilities.
func (c *Client) Initialize() (int, map[string]any, error) {
	ctx, cancel := context.WithTimeout(c.ctx, defaultRequestTimeout)
	defer cancel()

	params := map[string]any{
		"protocolVersion": acpProtocolVersion,
		"capabilities":    map[string]any{},
	}

	slog.Info("sent initialize request")
	result, err := c.call(ctx, "initialize", params)
	if err != nil {
		return 0, nil, fmt.Errorf("acp: initialize: %w", err)
	}

	var initResult struct {
		ProtocolVersion int            `json:"protocolVersion"`
		Capabilities    map[string]any `json:"agentCapabilities"`
	}
	if err := json.Unmarshal(result, &initResult); err != nil {
		return 0, nil, fmt.Errorf("acp: parse initialize result: %w", err)
	}

	capabilities := initResult.Capabilities
	if capabilities == nil {
		capabilities = map[string]any{}
	}
	slog.Info(fmt.Sprintf("received initialize response with %d capabilities", len(capabilities)))

	return initResult.ProtocolVersion, capabilities, nil
}

// NewSession creates a new ACP session and returns the session ID.
// It drains session/update notifications until session/ready is received.
func (c *Client) NewSession(cwd string) (string, error) {
	ctx, cancel := context.WithTimeout(c.ctx, defaultRequestTimeout)
	defer cancel()

	params := map[string]any{
		"cwd": cwd,
		"tools": []map[string]any{
			{
				"type":    "mcp",
				"name":    "beads",
				"command": "bd",
				"args":    []string{"mcp"},
			},
		},
	}

	id := int(c.nextID.Add(1))
	ch := make(chan json.RawMessage, 1)

	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()

	if err := c.send(id, "session/new", params); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return "", fmt.Errorf("acp: session/new send: %w", err)
	}

	// Drain notifications until session/ready or the response arrives.
	var sessionID string
	for {
		select {
		case notif := <-c.notifyCh:
			if notif.method == "session/ready" {
				var ready struct {
					SessionID string `json:"sessionId"`
				}
				if err := json.Unmarshal(notif.params, &ready); err == nil && ready.SessionID != "" {
					sessionID = ready.SessionID
				}
			}
		case raw := <-ch:
			var resp rpcResponse
			if err := json.Unmarshal(raw, &resp); err != nil {
				return "", fmt.Errorf("acp: parse session/new response: %w", err)
			}
			if resp.Error != nil {
				return "", fmt.Errorf("acp: session/new error %d: %s", resp.Error.Code, resp.Error.Message)
			}
			if sessionID == "" {
				// Try to parse session ID from the response result as fallback.
				var result struct {
					SessionID string `json:"sessionId"`
				}
				if err := json.Unmarshal(resp.Result, &result); err == nil {
					sessionID = result.SessionID
				}
			}
			if sessionID == "" {
				return "", fmt.Errorf("acp: session/new: no session ID in response or notifications")
			}
			return sessionID, nil
		case <-ctx.Done():
			c.mu.Lock()
			delete(c.pending, id)
			c.mu.Unlock()
			return "", fmt.Errorf("acp: session/new timed out: %w", ctx.Err())
		}
	}
}

// Prompt sends a prompt and streams session/update notifications to onUpdate.
// Returns the stop reason ("end_turn", "max_tokens", "error").
func (c *Client) Prompt(ctx context.Context, sessionID, content string, onUpdate func(update map[string]any)) (string, error) {
	params := map[string]any{
		"sessionId": sessionID,
		"messages": []map[string]any{
			{
				"role":    "user",
				"content": content,
			},
		},
	}

	id := int(c.nextID.Add(1))
	ch := make(chan json.RawMessage, 1)

	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()

	if err := c.send(id, "session/prompt", params); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return "", fmt.Errorf("acp: session/prompt send: %w", err)
	}

	for {
		select {
		case notif := <-c.notifyCh:
			if notif.method == "session/update" && onUpdate != nil {
				var update map[string]any
				if err := json.Unmarshal(notif.params, &update); err == nil {
					onUpdate(update)
				}
			}
		case raw := <-ch:
			var resp rpcResponse
			if err := json.Unmarshal(raw, &resp); err != nil {
				return "", fmt.Errorf("acp: parse prompt response: %w", err)
			}
			if resp.Error != nil {
				return "error", fmt.Errorf("acp: prompt error %d: %s", resp.Error.Code, resp.Error.Message)
			}

			var result struct {
				StopReason string `json:"stopReason"`
			}
			if err := json.Unmarshal(resp.Result, &result); err != nil {
				return "", fmt.Errorf("acp: parse prompt result: %w", err)
			}

			if result.StopReason == "error" {
				return "error", fmt.Errorf("acp: prompt stopped with error")
			}
			return result.StopReason, nil
		case <-ctx.Done():
			c.mu.Lock()
			delete(c.pending, id)
			c.mu.Unlock()
			return "", fmt.Errorf("acp: session/prompt timed out: %w", ctx.Err())
		}
	}
}

// Close sends shutdown and kills the process.
func (c *Client) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Best-effort shutdown notification.
	_ = c.send(int(c.nextID.Add(1)), "shutdown", nil)

	c.stdin.Close()

	done := make(chan error, 1)
	go func() { done <- c.cmd.Wait() }()

	select {
	case <-done:
	case <-ctx.Done():
		_ = c.cmd.Process.Kill()
	}
	return nil
}
