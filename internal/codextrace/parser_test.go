package codextrace

import (
	"path/filepath"
	"testing"
)

func validateCompletedAndIncompleteTurns(t *testing.T) {
	t.Helper()
	turns, err := ParseTurns(filepath.Join("..", "..", "testdata", "rollouts", "complete-tools.jsonl"))
	if err != nil {
		t.Fatalf("ParseTurns complete: %v", err)
	}
	exportable := ExportableTurns(turns)
	if len(exportable) != 1 {
		t.Fatalf("exportable turns = %d, want 1", len(exportable))
	}
	turn := exportable[0]
	if turn.SessionID != "sess-complete" || turn.TurnID != "turn-1" {
		t.Fatalf("wrong turn identity: %+v", turn)
	}
	if turn.TraceID != "1e087e4ea8aa8d8e29e604d2cd8704d9" {
		t.Fatalf("trace id = %q", turn.TraceID)
	}
	if turn.InputText() != "Summarize the repo and run checks" {
		t.Fatalf("input = %q", turn.InputText())
	}
	if turn.OutputText() != "Checks passed with sk-lf-live-secret and ghp_live_secret redacted." {
		t.Fatalf("output = %q", turn.OutputText())
	}
	if turn.TokenUsage == nil || turn.TokenUsage.InputTokens != 100 || turn.TokenUsage.ReasoningOutputTokens != 10 {
		t.Fatalf("token usage not parsed: %+v", turn.TokenUsage)
	}

	incomplete, err := ParseTurns(filepath.Join("..", "..", "testdata", "rollouts", "incomplete-turn.jsonl"))
	if err != nil {
		t.Fatalf("ParseTurns incomplete: %v", err)
	}
	if got := ExportableTurns(incomplete); len(got) != 0 {
		t.Fatalf("incomplete exportable turns = %d, want 0", len(got))
	}
}

// TEST-004
func TestParseCompletedAndIncompleteTurns(t *testing.T) {
	t.Parallel()
	validateCompletedAndIncompleteTurns(t)
}

// EVAL-002
func TestEvalParserGoldenCorpus(t *testing.T) {
	t.Parallel()
	validateCompletedAndIncompleteTurns(t)
}
