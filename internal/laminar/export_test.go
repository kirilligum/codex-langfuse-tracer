package laminar

import (
	"context"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kirilligum/codex-langfuse-tracer/internal/agenttrace"
	"github.com/kirilligum/codex-langfuse-tracer/internal/buildinfo"
	"github.com/kirilligum/codex-langfuse-tracer/internal/codextrace"
	"github.com/kirilligum/codex-langfuse-tracer/internal/config"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/protobuf/proto"
)

func TestLaminarSpanProjectionPreservesTraceContextAndMetadata(t *testing.T) {
	t.Parallel()

	turn := completeTurn(t)
	exporter := &memoryExporter{}
	if err := EmitSpans(context.Background(), turn, "repo--main-a1b2c3", "shaman", buildinfo.DefaultServiceName, exporter); err != nil {
		t.Fatalf("EmitSpans: %v", err)
	}
	spans := exporter.Snapshots()
	if got, want := len(spans), len(turn.Observations)+len(turn.ModelCalls)+2; got != want {
		t.Fatalf("span count = %d want %d", got, want)
	}

	root := spans.ByName("codex.agent")
	if root.TraceID != turn.TraceID {
		t.Fatalf("root trace id = %q want %q", root.TraceID, turn.TraceID)
	}
	if root.SpanID != agenttrace.StableSpanID("codex-agent", turn.TraceID, turn.TurnID, "") || root.ParentSpanID != "" {
		t.Fatalf("root identity is unstable: %#v", root)
	}
	if root.Attributes[spanTypeAttribute] != "DEFAULT" {
		t.Fatalf("root type = %q", root.Attributes[spanTypeAttribute])
	}
	if root.Attributes[spanInputAttribute] != jsonString(agenttrace.ExportText(turn.InputText())) || root.Attributes[spanOutputAttribute] != jsonString(agenttrace.ExportText(turn.OutputText())) {
		t.Fatal("root input/output does not match redacted canonical turn text")
	}
	if root.Attributes["lmnr.association.properties.user_id"] != "shaman" || root.Attributes[laminarMetadataPrefix+"environment"] != "repo--main-a1b2c3" {
		t.Fatalf("root identity metadata mismatch: %#v", root.Attributes)
	}
	if !strings.Contains(root.Attributes["lmnr.association.properties.tags"], "tool:command") {
		t.Fatalf("trace tags missing expected navigation tag: %q", root.Attributes["lmnr.association.properties.tags"])
	}
	if root.Attributes[laminarMetadataPrefix+"codex_insight_navigation"] == "" {
		t.Fatalf("insight navigation missing: %#v", root.Attributes)
	}
	if root.Attributes[laminarMetadataPrefix+"codex_score_had_file_changes"] != "true" {
		t.Fatalf("boolean deterministic score not typed as boolean: %#v", root.Attributes)
	}
	if root.Attributes[laminarMetadataPrefix+"codex_score_had_file_changes_data_type"] != agenttrace.ScoreDataTypeBoolean || root.Attributes[laminarMetadataPrefix+"codex_score_had_file_changes_comment"] == "" {
		t.Fatalf("deterministic score type or explanation missing: %#v", root.Attributes)
	}
	if root.ResourceAttrs["service.name"] != buildinfo.DefaultServiceName || root.ResourceAttrs["deployment.environment.name"] != "repo--main-a1b2c3" {
		t.Fatalf("resource attributes mismatch: %#v", root.ResourceAttrs)
	}
	for _, span := range spans {
		for key := range span.Attributes {
			if strings.HasPrefix(key, "langfuse.") {
				t.Fatalf("retained backend-specific Langfuse attribute %q", key)
			}
		}
	}

	transcript := spans.ByName("codex.transcript")
	if transcript.ParentSpanID != root.SpanID || transcript.Attributes[spanTypeAttribute] != "DEFAULT" {
		t.Fatalf("transcript is not a Laminar LLM child of the root: %#v", transcript)
	}
	if transcript.Attributes["gen_ai.system"] != "openai" || transcript.Attributes["gen_ai.request.model"] == "" {
		t.Fatalf("model identity missing: %#v", transcript.Attributes)
	}
	if transcript.Attributes["gen_ai.usage.input_tokens"] != "" || transcript.Attributes["gen_ai.usage.output_tokens"] != "" {
		t.Fatalf("aggregate transcript duplicates model-step usage: %#v", transcript.Attributes)
	}
	call := spans.ByName("codex.model.call.1")
	if call.Attributes[spanTypeAttribute] != "LLM" || call.ParentSpanID != root.SpanID || call.Attributes["gen_ai.usage.input_tokens"] == "" {
		t.Fatalf("model-step usage or hierarchy missing: %#v", call)
	}
}

