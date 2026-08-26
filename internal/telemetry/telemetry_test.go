package telemetry

import (
	"context"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/baggage"
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
	shutdown, err := Init(context.Background(), "test-service")
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() {
		if err := shutdown(context.Background()); err != nil {
			t.Fatalf("shutdown: %v", err)
		}
	})

	tp := sdktrace.NewTracerProvider()
	member, err := baggage.NewMember("dataset", "alpha")
	if err != nil {
		t.Fatalf("baggage.NewMember: %v", err)
	}
	bg, err := baggage.New(member)
	if err != nil {
		t.Fatalf("baggage.New: %v", err)
	}
	ctx := baggage.ContextWithBaggage(context.Background(), bg)
	ctx, span := tp.Tracer("test").Start(ctx, "bead.work")
	defer span.End()

	env := InjectEnv(ctx)
	tpv, ok := env[TraceparentEnv]
	if !ok || !strings.HasPrefix(tpv, "00-") {
		t.Fatalf("expected W3C traceparent in env, got %q", tpv)
	}
	if got := env[BaggageEnv]; !strings.Contains(got, "dataset=alpha") {
		t.Fatalf("expected baggage in env, got %q", got)
	}

	got := ExtractEnv(context.Background(), func(k string) string { return env[k] })
	if gotB := baggage.FromContext(got); gotB.String() == "" || !strings.Contains(gotB.String(), "dataset=alpha") {
		t.Fatalf("expected baggage to round trip, got %q", gotB.String())
	}
	gotSC := trace.SpanContextFromContext(got)
	wantSC := span.SpanContext()
	if gotSC.TraceID() != wantSC.TraceID() {
		t.Fatalf("trace ID mismatch: got %s want %s", gotSC.TraceID(), wantSC.TraceID())
	}
	if gotSC.SpanID() != wantSC.SpanID() {
		t.Fatalf("span ID mismatch: got %s want %s", gotSC.SpanID(), wantSC.SpanID())
	}
}
