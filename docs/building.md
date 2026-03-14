# Building Legion's Docker Images

This guide covers every rebuild scenario: routine incremental builds, single-service
rebuilds, forced base-image refreshes, and manual vessel smoke tests.

---

## Prerequisites

| Requirement    | Minimum | Notes                                                            |
| -------------- | ------- | ---------------------------------------------------------------- |
| Docker Desktop | 4.x+    | Compose V2 (`docker compose`, no hyphen) is required             |
| Go toolchain   | 1.25+   | Only needed to run binaries locally; builds happen inside Docker |
| `bd` CLI       | 0.60.0+ | Required for Beads issue lifecycle; see [Setup](SETUP-WSL.md)    |

> **WSL note:** If you are on Windows, see [SETUP-WSL.md](SETUP-WSL.md) for Docker
> socket and volume-path gotchas before running any build commands.

---

## Image overview

Legion has two custom images. Both use multi-stage builds: a Go builder compiles the
binary inside Docker, and a slim `debian:bookworm-slim` runtime stage carries only the
final binary and baked-in tools.

| Image tag                      | Dockerfile                  | Compose service               | Go builder base        |
| ------------------------------ | --------------------------- | ----------------------------- | ---------------------- |
| `legion/archon:latest`         | `cmd/archon/Dockerfile`     | `archon`                      | `golang:1.26-alpine`   |
| `legion/vessel-copilot:latest` | `Dockerfile.vessel-copilot` | _(none — spawned at runtime)_ | `golang:1.25-bookworm` |

The vessel image is **not a Compose service**. Archon spawns vessel containers on
demand via `docker run`; they are ephemeral and removed after each task. You build the
vessel image separately (see below).

---

## Standard rebuild

```bash
docker compose build
```

This rebuilds both `archon` and any other Compose services that have a `build:` block.
**Use this by default.** Docker reuses cached layers automatically — a typical
incremental build (source change, no new deps) takes seconds.

### What gets rebuilt

`docker compose build` only triggers a rebuild for services defined with a `build:`
key. In Legion's `docker-compose.yml` that is currently only `archon`. The `dolt`,
`otel-collector`, `tempo`, `loki`, `prometheus`, and `grafana` services pull published
images and are unaffected.

To also rebuild the vessel image (which has no Compose service entry), run:

```bash
docker compose build
docker build -f Dockerfile.vessel-copilot -t legion/vessel-copilot:latest .
```

---

## Rebuild a single service

### Archon only

```bash
docker compose build archon
```

### Vessel image only

The vessel image is built directly — there is no `vessel` Compose service to target.

```bash
docker build -f Dockerfile.vessel-copilot -t legion/vessel-copilot:latest .
```

---

## Layer caching — why rebuilds are fast

Both Dockerfiles are structured so that Go module downloads are cached in a dedicated
layer that is independent of application source code.

**`Dockerfile.vessel-copilot` — reference pattern:**

```dockerfile
# Stage 1: build
FROM golang:1.25-bookworm AS builder
WORKDIR /src

# 1. Copy module manifests FIRST — this layer is invalidated only when go.mod or
#    go.sum change, not on every source edit.
COPY go.mod go.sum ./

# 2. Download all dependencies into the module cache.
#    Docker caches this layer. As long as go.mod/go.sum are unchanged, subsequent
#    builds skip the network entirely.
RUN go mod download

# 3. Copy the full source — changing any .go file invalidates only this layer
#    and the compile step below, not the dep-download layer above.
COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /vessel-driver ./cmd/vessel-driver/
```

**Consequence:** adding a new import (`go get`) updates `go.mod` and `go.sum`, which
invalidates the `go mod download` layer and re-downloads all dependencies. A pure
source change (editing an existing `.go` file) reuses the cached download layer and
only recompiles.

---

## When to use `--no-cache`

**Avoid `--no-cache` during normal development.** It discards every cached layer,
re-runs every `RUN` step, re-downloads all Go modules and all baked-in tools (`bd`,
`dolt`, `gh`, `docker`, `copilot`) from the internet — this takes several minutes and
burns bandwidth.

