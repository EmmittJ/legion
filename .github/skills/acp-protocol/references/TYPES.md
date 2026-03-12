# ACP Go Types

Minimal Go type definitions for the vessel-driver ACP client. Stdlib only.

```go
package acp

import "encoding/json"

// --- JSON-RPC envelope types ---

type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int            `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

type Notification struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// --- initialize ---

type InitializeParams struct {
	ProtocolVersion    int        `json:"protocolVersion"`
	ClientInfo         ClientInfo `json:"clientInfo"`
	ClientCapabilities struct{}   `json:"clientCapabilities"`
}

type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type InitializeResult struct {
	ProtocolVersion    int         `json:"protocolVersion"`
	AgentCapabilities  interface{} `json:"agentCapabilities"`
	AgentInfo          ClientInfo  `json:"agentInfo"`
}

// --- session/new ---

type SessionNewParams struct {
	CWD        string      `json:"cwd"`
	MCPServers []MCPServer `json:"mcpServers"`
}

type MCPServer struct {
	Name    string   `json:"name"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
	Env     []string `json:"env"`
}

type SessionNewResult struct {
	SessionID string `json:"sessionId"`
}

// --- session/prompt ---

type SessionPromptParams struct {
	SessionID string         `json:"sessionId"`
	Prompt    []ContentBlock `json:"prompt"`
}

type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type SessionPromptResult struct {
	StopReason string `json:"stopReason"`
}

// --- session/update (notification) ---

type SessionUpdateParams struct {
	SessionID string          `json:"sessionId"`
	Update    json.RawMessage `json:"update"`
}

type UpdateBase struct {
	SessionUpdate string `json:"sessionUpdate"`
}

type AgentMessageChunk struct {
	SessionUpdate string       `json:"sessionUpdate"` // "agent_message_chunk"
	Content       ContentBlock `json:"content"`
}
```
