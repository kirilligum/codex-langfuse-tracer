package codextrace

import (
	"github.com/kirilligum/codex-langfuse-tracer/internal/agenttrace"
	"testing"
)

func TestModelStepsUsePerCallUsageAndIgnoreRepeatedUsageNotifications(t *testing.T) {
	turn := agenttrace.Turn{Model: "model", UserMessages: []string{"prompt"}}
	step := &modelStep{}
	assistant := map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "answer"}}}
	usage := map[string]any{"type": "token_count", "info": map[string]any{"last_token_usage": map[string]any{"input_tokens": 10, "output_tokens": 2}, "total_token_usage": map[string]any{"input_tokens": 999}}}
	step.consume(&turn, "response_item", assistant, "2026-10-04T00:00:01Z")
	step.consume(&turn, "event_msg", usage, "2026-10-04T00:00:02Z")
	step.consume(&turn, "event_msg", usage, "2026-10-04T00:00:03Z")
	if len(turn.ModelCalls) != 1 || turn.ModelCalls[0].Usage.InputTokens != 10 {
		t.Fatalf("usage duplicated or accumulated: %+v", turn.ModelCalls)
	}
	step.consume(&turn, "response_item", assistant, "2026-10-04T00:00:04Z")
	step.consume(&turn, "event_msg", map[string]any{"type": "task_complete"}, "2026-10-04T00:00:05Z")
	if len(turn.ModelCalls) != 2 || turn.ModelCalls[1].Usage != nil {
		t.Fatalf("terminal call usage fabricated: %+v", turn.ModelCalls)
	}
}
