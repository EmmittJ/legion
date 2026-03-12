---
name: acp-protocol
description: >
  Agent Client Protocol (ACP) implementation guide for Legion's vessel-driver. Apply when
  implementing or reviewing internal/acp/client.go or cmd/vessel-driver/main.go. Covers
  the full message sequence: initialize → session/new → session/prompt → session/update
  stream → end_turn response. Transport is JSON-RPC 2.0 over stdio. No SDK — stdlib only.
  Activate for: writing the ACP client, debugging ACP handshake failures, reviewing
  vessel-driver wire behavior, or injecting the Beads MCP server into a session.
license: MIT
metadata:
  version: "1.0"
  project: legion
  spec-version: "ACP v1 (protocolVersion: 1)"
---

## Overview

ACP is **JSON-RPC 2.0 over newline-delimited stdio**. The vessel-driver is the **client**;
`copilot --acp --stdio` is the **server** (agent). The driver starts the agent as a
subprocess and speaks to it over the subprocess's stdin/stdout.

The full Legion sequence is 4 steps:

```
1. initialize          (version + capability negotiation)
2. session/new         (cwd=/workspace, inject beads MCP server)
3. session/prompt      (send the issue description as user message)
4. stream session/update notifications until session/prompt response arrives
```

## Transport

```go
// Start the agent subprocess
cmd := exec.Command("copilot", "--acp", "--stdio")
cmd.Stdin = os.Stdin   // or a pipe
stdin, _  := cmd.StdinPipe()
stdout, _ := cmd.StdoutPipe()
cmd.Start()

// All messages are newline-delimited JSON
enc := json.NewEncoder(stdin)
dec := json.NewDecoder(bufio.NewReader(stdout))
```

- Each message is one JSON object terminated by `\n`
- Requests have `jsonrpc`, `id`, `method`, `params`
- Notifications have `jsonrpc`, `method`, `params` (no `id`)
- Responses have `jsonrpc`, `id`, `result` (or `error`)

## Step 1 — Initialize

```json
// → Client sends
{
  "jsonrpc": "2.0",
  "id": 0,
  "method": "initialize",
  "params": {
    "protocolVersion": 1,
    "clientInfo": { "name": "vessel-driver", "version": "0.1.0" },
    "clientCapabilities": {}
  }
}

// ← Agent responds
{
  "jsonrpc": "2.0",
  "id": 0,
  "result": {
    "protocolVersion": 1,
    "agentCapabilities": { ... },
    "agentInfo": { "name": "copilot", ... }
  }
}
```

If `result.protocolVersion != 1`: log and exit 1. Do not proceed.

## Step 2 — session/new (with Beads MCP injection)

```json
// → Client sends
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "session/new",
  "params": {
    "cwd": "/workspace",
    "mcpServers": [
      {
        "name": "beads",
        "command": "bd",
        "args": ["mcp"],
        "env": []
      }
    ]
  }
}

// ← Agent responds
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": { "sessionId": "sess_abc123" }
}
```

Store `sessionId` — required for all subsequent calls.

## Step 3 — session/prompt

```json
// → Client sends
{
  "jsonrpc": "2.0",
  "id": 2,
  "method": "session/prompt",
  "params": {
    "sessionId": "sess_abc123",
    "prompt": [
      { "type": "text", "text": "<issue title>\n\n<issue description>" }
    ]
  }
}
```

## Step 4 — Stream session/update, wait for response

After sending `session/prompt`, enter a read loop. The agent sends **notifications**
(`session/update`, no `id`) interleaved with the final **response** (has matching `id: 2`).

```go
for {
    var msg json.RawMessage
    if err := dec.Decode(&msg); err != nil { ... }

    // Is it the response to our prompt (id == 2)?
    var response struct {
        ID     *int            `json:"id"`
        Result json.RawMessage `json:"result"`
        Error  json.RawMessage `json:"error"`
    }
    json.Unmarshal(msg, &response)

    if response.ID != nil && *response.ID == 2 {
        // Done — parse stopReason
        var result struct { StopReason string `json:"stopReason"` }
        json.Unmarshal(response.Result, &result)
        if result.StopReason == "end_turn" { break }
        // non-end_turn = treat as failure
    }

    // Otherwise it's a notification — parse and write trace to Beads
    var notif struct {
        Method string          `json:"method"`
        Params json.RawMessage `json:"params"`
    }
    json.Unmarshal(msg, &notif)
    if notif.Method == "session/update" {
        writeTraceToBeads(notif.Params)
    }
}
```

## Stop Reasons

| `stopReason` | Meaning | Action |
|---|---|---|
| `end_turn` | Agent finished successfully | git push, bd close, exit 0 |
| `max_tokens` | Token limit hit | bd update failed, exit 1 |
| `refusal` | Agent refused | bd update failed, exit 1 |
| `cancelled` | Client cancelled | exit 1 |

## Error Handling

```go
// JSON-RPC error response
if response.Error != nil {
    var rpcErr struct {
        Code    int    `json:"code"`
        Message string `json:"message"`
    }
    json.Unmarshal(response.Error, &rpcErr)
    return fmt.Errorf("ACP error %d: %s", rpcErr.Code, rpcErr.Message)
}
```

## Go Types (~200 lines, stdlib only)

See [references/TYPES.md](references/TYPES.md) for the minimal Go type definitions
covering all ACP messages needed by the vessel driver.

## Common Mistakes

- **Forgetting `\n` after each message** — the decoder blocks waiting for a newline
- **Reading stdout before starting the process** — start the process, get the pipes, then encode/decode
- **Treating notifications as errors** — notifications have no `id`; check for `id` presence before error-checking
- **Not draining `session/update` notifications** — the response arrives *after* all notifications; you must loop
