# ADR-0008: Vessels run as Kubernetes Jobs

**Status:** Accepted · 2026-08-30 · supersedes ADR-0001

## Context

ADR-0001 built `internal/vessel` on `docker/go-sdk`: one bead ↔ one labeled container, Archon reconciling ready beads against running containers. That primitive proved the model but binds Legion to a single Docker daemon: no scheduling, no resource quotas across nodes, ad-hoc restart semantics, and a bespoke exit-harvesting path (`ContainerWait`).

Kubernetes already provides exactly the lifecycle a vessel needs — run-to-completion, exit-code capture, timeout enforcement, log retention after death, label-based queries — as the **Job** primitive.

## Decision

- A summoned vessel is a Kubernetes **Job** (`legion-<bead-id>`), one pod, `restartPolicy: Never`, `backoffLimit: 0` (a failed possession is a failed bead — Archon decides retries, not the kubelet), `activeDeadlineSeconds` = bead timeout, `ttlSecondsAfterFinished` as a safety net behind Archon's explicit reap.
- `internal/vessel` keeps its interface — `Spec`, `Summon`, `Watch`, `Logs`, `Reap` — reimplemented on `client-go`. Archon still never touches the runtime directly.
- Labels move to k8s labels: `legion.dev/managed`, `legion.dev/bead-id`, `legion.dev/vessel`. Crash recovery is unchanged in spirit: list Jobs by label, no local state.
- **No CRD / operator framework.** Beads (ADR-0003) is the sole source of truth for work; a `Bead` CRD would create a second one. Archon stays a plain client-go reconciler (poll first; informers if tick latency ever matters).
- Traceparent, bead ID, and repo/token env flow into the pod spec env exactly as they did into container env (ADR-0006 propagation unchanged). Secrets move from raw env to k8s Secrets referenced by the pod.
- Archon itself runs as an in-cluster Deployment with a namespace-scoped Role: `jobs` create/get/list/watch/delete, `pods` get/list, `pods/log` get. `lg` talks to Beads as before and can use kubeconfig for `lg status`/`log`.
- Local dev target: **kind or k3d**; `deploy/` gains manifests (or a kustomize base) for namespace, archon, dashboard (ADR-0007), RBAC, secrets.
- Integration tests: build-tagged against a kind cluster (envtest covers only the API server — Jobs never run pods there — so fakes for unit, kind for integration).

## Consequences

- Vessels gain scheduling, per-pod resource requests/limits, multi-node capacity, and pod logs that survive death — for free.
- `docker/go-sdk` dependency drops; `k8s.io/client-go` enters. `images/` are unchanged — same OCI images, now pulled by kubelets (registry access required; no local-build shortcut).
- Dev loop gets heavier: a kind cluster instead of a bare docker daemon. `lg init` should bootstrap it.
- Exit harvesting becomes Job status + pod termination state instead of `ContainerWait` — better documented, but Archon must handle pod-level failure modes Docker never showed (ImagePullBackOff, evictions, OOMKilled) and map them onto bead failure.
- Docker as a runtime is gone, not abstracted: one backend, per ADR-0001's own escape clause ("k8s would be a new backend behind the same primitive").
