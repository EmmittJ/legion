# Go Testing Patterns for Legion

## Table-Driven Tests

```go
func TestWatcherExitCode(t *testing.T) {
    cases := []struct {
        name     string
        exitCode int
        want     string // expected Beads status
    }{
        {"success", 0, "closed"},
        {"failure", 1, "failed"},
        {"oom-kill", 137, "failed"},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            got := statusFromExitCode(tc.exitCode)
            if got != tc.want {
                t.Errorf("exitCode %d: got %q, want %q", tc.exitCode, got, tc.want)
            }
        })
    }
}
```

## Faking exec.Command (Subprocess Tests)

Use the "TestMain as subprocess" pattern to stub `docker` and `bd` calls:

```go
// In test file
func fakeDocker(t *testing.T) (path string, cleanup func()) {
    t.Helper()
    dir := t.TempDir()
    script := filepath.Join(dir, "docker")
    os.WriteFile(script, []byte("#!/bin/sh\necho 'abc123'\n"), 0755)
    return dir, func() {}
}

func TestPulseSpawn(t *testing.T) {
    fakeBin, cleanup := fakeDocker(t)
    defer cleanup()
    t.Setenv("PATH", fakeBin+":"+os.Getenv("PATH"))
    // Now exec.Command("docker", ...) calls our fake
}
```

## Testing Archon Loops with Context Cancellation

```go
func TestPulseLoopCancels(t *testing.T) {
    ctx, cancel := context.WithCancel(context.Background())
    done := make(chan struct{})
    go func() {
        runPulseLoop(ctx, testCfg)
        close(done)
    }()
    cancel()
    select {
    case <-done:
        // passed
    case <-time.After(2 * time.Second):
        t.Fatal("pulse loop did not stop after context cancel")
    }
}
```

## Testing ACP Client (Fake Stdio)

```go
func TestACPInitialize(t *testing.T) {
    // Pipe: client writes to agentIn, reads from agentOut
    agentIn, clientOut := io.Pipe()
    clientIn, agentOut := io.Pipe()

    // Fake agent: reads initialize, writes canned response
    go func() {
        dec := json.NewDecoder(agentIn)
        enc := json.NewEncoder(agentOut)
        var req Request
        dec.Decode(&req)
        enc.Encode(Response{
            JSONRPC: "2.0", ID: &req.ID,
            Result: json.RawMessage(`{"protocolVersion":1}`),
        })
    }()

    client := NewClient(clientOut, clientIn)
    result, err := client.Initialize(context.Background())
    if err != nil { t.Fatal(err) }
    if result.ProtocolVersion != 1 { t.Errorf("want 1, got %d", result.ProtocolVersion) }
}
```

## Build Validation Script

```bash
#!/bin/bash
set -euo pipefail
go build ./...
go vet ./...
go test -race -count=1 ./...
echo "All checks passed"
```
