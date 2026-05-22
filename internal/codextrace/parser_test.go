package codextrace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kirilligum/codex-langfuse-tracer/internal/agenttrace"
	"github.com/kirilligum/codex-langfuse-tracer/internal/tracecontract"
)

func validateCompletedAndIncompleteTurns(t *testing.T) {
	t.Helper()
	turns, err := ParseTurns(filepath.Join("..", "..", "testdata", "sources", "codex", "complete-tools.jsonl"))
	if err != nil {
		t.Fatalf("ParseTurns complete: %v", err)
	}
	exportable := agenttrace.ExportableTurns(turns)
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

	incomplete, err := ParseTurns(filepath.Join("..", "..", "testdata", "sources", "codex", "incomplete-turn.jsonl"))
	if err != nil {
		t.Fatalf("ParseTurns incomplete: %v", err)
	}
	if got := agenttrace.ExportableTurns(incomplete); len(got) != 0 {
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

func TestResponseMessageContentShapes(t *testing.T) {
	t.Parallel()

	content := []any{
		map[string]any{"type": "input_text", "text": "first"},
		map[string]any{"type": "ignored", "text": "skip"},
		map[string]any{"type": "input_text", "text": "second"},
	}
	if got := textFromContent(content, "input_text"); got != "first\nsecond" {
		t.Fatalf("textFromContent(valid) = %q", got)
	}
	for _, unsupported := range []any{"plain text", map[string]any{"type": "input_text", "text": "ignored"}, nil} {
		if got := textFromContent(unsupported, "input_text"); got != "" {
			t.Fatalf("textFromContent(%#v) = %q, want empty", unsupported, got)
		}
	}
}

func TestRepeatedTurnContextPreservesAccumulatedTurn(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	raw := []byte(strings.Join([]string{
		`{"timestamp":"2026-05-01T10:00:00Z","type":"session_meta","payload":{"id":"sess-repeat","model":"gpt-5.5","cwd":"/tmp/repeat"}}`,
		`{"timestamp":"2026-05-01T10:00:01Z","type":"turn_context","payload":{"turn_id":"turn-repeat","cwd":"/tmp/repeat","model":"gpt-5.5"}}`,
		`{"timestamp":"2026-05-01T10:00:02Z","type":"event_msg","payload":{"type":"user_message","message":"Implement the plan"}}`,
		`{"timestamp":"2026-05-01T10:00:03Z","type":"event_msg","payload":{"type":"agent_message","phase":"commentary","message":"Reading files."}}`,
		`{"timestamp":"2026-05-01T10:00:04Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":10,"output_tokens":3,"total_tokens":13}}}}`,
		`{"timestamp":"2026-05-01T10:00:05Z","type":"turn_context","payload":{"turn_id":"turn-repeat","cwd":"/tmp/repeat","model":"gpt-5.5"}}`,
		`{"timestamp":"2026-05-01T10:00:06Z","type":"event_msg","payload":{"type":"agent_message","phase":"final_answer","message":"Done"}}`,
		`{"timestamp":"2026-05-01T10:00:07Z","type":"event_msg","payload":{"type":"task_complete","last_agent_message":"Done"}}`,
		"",
	}, "\n"))
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	turns, err := ParseTurns(path)
	if err != nil {
		t.Fatalf("ParseTurns: %v", err)
	}
	exportable := agenttrace.ExportableTurns(turns)
	if len(exportable) != 1 {
		t.Fatalf("exportable turns = %d, want 1", len(exportable))
	}
	turn := exportable[0]
	if turn.InputText() != "Implement the plan" {
		t.Fatalf("input = %q", turn.InputText())
	}
	if turn.OutputText() != "Done" {
		t.Fatalf("output = %q", turn.OutputText())
	}
	if turn.TokenUsage == nil || turn.TokenUsage.InputTokens != 10 {
		t.Fatalf("token usage was not preserved: %+v", turn.TokenUsage)
	}
	if len(turn.Observations) != 1 || turn.Observations[0].Name != "codex.message.commentary" {
		t.Fatalf("observations were not preserved: %+v", turn.Observations)
	}
}

func TestStableIDs(t *testing.T) {
	t.Parallel()

	traceID := agenttrace.StableTraceID(agenttrace.ProviderCodex, "session", "turn")
	if len(traceID) != 32 || traceID != agenttrace.StableTraceID(agenttrace.ProviderCodex, "session", "turn") {
		t.Fatalf("trace id is not stable 32-char hex: %q", traceID)
	}
	first := agenttrace.StableSpanID("prefix", traceID, "turn", "a")
	second := agenttrace.StableSpanID("prefix", traceID, "turn", "b")
	if len(first) != 16 || first == second {
		t.Fatalf("span ids not distinct and stable: %q %q", first, second)
	}
}

// TEST-503
func TestCodexParserUsesAgentTrace(t *testing.T) {
	t.Parallel()

	turns, err := ParseTurns(filepath.Join("..", "..", "testdata", "sources", "codex", "complete-tools.jsonl"))
	if err != nil {
		t.Fatalf("ParseTurns: %v", err)
	}
	if len(turns) == 0 {
		t.Fatal("ParseTurns returned no turns")
	}
	if got := reflect.TypeOf(turns[0]).PkgPath(); got != "github.com/kirilligum/codex-langfuse-tracer/internal/agenttrace" {
		t.Fatalf("turn package = %q", got)
	}
	if turns[0].Provider != agenttrace.ProviderCodex {
		t.Fatalf("turn provider = %q", turns[0].Provider)
	}
	if turns[0].TraceID != agenttrace.StableTraceID(agenttrace.ProviderCodex, turns[0].SessionID, turns[0].TurnID) {
		t.Fatalf("trace id = %q, want stable agenttrace codex id", turns[0].TraceID)
	}
}

// EVAL-003
func TestEvalCodexGoldenParityAfterAgentTrace(t *testing.T) {
	t.Parallel()

	turns, err := ParseTurns(filepath.Join("..", "..", "testdata", "sources", "codex", "complete-tools.jsonl"))
	if err != nil {
		t.Fatalf("ParseTurns: %v", err)
	}
	exportable := agenttrace.ExportableTurns(turns)
	if len(exportable) != 1 {
		t.Fatalf("exportable turns = %d, want 1", len(exportable))
	}
	actual := tracecontract.FromTurn(exportable[0])
	goldenRaw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "golden", "complete-tools.normalized.json"))
	if err != nil {
		t.Fatal(err)
	}
	var golden tracecontract.Trace
	if err := json.Unmarshal(goldenRaw, &golden); err != nil {
		t.Fatal(err)
	}
	if canonicalTraceJSON(actual) != canonicalTraceJSON(golden) {
		t.Fatalf("complete-tools golden changed\ngolden=%s\nactual=%s", canonicalTraceJSON(golden), canonicalTraceJSON(actual))
	}
}

func canonicalTraceJSON(value any) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}
