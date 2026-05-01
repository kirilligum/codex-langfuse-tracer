package langfuse

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel/trace"
)

type fixedIDGenerator struct {
	mu      sync.Mutex
	traceID trace.TraceID
	spanIDs []trace.SpanID
	next    int
}

func newFixedIDGenerator(traceIDHex string, spanIDHex []string) *fixedIDGenerator {
	traceID, _ := trace.TraceIDFromHex(traceIDHex)
	spanIDs := make([]trace.SpanID, 0, len(spanIDHex))
	for _, value := range spanIDHex {
		spanID, _ := trace.SpanIDFromHex(value)
		spanIDs = append(spanIDs, spanID)
	}
	return &fixedIDGenerator{traceID: traceID, spanIDs: spanIDs}
}

func (g *fixedIDGenerator) NewIDs(context.Context) (trace.TraceID, trace.SpanID) {
	return g.traceID, g.nextSpanID()
}

func (g *fixedIDGenerator) NewSpanID(context.Context, trace.TraceID) trace.SpanID {
	return g.nextSpanID()
}

func (g *fixedIDGenerator) nextSpanID() trace.SpanID {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.next >= len(g.spanIDs) {
		return trace.SpanID{}
	}
	spanID := g.spanIDs[g.next]
	g.next++
	return spanID
}
