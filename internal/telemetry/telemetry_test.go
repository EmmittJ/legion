package telemetry

import (
	"context"
	"strings"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

func TestInitNoExporter(t *testing.T) {
	shutdown, err := Init(context.Background(), "test-service")
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

func TestEnvPropagationRoundTrip(t *testing.T) {
	if _, err := Init(context.Background(), "test-service"); err != nil {
		t.Fatalf("Init: %v", err)
	}

	tp := sdktrace.NewTracerProvider()
	ctx, span := tp.Tracer("test").Start(context.Background(), "bead.work")
	defer span.End()

	env := InjectEnv(ctx)
	tpv, ok := env[TraceparentEnv]
	if !ok || !strings.HasPrefix(tpv, "00-") {
		t.Fatalf("expected W3C traceparent in env, got %q", tpv)
	}

	got := ExtractEnv(context.Background(), func(k string) string { return env[k] })
	gotSC := trace.SpanContextFromContext(got)
	wantSC := span.SpanContext()
	if gotSC.TraceID() != wantSC.TraceID() {
		t.Fatalf("trace ID mismatch: got %s want %s", gotSC.TraceID(), wantSC.TraceID())
	}
	if gotSC.SpanID() != wantSC.SpanID() {
		t.Fatalf("span ID mismatch: got %s want %s", gotSC.SpanID(), wantSC.SpanID())
	}
}
