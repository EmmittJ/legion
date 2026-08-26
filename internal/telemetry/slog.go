package telemetry

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/trace"
)

// correlatedHandler stamps every log record with the active trace and span
// IDs so logs join traces in the backend (ADR-0006).
type correlatedHandler struct {
	slog.Handler
}

func newCorrelatedHandler(h slog.Handler) slog.Handler {
	return &correlatedHandler{Handler: h}
}

func (h *correlatedHandler) Handle(ctx context.Context, r slog.Record) error {
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		r.AddAttrs(
			slog.String("trace_id", sc.TraceID().String()),
			slog.String("span_id", sc.SpanID().String()),
		)
	}
	return h.Handler.Handle(ctx, r)
}

func (h *correlatedHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &correlatedHandler{Handler: h.Handler.WithAttrs(attrs)}
}

func (h *correlatedHandler) WithGroup(name string) slog.Handler {
	return &correlatedHandler{Handler: h.Handler.WithGroup(name)}
}
