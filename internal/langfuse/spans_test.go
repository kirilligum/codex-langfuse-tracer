package langfuse

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kirilligum/codex-langfuse-tracer/internal/agenttrace"
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
	if agent.SpanID != agenttrace.StableSpanID("codex-agent", turn.TraceID, turn.TurnID, "") {
		t.Fatalf("agent span id = %q", agent.SpanID)
	}
	if agent.ParentSpanID != "" {
		t.Fatalf("agent parent = %q, want empty", agent.ParentSpanID)
	}
	if agent.Attributes["langfuse.observation.type"] != "agent" {
		t.Fatalf("agent type attr = %q", agent.Attributes["langfuse.observation.type"])
	}
	if agent.Attributes["langfuse.trace.input"] != agenttrace.ExportText(turn.InputText()) {
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

// TEST-505
func TestProviderProjectionNames(t *testing.T) {
	t.Parallel()

	turn := agenttrace.Turn{
		Provider:       agenttrace.ProviderClaude,
		SessionID:      "claude-provider",
		TurnID:         "turn-provider",
		TraceID:        agenttrace.StableTraceID(agenttrace.ProviderClaude, "claude-provider", "turn-provider"),
		StartTS:        "2026-05-04T12:00:00Z",
		EndTS:          "2026-05-04T12:00:01Z",
		UserMessages:   []string{"Run pwd"},
		AssistantTexts: []string{"Done"},
		Model:          "claude-haiku-4-5-20251001",
		Completed:      true,
		TerminalEntries: []agenttrace.TerminalEntry{
			{Timestamp: "2026-05-04T12:00:00Z", Label: "user", Text: "Run pwd"},
			{Timestamp: "2026-05-04T12:00:01Z", Label: "assistant.final", Text: "Done"},
		},
		Observations: []agenttrace.Observation{
			{Name: agenttrace.ToolObservationName(agenttrace.ProviderClaude, agenttrace.ToolFamilyCommand), Type: "tool", Input: "pwd", Output: "/tmp", Metadata: map[string]any{"status": "success", "failure_type": "none"}},
		},
	}
	spans := emitTurnSpans(t, turn)
	for _, name := range []string{"claude.agent", "claude.transcript", agenttrace.ToolObservationName(agenttrace.ProviderClaude, agenttrace.ToolFamilyCommand), "claude.terminal"} {
		if span := spans.ByName(name); span.Name == "" {
			t.Fatalf("missing provider span %s in %#v", name, spanNames(spans))
		}
	}
	for _, name := range []string{"codex.agent", "codex.transcript", "codex.terminal"} {
		if span := spans.ByName(name); span.Name != "" {
			t.Fatalf("unexpected codex span %s in Claude projection", name)
		}
	}
	agent := spans.ByName("claude.agent")
	if agent.Attributes["langfuse.trace.name"] != "claude.turn.transcript" {
		t.Fatalf("trace name attr = %q", agent.Attributes["langfuse.trace.name"])
	}
	if agent.Attributes["langfuse.trace.metadata.claude_session_id"] != "claude-provider" {
		t.Fatalf("provider metadata missing in %#v", agent.Attributes)
	}
	if _, ok := agent.Attributes["langfuse.trace.metadata.codex_session_id"]; ok {
		t.Fatalf("Claude span has codex metadata: %#v", agent.Attributes)
	}
	if agent.Attributes["langfuse.trace.metadata.claude_insight.command_tool_count"] != "1" {
		t.Fatalf("Claude insight metadata missing command count: %#v", agent.Attributes)
	}
	if agent.Attributes["langfuse.trace.metadata.claude_insight.navigation"] != "command:other files:read_only tool:command verification:not_applicable" {
		t.Fatalf("Claude navigation metadata = %q", agent.Attributes["langfuse.trace.metadata.claude_insight.navigation"])
	}
	if _, ok := agent.Attributes["langfuse.trace.metadata.codex_insight.navigation"]; ok {
		t.Fatalf("Claude span has codex insight metadata: %#v", agent.Attributes)
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
		"langfuse.trace.metadata.codex_insight.file_change_tool_count",
		"langfuse.trace.metadata.codex_insight.command_tool_count",
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

// TEST-527
func TestLangfuseProviderNeutralSemanticTagsExportedOnSpans(t *testing.T) {
	t.Parallel()
	validateLangfuseTraceTagsExportedOnSpans(t)
}

func validateLangfuseTraceTagsExportedOnSpans(t *testing.T) {
	t.Helper()
	turn := completeTurn(t)
	rawTags, err := json.Marshal(agenttrace.BuildInsightRollup(turn).Tags())
	if err != nil {
		t.Fatal(err)
	}
	wantTags := string(rawTags)
	if wantTags == "" {
		t.Fatal("fixture must produce tags")
	}

	spans := emitTurnSpans(t, turn)
	for _, name := range []string{"codex.agent", "codex.transcript", agenttrace.ToolObservationName(agenttrace.ProviderCodex, agenttrace.ToolFamilyCommand), agenttrace.ToolObservationName(agenttrace.ProviderCodex, agenttrace.ToolFamilyMCP)} {
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
// TEST-402
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
		"input":                   80,
		"input_cached_tokens":     20,
		"output":                  30,
		"output_reasoning_tokens": 10,
		"total":                   140,
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
		if span.Name != agenttrace.ToolObservationName(agenttrace.ProviderCodex, agenttrace.ToolFamilyCommand) {
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

	command := spans.ByName(agenttrace.ToolObservationName(agenttrace.ProviderCodex, agenttrace.ToolFamilyCommand))
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

func emitTurnSpans(t *testing.T, turn agenttrace.Turn) spanSnapshots {
	t.Helper()
	exporter := &memoryExporter{}
	if err := EmitTurn(context.Background(), turn, buildinfo.DefaultEnvironment, buildinfo.DefaultServiceName, exporter); err != nil {
		t.Fatalf("EmitTurn: %v", err)
	}
	return exporter.Snapshots()
}

func spanNames(spans spanSnapshots) []string {
	names := make([]string, 0, len(spans))
	for _, span := range spans {
		names = append(names, span.Name)
	}
	return names
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

// EVAL-005
func TestEvalProviderProjectionDeterminism(t *testing.T) {
	t.Parallel()

	turns := []agenttrace.Turn{completeTurn(t), {
		Provider:       agenttrace.ProviderClaude,
		SessionID:      "claude-determinism",
		TurnID:         "turn-determinism",
		TraceID:        agenttrace.StableTraceID(agenttrace.ProviderClaude, "claude-determinism", "turn-determinism"),
		StartTS:        "2026-05-04T12:00:00Z",
		EndTS:          "2026-05-04T12:00:01Z",
		UserMessages:   []string{"Run pwd"},
		AssistantTexts: []string{"Done"},
		Completed:      true,
		Observations: []agenttrace.Observation{
			{Name: agenttrace.ToolObservationName(agenttrace.ProviderClaude, agenttrace.ToolFamilyCommand), Type: "tool", Input: "pwd", Output: "/tmp", Metadata: map[string]any{"status": "success", "failure_type": "none"}},
		},
	}}
	for _, turn := range turns {
		first := canonicalSpanJSON(emitTurnSpans(t, turn))
		for i := 0; i < 10; i++ {
			if got := canonicalSpanJSON(emitTurnSpans(t, turn)); got != first {
				t.Fatalf("%s projection is nondeterministic\nfirst=%s\ngot=%s", turn.Provider, first, got)
			}
		}
	}
}

func completeTurn(t *testing.T) agenttrace.Turn {
	t.Helper()
	turns, err := codextrace.ParseTurns(filepath.Join("..", "..", "testdata", "sources", "codex", "complete-tools.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	exportable := agenttrace.ExportableTurns(turns)
	if len(exportable) != 1 {
		t.Fatalf("exportable turns = %d", len(exportable))
	}
	return exportable[0]
}

func canonicalSpanJSON(value any) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}

func failedCommandTurn(t *testing.T) agenttrace.Turn {
	t.Helper()
	turns, err := codextrace.ParseTurns(filepath.Join("..", "..", "testdata", "sources", "codex", "failed-command.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	exportable := agenttrace.ExportableTurns(turns)
	if len(exportable) != 1 {
		t.Fatalf("exportable failed turns = %d", len(exportable))
	}
	return exportable[0]
}
