package langfuse

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/kirilligum/codex-langfuse-tracer/internal/config"
)

// TEST-306
func TestLiveLangfuseTranscriptModelUsageAndCost(t *testing.T) {
	sessionID := os.Getenv("LIVE_LANGFUSE_SESSION_ID")
	if sessionID == "" {
		t.Skip("set LIVE_LANGFUSE_SESSION_ID to run live Langfuse cost verification")
	}

	cfg, err := config.Load(config.DefaultConfigPath())
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	observations := liveObservationsForSession(t, cfg, sessionID)
	transcript := liveNamedObservation(t, observations, "codex.transcript", "session "+sessionID)

	if transcript.ModelName() == "" {
		t.Fatalf("codex.transcript model is empty; usage cannot match Langfuse model pricing: %s", liveCostSummary(transcript))
	}
	if transcript.ModelID == "" {
		t.Fatalf("codex.transcript modelId is empty; Langfuse did not attach model pricing: %s", liveCostSummary(transcript))
	}
	if liveFloatValue(transcript.InputPrice) <= 0 || liveFloatValue(transcript.OutputPrice) <= 0 {
		t.Fatalf("codex.transcript prices are empty; Langfuse did not attach input/output pricing: %s", liveCostSummary(transcript))
	}
	if liveIntValue(transcript.UsageDetails["input"]) == 0 || liveIntValue(transcript.UsageDetails["output"]) == 0 || liveIntValue(transcript.UsageDetails["total"]) == 0 {
		t.Fatalf("codex.transcript usageDetails incomplete: %s", liveCostSummary(transcript))
	}
	if transcript.TotalCost <= 0 {
		t.Fatalf("codex.transcript totalCost = %v, want > 0: %s", transcript.TotalCost, liveCostSummary(transcript))
	}
}

func TestLiveWorkspaceIdentityTrace(t *testing.T) {
	traceID := os.Getenv("LIVE_LANGFUSE_IDENTITY_TRACE_ID")
	if traceID == "" {
		t.Skip("set LIVE_LANGFUSE_IDENTITY_TRACE_ID to run live workspace identity verification")
	}
	wantUserID := os.Getenv("LIVE_LANGFUSE_HOSTNAME")
	if wantUserID == "" {
		t.Skip("set LIVE_LANGFUSE_HOSTNAME to run live workspace identity verification")
	}
	wantEnvironment := os.Getenv("LIVE_LANGFUSE_ENVIRONMENT")
	if wantEnvironment == "" {
		t.Skip("set LIVE_LANGFUSE_ENVIRONMENT to run live workspace identity verification")
	}
	wantCWD := os.Getenv("LIVE_LANGFUSE_CWD")
	if wantCWD == "" {
		t.Skip("set LIVE_LANGFUSE_CWD to run live workspace identity verification")
	}
	wantBranch := os.Getenv("LIVE_LANGFUSE_BRANCH")
	if wantBranch == "" {
		t.Skip("set LIVE_LANGFUSE_BRANCH to run live workspace identity verification")
	}

	cfg, err := config.Load(config.DefaultConfigPath())
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	observations := liveObservationsForTrace(t, cfg, traceID, "core,basic,metadata,trace_context")
	if len(observations) == 0 {
		t.Fatalf("live identity trace has no observations")
	}
	for _, observation := range observations {
		if observation.UserID != wantUserID {
			t.Fatalf("observation %s userId = %q, want %q", observation.Name, observation.UserID, wantUserID)
		}
		if observation.Environment != wantEnvironment {
			t.Fatalf("observation %s environment = %q, want %q", observation.Name, observation.Environment, wantEnvironment)
		}
		if liveStringValue(observation.Metadata["cwd"]) != wantCWD {
			t.Fatalf("observation %s CWD metadata does not match", observation.Name)
		}
		if liveStringValue(observation.Metadata["git_branch"]) != wantBranch {
			t.Fatalf("observation %s git branch metadata does not match", observation.Name)
		}
	}

	scores := liveScores(t, cfg, traceID)
	if len(scores) == 0 {
		t.Fatal("live identity trace has no deterministic scores")
	}
	for _, score := range scores {
		if liveStringValue(score["environment"]) != wantEnvironment {
			t.Fatal("a deterministic score environment does not match the trace environment")
		}
	}
}

func liveObservationsForSession(t *testing.T, cfg config.LangfuseConfig, sessionID string) []Observation {
	t.Helper()
	return liveListObservations(t, cfg, ObservationQuery{
		Filter: stringFilter("sessionId", sessionID),
		Fields: "core,basic,metadata,model,usage,trace_context",
		Limit:  1000,
	})
}

func liveObservationsForTrace(t *testing.T, cfg config.LangfuseConfig, traceID, fields string) []Observation {
	t.Helper()
	return liveListObservations(t, cfg, ObservationQuery{TraceID: traceID, Fields: fields, Limit: 1000})
}

func liveListObservations(t *testing.T, cfg config.LangfuseConfig, query ObservationQuery) []Observation {
	t.Helper()
	observations, err := NewObservationClient(cfg).List(context.Background(), query)
	if err != nil {
		t.Fatalf("list Langfuse v2 observations: %v", err)
	}
	return observations
}

func liveNamedObservation(t *testing.T, observations []Observation, name, subject string) Observation {
	t.Helper()
	for _, observation := range observations {
		if observation.Name == name {
			return observation
		}
	}
	t.Fatalf("no %s observation found for %s: %s", name, subject, canonicalLiveJSON(observations))
	return Observation{}
}

func liveScores(t *testing.T, cfg config.LangfuseConfig, traceID string) []map[string]any {
	t.Helper()
	path := "/api/public/v3/scores?traceId=" + url.QueryEscape(traceID) + "&limit=100"
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, strings.TrimRight(cfg.Host, "/")+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", AuthHeader(cfg))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		t.Fatalf("GET %s returned HTTP %d", path, resp.StatusCode)
	}
	var body struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body.Data
}

func canonicalLiveJSON(value any) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}

func liveCostSummary(observation Observation) string {
	return canonicalLiveJSON(map[string]any{
		"id": observation.ID, "model": observation.ModelName(), "modelId": observation.ModelID,
		"usageDetails": observation.UsageDetails, "costDetails": observation.CostDetails,
		"inputPrice": observation.InputPrice, "outputPrice": observation.OutputPrice, "totalPrice": observation.TotalPrice,
		"totalCost": observation.TotalCost, "traceId": observation.TraceID, "environment": observation.Environment,
		"name": observation.Name, "type": observation.Type,
	})
}

func liveFloatValue(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case int:
		return float64(typed)
	case json.Number:
		result, _ := typed.Float64()
		return result
	case string:
		var result float64
		if _, err := fmt.Sscan(typed, &result); err == nil {
			return result
		}
	}
	return 0
}

func liveIntValue(value any) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	case json.Number:
		result, _ := typed.Int64()
		return int(result)
	case string:
		var result int
		if _, err := fmt.Sscan(typed, &result); err == nil {
			return result
		}
	}
	return 0
}

func liveStringValue(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}
