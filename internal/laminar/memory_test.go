package laminar

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
			attrs[string(attr.Key)] = attr.Value.Emit()
		}
		resourceAttrs := map[string]string{}
		for _, attr := range span.Resource().Attributes() {
			resourceAttrs[string(attr.Key)] = attr.Value.Emit()
		}
		parent := ""
		if span.Parent().IsValid() {
			parent = span.Parent().SpanID().String()
		}
		status := span.Status()
		snapshots = append(snapshots, spanSnapshot{
			Name:              span.Name(),
			TraceID:           span.SpanContext().TraceID().String(),
			SpanID:            span.SpanContext().SpanID().String(),
			ParentSpanID:      parent,
			StatusCode:        status.Code.String(),
			StatusDescription: status.Description,
			Attributes:        attrs,
			ResourceAttrs:     resourceAttrs,
		})
	}
	return snapshots
}

type spanSnapshot struct {
	Name              string
	TraceID           string
	SpanID            string
	ParentSpanID      string
	StatusCode        string
	StatusDescription string
	Attributes        map[string]string
	ResourceAttrs     map[string]string
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
