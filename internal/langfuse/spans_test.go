package langfuse

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
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

func TestCountMetadataExportedOnAgent(t *testing.T) {
	t.Parallel()

	spans := emitCompleteTurnSpans(t)
	agent := spans.ByName("codex.agent")
	for _, key := range []string{
		"langfuse.trace.metadata.codex_insight.other_command_count",
		"langfuse.trace.metadata.codex_insight.web_search_tool_count",
		"langfuse.trace.metadata.codex_insight.apply_patch_tool_count",
		"langfuse.trace.metadata.codex_insight.exec_command_tool_count",
		"langfuse.trace.metadata.codex_insight.navigation",
	} {
		if _, ok := agent.Attributes[key]; !ok {
			t.Fatalf("agent missing %s in %#v", key, agent.Attributes)
		}
	}
	if !strings.Contains(agent.Attributes["langfuse.trace.metadata.codex_insight.navigation"], "files:changed") {
		t.Fatalf("navigation = %q, want files:changed", agent.Attributes["langfuse.trace.metadata.codex_insight.navigation"])
	}
	for _, key := range []string{
		"langfuse.trace.metadata.codex_insight.has_file_changes",
		"langfuse.trace.metadata.codex_insight.is_read_only",
		"langfuse.trace.metadata.codex_insight.command_kinds",
		"langfuse.trace.metadata.codex_insight.ran_other_command",
		"langfuse.trace.metadata.codex_insight.used_web_search",
		"langfuse.trace.metadata.codex_insight.web_search_count",
		"langfuse.trace.metadata.codex_insight.trace_facets",
		"langfuse.trace.metadata.codex_insight.navigation_facets",
	} {
		if _, ok := agent.Attributes[key]; ok {
			t.Fatalf("agent has duplicate navigation attribute %s in %#v", key, agent.Attributes)
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

// TEST-404
func TestLangfuseTraceTagsExportedOnSpans(t *testing.T) {
	t.Parallel()
	validateLangfuseTraceTagsExportedOnSpans(t)
}

func validateLangfuseTraceTagsExportedOnSpans(t *testing.T) {
	t.Helper()
	turn := completeTurn(t)
	rawTags, err := json.Marshal(codextrace.BuildInsightRollup(turn).Tags())
	if err != nil {
		t.Fatal(err)
	}
	wantTags := string(rawTags)
	if wantTags == "" {
		t.Fatal("fixture must produce tags")
	}

	spans := emitTurnSpans(t, turn)
	for _, name := range []string{"codex.agent", "codex.transcript", "codex.tool.exec_command", "codex.tool.mcp"} {
		span := spans.ByName(name)
		if span.Name == "" {
			t.Fatalf("missing span %s", name)
		}
		if span.Attributes["langfuse.trace.tags"] != wantTags {
			t.Fatalf("%s langfuse.trace.tags = %q want %q\nattrs=%#v", name, span.Attributes["langfuse.trace.tags"], wantTags, span.Attributes)
		}
	}
	if strings.Contains(wantTags, "issues/list") {
		t.Fatalf("exact MCP tool leaked into tags: %q", wantTags)
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

// TEST-301
func TestLangfuseGenerationModelUsageAndNoCostDetails(t *testing.T) {
	t.Parallel()

	spans := emitCompleteTurnSpans(t)
	transcript := spans.ByName("codex.transcript")
	if transcript.Attributes["langfuse.observation.model.name"] != "gpt-5.5" {
		t.Fatalf("model name = %q", transcript.Attributes["langfuse.observation.model.name"])
	}
	var usage map[string]int
	if err := json.Unmarshal([]byte(transcript.Attributes["langfuse.observation.usage_details"]), &usage); err != nil {
		t.Fatalf("parse usage details: %v", err)
	}
	wantUsage := map[string]int{
		"input":            100,
		"output":           40,
		"total":            140,
		"cached_input":     20,
		"reasoning_output": 10,
	}
	if !reflect.DeepEqual(usage, wantUsage) {
		t.Fatalf("usage = %#v, want %#v", usage, wantUsage)
	}
	for _, span := range spans {
		for key := range span.Attributes {
			if strings.Contains(key, "cost_details") {
				t.Fatalf("%s unexpectedly has cost attribute %s", span.Name, key)
			}
		}
	}
}

// TEST-303
func TestLangfuseVersionReleaseAndFailedCommandLevel(t *testing.T) {
	t.Parallel()

	for _, span := range emitCompleteTurnSpans(t) {
		if span.Attributes["langfuse.version"] != buildinfo.Version {
			t.Fatalf("%s version = %q", span.Name, span.Attributes["langfuse.version"])
		}
		if span.Attributes["langfuse.release"] != buildinfo.Version {
			t.Fatalf("%s release = %q", span.Name, span.Attributes["langfuse.release"])
		}
	}

	errorCommands := 0
	successCommands := 0
	for _, span := range emitTurnSpans(t, failedCommandTurn(t)) {
		if span.Name != "codex.tool.exec_command" {
			continue
		}
		var metadata map[string]any
		if err := json.Unmarshal([]byte(span.Attributes["langfuse.observation.metadata"]), &metadata); err != nil {
			t.Fatalf("parse command metadata: %v", err)
		}
		switch metadata["failure_type"] {
		case "nonzero_exit":
			errorCommands++
			if span.Attributes["langfuse.observation.level"] != "ERROR" {
				t.Fatalf("failed command level = %q", span.Attributes["langfuse.observation.level"])
			}
			if span.Attributes["langfuse.observation.status_message"] != "nonzero_exit" {
				t.Fatalf("failed command status message = %q", span.Attributes["langfuse.observation.status_message"])
			}
		case "none":
			successCommands++
			if span.Attributes["langfuse.observation.level"] == "ERROR" {
				t.Fatalf("successful command has error level: %#v", span.Attributes)
			}
		}
	}
	if errorCommands != 1 || successCommands != 1 {
		t.Fatalf("command failure counts = %d/%d, want 1/1", errorCommands, successCommands)
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
	return emitTurnSpans(t, completeTurn(t))
}

func emitTurnSpans(t *testing.T, turn codextrace.Turn) spanSnapshots {
	t.Helper()
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

// EVAL-404
func TestEvalLangfuseTagProjection(t *testing.T) {
	t.Parallel()
	validateLangfuseTraceTagsExportedOnSpans(t)
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

func failedCommandTurn(t *testing.T) codextrace.Turn {
	t.Helper()
	turns, err := codextrace.ParseTurns(filepath.Join("..", "..", "testdata", "rollouts", "failed-command.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	exportable := codextrace.ExportableTurns(turns)
	if len(exportable) != 1 {
		t.Fatalf("exportable failed turns = %d", len(exportable))
	}
	return exportable[0]
}
