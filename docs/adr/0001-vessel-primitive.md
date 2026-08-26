# ADR-0001: Vessel is a first-party container primitive

**Status:** Accepted · 2026-08-26

## Context

v0 spawned containers via `docker run` shell-outs scattered through Archon. Lifecycle, log capture, and exit handling were ad hoc; testing required a live Docker daemon everywhere. Docker now ships a first-party, Go-idiomatic SDK (`github.com/docker/go-sdk`).

## Decision

`internal/vessel` owns the complete container lifecycle — `Spec`, `Summon`, `Watch`, `Logs`, `Reap` — implemented exclusively on `docker/go-sdk`. Archon consumes the primitive; it never touches Docker directly. One vessel ↔ one bead, enforced by labeling containers with `legion.bead.id`.

## Consequences

- Archon shrinks to a reconciler over two lists (ready beads, labeled containers).
- State reconstruction after a crash = query Docker for `legion.bead.id`-labeled containers; no state files.
- Unit tests fake the SDK client; only integration tests (build-tagged) need a daemon.
- Docker is the only supported runtime for v1 (compose for the archon itself); podman/k8s would be a new backend behind the same primitive.
