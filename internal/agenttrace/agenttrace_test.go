package agenttrace

import "testing"

// TEST-502
func TestAgentTraceSharedOwnership(t *testing.T) {
	t.Parallel()

	turn := Turn{
		Provider:       ProviderCodex,
		SessionID:      "sess-shared",
		TurnID:         "turn-shared",
		TraceID:        StableTraceID(ProviderCodex, "sess-shared", "turn-shared"),
		StartTS:        "2026-05-04T00:00:00Z",
		EndTS:          "2026-05-04T00:00:01Z",
		UserMessages:   []string{"Basic dGVzdGRhdGF0ZXN0ZGF0YXRlc3RkYXRh"},
		AssistantTexts: []string{"done"},
		Completed:      true,
	}
	AddTerminalEntry(&turn, turn.StartTS, "user", turn.InputText())
	AddObservation(&turn, "codex.message.commentary", turn.EndTS, "", "done", map[string]any{"phase": "commentary"}, "span", nil)

	if len(ExportableTurns([]Turn{turn})) != 1 {
		t.Fatal("shared exportability must live in agenttrace")
	}
	if turn.TraceID != "055d2d9671460f45db598dbe2d13ef17" {
		t.Fatalf("codex trace ID changed: %s", turn.TraceID)
	}
	if claudeTraceID := StableTraceID(ProviderClaude, "sess-shared", "turn-shared"); claudeTraceID == turn.TraceID {
		t.Fatalf("provider trace IDs collide: %s", claudeTraceID)
	}
	if exported := ExportText(turn.InputText()); exported == turn.InputText() {
		t.Fatalf("redaction not applied to exported input: %q", exported)
	}
	if TerminalObservation(turn) == nil {
		t.Fatal("terminal observation missing")
	}
	if BuildInsightRollup(turn).ToolCount != 0 {
		t.Fatal("non-tool span counted as tool")
	}
}
