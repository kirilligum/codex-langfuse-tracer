package laminar

import (
	"context"
	"encoding/json"
	"github.com/kirilligum/codex-langfuse-tracer/internal/agenttrace"
	"github.com/kirilligum/codex-langfuse-tracer/internal/buildinfo"
	"strings"
	"testing"
)

// TEST-002: the same input event is projected for either existing provider.
func TestTurnInputBeforeModelWorkAndAtFirstCompletion(t *testing.T) {
	for _, provider := range []string{"codex", "claude"} {
		t.Run(provider, func(t *testing.T) {
			turn := agenttrace.Turn{
				Provider: provider, SessionID: "s", TurnID: "t",
				TraceID: agenttrace.StableTraceID(provider, "s", "t"),
				StartTS: "2026-10-04T12:00:00Z", UserMessages: []string{"inspect 👋 ghp_SECRET_SENTINEL_123456789"},
				ExportDelta: true,
			}
			partial := &memoryExporter{}
			if err := EmitSpans(context.Background(), turn, "default", "host", buildinfo.DefaultServiceName, partial); err != nil {
				t.Fatal(err)
			}
			spans := partial.Snapshots()
			if len(spans) != 1 || spans[0].Name != "turn.input" {
				t.Fatalf("prompt-only spans=%+v", spans)
			}
			input := spans[0]
			if input.ParentSpanID != agenttrace.StableSpanID(turn.Profile().AgentSpanPrefix, turn.TraceID, turn.TurnID, "") {
				t.Fatalf("input parent=%s", input.ParentSpanID)
			}
			if input.Attributes["lmnr.span.output"] != "" || input.Attributes["gen_ai.usage.input_tokens"] != "" {
				t.Fatalf("input carries output or usage: %+v", input.Attributes)
			}
			var prepared string
			if err := json.Unmarshal([]byte(input.Attributes["lmnr.span.input"]), &prepared); err != nil {
				t.Fatal(err)
			}
			if prepared != "inspect 👋 gh<redacted>" {
				t.Fatalf("unexpected prepared input=%q", prepared)
			}
			if input.Attributes[laminarMetadataPrefix+"cli_input_preview"] != prepared {
				t.Fatalf("preview differs from input: %+v", input.Attributes)
			}

			turn.Completed = true
			turn.AssistantTexts = []string{"done"}
			turn.EndTS = "2026-10-04T12:00:01Z"
			complete := &memoryExporter{}
			if err := EmitSpans(context.Background(), turn, "default", "host", buildinfo.DefaultServiceName, complete); err != nil {
				t.Fatal(err)
			}
			finalInput := complete.Snapshots().ByName("turn.input")
			if finalInput.SpanID != input.SpanID {
				t.Fatalf("input identity changed on completion: %s -> %s", input.SpanID, finalInput.SpanID)
			}
		})
	}
}

// TEST-002: Unicode previews use one bounded value derived from redacted input.
func TestTurnInputPreviewIsBoundedAfterRedaction(t *testing.T) {
	turn := agenttrace.Turn{
		Provider: "codex", SessionID: "s", TurnID: "t",
		TraceID: agenttrace.StableTraceID("codex", "s", "t"),
		StartTS: "2026-10-04T12:00:00Z", ExportDelta: true,
		UserMessages: []string{strings.Repeat("🙂", 2050) + " ghp_SECRET_SENTINEL_123456789"},
	}
	exporter := &memoryExporter{}
	if err := EmitSpans(context.Background(), turn, "default", "host", buildinfo.DefaultServiceName, exporter); err != nil {
		t.Fatal(err)
	}
	spans := exporter.Snapshots()
	if len(spans) != 1 {
		t.Fatalf("input spans=%d", len(spans))
	}
	preview := spans[0].Attributes[laminarMetadataPrefix+"cli_input_preview"]
	if len([]rune(preview)) != 2048 || len([]byte(preview)) != 8192 {
		t.Fatalf("preview codepoints=%d bytes=%d", len([]rune(preview)), len([]byte(preview)))
	}
	if strings.Contains(spans[0].Attributes[spanInputAttribute], "SECRET_SENTINEL") {
		t.Fatal("unredacted secret in input span")
	}
}

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
	if len(spans) != 3 {
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
	turn.InputEmitted = true
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
