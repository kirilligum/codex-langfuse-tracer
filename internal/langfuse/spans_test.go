package langfuse

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/kirilligum/codex-langfuse-tracer/internal/buildinfo"
	"github.com/kirilligum/codex-langfuse-tracer/internal/codextrace"
)

// TEST-007
func TestSpanShapeAndIDs(t *testing.T) {
	t.Parallel()

	turn := completeTurn(t)
	exporter := &memoryExporter{}
	if err := EmitTurn(context.Background(), turn, buildinfo.DefaultEnvironment, buildinfo.DefaultServiceName, exporter); err != nil {
		t.Fatalf("EmitTurn: %v", err)
	}
	spans := exporter.Snapshots()
	if len(spans) != len(turn.Observations)+3 {
		t.Fatalf("span count = %d, want %d", len(spans), len(turn.Observations)+3)
	}

	agent := spans.ByName("codex.agent")
	if agent.TraceID != turn.TraceID {
		t.Fatalf("agent trace id = %q want %q", agent.TraceID, turn.TraceID)
	}
	if agent.SpanID != codextrace.StableSpanID("codex-agent", turn.TraceID, turn.TurnID, "") {
		t.Fatalf("agent span id = %q", agent.SpanID)
	}
	if agent.ParentSpanID != "" {
		t.Fatalf("agent parent = %q, want empty", agent.ParentSpanID)
	}
	if agent.Attributes["langfuse.observation.type"] != "agent" {
		t.Fatalf("agent type attr = %q", agent.Attributes["langfuse.observation.type"])
	}
	if agent.Attributes["langfuse.trace.input"] != codextrace.ExportText(turn.InputText()) {
		t.Fatalf("trace input attr = %q", agent.Attributes["langfuse.trace.input"])
	}

	transcript := spans.ByName("codex.transcript")
	if transcript.ParentSpanID != agent.SpanID {
		t.Fatalf("transcript parent = %q want %q", transcript.ParentSpanID, agent.SpanID)
	}
	if transcript.Attributes["langfuse.observation.type"] != "generation" {
		t.Fatalf("transcript type attr = %q", transcript.Attributes["langfuse.observation.type"])
	}

	terminal := spans.ByName("codex.terminal")
	if terminal.ParentSpanID != agent.SpanID {
		t.Fatalf("terminal parent = %q want %q", terminal.ParentSpanID, agent.SpanID)
	}
}

func completeTurn(t *testing.T) codextrace.Turn {
	t.Helper()
	turns, err := codextrace.ParseTurns(filepath.Join("..", "..", "testdata", "rollouts", "complete-tools.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	exportable := codextrace.ExportableTurns(turns)
	if len(exportable) != 1 {
		t.Fatalf("exportable turns = %d", len(exportable))
	}
	return exportable[0]
}