func TestLaminarToolProjectionPreservesInputsOutputsAndErrors(t *testing.T) {
	t.Parallel()

	turn := failedCommandTurn(t)
	spans := emitTurnSpans(t, turn)
	var failed spanSnapshot
	for _, span := range spans {
		if span.Name == "codex.tool.command" && span.Attributes["error.type"] == "nonzero_exit" {
			failed = span
			break
		}
	}
	if failed.Name == "" {
		t.Fatalf("failed command span not projected with an error type: %#v", spans)
	}
	if failed.Attributes[spanTypeAttribute] != "TOOL" || failed.Attributes[spanInputAttribute] == "" || failed.Attributes[spanOutputAttribute] == "" {
		t.Fatalf("tool observation shape incomplete: %#v", failed.Attributes)
	}
	if failed.StatusCode != "Error" || failed.StatusDescription != "nonzero_exit" {
		t.Fatalf("tool OTel status = %s %q", failed.StatusCode, failed.StatusDescription)
	}
	if failed.Attributes[laminarMetadataPrefix+"observation_failure_type"] != "nonzero_exit" {
		t.Fatalf("tool metadata did not retain failure type: %#v", failed.Attributes)
	}
}

func TestLaminarClaudeProviderUsesAnthropicSemanticConvention(t *testing.T) {
	t.Parallel()

	turn := agenttrace.Turn{
		Provider: agenttrace.ProviderClaude, SessionID: "claude-session", TurnID: "turn-1",
		TraceID: agenttrace.StableTraceID(agenttrace.ProviderClaude, "claude-session", "turn-1"),
		StartTS: "2026-05-04T12:00:00Z", EndTS: "2026-05-04T12:00:01Z",
		UserMessages: []string{"Run pwd"}, AssistantTexts: []string{"Done"}, Model: "claude-sonnet-4-6",
		TokenUsage:   &agenttrace.TokenUsage{InputTokens: 20, OutputTokens: 3, CacheReadInputTokens: 8},
		Completed:    true,
		Observations: []agenttrace.Observation{{Name: "claude.tool.command", Type: "tool", Input: "pwd", Output: "/tmp"}},
	}
	spans := emitTurnSpans(t, turn)
	transcript := spans.ByName("claude.transcript")
	if transcript.Attributes["gen_ai.system"] != "anthropic" || transcript.Attributes["gen_ai.request.model"] != "claude-sonnet-4-6" {
		t.Fatalf("Claude model identity mismatch: %#v", transcript.Attributes)
	}
	if transcript.Attributes["gen_ai.usage.cache_read.input_tokens"] != "8" {
		t.Fatalf("Claude cache-read usage missing: %#v", transcript.Attributes)
	}
	if !strings.Contains(spans.ByName("claude.agent").Attributes["lmnr.association.properties.tags"], "tool:command") {
		t.Fatal("Claude tool navigation tag missing")
	}
}

