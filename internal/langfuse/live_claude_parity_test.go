package langfuse

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

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

// TestLiveClaudeSmokeTrace checks a basic automatically exported Claude turn
// without requiring the tool observations needed by TestLiveClaudeParityTrace.
// TEST-534
func TestLiveClaudeSmokeTrace(t *testing.T) {
	traceID := os.Getenv("LIVE_LANGFUSE_CLAUDE_SMOKE_TRACE_ID")
	if traceID == "" {
		t.Skip("set LIVE_LANGFUSE_CLAUDE_SMOKE_TRACE_ID to verify a live Claude smoke trace")
	}

	cfg, err := config.Load(config.DefaultConfigPath())
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client := NewObservationClient(cfg)
	query := ObservationQuery{TraceID: traceID, Fields: "core,basic,io,trace_context", Limit: 1000}

	var previousSignature string
	var stableSince time.Time
	var lastErr error
	var lastCount int
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		observations, listErr := client.List(ctx, query)
		lastErr = listErr
		lastCount = len(observations)
		if listErr == nil {
			roots, transcripts, validationErr := validateClaudeObservationRows(traceID, observations)
			if validationErr != nil {
				t.Fatal(validationErr)
			}
			if roots > 1 || transcripts > 1 {
				t.Fatalf("trace %s has duplicate root/transcript rows: roots=%d transcripts=%d observations=%d", traceID, roots, transcripts, len(observations))
			}
			if roots == 1 && transcripts == 1 {
				signature := claudeObservationSignature(observations)
				if signature != previousSignature {
					previousSignature = signature
					stableSince = time.Now()
				} else if time.Since(stableSince) >= 5*time.Second {
					t.Logf("trace_id=%s root_count=%d transcript_count=%d observation_count=%d stable_for=%s", traceID, roots, transcripts, len(observations), time.Since(stableSince).Round(time.Second))
					return
				}
			} else {
				previousSignature = ""
				stableSince = time.Time{}
			}
		}

		select {
		case <-ctx.Done():
			if lastErr != nil {
				t.Fatalf("timed out waiting for a stable Claude smoke trace %s (observations=%d): %v", traceID, lastCount, lastErr)
			}
			t.Fatalf("timed out waiting for a stable Claude smoke trace %s (observations=%d)", traceID, lastCount)
		case <-ticker.C:
		}
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
	observations := liveObservationsForTrace(t, cfg, traceID, "core,basic,io,metadata,model,usage,trace_context")
	roots, transcripts, err := validateClaudeObservationRows(traceID, observations)
	if err != nil {
		t.Fatal(err)
	}
	if roots != 1 || transcripts != 1 {
		t.Fatalf("Claude trace %s has roots=%d transcripts=%d; want exactly one of each", traceID, roots, transcripts)
	}
	for _, observation := range observations {
		if observation.Name != "" {
			result[observation.Name] = observation
		}
	}
	return result
}

func validateClaudeObservationRows(traceID string, observations []Observation) (int, int, error) {
	if strings.TrimSpace(traceID) == "" {
		return 0, 0, fmt.Errorf("Claude trace ID is empty")
	}
	seenIDs := make(map[string]struct{}, len(observations))
	var roots, transcripts int
	for _, observation := range observations {
		if observation.ID == "" {
			return roots, transcripts, fmt.Errorf("Claude trace %s contains an observation with no ID", traceID)
		}
		if _, exists := seenIDs[observation.ID]; exists {
			return roots, transcripts, fmt.Errorf("Claude trace %s contains repeated observation ID %s", traceID, observation.ID)
		}
		seenIDs[observation.ID] = struct{}{}
		if observation.TraceID != traceID {
			return roots, transcripts, fmt.Errorf("Claude trace query %s returned an observation for trace %s", traceID, observation.TraceID)
		}
		if observation.Name == "claude.transcript" && observation.IsRootObservation {
			return roots, transcripts, fmt.Errorf("Claude trace %s claude.transcript observation is marked as root", traceID)
		}
		if observation.IsRootObservation {
			roots++
			if observation.Name != "claude.agent" {
				return roots, transcripts, fmt.Errorf("Claude trace %s root observation has unexpected name %s", traceID, observation.Name)
			}
			var input, output string
			if json.Unmarshal([]byte(observation.Input), &input) != nil || strings.TrimSpace(input) == "" {
				return roots, transcripts, fmt.Errorf("Claude trace %s root observation has no serialized input", traceID)
			}
			if json.Unmarshal([]byte(observation.Output), &output) != nil || strings.TrimSpace(output) == "" {
				return roots, transcripts, fmt.Errorf("Claude trace %s root observation has no serialized output", traceID)
			}
		}
		if observation.Name == "claude.agent" && !observation.IsRootObservation {
			return roots, transcripts, fmt.Errorf("Claude trace %s claude.agent observation is not a root", traceID)
		}
		if observation.Name == "claude.transcript" {
			transcripts++
		}
	}
	return roots, transcripts, nil
}

func claudeObservationSignature(observations []Observation) string {
	rows := make([]string, 0, len(observations))
	for _, observation := range observations {
		rows = append(rows, observation.ID+"\x00"+observation.Name+"\x00"+strconv.FormatBool(observation.IsRootObservation))
	}
	sort.Strings(rows)
	return strings.Join(rows, "\n")
}

func liveHasString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
