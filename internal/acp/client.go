package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
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
	ctx    context.Context
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	reader io.Reader // stdout of copilot process; read by readLoop via json.Decoder

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

	cmd.Stderr = os.Stderr // route copilot stderr to container stderr; default is /dev/null when stdin/stdout are piped

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("acp: start copilot: %w", err)
	}

	c := &Client{
		ctx:      ctx,
		cmd:      cmd,
		stdin:    stdin,
		reader:   stdout,
		pending:  make(map[int]chan json.RawMessage),
		notifyCh: make(chan notification, 256), // large buffer — Copilot streams many updates
	}

	go c.readLoop()

	return c, nil
}

// readLoop reads NDJSON from copilot stdout and dispatches messages.
// Uses json.NewDecoder (not bufio.Scanner) so there is no line-length
// ceiling — Copilot can send arbitrarily large session/update payloads.
func (c *Client) readLoop() {
	dec := json.NewDecoder(bufio.NewReaderSize(c.reader, 1<<20)) // 1 MiB read buffer
	for {
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			if err != io.EOF {
				slog.Warn("acp: readLoop exiting", "err", err)
			} else {
				if exitErr := c.cmd.Wait(); exitErr != nil {
					slog.Warn("acp: copilot exited with error", "err", exitErr)
				} else {
					slog.Info("acp: copilot exited cleanly")
				}
			}
			return
		}
		if len(raw) == 0 {
			continue
		}

		// Peek at the id field to decide: response (has id) or notification (no id).
		var envelope struct {
			ID *int `json:"id"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			continue
		}

		if envelope.ID == nil {
			// Notification — no id field.
			var notif rpcNotification
			if err := json.Unmarshal(raw, &notif); err != nil {
				continue
			}
			select {
			case c.notifyCh <- notification{method: notif.Method, params: notif.Params}:
			default:
				// Drop if buffer full — caller is not draining fast enough.
			}
			continue
		}

		// Response — dispatch to the waiting caller by id.
		c.mu.Lock()
		ch, ok := c.pending[*envelope.ID]
		if ok {
			delete(c.pending, *envelope.ID)
		}
		c.mu.Unlock()

		if ok {
			// Send full raw bytes; caller re-parses into rpcResponse.
			ch <- json.RawMessage(append([]byte(nil), raw...))
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

	slog.Info("acp →", "method", method, "id", id, "payload", string(data))

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
		"protocolVersion":    acpProtocolVersion,
		"clientInfo":         map[string]any{"name": "vessel-driver", "version": "0.1.0"},
		"clientCapabilities": map[string]any{},
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
		"cwd":        cwd,
		"mcpServers": []any{},
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
//
// Copilot delivers the stop reason in two possible ways depending on the build:
//
//	a) In the session/prompt response result: {"stopReason":"end_turn"}
//	b) In a session/update notification's update object: {"stopReason":"end_turn"}
//	   followed by a session/prompt response with null/absent result.
//
// Both are handled: the loop captures any stopReason seen in notifications and
// falls back to it when the response result is nil or empty.
func (c *Client) Prompt(ctx context.Context, sessionID, content string, onUpdate func(update map[string]any)) (string, error) {
	params := map[string]any{
		"sessionId": sessionID,
		// "prompt" is the ACP spec field — NOT "messages".
		// Each entry must carry type:"text" and a "text" key.
		"prompt": []map[string]any{
			{
				"type": "text",
				"text": content,
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

	// capturedStopReason holds the stopReason seen in a session/update
	// notification.  Some Copilot builds deliver stopReason here instead of
	// (or in addition to) the session/prompt response result.
	var capturedStopReason string

	for {
		select {
		case notif := <-c.notifyCh:
			if notif.method == "session/update" {
				// Try to capture stopReason from the nested update object.
				// Shape: {"sessionId":"...","update":{"stopReason":"end_turn",...}}
				var params struct {
					Update json.RawMessage `json:"update"`
				}
				if err := json.Unmarshal(notif.params, &params); err == nil && len(params.Update) > 0 {
					var upd struct {
						StopReason string `json:"stopReason"`
					}
					if err := json.Unmarshal(params.Update, &upd); err == nil && upd.StopReason != "" {
						capturedStopReason = upd.StopReason
					}
				}
				if onUpdate != nil {
					var m map[string]any
					if err := json.Unmarshal(notif.params, &m); err == nil {
						onUpdate(m)
					}
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

			// Parse stopReason from the response result when present.
			if len(resp.Result) > 0 && string(resp.Result) != "null" {
				var result struct {
					StopReason string `json:"stopReason"`
				}
				if err := json.Unmarshal(resp.Result, &result); err != nil {
					return "", fmt.Errorf("acp: parse prompt result: %w", err)
				}
				if result.StopReason == "error" {
					return "error", fmt.Errorf("acp: prompt stopped with error")
				}
				if result.StopReason != "" {
					return result.StopReason, nil
				}
			}

			// Result was null/absent — Copilot delivered stopReason via
			// session/update notifications.  Use what we captured, or default
			// to end_turn (the server acked completion without an error).
			if capturedStopReason != "" {
				return capturedStopReason, nil
			}
			return "end_turn", nil

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