func TestIncompleteTurnRejectedByLaminarProjection(t *testing.T) {
	t.Parallel()

	turn := completeTurn(t)
	turn.Completed = false
	if err := emitSpans(context.Background(), turn, "default", "shaman", buildinfo.DefaultServiceName, &memoryExporter{}); err == nil {
		t.Fatal("incomplete turn was accepted by span projection")
	}
}

func TestLaminarHTTPExportAuthenticatesAndSendsOneOTLPBatch(t *testing.T) {
	t.Parallel()

	const token = "test-receiver-token"
	var gotAuth string
	var gotPath string
	var batch collectortrace.ExportTraceServiceRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if err := proto.Unmarshal(body, &batch); err != nil {
			t.Errorf("decode OTLP batch: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	turn := completeTurn(t)
	status, err := ExportSpans(context.Background(), configForTest(server.URL, token), turn, "default", "shaman", buildinfo.DefaultServiceName)
	if err != nil {
		t.Fatalf("ExportSpans: %v", err)
	}
	if status != http.StatusOK || gotPath != "/v1/traces" || gotAuth != "Bearer "+token {
		t.Fatalf("status=%d path=%q auth=%q", status, gotPath, gotAuth)
	}
	var spanCount int
	for _, resourceSpans := range batch.ResourceSpans {
		for _, scopeSpans := range resourceSpans.ScopeSpans {
			spanCount += len(scopeSpans.Spans)
			for _, span := range scopeSpans.Spans {
				if got := hex.EncodeToString(span.TraceId); got != turn.TraceID {
					t.Fatalf("OTLP trace id = %s want %s", got, turn.TraceID)
				}
			}
		}
	}
	if spanCount != len(turn.Observations)+len(turn.ModelCalls)+2 {
		t.Fatalf("OTLP spans=%d want=%d", spanCount, len(turn.Observations)+len(turn.ModelCalls)+2)
	}
	if got := otlpString(batch.ResourceSpans[0].Resource.Attributes, "service.name"); got != buildinfo.DefaultServiceName {
		t.Fatalf("service.name = %q", got)
	}
}

func TestLaminarHTTPExportRejectsCollectorError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	status, err := ExportSpans(ctx, configForTest(server.URL, "test-token"), completeTurn(t), "default", "shaman", buildinfo.DefaultServiceName)
	if err == nil || status != http.StatusUnauthorized {
		t.Fatalf("ExportSpans status=%d err=%v, want HTTP 401 error", status, err)
	}
}

func emitTurnSpans(t *testing.T, turn agenttrace.Turn) spanSnapshots {
	t.Helper()
	exporter := &memoryExporter{}
	if err := emitSpans(context.Background(), turn, "default", "shaman", buildinfo.DefaultServiceName, exporter); err != nil {
		t.Fatalf("emitSpans: %v", err)
	}
	return exporter.Snapshots()
}

func completeTurn(t *testing.T) agenttrace.Turn {
	t.Helper()
	turns, err := codextrace.ParseTurns(filepath.Join("..", "..", "testdata", "sources", "codex", "complete-tools.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	exportable := agenttrace.ExportableTurns(turns)
	if len(exportable) != 1 {
		t.Fatalf("exportable turns = %d", len(exportable))
	}
	return exportable[0]
}

func failedCommandTurn(t *testing.T) agenttrace.Turn {
	t.Helper()
	turns, err := codextrace.ParseTurns(filepath.Join("..", "..", "testdata", "sources", "codex", "failed-command.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	exportable := agenttrace.ExportableTurns(turns)
	if len(exportable) != 1 {
		t.Fatalf("exportable failed turns = %d", len(exportable))
	}
	return exportable[0]
}

func configForTest(baseURL, token string) config.LaminarConfig {
	return config.LaminarConfig{BaseURL: baseURL, Token: token}
}

func otlpString(attributes []*commonv1.KeyValue, key string) string {
	for _, attribute := range attributes {
		if attribute.Key == key {
			return attribute.Value.GetStringValue()
		}
	}
	return ""
}
