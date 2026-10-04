package laminar

import (
	"context"
	"github.com/kirilligum/codex-langfuse-tracer/internal/agenttrace"
	"github.com/kirilligum/codex-langfuse-tracer/internal/buildinfo"
	"strings"
	"testing"
)

func TestProgressiveStepsResolveUnderFinalRootWithoutDuplicateSpans(t *testing.T) {
	turn := completeTurn(t)
	full := &memoryExporter{}
	if err := EmitSpans(context.Background(), turn, "default", "host", buildinfo.DefaultServiceName, full); err != nil {
		t.Fatal(err)
	}
	expected := full.Snapshots()
	partial := turn
	partial.Completed = false
	partial.ExportDelta = true
	partial.Observations = turn.Observations[:1]
	partial.ModelCalls = turn.ModelCalls[:1]
	exporter := &memoryExporter{}
	if err := EmitSpans(context.Background(), partial, "default", "host", buildinfo.DefaultServiceName, exporter); err != nil {
		t.Fatal(err)
	}
	spans := exporter.Snapshots()
	if len(spans) != 2 {
		t.Fatalf("live span count=%d", len(spans))
	}
	parent := agenttrace.StableSpanID(turn.Profile().AgentSpanPrefix, turn.TraceID, turn.TurnID, "")
	for _, span := range spans {
		if span.ParentSpanID != parent || span.Name == "codex.agent" {
			t.Fatalf("unfinished span hierarchy: %+v", span)
		}
		if !strings.Contains(span.Attributes["lmnr.span.ids_path"], "00000000-0000-0000-"+parent[:4]+"-"+parent[4:]) {
			t.Fatalf("missing pending parent path: %+v", span.Attributes)
		}
	}
	turn.ExportDelta = true
	turn.FirstObservation = 1
	turn.FirstModelCall = 1
	if err := EmitSpans(context.Background(), turn, "default", "host", buildinfo.DefaultServiceName, exporter); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, span := range exporter.Snapshots() {
		if seen[span.SpanID] {
			t.Fatalf("span resent at finalization: %s", span.SpanID)
		}
		seen[span.SpanID] = true
	}
	if len(seen) != len(expected) {
		t.Fatalf("partial+final=%d full=%d", len(seen), len(expected))
	}
	for _, span := range expected {
		if !seen[span.SpanID] {
			t.Fatalf("missing stable span %s", span.SpanID)
		}
	}
}

func TestProgressiveProjectionRejectsInvalidCheckpoint(t *testing.T) {
	for _, first := range []int{-1, 100000} {
		turn := completeTurn(t)
		turn.ExportDelta = true
		turn.FirstObservation = first
		if err := EmitSpans(context.Background(), turn, "default", "host", buildinfo.DefaultServiceName, &memoryExporter{}); err == nil {
			t.Fatal("invalid checkpoint accepted")
		}
	}
}
