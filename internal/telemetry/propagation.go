package telemetry

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

// TraceparentEnv is the environment variable carrying W3C trace context
// across the Docker boundary (archon → vessel → animus). ADR-0006.
const TraceparentEnv = "TRACEPARENT"

const tracestateEnv = "TRACESTATE"

// envCarrier adapts W3C env-style entries to the OTel TextMapCarrier
// interface.
type envCarrier map[string]string

func (c envCarrier) Get(key string) string {
	switch key {
	case "traceparent":
		return c[TraceparentEnv]
	case "tracestate":
		return c[tracestateEnv]
	}
	return ""
}

func (c envCarrier) Set(key, value string) {
	switch key {
	case "traceparent":
		c[TraceparentEnv] = value
	case "tracestate":
		c[tracestateEnv] = value
	}
}

func (c envCarrier) Keys() []string {
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	return keys
}

// InjectEnv returns TRACEPARENT/TRACESTATE entries for the active span
// context, ready to merge into a vessel's environment.
func InjectEnv(ctx context.Context) map[string]string {
	c := envCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, c)
	return c
}

// ExtractEnv resolves trace context from process environment values
// (e.g. inside the vessel, from an os.Getenv-style lookup).
func ExtractEnv(ctx context.Context, lookup func(string) string) context.Context {
	c := envCarrier{
		TraceparentEnv: lookup(TraceparentEnv),
		tracestateEnv:  lookup(tracestateEnv),
	}
	return otel.GetTextMapPropagator().Extract(ctx, propagation.TextMapCarrier(c))
}
