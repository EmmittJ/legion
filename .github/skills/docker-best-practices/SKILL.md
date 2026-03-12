---
name: docker-best-practices
description: >
  Dockerfile and Docker Compose best practices for Legion's container infrastructure.
  Apply when writing or reviewing Dockerfiles, docker-compose.yml, or vessel image
  configuration. Covers multi-stage builds, slim base images, layer caching, security
  hardening, and Legion-specific vessel image hierarchy. Activate for: any Dockerfile
  change, docker-compose.yml modification, vessel image build issues, or container
  security review.
license: MIT
metadata:
  version: "1.0"
  project: legion
---

## Legion Container Hierarchy

```
legion/vessel-base          ← debian:bookworm-slim + git + bd CLI + vessel-driver
    └── legion/vessel-copilot   ← vessel-base + copilot binary + GITHUB_TOKEN wiring

legion/archon               ← FROM scratch + static archon binary only
```

The base image uses `debian:bookworm-slim` (not `scratch`) because `git` requires libc.
The archon image uses `FROM scratch` because it's a fully static Go binary.

## Archon Dockerfile (FROM scratch)

```dockerfile
# Build stage — compile static binary
FROM golang:1.21-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /archon ./cmd/archon

# Runtime stage — nothing but the binary
FROM scratch
COPY --from=build /archon /archon
ENTRYPOINT ["/archon"]
```

Key flags: `CGO_ENABLED=0` (no libc dep), `-ldflags="-s -w"` (strip debug info, smaller binary).

## Vessel Base Dockerfile (debian:bookworm-slim)

```dockerfile
FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    git curl ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Pin bd to a specific version for reproducible builds
ARG BD_VERSION=latest
RUN curl -fsSL \
    https://github.com/steveyegge/beads/releases/download/${BD_VERSION}/bd-linux-amd64 \
    -o /usr/local/bin/bd && chmod +x /usr/local/bin/bd

COPY vessel-driver /usr/local/bin/vessel-driver
```

## Vessel Copilot Dockerfile

```dockerfile
FROM legion/vessel-base:latest

# Copilot binary (build-time COPY — requires binary in build context)
COPY copilot /usr/local/bin/copilot

# Never set GITHUB_TOKEN in the Dockerfile — inject at runtime via docker run -e
ENTRYPOINT ["/usr/local/bin/vessel-driver"]
```

## docker-compose.yml Pattern

```yaml
services:
  dolt:
    image: dolthub/dolt-sql-server:latest
    ports:
      - "3307:3306"
    volumes:
      - dolt-data:/var/lib/dolt
    healthcheck:
      test: ["CMD", "mysqladmin", "ping", "-h", "127.0.0.1", "-P", "3306"]
      interval: 5s
      timeout: 3s
      retries: 10

  archon:
    build:
      context: .
      dockerfile: cmd/archon/Dockerfile
    environment:
      - DOLT_DSN=mysql://root@dolt:3306/legion
      - REPO_URL=${REPO_URL}
      - VESSEL_IMAGE=legion/vessel-copilot:latest
    depends_on:
      dolt:
        condition: service_healthy
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
    restart: unless-stopped

volumes:
  dolt-data:
```

## Layer Caching Rules

1. `COPY go.mod go.sum` + `go mod download` **before** `COPY . .` — module cache survives source changes
2. `apt-get install` in a single `RUN` with `&& rm -rf /var/lib/apt/lists/*` — one layer, no cache bloat
3. `ARG` before expensive steps to enable cache-busting only when the arg changes

## Security Checklist

- [ ] No secrets in Dockerfile — `GITHUB_TOKEN`, `DOLT_DSN`, etc. are runtime env vars only
- [ ] Non-root user for vessel containers: `RUN useradd -r vessel && USER vessel`
- [ ] Pin base image tags — not `latest` in production builds
- [ ] `--no-install-recommends` on all `apt-get install` calls
- [ ] Strip debug symbols with `-ldflags="-s -w"` on all Go builds
- [ ] Docker socket mount (`/var/run/docker.sock`) only on archon — never on vessels

## Common Mistakes

| Mistake | Fix |
|---|---|
| `COPY . .` before `go mod download` | Layer cache busted on every source change |
| `latest` tag on vessel image in compose | Non-reproducible; pin with a digest or tag |
| Secrets in ENV in Dockerfile | Move to `docker run -e` or compose `environment:` from host env |
| `apt-get install` without `--no-install-recommends` | Bloated image; extra attack surface |
| Missing `healthcheck` on dolt | Archon starts before DB is ready; race condition |

See [references/COMPOSE.md](references/COMPOSE.md) for the full docker-compose reference.
