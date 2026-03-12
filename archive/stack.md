# Legion Tech Stack

> Canonical language, framework, and dependency decisions for the Legion project.
> These are settled choices — not open questions.

---

## Language: Go

All Legion components are written in **Go 1.21+**.

**Why Go (not Python, not TypeScript):**

| Concern               | Go Answer                                                                                                                                 |
| --------------------- | ----------------------------------------------------------------------------------------------------------------------------------------- |
| Beads integration     | `import github.com/steveyegge/beads` — direct use of `types.Issue`, `storage.Storage`, all Beads types. No serialization layer.           |
| K8s controller        | `controller-runtime` + `client-go` are the canonical K8s controller libraries. Every production operator is Go.                           |
| Vessel Driver binary  | Compiles to a single static binary. Vessel base image can be `FROM scratch` + the binary + `git` + `bd`. No interpreter in the container. |
| CLI parity with Beads | `bd` is Cobra. `lg` follows the same structure — config loading, Dolt server auto-start, `--json` flags, all copy-paste viable.           |
| ACP protocol          | No official Go SDK yet; ACP is JSON-RPC over stdio. The Vessel Driver client is ~200 lines of standard library code.                      |

---

## Core Dependencies

### Archon (Controller)

| Package                          | Version | Purpose                                              |
| -------------------------------- | ------- | ---------------------------------------------------- |
| `sigs.k8s.io/controller-runtime` | v0.17+  | Reconciliation loop framework, informers, work queue |
| `k8s.io/client-go`               | v0.29+  | K8s API client (Jobs, Events, RBAC)                  |
| `k8s.io/api`                     | v0.29+  | K8s API types                                        |
| `k8s.io/apimachinery`            | v0.29+  | Object meta, runtime schemas                         |
| `github.com/steveyegge/beads`    | latest  | Beads types + storage interface                      |
| `github.com/dolthub/driver`      | v0.1+   | Dolt SQL driver (MySQL-compatible)                   |
| `github.com/go-sql-driver/mysql` | v1.8+   | MySQL driver (fallback)                              |
| `github.com/spf13/cobra`         | v1.8+   | CLI framework (same as `bd`)                         |
| `github.com/spf13/viper`         | v1.18+  | Configuration (same as `bd`)                         |
| `github.com/BurntSushi/toml`     | v1.3+   | Formula TOML parsing                                 |
| `go.opentelemetry.io/otel`       | v1.24+  | Observability / tracing                              |

### Vessel Driver

| Package                              | Purpose               |
| ------------------------------------ | --------------------- |
| `encoding/json` (stdlib)             | ACP JSON-RPC encoding |
| `bufio` + `os.Stdin/Stdout` (stdlib) | ACP stdio transport   |
| `github.com/steveyegge/beads`        | Beads read/write      |
| `github.com/dolthub/driver`          | Dolt connection       |

### `lg` CLI

Same dependency set as Archon minus `controller-runtime`. Mirrors `bd` CLI structure exactly.

---

## Storage: Dolt

**Dolt** is the storage engine for Beads.

- MySQL-compatible SQL interface — any Go MySQL driver works
- Git-native version control on tables (branch, merge, time-travel)
- DoltHub remote for Wasteland federation sync
- Auto-commit on every write — full audit trail for free

**Dolt features Legion relies on:**

| Feature             | Use                                        |
| ------------------- | ------------------------------------------ |
| `AS OF 'timestamp'` | Time-travel debugging of Molecule state    |
| `dolt_diff()`       | Observability dashboards                   |
| Branch + merge      | Shadow branch management in Beads          |
| DoltHub remotes     | Wasteland federation (`bd dolt push/pull`) |
| Commit history      | Immutable Wisp audit trail                 |

**Connection:** Dolt runs as a sidecar or separate pod (server mode) inside the cluster. Archon and Vessel Driver both connect via `mysql://` DSN. Beads' existing Dolt server auto-start logic is reused directly.

---

## Agent Protocol: ACP

**Agent Client Protocol** (v0.11.2, Apache-2.0) is the interface between the Vessel Driver and any AI agent backend.

- Transport: JSON-RPC over **stdio** (agent and driver in same container)
- Remote HTTP/WebSocket transport exists but is WIP — stdio is the production path
- The Vessel Driver is the ACP **client**; the AI agent is the ACP **server**

