package langfuse

import (
	"os"
	"strings"
	"testing"

	"github.com/kirilligum/codex-langfuse-tracer/internal/agenttrace"
	"github.com/kirilligum/codex-langfuse-tracer/internal/config"
)

// TEST-532
func TestLiveClaudeParityTrace(t *testing.T) {
	traceID := os.Getenv("LIVE_LANGFUSE_CLAUDE_TRACE_ID")
	if traceID == "" {
		t.Skip("set LIVE_LANGFUSE_CLAUDE_TRACE_ID to run live Claude Langfuse parity verification")
	}

	cfg, err := config.Load(config.DefaultConfigPath())
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	observations := liveClaudeObservations(t, cfg, traceID)
	for _, name := range []string{
		"claude.agent",
		"claude.transcript",
		agenttrace.ToolObservationName(agenttrace.ProviderClaude, agenttrace.ToolFamilyCommand),
		agenttrace.ToolObservationName(agenttrace.ProviderClaude, agenttrace.ToolFamilyFileChange),
		agenttrace.ToolObservationName(agenttrace.ProviderClaude, agenttrace.ToolFamilyMCP),
	} {
		if _, ok := observations[name]; !ok {
			t.Fatalf("missing live Claude observation %s in %s", name, canonicalLiveJSON(observations))
		}
	}

	agent := observations["claude.agent"]
	if agent.TraceName != "claude.turn.transcript" {
		t.Fatalf("trace name = %q, want claude.turn.transcript: %s", agent.TraceName, canonicalLiveJSON(agent))
	}
	transcript := observations["claude.transcript"]
	if model := transcript.ModelName(); !strings.HasPrefix(model, "claude-") {
		t.Fatalf("claude.transcript model = %q: %s", model, canonicalLiveJSON(transcript))
	}
	if transcript.ModelID == "" {
		t.Fatalf("claude.transcript modelId is empty; Langfuse pricing did not match: %s", canonicalLiveJSON(transcript))
	}
	usage := transcript.UsageDetails
	if liveIntValue(usage["input"]) == 0 || liveIntValue(usage["output"]) == 0 || liveIntValue(usage["total"]) == 0 {
		t.Fatalf("claude.transcript usageDetails incomplete: %s", canonicalLiveJSON(transcript))
	}
	assertClaudeUsageMath(t, transcript)
	if transcript.TotalCost <= 0 {
		t.Fatalf("claude.transcript totalCost = %v, want > 0: %s", transcript.TotalCost, canonicalLiveJSON(transcript))
	}

	for _, tag := range []string{"tool:command", "tool:file_change", "tool:mcp"} {
		if !liveHasString(agent.Tags, tag) {
			t.Fatalf("trace tags missing %q in %#v", tag, agent.Tags)
		}
	}
	hasMCPServerTag := false
	for _, tag := range agent.Tags {
		if strings.HasPrefix(tag, "mcp:") {
			hasMCPServerTag = true
		}
	}
	if !hasMCPServerTag {
		t.Fatalf("trace tags missing mcp:<server> tag in %#v", agent.Tags)
	}
}

// TEST-533
func TestLiveClaudeCostTrace(t *testing.T) {
	traceID := os.Getenv("LIVE_LANGFUSE_CLAUDE_COST_TRACE_ID")
	if traceID == "" {
		t.Skip("set LIVE_LANGFUSE_CLAUDE_COST_TRACE_ID to run live Claude Langfuse cost verification")
	}

	cfg, err := config.Load(config.DefaultConfigPath())
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	transcript := liveClaudeObservations(t, cfg, traceID)["claude.transcript"]
	if transcript.Name == "" {
		t.Fatalf("missing claude.transcript for trace %s", traceID)
	}
	if transcript.TraceName != "claude.turn.transcript" {
		t.Fatalf("trace name = %q, want claude.turn.transcript", transcript.TraceName)
	}
	if transcript.TotalCost <= 0 {
		t.Fatalf("claude.transcript totalCost = %v, want > 0: %s", transcript.TotalCost, canonicalLiveJSON(transcript))
	}
	if !strings.HasPrefix(transcript.ModelName(), "claude-") || transcript.ModelID == "" {
		t.Fatalf("claude.transcript model pricing is incomplete: %s", canonicalLiveJSON(transcript))
	}
	if liveFloatValue(transcript.InputPrice) == 0 || liveFloatValue(transcript.OutputPrice) == 0 {
		t.Fatalf("claude.transcript prices are empty: %s", canonicalLiveJSON(transcript))
	}
	if liveIntValue(transcript.UsageDetails["input"]) == 0 || liveIntValue(transcript.UsageDetails["output"]) == 0 || liveIntValue(transcript.UsageDetails["total"]) == 0 {
		t.Fatalf("claude.transcript usageDetails incomplete: %s", canonicalLiveJSON(transcript))
	}
	assertClaudeUsageMath(t, transcript)
}

func assertClaudeUsageMath(t *testing.T, transcript Observation) {
	t.Helper()
	usage := transcript.UsageDetails
	input := liveIntValue(usage["input"])
	cacheCreation := liveIntValue(usage["cache_creation_input_tokens"])
	cacheRead := liveIntValue(usage["cache_read_input_tokens"])
	output := liveIntValue(usage["output"])
	total := liveIntValue(usage["total"])
	knownTotal := input + cacheCreation + cacheRead + output
	if total < knownTotal {
		t.Fatalf("claude.transcript total tokens = %d, want at least input+cache+output %d: %s", total, knownTotal, canonicalLiveJSON(transcript))
	}
	cost := transcript.CostDetails
	if cacheCreation > 0 && liveFloatValue(cost["cache_creation_input_tokens"]) <= 0 {
		t.Fatalf("claude.transcript cache creation tokens have no cost: %s", canonicalLiveJSON(transcript))
	}
	if cacheRead > 0 && liveFloatValue(cost["cache_read_input_tokens"]) <= 0 {
		t.Fatalf("claude.transcript cache read tokens have no cost: %s", canonicalLiveJSON(transcript))
	}
}

func liveClaudeObservations(t *testing.T, cfg config.LangfuseConfig, traceID string) map[string]Observation {
	t.Helper()
	result := map[string]Observation{}
	for _, observation := range liveObservationsForTrace(t, cfg, traceID, "core,basic,io,metadata,model,usage,trace_context") {
		if observation.Name != "" {
			result[observation.Name] = observation
		}
	}
	return result
}

func liveHasString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
