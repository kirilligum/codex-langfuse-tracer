package langfuse

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
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

// TEST-107
func TestInsightMetadataExportedOnAgent(t *testing.T) {
	t.Parallel()
	validateInsightMetadataExportedOnAgent(t)
}

// TEST-203
func TestNavigationFacetsMetadataExportedOnAgent(t *testing.T) {
	t.Parallel()

	spans := emitCompleteTurnSpans(t)
	agent := spans.ByName("codex.agent")
	for _, key := range []string{
		"langfuse.trace.metadata.codex_insight.has_file_changes",
		"langfuse.trace.metadata.codex_insight.is_read_only",
		"langfuse.trace.metadata.codex_insight.command_kinds",
		"langfuse.trace.metadata.codex_insight.ran_other_command",
		"langfuse.trace.metadata.codex_insight.other_command_count",
		"langfuse.trace.metadata.codex_insight.used_web_search",
		"langfuse.trace.metadata.codex_insight.web_search_count",
	} {
		if _, ok := agent.Attributes[key]; !ok {
			t.Fatalf("agent missing %s in %#v", key, agent.Attributes)
		}
	}
	for _, span := range spans {
		if span.Name == "codex.agent" {
			continue
		}
		for key := range span.Attributes {
			if strings.HasPrefix(key, "langfuse.trace.metadata.codex_insight.") {
				t.Fatalf("%s repeats root insight attribute %s", span.Name, key)
			}
		}
	}
}

func validateInsightMetadataExportedOnAgent(t *testing.T) {
	t.Helper()
	spans := emitCompleteTurnSpans(t)
	agent := spans.ByName("codex.agent")
	for _, key := range []string{
		"langfuse.trace.metadata.codex_insight.tool_count",
		"langfuse.trace.metadata.codex_insight.command_count",
		"langfuse.trace.metadata.codex_insight.verification_status",
		"langfuse.trace.metadata.codex_insight.changed_extensions",
	} {
		if _, ok := agent.Attributes[key]; !ok {
			t.Fatalf("agent missing %s in %#v", key, agent.Attributes)
		}
	}
	for _, span := range spans {
		if span.Name == "codex.agent" {
			continue
		}
		for key := range span.Attributes {
			if strings.HasPrefix(key, "langfuse.trace.metadata.codex_insight.") {
				t.Fatalf("%s repeats root insight attribute %s", span.Name, key)
			}
		}
	}

	command := spans.ByName("codex.tool.exec_command")
	var metadata map[string]any
	if err := json.Unmarshal([]byte(command.Attributes["langfuse.observation.metadata"]), &metadata); err != nil {
		t.Fatalf("parse command metadata: %v", err)
	}
	for _, key := range []string{"command_kind", "duration_ms", "failure_type"} {
		if _, ok := metadata[key]; !ok {
			t.Fatalf("command metadata missing %s in %#v", key, metadata)
		}
	}
}

func emitCompleteTurnSpans(t *testing.T) spanSnapshots {
	t.Helper()
	turn := completeTurn(t)
	exporter := &memoryExporter{}
	if err := EmitTurn(context.Background(), turn, buildinfo.DefaultEnvironment, buildinfo.DefaultServiceName, exporter); err != nil {
		t.Fatalf("EmitTurn: %v", err)
	}
	return exporter.Snapshots()
}

// EVAL-104
func TestEvalInsightMetadataProjection(t *testing.T) {
	t.Parallel()
	validateInsightMetadataExportedOnAgent(t)
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
