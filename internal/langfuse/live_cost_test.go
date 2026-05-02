package langfuse

import (
	"context"
	"encoding/json"
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
	trace := liveTraceForSession(t, cfg, sessionID)
	transcript := liveTranscriptObservation(t, cfg, liveStringValue(trace["id"]))

	if liveStringValue(transcript["model"]) == "" {
		t.Fatalf("codex.transcript model is empty; usage cannot match Langfuse model pricing: %s", liveCostSummary(transcript))
	}
	usage := liveMapValue(transcript["usageDetails"])
	if liveIntValue(usage["input"]) == 0 || liveIntValue(usage["output"]) == 0 || liveIntValue(usage["total"]) == 0 {
		t.Fatalf("codex.transcript usageDetails incomplete: %s", liveCostSummary(transcript))
	}
	if cost := liveFloatValue(transcript["calculatedTotalCost"]); cost <= 0 {
		t.Fatalf("codex.transcript calculatedTotalCost = %v, want > 0: %s", transcript["calculatedTotalCost"], liveCostSummary(transcript))
	}
	if cost := liveFloatValue(trace["totalCost"]); cost <= 0 {
		t.Fatalf("trace totalCost = %v, want > 0: %s", trace["totalCost"], canonicalLiveJSON(map[string]any{
			"id":        trace["id"],
			"sessionId": trace["sessionId"],
			"totalCost": trace["totalCost"],
		}))
	}
}

func liveTraceForSession(t *testing.T, cfg config.LangfuseConfig, sessionID string) map[string]any {
	t.Helper()
	body := liveGet(t, cfg, "/api/public/traces?sessionId="+url.QueryEscape(sessionID)+"&limit=10")
	for _, raw := range liveSliceValue(body["data"]) {
		trace := liveMapValue(raw)
		if liveStringValue(trace["sessionId"]) == sessionID {
			return trace
		}
	}
	t.Fatalf("no Langfuse trace found for session %s: %s", sessionID, canonicalLiveJSON(body))
	return nil
}

func liveTranscriptObservation(t *testing.T, cfg config.LangfuseConfig, traceID string) map[string]any {
	t.Helper()
	body := liveGet(t, cfg, "/api/public/observations?traceId="+url.QueryEscape(traceID)+"&name=codex.transcript&limit=10")
	for _, raw := range liveSliceValue(body["data"]) {
		observation := liveMapValue(raw)
		if liveStringValue(observation["name"]) == "codex.transcript" {
			return observation
		}
	}
	t.Fatalf("no codex.transcript observation found for trace %s: %s", traceID, canonicalLiveJSON(body))
	return nil
}

func liveGet(t *testing.T, cfg config.LangfuseConfig, path string) map[string]any {
	t.Helper()
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
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body
}

func canonicalLiveJSON(value any) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}

func liveCostSummary(observation map[string]any) string {
	return canonicalLiveJSON(map[string]any{
		"id":                     observation["id"],
		"model":                  observation["model"],
		"modelId":                observation["modelId"],
		"usage":                  observation["usage"],
		"usageDetails":           observation["usageDetails"],
		"calculatedInputCost":    observation["calculatedInputCost"],
		"calculatedOutputCost":   observation["calculatedOutputCost"],
		"calculatedTotalCost":    observation["calculatedTotalCost"],
		"inputPrice":             observation["inputPrice"],
		"outputPrice":            observation["outputPrice"],
		"totalPrice":             observation["totalPrice"],
		"usagePricingTierId":     observation["usagePricingTierId"],
		"usagePricingTierName":   observation["usagePricingTierName"],
		"traceId":                observation["traceId"],
		"environment":            observation["environment"],
		"name":                   observation["name"],
		"type":                   observation["type"],
		"langfuseModelAttribute": liveMapValue(observation["metadata"])["attributes"],
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
	default:
		return 0
	}
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
	default:
		return 0
	}
}

func liveStringValue(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

func liveMapValue(value any) map[string]any {
	if typed, ok := value.(map[string]any); ok {
		return typed
	}
	return map[string]any{}
}

func liveSliceValue(value any) []any {
	if typed, ok := value.([]any); ok {
		return typed
	}
	return nil
}