Use it only when you need to **force a base image refresh**, for example to pick up a
new `golang:1.26` patch release or a `debian:bookworm-slim` security update:

```bash
# Refresh the archon image, pulling the latest golang:1.26-alpine and bookworm-slim
docker compose build --no-cache archon

# Refresh the vessel image
docker build --no-cache -f Dockerfile.vessel-copilot -t legion/vessel-copilot:latest .
```

After using `--no-cache`, subsequent builds return to normal cached behaviour.

---

## Full stack restart after a rebuild

Rebuild then recreate all containers in one step:

```bash
docker compose build && docker compose up -d --force-recreate
```

`--force-recreate` replaces running containers with freshly started ones even if their
configuration has not changed — required after a rebuild to ensure Archon and Dolt are
running from the new image layers.

To tail logs after the restart:

```bash
docker compose logs -f archon
```

---

## Observability endpoints

Once the stack is up, the following UIs are available on your host:

| Service    | URL                   | Notes                                      |
| ---------- | --------------------- | ------------------------------------------ |
| Grafana    | http://localhost:3300 | Unified traces, logs, metrics — start here |
| Prometheus | http://localhost:9090 | Raw metrics query                          |
| Tempo      | via Grafana           | Distributed traces                         |
| Loki       | via Grafana           | Structured logs                            |

---

## Vessel smoke test

Use this to verify the vessel image works before deploying into the full stack.
The command replicates exactly what Archon passes to `docker run` when spawning a vessel.

```bash
docker run --rm \
  --network legion_legion-net \
  -e ISSUE_ID=<beads-issue-id> \
  -e REPO_URL=https://github.com/<owner>/<repo> \
  -e GITHUB_TOKEN=<ghp_xxx> \
  -e DOLT_HOST=dolt \
  -e DOLT_PORT=3306 \
  -e VESSEL_MODEL=gpt-5-mini \
  legion/vessel-copilot:latest
```

**Required env vars:**

| Variable       | Description                                            |
| -------------- | ------------------------------------------------------ |
| `ISSUE_ID`     | Beads issue the vessel will work on                    |
| `REPO_URL`     | Full HTTPS URL of the repository to clone              |
| `GITHUB_TOKEN` | Personal access token with `repo` scope                |
| `DOLT_HOST`    | Dolt hostname — use `dolt` when on `legion_legion-net` |
| `DOLT_PORT`    | Dolt SQL port — `3306` inside the Docker network       |
| `VESSEL_MODEL` | Copilot model identifier (e.g. `gpt-5-mini`)           |

**Notes:**

- `--network legion_legion-net` — Docker prefixes the network name with the Compose
  project name (`legion`). The project name is derived from the directory name unless
  overridden with `COMPOSE_PROJECT_NAME`. If your directory is named differently,
  adjust accordingly (`<project>_legion-net`).
- `--rm` — vessel containers are ephemeral; always pass this flag during smoke tests.
- The container's entrypoint is `/usr/local/bin/vessel-driver`. No command arguments
  are needed; vessel-driver reads all config from environment variables.
- The Dolt service must be running (`docker compose up -d dolt`) before the vessel
  can connect to it.

### Quick pre-flight check

```bash
# Verify the vessel image exists locally
docker image inspect legion/vessel-copilot:latest --format '{{.Id}}'

# Confirm the Dolt container is up and reachable on the legion-net
docker compose ps dolt
```

---

## Reference: manual `docker build` commands

When you need to build outside of Compose (e.g. in CI or to produce a tagged release):

```bash
# Archon — build context is the repo root
docker build \
  -f cmd/archon/Dockerfile \
  -t legion/archon:latest \
  .

# Vessel — build context is also the repo root (go.mod lives there)
docker build \
  -f Dockerfile.vessel-copilot \
  -t legion/vessel-copilot:latest \
  .
```

Both Dockerfiles use `.` as the build context because `go.mod` and the full module
source live at the repo root. Do not change the context path or the `COPY go.mod`
and `COPY . .` steps will fail.
