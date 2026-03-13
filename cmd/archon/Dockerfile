# Archon — Legion controller binary
# Multi-stage: golang:1.26-alpine builder → debian:bookworm-slim runtime
#
# Build (from repo root):
#   docker build -f cmd/archon/Dockerfile -t legion/archon:latest .
#
# The final image is debian:bookworm-slim: static binary + bd CLI + CA certs.
# Archon runs as root (required for /var/run/docker.sock access).

# ── Stage 1: builder ──────────────────────────────────────────────────────────
FROM golang:1.26-alpine AS builder

WORKDIR /build

# Copy module manifest first — layer caches dependency downloads separately
# from source changes, keeping incremental rebuilds fast.
COPY go.mod ./

# Copy full source and build.
# -ldflags="-s -w" strips debug symbols and DWARF info (~30% binary size reduction).
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o archon ./cmd/archon

# ── Stage 2: runtime ──────────────────────────────────────────────────────────
FROM debian:bookworm-slim

# Install runtime deps: curl+ca-certs for bd download; bd itself for Beads CLI calls
RUN apt-get update \
    && apt-get install -y --no-install-recommends curl ca-certificates tar git \
    && rm -rf /var/lib/apt/lists/*

# Beads CLI — Archon shells out to bd for issue lifecycle management
RUN set -eux; \
    BD_VER=$(curl -fsSL https://api.github.com/repos/steveyegge/beads/releases/latest \
        | grep '"tag_name"' | sed 's/.*"v\([^"]*\)".*/\1/'); \
    curl -fsSL "https://github.com/steveyegge/beads/releases/download/v${BD_VER}/beads_${BD_VER}_linux_amd64.tar.gz" \
        -o /tmp/beads.tar.gz \
    && tar -xzf /tmp/beads.tar.gz -C /tmp \
    && mv /tmp/bd /usr/local/bin/bd \
    && chmod +x /usr/local/bin/bd \
    && rm -f /tmp/beads.tar.gz; true

# Dolt — bd init auto-starts dolt as a subprocess; must be present in PATH
RUN set -eux; \
    DOLT_VER=$(curl -fsSL https://api.github.com/repos/dolthub/dolt/releases/latest \
        | grep '"tag_name"' | sed 's/.*"v\([^"]*\)".*/\1/'); \
    curl -fsSL "https://github.com/dolthub/dolt/releases/download/v${DOLT_VER}/dolt-linux-amd64.tar.gz" \
        -o /tmp/dolt.tar.gz \
    && mkdir -p /tmp/dolt-extract \
    && tar -xzf /tmp/dolt.tar.gz -C /tmp/dolt-extract \
    && find /tmp/dolt-extract -type f -name 'dolt' -exec mv {} /usr/local/bin/dolt \; \
    && chmod +x /usr/local/bin/dolt \
    && rm -rf /tmp/dolt.tar.gz /tmp/dolt-extract

# Docker CLI — Archon shells out to docker run/stop/rm for vessel lifecycle
RUN set -eux; \
    curl -fsSL "https://download.docker.com/linux/static/stable/x86_64/docker-27.5.1.tgz" \
        -o /tmp/docker.tgz \
    && tar -xzf /tmp/docker.tgz --strip-components=1 -C /usr/local/bin/ docker/docker \
    && chmod +x /usr/local/bin/docker \
    && rm -f /tmp/docker.tgz

# GitHub CLI — `gh auth setup-git` wires git's credential helper so tokens
# never appear in remote URLs or log output
RUN set -eux; \
    GH_VER=$(curl -fsSL https://api.github.com/repos/cli/cli/releases/latest \
        | grep '"tag_name"' | sed 's/.*"v\([^"]*\)".*/\1/'); \
    curl -fsSL "https://github.com/cli/cli/releases/download/v${GH_VER}/gh_${GH_VER}_linux_amd64.tar.gz" \
        -o /tmp/gh.tar.gz \
    && mkdir -p /tmp/gh-extract \
    && tar -xzf /tmp/gh.tar.gz -C /tmp/gh-extract \
    && find /tmp/gh-extract -type f -name 'gh' -exec mv {} /usr/local/bin/gh \; \
    && chmod +x /usr/local/bin/gh \
    && rm -rf /tmp/gh.tar.gz /tmp/gh-extract

# CA certificates from builder (already in debian base, but keep for consistency)
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Static binary — the only application process in the container
COPY --from=builder /build/archon /archon

WORKDIR /app

# Entrypoint: initialize bd against the GitHub-backed embedded Dolt, then run archon
COPY cmd/archon/entrypoint.sh /entrypoint.sh
RUN sed -i 's/\r//' /entrypoint.sh && chmod +x /entrypoint.sh

ENTRYPOINT ["/entrypoint.sh"]
