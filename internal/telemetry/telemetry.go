// Package telemetry is Legion's OTel foundation. Every binary calls Init
// exactly once; all spans, metrics, and logs flow through it. See ADR-0006.
package telemetry

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
)

// Attribute keys — Legion's semantic conventions (ADR-0006).
const (
	AttrBeadID     = "legion.bead.id"
	AttrVesselName = "legion.vessel.name"
	AttrPersona    = "legion.persona"
	AttrHarness    = "legion.harness"
	AttrBranch     = "legion.branch"
)

// Shutdown flushes and stops all telemetry pipelines.
type Shutdown func(context.Context) error

// Init configures global trace and metric providers, W3C propagation, and a
// correlated slog default logger for the named service. When
// OTEL_EXPORTER_OTLP_ENDPOINT is unset, exporters are omitted and telemetry
// is a cheap no-op — spans still propagate context, logs still correlate.
func Init(ctx context.Context, service string) (Shutdown, error) {
	res, err := resource.Merge(resource.Default(), resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName(service),
	))
	if err != nil {
		return nil, err
	}

	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))

	var shutdowns []Shutdown

	exporting := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != ""

	tpOpts := []sdktrace.TracerProviderOption{sdktrace.WithResource(res)}
	if exporting {
		te, err := otlptracehttp.New(ctx)
		if err != nil {
			return nil, err
		}
		tpOpts = append(tpOpts, sdktrace.WithBatcher(te))
	}
	tp := sdktrace.NewTracerProvider(tpOpts...)
	otel.SetTracerProvider(tp)
	shutdowns = append(shutdowns, tp.Shutdown)

	mpOpts := []sdkmetric.Option{sdkmetric.WithResource(res)}
	if exporting {
		me, err := otlpmetrichttp.New(ctx)
		if err != nil {
			return nil, err
		}
		mpOpts = append(mpOpts, sdkmetric.WithReader(
			sdkmetric.NewPeriodicReader(me, sdkmetric.WithInterval(15*time.Second)),
		))
	}
	mp := sdkmetric.NewMeterProvider(mpOpts...)
	otel.SetMeterProvider(mp)
	shutdowns = append(shutdowns, mp.Shutdown)

	slog.SetDefault(slog.New(newCorrelatedHandler(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: slogLevel(),
	}))))

	return func(ctx context.Context) error {
		var errs []error
		for _, s := range shutdowns {
			errs = append(errs, s(ctx))
		}
		return errors.Join(errs...)
	}, nil
}

func slogLevel() slog.Level {
	switch os.Getenv("LEGION_LOG_LEVEL") {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
