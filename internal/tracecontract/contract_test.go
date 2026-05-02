package tracecontract

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kirilligum/codex-langfuse-tracer/internal/codextrace"
)

// TEST-403
func TestFromTurnNormalizesTrace(t *testing.T) {
	t.Parallel()

	turns, err := codextrace.ParseTurns(filepath.Join("..", "..", "testdata", "rollouts", "complete-tools.jsonl"))
	if err != nil {
		t.Fatalf("ParseTurns: %v", err)
	}
	exportable := codextrace.ExportableTurns(turns)
	if len(exportable) != 1 {
		t.Fatalf("exportable turns = %d, want 1", len(exportable))
	}

	trace := FromTurn(exportable[0])
	if trace.SchemaVersion != 1 || trace.TraceID != "1e087e4ea8aa8d8e29e604d2cd8704d9" {
		t.Fatalf("bad trace identity: %+v", trace)
	}
	if trace.TokenUsage["input"] != 80 || trace.TokenUsage["input_cached_tokens"] != 20 || trace.TokenUsage["output"] != 30 || trace.TokenUsage["output_reasoning_tokens"] != 10 {
		t.Fatalf("token usage not normalized: %#v", trace.TokenUsage)
	}
	if trace.Metadata["tool_count"] != 5 || trace.Metadata["verification_status"] != "not_run" {
		t.Fatalf("insight metadata not normalized: %#v", trace.Metadata)
	}
	if len(trace.Observations) < 3 || trace.Observations[0].Name != "codex.agent" || trace.Observations[1].Name != "codex.transcript" {
		t.Fatalf("missing leading observations: %#v", trace.Observations)
	}

	raw, err := json.Marshal(trace)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"sk-lf-live-secret", "ghp_live_secret", "HIDDEN_REASONING_SENTINEL", "ENCRYPTED_REASONING_SENTINEL"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("normalized trace leaked %q in %s", forbidden, string(raw))
		}
	}
}

func TestUsageDetailsEmptyWhenMissing(t *testing.T) {
	t.Parallel()

	if got := FromTurn(codextrace.Turn{}).TokenUsage; got != nil {
		t.Fatalf("empty turn token usage = %#v, want nil", got)
	}
}
