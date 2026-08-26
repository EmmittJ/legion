# syntax=docker/dockerfile:1
# vessel-copilot: vessel-base + GitHub Copilot CLI as the harness.
#
# Build from the repo root (after building vessel-base):
#   docker build --build-arg COPILOT_CLI_VERSION=1.2.3 \
#       -f images/vessel-copilot/Dockerfile -t legion/vessel-copilot .

ARG BASE=legion/vessel-base:latest
FROM node:22-trixie-slim AS node

FROM ${BASE}
ARG COPILOT_CLI_VERSION=latest
USER root
# Node runtime for the Copilot CLI, lifted from the official image.
COPY --from=node /usr/local/bin/node /usr/local/bin/node
COPY --from=node /usr/local/lib/node_modules /usr/local/lib/node_modules
RUN ln -s /usr/local/lib/node_modules/npm/bin/npm-cli.js /usr/local/bin/npm \
    && ln -s /usr/local/lib/node_modules/npm/bin/npx-cli.js /usr/local/bin/npx \
    && npm install -g @github/copilot@${COPILOT_CLI_VERSION} \
    && npm cache clean --force
USER legion

# The vessel contract: how the animus starts and steers this harness.
# Copilot CLI speaks ACP over stdio with --acp; personas (custom agents
# in the target repo's .github/agents/) are selected with --agent.
ENV HARNESS_CMD="copilot --acp --stdio" \
    HARNESS_PERSONA_FLAG="--agent"
