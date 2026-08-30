# Legion Architecture

**File a bead; Legion works it.**

The bead is the unit of work. The vessel is the unit of execution. Everything else is machinery mapping one to the other.

## Vocabulary

"Agent" is banned as a bare noun. See [AGENTS.md](../AGENTS.md) for the canonical table: **bead** (work), **vessel** (body — image with harness baked in), **harness** (instrument — the ACP CLI inside), **animus** (spirit — Legion's in-container driver), **persona** (face — target-repo-defined custom agent), **archon** (reconciler).

Lifecycle verbs: Archon **summons** a vessel for a bead; the animus **possesses** it; on exit the vessel is **reaped** and the bead closed.

## System diagram

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

## Flow of one bead

1. `lg invoke "task" [--vessel claude] [--persona reviewer]` → bead created with labels `vessel:claude`, `persona:reviewer`.
2. Archon's reconcile tick: ready bead, no vessel → **summon**. `vessel:` label → image via the registry in `.legion/config.toml`; W3C traceparent + bead ID passed as env.
3. In-container, the animus: `bd dolt pull` → reads its bead → clones target repo → branches `legion/<bead-id>` → starts the harness as an ACP subprocess → `session/new` (injecting its own MCP server via `mcpServers`, persona name passed through) → prompts with bead title/description/criteria.
4. The model works; it can call `bead_get`, `bead_trace`, `bead_discover`, `bead_children` over MCP. The animus streams session updates into traces.
5. On turn end: animus commits nothing itself — the harness's work is pushed as `legion/<bead-id>`; animus writes final traces, `bd dolt push`, exits 0 (or nonzero).
6. Archon's reaper sees the exit → closes the bead (exit 0) or marks it failed (nonzero/timeout) → removes the container. **Only Archon closes beads** — a crashed vessel can never leave a zombie bead.

## Components & acceptance criteria

### `internal/telemetry`
OTel foundation: OTLP exporters, resource attrs, traceparent env propagation helpers, `slog` handler with trace/bead/vessel correlation, `legion.*` attribute constants (ADR-0006).
- ✅ One `Init(ctx, service)` used by all three binaries; no `log.Printf` anywhere in the repo.
- ✅ Span started in archon is the ancestor of animus + MCP spans (verified in e2e).

### `internal/bead`
`Bead` domain type + `bd` CLI wrapper: `Ready`, `Claim`, `Get`, `Trace`, `Close`, `Fail`, `Create` (discovered-from), label helpers for `vessel:`/`persona:`, dolt pull/push bootstrap.
- ✅ All bd calls use `--json`; wrapper is the only place `bd` is exec'd.
- ✅ Routing labels round-trip: set at create, readable at summon.

### `internal/vessel`
Container primitive on `docker/go-sdk`: `Spec` (image, env, mounts, limits), `Summon`, `Watch` (exit harvesting), `Logs` (streaming), `Reap`.
- ✅ No shell-outs to `docker`; SDK only.
- ✅ Vessel exit code + logs harvestable after container death.
- ✅ Unit tests with SDK-level fakes; integration test behind a build tag needing Docker.

### `internal/acp`
Session layer over `coder/acp-go-sdk` (ACP v1): start harness subprocess, initialize → session/new (with `mcpServers`) → prompt → stream updates → end_turn.
- ✅ SDK owns wire compat; this layer adds only lifecycle + trace hooks.
- ✅ Session updates surfaced as a channel/iterator the animus consumes.

### `cmd/archon`
Reconciler daemon: one tick = list ready beads + list running vessels → summon missing, reap finished. Sole authority to close/fail beads.
- ✅ Crash-safe: restart reconstructs state entirely from Beads + Docker; no local state file.
- ✅ Configurable concurrency cap and per-bead timeout.

### `cmd/animus`
In-vessel entrypoint (ACP client down, MCP server up). Tools: `bead_get`, `bead_trace`, `bead_discover`, `bead_children` — scoped to its bead + descendants, audited via Dolt history.
- ✅ No `bd` CLI or credentials in the model's toolspace; MCP tools are the only surface.
- ✅ Persona name passed through to harness; never parses persona files.

### `cmd/lg`
Operator CLI: `init` (bd init + `.legion/config.toml`), `invoke` (`--vessel`, `--persona` → labels), `status`, `log`.
- ✅ `lg init` ships no persona/prompt files.
- ✅ Human-readable output; `--json` for machines.

### `images/`
`vessel-base` (git, bd, animus) + per-harness images (`vessel-copilot` first).
- ✅ Multi-stage builds, slim runtime, non-root user.
- ✅ Adding a harness = one Dockerfile + one registry entry in config.

## Configuration (`.legion/config.toml`)

```toml
repo_url = "https://github.com/you/yourrepo"
default_vessel = "copilot"

[vessels]
copilot = "ghcr.io/emmittj/legion/vessel-copilot:latest"
claude  = "ghcr.io/emmittj/legion/vessel-claude:latest"

[archon]
poll_interval = "5s"
max_vessels = 3
bead_timeout = "30m"
```

## ADR index

| ADR | Decision |
|---|---|
| [0001](adr/0001-vessel-primitive.md) | Vessel is a first-party container primitive on `docker/go-sdk` |
| [0002](adr/0002-acp-v1-official-sdk.md) | ACP v1 via official `coder/acp-go-sdk`; no vendored fork |
| [0003](adr/0003-beads-embedded-dolt.md) | Beads (embedded Dolt) is the sole state store |
| [0004](adr/0004-vessel-registry-persona-passthrough.md) | Per-bead routing via labels; Legion owns zero persona formats |
| [0005](adr/0005-animus-mcp-server.md) | Animus serves scoped MCP tools into the session |
| [0006](adr/0006-otel-conventions.md) | OTel everywhere; `legion.*` semantic conventions |
| [0007](adr/0007-aspire-dashboard.md) | Aspire dashboard replaces the four-service obs stack |
| [0008](adr/0008-kubernetes-vessels.md) | Vessels run as Kubernetes Jobs; supersedes ADR-0001 |
| [0009](adr/0009-archon-ahp-host.md) | Archon becomes an AHP host; ACP stays the harness protocol |
