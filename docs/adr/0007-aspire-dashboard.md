# ADR-0007: Aspire dashboard replaces the four-service obs stack

**Status:** Accepted · 2026-08-30 · amends ADR-0006

## Context

ADR-0006 shipped a compose `obs` profile of four services: OTel Collector, Prometheus, Tempo, and Grafana (`deploy/obs/`). That stack exists only to *display* telemetry Legion already emits over OTLP. It is four containers, four configs, and a Grafana datasource wiring exercise — heavy for a dev-loop tool whose operator wants "what happened to legion-x4f?" answered in one place.

The [Aspire dashboard](https://aspire.dev/) is a standalone, OTLP-native UI (`mcr.microsoft.com/dotnet/aspire-dashboard`): one container that ingests OTLP gRPC/HTTP directly and renders traces, structured logs, and metrics in a single correlated view. It is usable without any .NET/Aspire app model.

## Decision

- Replace `deploy/obs/` (collector, Prometheus, Tempo, Grafana + configs) with a single Aspire dashboard instance.
- No Go changes. Every binary keeps exporting via `internal/telemetry`; only `OTEL_EXPORTER_OTLP_ENDPOINT` retargets to the dashboard's OTLP ingestion port (4317 gRPC / 4318 HTTP).
- On Kubernetes (ADR-0008): one Deployment + Service (`legion-dashboard`), OTLP port cluster-internal, browser UI (18888) reached via port-forward or ingress. Browser access protected by the dashboard's token auth; OTLP ingestion restricted to the namespace.
- ADR-0006's telemetry conventions (`legion.*` attributes, one root span per bead, propagation discipline) are unchanged — only the sink is replaced.

## Consequences

- Obs deployment shrinks from 4 services + 4 config files to 1 container, zero config.
- Traces, logs, and metrics correlate in one UI keyed by trace ID — the "one query" goal of ADR-0006 gets cheaper.
- Telemetry is **in-memory and ephemeral**: bounded retention, lost on restart. Acceptable for the dev/operator loop; if durable history is ever needed, re-introduce an OTel Collector in front (dashboard remains one of N exporters) — no Legion code changes either way.
- No PromQL/alerting surface. Legion has no alerting requirement today; the collector-in-front escape hatch covers it later.
