# ADR-0009: Archon becomes an AHP host

**Status:** Accepted (direction) · 2026-08-30 · implementation begins with a read-only spike

## Context

Legion's possessions run headless: the only windows into a live session are telemetry (ADR-0006/0007) and `lg log`. The [Agent Host Protocol](https://microsoft.github.io/agent-host-protocol/) (`microsoft/agent-host-protocol`, MIT, spec 0.9.0) is an open, LSP/DAP-style standard for exactly this gap: a **host** owns agent sessions as an authoritative state tree; any number of **clients** attach over JSON-RPC (typically WebSocket), receive a snapshot per subscribed channel (`ahp-root://`, `ahp-session:/<id>`, `ahp-chat:/<id>`, …), then apply `serverSeq`-ordered action envelopes through pure reducers. Clients are detachable viewers/controllers — VS Code's Agents window is one such client, `ahpx` (a third-party CLI) is another, and the arbitrary-external-host connection path is proven in both.

Decisive findings from research (2026-08-30, spec 0.9.0 @ `a2ae897`):

- **ACP-below-AHP is the documented intended composition** (`docs/guide/ahp-and-acp.md`): the host speaks AHP upstream to N clients and ACP downstream to agents. That diagram *is* Legion — Archon on top, animus/harness ACP sessions below.
- **Official Go packages exist**: `clients/go` ships `ahptypes` (generated wire types), `ahp` (client + generated reducers), `ahpws` (WebSocket transport); sole dependency `coder/websocket`. All six SDKs are **client-only** — there is no host SDK in any language; the host side is a hand-written dispatch loop over `ahptypes`.
- **The channels Legion needs are Stability-2** (Root, Session, Chat, Terminal, OTLP); churn is concentrated in non-stable channels (automations, annotations). Pre-1.0: breaking changes may land in MINOR releases (~every 2–3 weeks), so the host pins a spec version and negotiates via `ahptypes.SupportedProtocolVersions()`.
- **A minimal read-only host is small**: `initialize`, `ping`, `subscribe`, `unsubscribe`, `listSessions`, `reconnect`, plus pushed `action` / `root/session*` notifications and a replay ring buffer keyed by `serverSeq`.

## Decision

- **Archon is the AHP host.** One host, N sessions; sessions are host state and can outlive any client. The animus is not a host — it bridges upward: ACP `session/update` events flow to Archon, which translates them into AHP chat actions (`chat/turnStarted`, `chat/delta`, `chat/turnComplete`) and broadcasts to subscribers.
- **ACP stays the harness protocol** (ADR-0002), unchanged, inside the vessel. AHP is a client-facing surface only.
- **Vocabulary guard:** an AHP "session" maps to a possession; AHP "agents" in root state are vessel names. "Agent" still never enters Legion's nouns.
- Build on `ahptypes` for all wire types; write the server loop (WebSocket accept, JSON-RPC dispatch, snapshot + broadcast, `serverSeq` + replay buffer) in a new `internal/ahphost`. Pin spec 0.9.0.
- Auth: WebSocket connection token (validated at upgrade), per the reference server's model. No per-resource OAuth — Archon's sessions have no end-user auth flow.
- **Phase 1 (spike, read-only):** root + session + chat channels, `dispatchAction` rejected; attach VS Code Agents window and `ahpx watch` to a live possession and watch the turn stream. Exit criterion for the spike: a live possession visible and streaming in an external client.
- **Phase 2 (separate ADR):** steering — accepting client input mid-possession. Deliberately deferred: it must reconcile with "the bead is the source of truth" and "only Archon closes beads," and it touches `dispatchAction`/input-request semantics that deserve their own decision.

## Consequences

- Legion gets observation UIs for free — VS Code Agents window, ahpx, anything speaking AHP — without building or owning any client.
- Archon grows a WebSocket surface and per-session state mirrors (it already holds the reconciliation state; chat state is new). The ACP→AHP mapper is hand-written; no reference library exists.
- Pre-1.0 tracking cost: version pin + `release-metadata.json`/CHANGELOG watching. The Stability-2 designation of the needed channels has held across recent releases.
- On Kubernetes (ADR-0008), the AHP port becomes a Service; remote access via port-forward, ingress, or dev tunnels.
- The `ahp-otlp:` channel (stable) may later let AHP clients see Legion telemetry inline — complements, not replaces, ADR-0007.
