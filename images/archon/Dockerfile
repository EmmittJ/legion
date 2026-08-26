# syntax=docker/dockerfile:1
# archon: Legion's reconciler daemon, containerized. Needs the Docker
# socket to summon vessels and a .legion/config.toml mounted in. It
# bootstraps its own Beads copy from the Dolt remote and syncs each tick.
#
# Build from the repo root:
#   docker build -f images/archon/Dockerfile -t legion/archon .

ARG GO_VERSION=1.26
FROM golang:${GO_VERSION}-trixie AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/archon ./cmd/archon

FROM debian:trixie-slim AS bd
ARG TARGETARCH
ARG BD_VERSION=1.2.2
ADD https://github.com/gastownhall/beads/releases/download/v${BD_VERSION}/beads_${BD_VERSION}_linux_${TARGETARCH}.tar.gz /tmp/bd.tar.gz
RUN tar -xzf /tmp/bd.tar.gz -C /usr/local/bin bd && chmod +x /usr/local/bin/bd

FROM debian:trixie-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates git \
    && rm -rf /var/lib/apt/lists/*

COPY --from=bd /usr/local/bin/bd /usr/local/bin/bd
COPY --from=build /out/archon /usr/local/bin/archon

# Runs as root: the Docker socket mount requires it in the default
# daemon setup. The archon touches nothing else on the host.
WORKDIR /legion
ENTRYPOINT ["archon"]
CMD ["-config", "/legion/.legion/config.toml"]
