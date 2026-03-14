package acp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

// maxErrorBodySize is the maximum number of bytes read from an HTTP error response body.
const maxErrorBodySize = 1024

// ErrTransportClosed is returned when a message is sent on a closed transport.
var ErrTransportClosed = errors.New("transport closed")

// SSE framing constants, allocated once to avoid per-message allocations.
var (
	sseEventPrefix = []byte("event: message\ndata: ")
	sseEventSuffix = []byte("\n\n")
	sseDataPrefix  = []byte("data: ")
)

// HTTPServerTransport implements Transport for an HTTP-based agent server.
//
// The server receives JSON-RPC requests via HTTP POST and sends
// responses and notifications back to the client via Server-Sent Events (SSE).
//
// Endpoints:
//   - POST /message — client sends JSON-RPC messages to the agent
//   - GET /events — client opens an SSE stream for agent-to-client messages
type HTTPServerTransport struct {
	inbox     chan json.RawMessage
	outbox    chan json.RawMessage
	done      chan struct{}
	closeOnce sync.Once
}

// NewHTTPServerTransport creates a new HTTP server transport.
//
// Use Handler() to get an http.Handler to serve on your HTTP server.
// The returned transport implements Transport for use with NewConnection.
func NewHTTPServerTransport() *HTTPServerTransport {
	return &HTTPServerTransport{
		inbox:  make(chan json.RawMessage, 100),
		outbox: make(chan json.RawMessage, 100),
		done:   make(chan struct{}),
	}
}

func (t *HTTPServerTransport) ReadMessage(ctx context.Context) (json.RawMessage, error) {
	select {
	case msg := <-t.inbox:
		return msg, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-t.done:
		return nil, io.EOF
	}
}

func (t *HTTPServerTransport) WriteMessage(ctx context.Context, data json.RawMessage) error {
	select {
	case t.outbox <- data:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-t.done:
		return ErrTransportClosed
	}
}

func (t *HTTPServerTransport) Close() error {
	t.closeOnce.Do(func() { close(t.done) })
	return nil
}

// Handler returns an http.Handler that serves the ACP HTTP transport endpoints.
//
// Routes:
//   - POST /message — receives JSON-RPC messages from the client
//   - GET /events — serves the SSE stream for agent-to-client messages
func (t *HTTPServerTransport) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /message", t.handleMessage)
	mux.HandleFunc("GET /events", t.handleSSE)
	return mux
}

func (t *HTTPServerTransport) handleMessage(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxMessageSize))
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}

	select {
	case t.inbox <- json.RawMessage(body):
		w.WriteHeader(http.StatusAccepted)
	case <-t.done:
		http.Error(w, "transport closed", http.StatusServiceUnavailable)
	case <-r.Context().Done():
		http.Error(w, "request cancelled", http.StatusRequestTimeout)
	}
}

func (t *HTTPServerTransport) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	for {
		select {
		case msg := <-t.outbox:
			w.Write(sseEventPrefix)
			w.Write(msg)
			w.Write(sseEventSuffix)
			flusher.Flush()
		case <-t.done:
			return
		case <-r.Context().Done():
			return
		}
	}
}

// HTTPClientTransport implements Transport for an HTTP-based ACP client.
//
// The client sends JSON-RPC messages via HTTP POST and receives
// agent-to-client messages via an SSE stream.
//
// Call Connect before using the transport with a Connection.
type HTTPClientTransport struct {
	postURL     string
	sseURL      string
	client      *http.Client
	inbox       chan json.RawMessage
	done        chan struct{}
	closeOnce   sync.Once
	connectOnce sync.Once
	cancel      context.CancelFunc // cancels the SSE connection context
}

// HTTPClientOption configures an HTTPClientTransport.
type HTTPClientOption func(*HTTPClientTransport)

// WithHTTPClient sets the HTTP client to use for requests.
func WithHTTPClient(client *http.Client) HTTPClientOption {
	return func(t *HTTPClientTransport) { t.client = client }
}

// NewHTTPClientTransport creates a new HTTP client transport.
//
// Parameters:
//   - baseURL: The base URL of the agent's HTTP server (e.g., "http://localhost:8080")
//
// The transport automatically connects to:
//   - POST {baseURL}/message for sending messages
//   - GET {baseURL}/events for receiving SSE messages
func NewHTTPClientTransport(baseURL string, opts ...HTTPClientOption) *HTTPClientTransport {
	baseURL = strings.TrimRight(baseURL, "/")
	t := &HTTPClientTransport{
		postURL: baseURL + "/message",
		sseURL:  baseURL + "/events",
		client: http.DefaultClient,
		inbox:  make(chan json.RawMessage, 100),
		done:   make(chan struct{}),
		cancel: func() {}, // no-op until Connect is called
	}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

// Connect starts the SSE listener for receiving agent-to-client messages.
// This must be called exactly once before using the transport with a Connection.
// The provided context controls the SSE connection lifecycle.
func (t *HTTPClientTransport) Connect(ctx context.Context) error {
	var connectErr error
	t.connectOnce.Do(func() {
		sseCtx, cancel := context.WithCancel(ctx)
		t.cancel = cancel

		req, err := http.NewRequestWithContext(sseCtx, http.MethodGet, t.sseURL, nil)
		if err != nil {
			cancel()
			connectErr = fmt.Errorf("failed to create SSE request: %w", err)
			return
		}
		req.Header.Set("Accept", "text/event-stream")

		resp, err := t.client.Do(req)
		if err != nil {
			cancel()
			connectErr = fmt.Errorf("failed to connect to SSE: %w", err)
			return
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			cancel()
			connectErr = fmt.Errorf("SSE connection returned status %d", resp.StatusCode)
			return
		}

		go t.readSSE(resp.Body)
	})
	return connectErr
}

func (t *HTTPClientTransport) readSSE(body io.ReadCloser) {
	defer body.Close()
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, initialBufSize), maxMessageSize)

	for scanner.Scan() {
		line := scanner.Bytes()
		if bytes.HasPrefix(line, sseDataPrefix) {
			payload := line[len(sseDataPrefix):]
			cp := make(json.RawMessage, len(payload))
			copy(cp, payload)
			select {
			case t.inbox <- cp:
			case <-t.done:
				return
			}
		}
	}
}

func (t *HTTPClientTransport) ReadMessage(ctx context.Context) (json.RawMessage, error) {
	select {
	case msg := <-t.inbox:
		return msg, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-t.done:
		return nil, io.EOF
	}
}

func (t *HTTPClientTransport) WriteMessage(ctx context.Context, data json.RawMessage) error {
	select {
	case <-t.done:
		return ErrTransportClosed
	default:
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.postURL, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send message: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusAccepted {
		io.Copy(io.Discard, resp.Body)
		return nil
	}

	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodySize))
	switch {
	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		return fmt.Errorf("client error %d: %s", resp.StatusCode, body)
	case resp.StatusCode >= 500:
		return fmt.Errorf("server error %d: %s", resp.StatusCode, body)
	default:
		return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, body)
	}
}

func (t *HTTPClientTransport) Close() error {
	t.closeOnce.Do(func() {
		t.cancel() // cancel SSE context first to unblock readSSE
		close(t.done)
	})
	return nil
}
