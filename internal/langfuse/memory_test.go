package langfuse

import (
	"context"
	"sync"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

type memoryExporter struct {
	mu    sync.Mutex
	spans []sdktrace.ReadOnlySpan
}

func (m *memoryExporter) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.spans = append(m.spans, spans...)
	return nil
}

func (m *memoryExporter) Shutdown(context.Context) error {
	return nil
}

func (m *memoryExporter) Snapshots() spanSnapshots {
	m.mu.Lock()
	defer m.mu.Unlock()
	snapshots := make(spanSnapshots, 0, len(m.spans))
	for _, span := range m.spans {
		attrs := map[string]string{}
		for _, attr := range span.Attributes() {
			attrs[string(attr.Key)] = attr.Value.AsString()
		}
		parent := ""
		if span.Parent().IsValid() {
			parent = span.Parent().SpanID().String()
		}
		snapshots = append(snapshots, spanSnapshot{
			Name:         span.Name(),
			TraceID:      span.SpanContext().TraceID().String(),
			SpanID:       span.SpanContext().SpanID().String(),
			ParentSpanID: parent,
			Attributes:   attrs,
		})
	}
	return snapshots
}

type spanSnapshot struct {
	Name         string
	TraceID      string
	SpanID       string
	ParentSpanID string
	Attributes   map[string]string
}

type spanSnapshots []spanSnapshot

func (s spanSnapshots) ByName(name string) spanSnapshot {
	for _, span := range s {
		if span.Name == name {
			return span
		}
	}
	return spanSnapshot{}
}
