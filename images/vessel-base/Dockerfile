# syntax=docker/dockerfile:1
# vessel-base: everything a vessel needs except the harness.
# Per-harness images (vessel-copilot, …) build FROM this and bake in
# their ACP CLI plus HARNESS_CMD / HARNESS_PERSONA_FLAG.
#
# Build from the repo root:
#   docker build -f images/vessel-base/Dockerfile -t legion/vessel-base .

ARG GO_VERSION=1.26
FROM golang:${GO_VERSION}-trixie AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/animus ./cmd/animus

FROM debian:trixie-slim AS bd
ARG TARGETARCH
ARG BD_VERSION=1.2.2
ADD https://github.com/gastownhall/beads/releases/download/v${BD_VERSION}/beads_${BD_VERSION}_linux_${TARGETARCH}.tar.gz /tmp/bd.tar.gz
RUN tar -xzf /tmp/bd.tar.gz -C /usr/local/bin bd && chmod +x /usr/local/bin/bd

FROM debian:trixie-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates git curl \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --create-home --uid 1000 legion \
    && mkdir /work && chown legion:legion /work

COPY --from=bd /usr/local/bin/bd /usr/local/bin/bd
COPY --from=build /out/animus /usr/local/bin/animus

USER legion
ENV HOME=/home/legion \
    LEGION_WORK_DIR=/work
WORKDIR /work

# The animus possesses the vessel: bootstrap beads, clone, drive the
# harness over ACP, push the branch. Archon provides LEGION_BEAD_ID,
# LEGION_REPO_URL, and trace context via env.
ENTRYPOINT ["animus"]
