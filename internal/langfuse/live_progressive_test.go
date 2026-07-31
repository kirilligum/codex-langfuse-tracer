package langfuse

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kirilligum/codex-langfuse-tracer/internal/agenttrace"
	"github.com/kirilligum/codex-langfuse-tracer/internal/buildinfo"
	"github.com/kirilligum/codex-langfuse-tracer/internal/config"
)

// TEST-600
// EVAL-600
func TestLiveProgressiveChildBeforeParent(t *testing.T) {
	if os.Getenv("LIVE_LANGFUSE_PROGRESSIVE_PROBE") != "1" {
		t.Skip("set LIVE_LANGFUSE_PROGRESSIVE_PROBE=1 to run the local progressive trace probe")
	}

	cfg, err := config.Load(config.DefaultConfigPath())
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	parsedHost, err := url.Parse(cfg.Host)
	if err != nil {
		t.Fatalf("parse Langfuse host: %v", err)
	}
	if !isLoopbackHostname(parsedHost.Hostname()) {
		t.Fatalf("progressive probe requires loopback Langfuse, got host %q", parsedHost.Hostname())
	}

	probeStarted := time.Now().UTC()
	sessionID := fmt.Sprintf("progressive-probe-%d", probeStarted.UnixNano())
	turnID := "turn-probe"
	traceID := agenttrace.StableTraceID(agenttrace.ProviderCodex, sessionID, turnID)
	agentSpanID := agenttrace.StableSpanID("codex-agent", traceID, turnID, "")
	childSpanID := agenttrace.StableSpanID("codex-observation", traceID, turnID, "0")
	now := time.Now().UTC()
	turn := agenttrace.Turn{
		Provider:     agenttrace.ProviderCodex,
		SessionID:    sessionID,
		TurnID:       turnID,
		TraceID:      traceID,
		StartTS:      now.Format(time.RFC3339Nano),
		UserMessages: []string{"progressive feasibility probe"},
		Observations: []agenttrace.Observation{{
			Name:            "codex.tool.command",
			Type:            "tool",
			Input:           "probe",
			Output:          "complete",
			StartTimeUnixNS: fmt.Sprint(now.UnixNano()),
			EndTimeUnixNS:   fmt.Sprint(now.Add(time.Millisecond).UnixNano()),
			Metadata:        map[string]any{"status": "completed"},
		}},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	status, err := ExportSpans(ctx, cfg, turn, 0, false, buildinfo.DefaultEnvironment, buildinfo.DefaultServiceName)
	cancel()
	if err != nil {
		t.Fatalf("export partial spans status=%d: %v", status, err)
	}
	childBeforeParent := waitForLiveObservations(t, cfg, traceID, 1, 30*time.Second)
	child := liveObservationByID(childBeforeParent, childSpanID)
	if child == nil {
		t.Fatalf("child %s not visible before parent: %s", childSpanID, canonicalLiveJSON(childBeforeParent))
	}
	if liveObservationByID(childBeforeParent, agentSpanID) != nil {
		t.Fatalf("parent %s unexpectedly visible before ingestion", agentSpanID)
	}

	turn.Completed = true
	turn.AssistantTexts = []string{"progressive feasibility complete"}
	turn.EndTS = now.Add(2 * time.Millisecond).Format(time.RFC3339Nano)
	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
	status, err = ExportSpans(ctx, cfg, turn, 1, true, buildinfo.DefaultEnvironment, buildinfo.DefaultServiceName)
	cancel()
	if err != nil {
		t.Fatalf("export final spans status=%d: %v", status, err)
	}
	observations := waitForLiveObservations(t, cfg, traceID, 3, 30*time.Second)
	child = liveObservationByID(observations, childSpanID)
	parent := liveObservationByID(observations, agentSpanID)
	if child == nil || parent == nil {
		t.Fatalf("incomplete resolved hierarchy: %s", canonicalLiveJSON(observations))
	}
	if got := liveStringValue(child["parentObservationId"]); got != agentSpanID {
		t.Fatalf("child parentObservationId = %q, want %q: %s", got, agentSpanID, canonicalLiveJSON(child))
	}
	seen := map[string]bool{}
	for _, observation := range observations {
		id := liveStringValue(observation["id"])
		if seen[id] {
			t.Fatalf("duplicate observation id %q: %s", id, canonicalLiveJSON(observations))
		}
		seen[id] = true
	}
	if len(observations) != 3 {
		t.Fatalf("observation count = %d, want child, agent, and transcript: %s", len(observations), canonicalLiveJSON(observations))
	}

	t.Logf("trace_id=%s child_span_id=%s parent_span_id=%s elapsed=%s", traceID, childSpanID, agentSpanID, time.Since(probeStarted).Round(time.Millisecond))
}

func isLoopbackHostname(hostname string) bool {
	if strings.EqualFold(hostname, "localhost") {
		return true
	}
	ip := net.ParseIP(hostname)
	return ip != nil && ip.IsLoopback()
}

func waitForLiveObservations(t *testing.T, cfg config.LangfuseConfig, traceID string, minimum int, timeout time.Duration) []map[string]any {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastBody map[string]any
	var lastErr error
	for time.Now().Before(deadline) {
		lastBody, lastErr = liveGetResult(cfg, "/api/public/observations?traceId="+url.QueryEscape(traceID)+"&limit=100")
		if lastErr == nil {
			observations := make([]map[string]any, 0)
			for _, raw := range liveSliceValue(lastBody["data"]) {
				observations = append(observations, liveMapValue(raw))
			}
			if len(observations) >= minimum {
				return observations
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	if lastErr != nil {
		t.Fatalf("wait for live observations: %v", lastErr)
	}
	t.Fatalf("timed out waiting for %d observations: %s", minimum, canonicalLiveJSON(lastBody))
	return nil
}

func liveGetResult(cfg config.LangfuseConfig, path string) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(cfg.Host, "/")+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", AuthHeader(cfg))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("GET %s returned HTTP %d", path, resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	return body, nil
}

func liveObservationByID(observations []map[string]any, id string) map[string]any {
	for _, observation := range observations {
		if liveStringValue(observation["id"]) == id {
			return observation
		}
	}
	return nil
}
