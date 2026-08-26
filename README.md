# Legion

**File a bead; Legion works it.**

Legion turns tracked tasks into autonomous coding work. You file a **bead**
(a task in [Beads](https://github.com/gastownhall/beads), a git-backed,
Dolt-powered issue tracker); Legion's reconciler **summons** a **vessel** —
a container with an AI coding harness baked in — that clones your repo,
does the work, and pushes a branch. The bead closes when the vessel exits
clean.

```
Operator ── lg ──► Beads (embedded Dolt) ◄── Archon (reconciler)
                                                 │ summon / reap
                                          ┌──────┴──────┐
                                          │   Vessel    │  (docker/go-sdk)
                                          │ ┌─────────┐ │
                                          │ │ animus  │──ACP/stdio──► harness
                                          │ └────▲────┘ │   (copilot, claude,
                                          └──────┼──────┘    codex, opencode…)
                                            MCP tools
                                        (bead_get / trace / discover)
```

## Vocabulary

"Agent" is banned as a bare noun around here.

| Term | Meaning |
|---|---|
| **Bead** | The unit of work — a tracked task in Beads. |
| **Vessel** | The unit of execution — a container image with an ACP-speaking harness baked in. |
| **Harness** | The AI CLI inside the vessel (Copilot CLI, Claude Code, …). |
| **Animus** | Legion's in-vessel driver: ACP client downward to the harness, MCP server upward to the model. |
| **Persona** | A custom agent defined *in your repo* that the harness loads by name. Legion passes the name through, nothing more. |
| **Archon** | The reconciler daemon. Summons vessels for ready beads, reaps exits, and is the only thing that closes or fails a bead. |

## Quickstart

Prerequisites: [Docker](https://docs.docker.com/get-docker/),
[bd](https://github.com/gastownhall/beads) ≥ 1.2, Go ≥ 1.26 (to build), and a
`GH_TOKEN` with push access to your repo.

```sh
# 1. Build the binaries and images
go install ./cmd/lg
docker build -f images/vessel-base/Dockerfile    -t legion/vessel-base .
docker build -f images/vessel-copilot/Dockerfile -t legion/vessel-copilot .

# 2. In the repo you want Legion to work on
lg init                       # bd init + .legion/config.toml template
$EDITOR .legion/config.toml   # check repo_url and the vessel registry

# 3. Start the reconciler (containerized; or `go run ./cmd/archon` on the host)
docker compose up -d archon

# 4. File work and watch it happen
lg invoke "Add a health check endpoint" --vessel copilot
lg status
lg log <bead-id> -f
```

The result lands as branch `legion/<bead-id>` on your repo; the bead closes
automatically when the vessel exits clean, or reopens with a `FAILED:` trace
when it doesn't.

### Routing

Routing is data on the bead, stored as labels:

```sh
lg invoke "Review the auth module" --vessel claude --persona reviewer
```

- `--vessel` picks the image via the `[vessels]` registry in `.legion/config.toml`.
- `--persona` names a custom agent defined in *your* repo; the harness resolves
  it natively. Legion never parses, ships, or templates persona files.

### Observability

Every layer emits OpenTelemetry: one root trace per bead, from summon through
every ACP turn to reap. Bring up the local stack and point Legion at it:

```sh
docker compose --profile obs up -d
OTEL_EXPORTER_OTLP_ENDPOINT=http://host.docker.internal:4318 docker compose up -d archon
```

Grafana (Tempo + Prometheus pre-provisioned) is at http://localhost:3000.

## Layout

| Path | What |
|---|---|
| `cmd/lg` | Operator CLI: `init`, `invoke`, `status`, `log` |
| `cmd/archon` | Reconciler daemon |
| `cmd/animus` | In-vessel driver (also the MCP server, as `animus mcp`) |
| `internal/{bead,vessel,acp,archon,animus,config,telemetry}` | The layers |
| `images/` | `vessel-base`, `vessel-copilot`, `archon` Dockerfiles |
| `deploy/obs/` | OTel Collector, Tempo, Prometheus, Grafana configs |
| `docs/` | [Architecture](docs/architecture.md) and [ADRs](docs/adr/) |

## Development

```sh
go build ./... && go vet ./... && go test ./...
go test -tags integration ./internal/vessel   # needs a Docker daemon
```

Work is tracked in Beads (`bd ready`, `bd show <id>`). Conventional commits.
See [AGENTS.md](AGENTS.md) for the contributor contract and
[docs/architecture.md](docs/architecture.md) for the full design.