**Compatible backends (all interchangeable via container image swap):**

| Backend        | Container               | Auth Env Var           |
| -------------- | ----------------------- | ---------------------- |
| Claude Code    | `legion/vessel-claude`  | `ANTHROPIC_API_KEY`    |
| GitHub Copilot | `legion/vessel-copilot` | `GITHUB_TOKEN`         |
| Gemini CLI     | `legion/vessel-gemini`  | `GOOGLE_API_KEY`       |
| Goose          | `legion/vessel-goose`   | `GOOSE_PROVIDER` + key |
| Codex CLI      | `legion/vessel-codex`   | `OPENAI_API_KEY`       |

**No official Go ACP SDK** — the Vessel Driver implements the client directly using `encoding/json` + stdio. The protocol is simple enough that an SDK adds no meaningful value at this scale.

---

## Container Strategy

### Image Hierarchy

```
legion/vessel-base          ← git, bd (beads CLI), vessel-driver binary
    ├── legion/vessel-claude    ← claude-code binary + ANTHROPIC_API_KEY wiring
    ├── legion/vessel-copilot   ← gh copilot CLI + GITHUB_TOKEN wiring
    ├── legion/vessel-gemini    ← gemini CLI + GOOGLE_API_KEY wiring
    └── legion/vessel-gauntlet  ← test runner(s), linters, build tools
```

**Base image:** `FROM debian:bookworm-slim` (not scratch — `git` requires libc)

### Archon Image

```
FROM scratch
COPY archon /archon          ← static Go binary
ENTRYPOINT ["/archon"]
```

---

## Kubernetes

### Minimum Version

K8s 1.28+ (for Job backoff policy per-index, stable batch/v1 APIs)

### Resource Model

- **Archon:** Single `Deployment` (1 replica). Leader election via K8s Lease for HA.
- **Vessels:** `batch/v1 Job` with `restartPolicy: Never`. One Job per Molecule.
- **Beads:** `StatefulSet` (Dolt server) + PVC, or external managed MySQL.

### RBAC

Archon service account needs:

- `batch/v1 Jobs`: create, get, list, watch, delete
- `v1 Events`: create
- `v1 Secrets`: get (credential lookup for Vessel env injection)
- `v1 ConfigMaps`: get, list (formula storage if not in Beads)

### Local Development

Docker Compose (defined in `refs/k8s.md`) runs:

- `dolthub/dolt-sql-server` — Beads
- `archon` binary — controller
- `/var/run/docker.sock` mount — spawns Vessel containers as siblings

---

## Beads CLI (`bd`)

The `bd` binary ships inside every Vessel image. It is the Vessel's tool for reading and writing Beads:

```dockerfile
RUN curl -L https://github.com/steveyegge/beads/releases/latest/download/bd-linux-amd64 \
    -o /usr/local/bin/bd && chmod +x /usr/local/bin/bd
```

The Beads MCP server (`beads-mcp`, Python FastMCP) is injected into every ACP session via `mcpServers` in the `NewSessionRequest`. The AI agent sees Beads tools (`beads_create`, `beads_update`, `beads_close`, etc.) natively alongside its built-in tools.

---

## `lg` CLI Structure

Mirrors `bd` exactly — same Cobra + Viper pattern:

```
lg/
├── cmd/
│   ├── root.go         ← global flags, config loading, Dolt auto-start
│   ├── invoke.go       ← lg invoke
│   ├── status.go       ← lg status
│   ├── formula.go      ← lg formula list/show/validate
│   ├── wasteland.go    ← lg wasteland status/wanted/claim
│   ├── vessels.go      ← lg vessels
│   └── Beads.go     ← lg Beads query
├── internal/
│   ├── archon/         ← controller loops (shared with archon binary)
│   ├── formula/        ← TOML parser + expander
│   └── vessel/         ← Job template generation
└── main.go
```

---

## Not Used

| Technology                  | Reason                                                       |
| --------------------------- | ------------------------------------------------------------ |
| Python                      | No direct Beads import; weaker K8s controller ecosystem      |
| TypeScript/Node             | Same; static binary advantage lost                           |
| Helm                        | Overkill for Phase 1; plain YAML + `lg config` is sufficient |
| Service mesh                | Vessels don't talk to each other; all coordination via Beads |
| Message queue (Kafka, NATS) | Beads IS the queue; Dolt polling is sufficient at this scale |
