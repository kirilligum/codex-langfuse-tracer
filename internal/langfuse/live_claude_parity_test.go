package langfuse

import (
	"net/url"
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
	trace := liveGet(t, cfg, "/api/public/traces/"+url.PathEscape(traceID))
	if name := liveStringValue(trace["name"]); name != "claude.turn.transcript" {
		t.Fatalf("trace name = %q, want claude.turn.transcript: %s", name, canonicalLiveJSON(trace))
	}

	observations := liveClaudeObservations(t, cfg, traceID)
	for _, name := range []string{
		"claude.agent",
		"claude.transcript",
		"claude.terminal",
		agenttrace.ToolObservationName(agenttrace.ProviderClaude, agenttrace.ToolFamilyCommand),
		agenttrace.ToolObservationName(agenttrace.ProviderClaude, agenttrace.ToolFamilyFileChange),
		agenttrace.ToolObservationName(agenttrace.ProviderClaude, agenttrace.ToolFamilyMCP),
	} {
		if observations[name] == nil {
			t.Fatalf("missing live Claude observation %s in %s", name, canonicalLiveJSON(observations))
		}
	}

	transcript := observations["claude.transcript"]
	if model := liveStringValue(transcript["model"]); !strings.HasPrefix(model, "claude-") {
		t.Fatalf("claude.transcript model = %q: %s", model, canonicalLiveJSON(transcript))
	}
	if modelID := liveStringValue(transcript["modelId"]); modelID == "" {
		t.Fatalf("claude.transcript modelId is empty; Langfuse pricing did not match: %s", canonicalLiveJSON(transcript))
	}
	usage := liveMapValue(transcript["usageDetails"])
	if liveIntValue(usage["input"]) == 0 || liveIntValue(usage["output"]) == 0 || liveIntValue(usage["total"]) == 0 {
		t.Fatalf("claude.transcript usageDetails incomplete: %s", canonicalLiveJSON(transcript))
	}
	if cost := liveFloatValue(transcript["calculatedTotalCost"]); cost <= 0 {
		t.Fatalf("claude.transcript calculatedTotalCost = %v, want > 0: %s", transcript["calculatedTotalCost"], canonicalLiveJSON(transcript))
	}

	tags := liveStringSlice(trace["tags"])
	for _, tag := range []string{"tool:command", "tool:file_change", "tool:mcp"} {
		if !liveHasString(tags, tag) {
			t.Fatalf("trace tags missing %q in %#v", tag, tags)
		}
	}
	hasMCPServerTag := false
	for _, tag := range tags {
		if strings.HasPrefix(tag, "mcp:") {
			hasMCPServerTag = true
		}
	}
	if !hasMCPServerTag {
		t.Fatalf("trace tags missing mcp:<server> tag in %#v", tags)
	}
}

func liveClaudeObservations(t *testing.T, cfg config.LangfuseConfig, traceID string) map[string]map[string]any {
	t.Helper()
	body := liveGet(t, cfg, "/api/public/observations?traceId="+url.QueryEscape(traceID)+"&limit=100")
	observations := map[string]map[string]any{}
	for _, raw := range liveSliceValue(body["data"]) {
		observation := liveMapValue(raw)
		name := liveStringValue(observation["name"])
		if name != "" {
			observations[name] = observation
		}
	}
	return observations
}

func liveStringSlice(value any) []string {
	var result []string
	for _, raw := range liveSliceValue(value) {
		if text := liveStringValue(raw); text != "" {
			result = append(result, text)
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
