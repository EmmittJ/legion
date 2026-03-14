// Package telemetry initialises OpenTelemetry trace, metric, and log providers.
// Call Setup once at binary startup and defer the returned shutdown function.
package telemetry

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	promexporter "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// Setup initialises OpenTelemetry trace, metric, and log providers for serviceName.
//
// Trace exporter failures are non-fatal: a noop tracer is substituted so the
// binary runs without distributed tracing.  Prometheus exporter failure is
// treated as a hard error — /metrics cannot be served without it — but a noop
// meter is still returned so metric-call sites never need nil guards.
// Log exporter failures are non-fatal: slog continues writing JSON to stderr.
//
// The returned mux has /metrics registered via promhttp.Handler(); mount it on
// an HTTP server (e.g. :2112).  mux is nil on Prometheus failure.
func Setup(ctx context.Context, serviceName string) (
	tracer trace.Tracer,
	meter metric.Meter,
	mux *http.ServeMux,
	shutdown func(context.Context) error,
	err error,
) {
	// Collect shutdown funcs; buildShutdown fans them out at the end.
	var shutdowns []func(context.Context) error

	// ── slog: JSON to stderr initially; replaced by OTel bridge below ─────────
	slog.SetDefault(
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).With("service", serviceName),
	)

	// ── Resource: service name embedded in all telemetry ─────────────────────
	res, resErr := resource.New(ctx,
		resource.WithAttributes(semconv.ServiceNameKey.String(serviceName)),
	)
	if resErr != nil {
		slog.Warn("otel resource warning", "err", resErr)
		res = resource.Empty()
	}

	// ── Trace provider (OTLP HTTP → Jaeger/Tempo) ─────────────────────────────
	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if endpoint == "" {
		endpoint = "http://host.docker.internal:4318"
	}

	fmt.Fprintf(os.Stderr, "[TELEMETRY-DEBUG] Building trace opts for endpoint: %s\n", endpoint)
	traceOpts, parseErr := buildTraceOpts(endpoint)
	if parseErr != nil {
		slog.Warn("OTLP endpoint parse error, using noop tracer",
			"endpoint", endpoint, "err", parseErr)
		tracer = trace.NewNoopTracerProvider().Tracer(serviceName)
	} else {
		fmt.Fprintf(os.Stderr, "[TELEMETRY-DEBUG] Creating OTLP trace exporter...\n")
		traceExp, traceErr := otlptracehttp.New(ctx, traceOpts...)
		fmt.Fprintf(os.Stderr, "[TELEMETRY-DEBUG] OTLP trace exporter created: err=%v\n", traceErr)
		if traceErr != nil {
			slog.Warn("OTLP trace exporter init failed, using noop tracer", "err", traceErr)
			tracer = trace.NewNoopTracerProvider().Tracer(serviceName)
		} else {
			tp := sdktrace.NewTracerProvider(
				sdktrace.WithBatcher(traceExp),
				sdktrace.WithResource(res),
			)
			otel.SetTracerProvider(tp)
			tracer = tp.Tracer(serviceName)
			shutdowns = append(shutdowns, tp.Shutdown)
		}
	}

	// ── Metric provider (Prometheus) ──────────────────────────────────────────
	promExp, promErr := promexporter.New()
	if promErr != nil {
		// Hard failure for /metrics, but return a noop meter so callers never
		// need nil guards on metric instruments.
		slog.Error("prometheus exporter failed, metrics disabled", "err", promErr)
		meter = otel.GetMeterProvider().Meter(serviceName) // global default = noop
		shutdown = buildShutdown(shutdowns)
		return tracer, meter, nil, shutdown, fmt.Errorf("prometheus exporter: %w", promErr)
	}

	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(promExp),
		sdkmetric.WithResource(res),
	)
	otel.SetMeterProvider(mp)
	meter = mp.Meter(serviceName)
	shutdowns = append(shutdowns, mp.Shutdown)

	// ── /metrics ServeMux ─────────────────────────────────────────────────────
	mux = http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())

	// ── Log provider (TEMPORARILY DISABLED) ──────────────────────────────────
	// BLOCKED: OTel log batch processor buffers logs asynchronously, causing
	// startup logs and other critical diagnostics to disappear from stderr before
	// they're flushed. This needs a fix: switch to sync processor OR add explicit
	// flush after critical operations. For MVP, slog writes JSON directly to stderr
	// (configured above), which preserves all logs in real-time.
	// TODO(diablo): Re-enable with sync processor or per-message flush.

	shutdown = buildShutdown(shutdowns)
	return tracer, meter, mux, shutdown, nil
}

// buildShutdown returns a composite shutdown function that drains all providers.
// Each shutdown is called even if a previous one errors; all errors are joined.
func buildShutdown(fns []func(context.Context) error) func(context.Context) error {
	return func(ctx context.Context) error {
		var errs []string
		for _, fn := range fns {
			if e := fn(ctx); e != nil {
				errs = append(errs, e.Error())
			}
		}
		if len(errs) > 0 {
			return fmt.Errorf("telemetry shutdown: %s", strings.Join(errs, "; "))
		}
		return nil
	}
}

// buildTraceOpts converts an OTLP endpoint URL into otlptracehttp options.
// Scheme determines TLS: http → WithInsecure(), https → default TLS.
func buildTraceOpts(endpoint string) ([]otlptracehttp.Option, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse %q: %w", endpoint, err)
	}
	opts := []otlptracehttp.Option{
		otlptracehttp.WithEndpoint(u.Host),
	}
	if u.Scheme == "http" {
		opts = append(opts, otlptracehttp.WithInsecure())
	}
	if p := u.Path; p != "" && p != "/" {
		opts = append(opts, otlptracehttp.WithURLPath(p))
	}
	return opts, nil
}
