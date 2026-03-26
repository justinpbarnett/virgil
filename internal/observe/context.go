package observe

import "context"

type traceKey struct{}

type traceIDs struct {
	TraceID string
	SpanID  string
}

// WithTraceContext returns a copy of ctx carrying the given traceID and spanID.
// Components that receive this context can call TraceFromContext to attach
// events to the same trace without being passed IDs explicitly.
func WithTraceContext(ctx context.Context, traceID, spanID string) context.Context {
	return context.WithValue(ctx, traceKey{}, traceIDs{TraceID: traceID, SpanID: spanID})
}

// TraceFromContext returns the traceID and spanID stored in ctx.
// Returns empty strings if the context carries no trace.
func TraceFromContext(ctx context.Context) (traceID, spanID string) {
	if v, ok := ctx.Value(traceKey{}).(traceIDs); ok {
		return v.TraceID, v.SpanID
	}
	return "", ""
}
