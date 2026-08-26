package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"sync"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
)

// fakeHarness speaks raw newline-delimited JSON-RPC on the far side of the
// pipes, standing in for a real ACP harness.
func fakeHarness(t *testing.T, in io.Reader, out io.Writer) {
	t.Helper()
	enc := json.NewEncoder(out)
	var mu sync.Mutex
	reply := func(v any) {
		mu.Lock()
		defer mu.Unlock()
		if err := enc.Encode(v); err != nil {
			t.Logf("fake harness write: %v", err)
		}
	}
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	for scanner.Scan() {
		var msg struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			t.Logf("fake harness parse: %v", err)
			continue
		}
		switch msg.Method {
		case "initialize":
			reply(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": map[string]any{
				"protocolVersion": 1,
			}})
		case "session/new":
			var p struct {
				Cwd        string            `json:"cwd"`
				McpServers []json.RawMessage `json:"mcpServers"`
			}
			_ = json.Unmarshal(msg.Params, &p)
			if p.Cwd != "/work/repo" {
				t.Errorf("session/new cwd = %q", p.Cwd)
			}
			if len(p.McpServers) != 1 {
				t.Errorf("session/new mcpServers = %d, want 1", len(p.McpServers))
			}
			reply(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": map[string]any{
				"sessionId": "sess-42",
			}})
		case "session/prompt":
			// Stream an update, then finish the turn.
			reply(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{
				"sessionId": "sess-42",
				"update": map[string]any{
					"sessionUpdate": "agent_message_chunk",
					"content":       map[string]any{"type": "text", "text": "working on it"},
				},
			}})
			reply(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": map[string]any{
				"stopReason": "end_turn",
			}})
		}
	}
}

func TestHandshakePromptAndUpdates(t *testing.T) {
	clientToAgent, agentIn := io.Pipe()
	agentToClient, clientIn := io.Pipe()
	t.Cleanup(func() {
		agentIn.Close()
		clientIn.Close()
	})
	go fakeHarness(t, clientToAgent, clientIn)

	var mu sync.Mutex
	var updates []string
	cfg := Config{
		Cwd: "/work/repo",
		McpServers: []acp.McpServer{
			StdioMcpServer("legion", "/usr/local/bin/animus", []string{"mcp"}, map[string]string{"LEGION_BEAD_ID": "legion-1"}),
		},
		OnUpdate: func(_ context.Context, n acp.SessionNotification) {
			mu.Lock()
			defer mu.Unlock()
			if c := n.Update.AgentMessageChunk; c != nil && c.Content.Text != nil {
				updates = append(updates, c.Content.Text.Text)
			}
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	s, err := connect(ctx, cfg, agentIn, agentToClient)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if s.ID() != "sess-42" {
		t.Errorf("session id = %q", s.ID())
	}

	stop, err := s.Prompt(ctx, "Do the thing (legion-1)")
	if err != nil {
		t.Fatalf("prompt: %v", err)
	}
	if stop != acp.StopReasonEndTurn {
		t.Errorf("stop reason = %q", stop)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(updates) != 1 || updates[0] != "working on it" {
		t.Errorf("updates = %v", updates)
	}
}

func TestPermissionAutoGrant(t *testing.T) {
	h := &headlessClient{}
	res, err := h.RequestPermission(context.Background(), acp.RequestPermissionRequest{
		Options: []acp.PermissionOption{
			{OptionId: "reject", Kind: acp.PermissionOptionKindRejectOnce},
			{OptionId: "once", Kind: acp.PermissionOptionKindAllowOnce},
			{OptionId: "always", Kind: acp.PermissionOptionKindAllowAlways},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome.Selected == nil || res.Outcome.Selected.OptionId != "always" {
		t.Errorf("outcome = %+v, want allow_always selected", res.Outcome)
	}

	res, err = h.RequestPermission(context.Background(), acp.RequestPermissionRequest{
		Options: []acp.PermissionOption{
			{OptionId: "reject", Kind: acp.PermissionOptionKindRejectOnce},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome.Cancelled == nil {
		t.Errorf("outcome = %+v, want cancelled when no allow offered", res.Outcome)
	}
}

func TestStdioMcpServer(t *testing.T) {
	s := StdioMcpServer("legion", "animus", []string{"mcp"}, map[string]string{"K": "V"})
	if s.Stdio == nil || s.Stdio.Name != "legion" || s.Stdio.Command != "animus" {
		t.Fatalf("bad server: %+v", s)
	}
	if len(s.Stdio.Env) != 1 || s.Stdio.Env[0].Name != "K" || s.Stdio.Env[0].Value != "V" {
		t.Errorf("env = %+v", s.Stdio.Env)
	}
}
