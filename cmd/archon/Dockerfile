# Archon — Legion controller binary
# Multi-stage: golang:1.21-alpine builder → scratch runtime
#
# Build (from repo root):
#   docker build -f cmd/archon/Dockerfile -t legion/archon:latest .
#
# The final image is FROM scratch: only the static binary + CA certs.
# Archon runs as root (required for /var/run/docker.sock access).

# ── Stage 1: builder ──────────────────────────────────────────────────────────
FROM golang:1.21-alpine AS builder

WORKDIR /build

# Copy module manifest first — layer caches dependency downloads separately
# from source changes, keeping incremental rebuilds fast.
COPY go.mod ./

# Copy full source and build.
# -ldflags="-s -w" strips debug symbols and DWARF info (~30% binary size reduction).
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o archon ./cmd/archon

# ── Stage 2: runtime ──────────────────────────────────────────────────────────
FROM scratch

# CA certificates — required for Archon to make TLS calls (Docker API, Beads/Dolt, GitHub)
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Static binary — the only process in the container
COPY --from=builder /build/archon /archon

ENTRYPOINT ["/archon"]
