package langfuse

import (
	"context"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kirilligum/codex-langfuse-tracer/internal/agenttrace"
	"github.com/kirilligum/codex-langfuse-tracer/internal/buildinfo"
	"github.com/kirilligum/codex-langfuse-tracer/internal/config"
)

// TEST-600
func TestLiveCompletedTraceShape(t *testing.T) {
	if os.Getenv("LIVE_LANGFUSE_COMPLETED_TRACE_PROBE") != "1" {
		t.Skip("set LIVE_LANGFUSE_COMPLETED_TRACE_PROBE=1 to run the local completed trace probe")
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
		t.Fatalf("completed trace probe requires loopback Langfuse, got host %q", parsedHost.Hostname())
	}

	started := time.Now().UTC()
	sessionID := "completed-probe-" + started.Format("20060102T150405.000000000Z")
	turnID := "turn-probe"
	traceID := agenttrace.StableTraceID(agenttrace.ProviderCodex, sessionID, turnID)
	turn := agenttrace.Turn{
		Provider:     agenttrace.ProviderCodex,
		SessionID:    sessionID,
		TurnID:       turnID,
		TraceID:      traceID,
		StartTS:      started.Format(time.RFC3339Nano),
		EndTS:        started.Add(2 * time.Millisecond).Format(time.RFC3339Nano),
		UserMessages: []string{"completed shape probe"},
		AssistantTexts: []string{
			"completed shape probe response",
		},
		Completed: true,
		Observations: []agenttrace.Observation{{
			Name:            "codex.tool.command",
			Type:            "tool",
			Input:           "printf probe",
			Output:          "probe",
			StartTimeUnixNS: strconv.FormatInt(started.UnixNano(), 10),
			EndTimeUnixNS:   strconv.FormatInt(started.Add(time.Millisecond).UnixNano(), 10),
			Metadata:        map[string]any{"status": "success", "failure_type": "none"},
		}},
	}
	userID, err := HostnameUserID()
	if err != nil {
		t.Fatal(err)
	}
	status, err := ExportSpans(context.Background(), cfg, turn, "default", userID, buildinfo.DefaultServiceName)
	if err != nil {
		t.Fatalf("export completed trace status=%d: %v", status, err)
	}

	observations := waitForCompletedObservations(t, cfg, traceID, 30*time.Second)
	byName := map[string]Observation{}
	seen := map[string]bool{}
	for _, observation := range observations {
		if seen[observation.ID] {
			t.Fatalf("duplicate observation id %q: %s", observation.ID, canonicalLiveJSON(observations))
		}
		seen[observation.ID] = true
		byName[observation.Name] = observation
		if strings.HasSuffix(observation.Name, ".terminal") {
			t.Fatalf("synthetic terminal observation present: %s", canonicalLiveJSON(observation))
		}
	}
	root, ok := byName["codex.agent"]
	if !ok || !root.IsRootObservation {
		t.Fatalf("missing logical root observation: %s", canonicalLiveJSON(observations))
	}
	if root.Input != agenttrace.ExportText(turn.InputText()) || root.Output != agenttrace.ExportText(turn.OutputText()) {
		t.Fatalf("root I/O = %q/%q, want %q/%q", root.Input, root.Output, agenttrace.ExportText(turn.InputText()), agenttrace.ExportText(turn.OutputText()))
	}
	transcript, ok := byName["codex.transcript"]
	if !ok || transcript.Input != root.Input || transcript.Output != root.Output {
		t.Fatalf("generation I/O does not match root: root=%s transcript=%s", canonicalLiveJSON(root), canonicalLiveJSON(transcript))
	}
	if _, ok := byName["codex.tool.command"]; !ok {
		t.Fatalf("missing command observation: %s", canonicalLiveJSON(observations))
	}

	t.Logf("trace_id=%s observation_count=%d", traceID, len(observations))
}

func waitForCompletedObservations(t *testing.T, cfg config.LangfuseConfig, traceID string, timeout time.Duration) []Observation {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var observations []Observation
	var lastErr error
	for time.Now().Before(deadline) {
		observations, lastErr = NewObservationClient(cfg).List(context.Background(), ObservationQuery{
			TraceID: traceID,
			Fields:  "core,basic,io,metadata,model,usage,trace_context",
			Limit:   100,
		})
		if lastErr == nil && len(observations) >= 3 {
			return observations
		}
		time.Sleep(250 * time.Millisecond)
	}
	if lastErr != nil {
		t.Fatalf("wait for completed observations: %v", lastErr)
	}
	t.Fatalf("timed out waiting for completed observations: %s", canonicalLiveJSON(observations))
	return nil
}

func isLoopbackHostname(hostname string) bool {
	if strings.EqualFold(hostname, "localhost") {
		return true
	}
	ip := net.ParseIP(hostname)
	return ip != nil && ip.IsLoopback()
}
