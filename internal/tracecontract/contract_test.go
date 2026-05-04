package tracecontract

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kirilligum/codex-langfuse-tracer/internal/agenttrace"
	"github.com/kirilligum/codex-langfuse-tracer/internal/codextrace"
)

// TEST-403
func TestFromTurnNormalizesTrace(t *testing.T) {
	t.Parallel()

	turns, err := codextrace.ParseTurns(filepath.Join("..", "..", "testdata", "sources", "codex", "complete-tools.jsonl"))
	if err != nil {
		t.Fatalf("ParseTurns: %v", err)
	}
	exportable := agenttrace.ExportableTurns(turns)
	if len(exportable) != 1 {
		t.Fatalf("exportable turns = %d, want 1", len(exportable))
	}

	trace := FromTurn(exportable[0])
	if trace.SchemaVersion != 1 || trace.Provider != agenttrace.ProviderCodex || trace.TraceID != "1e087e4ea8aa8d8e29e604d2cd8704d9" {
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

	if got := FromTurn(agenttrace.Turn{}).TokenUsage; got != nil {
		t.Fatalf("empty turn token usage = %#v, want nil", got)
	}
}

// TEST-505
func TestTraceContractProviderFields(t *testing.T) {
	t.Parallel()

	trace := FromTurn(agenttrace.Turn{
		Provider:       agenttrace.ProviderClaude,
		SessionID:      "claude-contract",
		TurnID:         "turn-contract",
		TraceID:        agenttrace.StableTraceID(agenttrace.ProviderClaude, "claude-contract", "turn-contract"),
		StartTS:        "2026-05-04T12:00:00Z",
		EndTS:          "2026-05-04T12:00:01Z",
		UserMessages:   []string{"Say hi"},
		AssistantTexts: []string{"hi"},
		Completed:      true,
	})
	if trace.Provider != agenttrace.ProviderClaude {
		t.Fatalf("provider = %q", trace.Provider)
	}
	if trace.Name != "claude.turn.transcript" {
		t.Fatalf("trace name = %q", trace.Name)
	}
	if len(trace.Observations) < 2 || trace.Observations[0].Name != "claude.agent" || trace.Observations[1].Name != "claude.transcript" {
		t.Fatalf("observations = %#v", trace.Observations)
	}
}
