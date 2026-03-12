---
name: go-best-practices
description: >
  Idiomatic Go patterns for the Legion codebase. Apply when writing or reviewing Go code
  in cmd/archon, cmd/vessel-driver, cmd/lg, or internal/. Covers error handling, project
  structure, concurrency, stdlib use, and Go 1.21+ conventions. Activate for: any Go
  implementation task, code review of Go files, or debugging Go build/vet failures.
license: MIT
metadata:
  version: "1.0"
  project: legion
---

## Overview

Legion is three Go binaries (`archon`, `vessel-driver`, `lg`) sharing packages under
`internal/`. All code targets **Go 1.21+**. No framework dependencies — stdlib + Beads +
Cobra + Viper. Keep it simple; the MVP is ~500 lines of real code.

## Project Layout

```
legion/
├── cmd/
│   ├── archon/main.go          ← Archon binary entry point
│   ├── vessel-driver/main.go   ← Vessel Driver entry point
│   └── lg/main.go              ← lg CLI entry point
├── internal/
│   ├── acp/client.go           ← ACP JSON-RPC client (stdlib only, ~200 lines)
│   └── archon/                 ← pulse/watcher loop logic (shared)
└── go.mod
```

- `cmd/` — thin entry points only; real logic lives in `internal/`
- `internal/` — not importable outside the module; all shared packages go here
- No `pkg/` — there are no public libraries in this repo

## Error Handling

```go
// DO: wrap with context on every propagation
if err != nil {
    return fmt.Errorf("pulse loop: claim issue %s: %w", id, err)
}

// DO: sentinel errors for testable conditions
var ErrIssueNotFound = errors.New("issue not found")

// DO: errors.Is for inspection
if errors.Is(err, ErrIssueNotFound) { ... }

// DON'T: swallow errors
result, _ := bd.Show(id)   // ← never

// DON'T: panic in library code; only in main() for unrecoverable startup
```

## Exit Codes (Critical for Vessel Driver)

The vessel-driver exit code IS the signal to Archon's watcher loop:

```go
// Always update Beads status before os.Exit
if err := beads.UpdateStatus(issueID, "failed"); err != nil {
    log.Printf("warn: failed to update beads on error path: %v", err)
}
os.Exit(1)  // watcher marks issue failed

// On success
os.Exit(0)  // watcher sees clean exit; issue already marked closed by driver
```

## Concurrency (Archon Loops)

```go
// Pulse and Watcher run as goroutines — use a context for shutdown
ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
defer cancel()

go runPulseLoop(ctx, cfg)
go runWatcherLoop(ctx, cfg)
<-ctx.Done()
```

- Use `time.Ticker` for interval loops, not `time.Sleep`
- Pass `context.Context` as first argument to every function that needs cancellation
- Use a `sync.Map` or channel to share running-container state between loops without a mutex race

## Subprocess Calls (`docker`, `bd`)

```go
// Shell out using exec.CommandContext — respects ctx cancellation
cmd := exec.CommandContext(ctx, "docker", "run", "-d",
    "-e", "ISSUE_ID="+issueID,
    "-e", "DOLT_DSN="+cfg.DoltDSN,
    cfg.VesselImage,
)
out, err := cmd.Output()
if err != nil {
    return fmt.Errorf("docker run: %w", err)
}
containerID := strings.TrimSpace(string(out))
```

- Always use `CommandContext` not `Command` so long-running containers can be cancelled
- Capture both stdout and stderr: `cmd.CombinedOutput()` for diagnostics

## Defer and Cleanup

```go
f, err := os.Open(path)
if err != nil {
    return fmt.Errorf("open %s: %w", path, err)
}
defer f.Close()
```

- Every opened file, started process, and acquired lock gets a `defer` on the same line it's created

## Testing

```go
// Prefer table-driven tests
func TestPulseLoop(t *testing.T) {
    cases := []struct {
        name   string
        issues []string
        want   int
    }{
        {"no issues", nil, 0},
        {"one ready", []string{"bd-42"}, 1},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) { ... })
    }
}
```

See [references/TESTING.md](references/TESTING.md) for Legion-specific test patterns.

## Quick Reference

| Pattern | Rule |
|---|---|
| Error wrap | `fmt.Errorf("context: %w", err)` always |
| Error check | Immediately after call; never defer to later |
| Panic | Only in `main()` for unrecoverable startup |
| Exit codes | `os.Exit(0)` = done, `os.Exit(1)` = failed — always after Beads update |
| Loops | `time.Ticker` + `select { case <-ctx.Done() }` |
| Subprocesses | `exec.CommandContext` always |
| Packages | Logic in `internal/`, thin wiring in `cmd/` |
