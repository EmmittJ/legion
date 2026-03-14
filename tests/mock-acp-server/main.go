// mock-acp-server — scripted ACP server for vessel-driver integration testing.
//
// Replaces `copilot --acp --stdio` as the subprocess that vessel-driver spawns.
// Implements JSON-RPC 2.0 over newline-delimited stdio (NDJSON), covering the
// three-message ACP lifecycle: initialize → session/new → session/prompt.
//
// # Usage
//
// Build:
//
//	go build -o tests/mock-acp-server/mock-acp-server ./tests/mock-acp-server/
//
// Run vessel-driver against the mock instead of real Copilot:
//
//	COPILOT_CMD="./tests/mock-acp-server/mock-acp-server" vessel-driver \
//	    --issue <id> --repo <path>
//
// Flags:
//
//	--fail        Respond to session/prompt with stopReason:"refusal" instead
//	              of "end_turn".  Use to exercise vessel-driver's failure path.
//	--delay=100ms Sleep between each session/update notification to simulate
//	              realistic streaming.  Set to 0 to disable.
//
// The mock logs every received message to stderr so output appears in test
// logs without polluting the stdout JSON-RPC stream.
//
// # Protocol handled
//
//	→ initialize       → { protocolVersion:1, agentCapabilities:{streaming:true}, ... }
//	→ session/new      → { sessionId:"mock-session-abc123" }
//	→ session/prompt   → 3×session/update notifications, then { stopReason:"end_turn" }
//	→ shutdown         → exit 0
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Wire types
// ─────────────────────────────────────────────────────────────────────────────

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int            `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Result  any    `json:"result"`
}

type rpcNotification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

// ─────────────────────────────────────────────────────────────────────────────
// Main
// ─────────────────────────────────────────────────────────────────────────────

func main() {
	fail := flag.Bool("fail", false, "respond to session/prompt with stopReason:refusal")
	delay := flag.Duration("delay", 100*time.Millisecond, "delay between session/update notifications")
	flag.Parse()

	// All diagnostic output goes to stderr so it never corrupts the stdout stream.
	logger := log.New(os.Stderr, "[mock-acp] ", log.LstdFlags)

	enc := json.NewEncoder(os.Stdout)
	reader := bufio.NewReader(os.Stdin)

	logger.Printf("mock-acp-server started (fail=%v delay=%v)", *fail, *delay)

	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			// EOF or broken pipe — normal shutdown.
			logger.Printf("stdin closed: %v", err)
			return
		}
		if len(line) == 0 || (len(line) == 1 && line[0] == '\n') {
			continue
		}

		var req rpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			logger.Printf("ERROR unmarshal request: %v  (raw: %s)", err, line)
			continue
		}

		id := 0
		if req.ID != nil {
			id = *req.ID
		}

		logger.Printf("← method=%q id=%d", req.Method, id)

		switch req.Method {
		case "initialize":
			handleInitialize(enc, logger, id)

		case "session/new":
			handleSessionNew(enc, logger, id)

		case "session/prompt":
			handleSessionPrompt(enc, logger, id, *fail, *delay)

		case "shutdown":
			logger.Printf("shutdown received — exiting")
			os.Exit(0)

		default:
			logger.Printf("WARN unhandled method %q", req.Method)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Handlers
// ─────────────────────────────────────────────────────────────────────────────

// handleInitialize responds to the initialize handshake.
//
// Expected request:
//
//	{ "jsonrpc":"2.0", "id":0, "method":"initialize",
//	  "params":{"protocolVersion":1,"clientInfo":{...},"clientCapabilities":{}} }
//
// Response:
//
//	{ "jsonrpc":"2.0", "id":0,
//	  "result":{"protocolVersion":1,
//	            "agentCapabilities":{"streaming":true},
//	            "agentInfo":{"name":"mock-copilot","version":"0.1.0"}} }
func handleInitialize(enc *json.Encoder, logger *log.Logger, id int) {
	resp := rpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result: map[string]any{
			"protocolVersion": 1,
			"agentCapabilities": map[string]any{
				"streaming": true,
			},
			"agentInfo": map[string]any{
				"name":    "mock-copilot",
				"version": "0.1.0",
			},
		},
	}
	write(enc, logger, "initialize response", resp)
}

// handleSessionNew responds to the session/new request.
//
// Expected request:
//
//	{ "jsonrpc":"2.0", "id":1, "method":"session/new",
//	  "params":{"cwd":"/workspace","mcpServers":[...]} }
//
// Response:
//
//	{ "jsonrpc":"2.0", "id":1, "result":{"sessionId":"mock-session-abc123"} }
func handleSessionNew(enc *json.Encoder, logger *log.Logger, id int) {
	resp := rpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result: map[string]any{
			"sessionId": "mock-session-abc123",
		},
	}
	write(enc, logger, "session/new response", resp)
}

// handleSessionPrompt responds to the session/prompt request.
//
// Before the final response it emits three session/update notifications to
// simulate streaming output from the agent.  Each notification is separated
// by `delay` to give vessel-driver a realistic stream to drain.
//
// Normal response:   { ..., "result":{"stopReason":"end_turn"} }
// --fail response:   { ..., "result":{"stopReason":"refusal"} }
func handleSessionPrompt(enc *json.Encoder, logger *log.Logger, id int, fail bool, delay time.Duration) {
	updates := []string{"Thinking...", "Working on it...", "Done."}
	for _, text := range updates {
		notif := rpcNotification{
			JSONRPC: "2.0",
			Method:  "session/update",
			Params: map[string]any{
				"type":      "text",
				"text":      text,
				"sessionId": "mock-session-abc123",
			},
		}
		write(enc, logger, fmt.Sprintf("session/update %q", text), notif)
		if delay > 0 {
			time.Sleep(delay)
		}
	}

	stopReason := "end_turn"
	if fail {
		stopReason = "refusal"
	}
	logger.Printf("→ session/prompt response stopReason=%q", stopReason)

	resp := rpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result: map[string]any{
			"stopReason": stopReason,
		},
	}
	write(enc, logger, "session/prompt response", resp)
}

// ─────────────────────────────────────────────────────────────────────────────
// Helper
// ─────────────────────────────────────────────────────────────────────────────

// write encodes v as NDJSON to enc and logs any error to logger.
func write(enc *json.Encoder, logger *log.Logger, label string, v any) {
	if err := enc.Encode(v); err != nil {
		logger.Printf("ERROR writing %s: %v", label, err)
	}
}
