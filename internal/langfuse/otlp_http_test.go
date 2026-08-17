package langfuse

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kirilligum/codex-langfuse-tracer/internal/agenttrace"
	"github.com/kirilligum/codex-langfuse-tracer/internal/buildinfo"
	"github.com/kirilligum/codex-langfuse-tracer/internal/config"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/protobuf/proto"
)

type otlpSpan struct {
	Name         string
	SpanID       string
	ParentSpanID string
	Environment  string
	UserID       string
}

// TEST-603
func TestOTLPProgressiveThenFinal(t *testing.T) {
	t.Parallel()

	requests := make(chan []otlpSpan, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/public/otel/v1/traces" {
			t.Fatalf("unexpected request %s", r.URL.Path)
		}
		requests <- decodeOTLPSpans(t, r)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := config.LangfuseConfig{Host: server.URL, PublicKey: "pk-lf-test", SecretKey: "sk-lf-test"}
	complete := completeTurn(t)
	partial := complete
	partial.Completed = false
	partial.EndTS = partial.Observations[0].EndTimeUnixNS
	partial.AssistantTexts = nil
	partial.TokenUsage = nil
	partial.Observations = append([]agenttrace.Observation(nil), complete.Observations[:1]...)
	const environment = "repository--feature-one-a1b2c3"
	const hostname = "devbox-01"

	if status, err := ExportSpans(context.Background(), cfg, partial, 0, false, environment, hostname, buildinfo.DefaultServiceName); err != nil || status != http.StatusOK {
		t.Fatalf("partial ExportSpans status=%d err=%v", status, err)
	}
	if status, err := ExportSpans(context.Background(), cfg, complete, 1, true, environment, hostname, buildinfo.DefaultServiceName); err != nil || status != http.StatusOK {
		t.Fatalf("final ExportSpans status=%d err=%v", status, err)
	}

	partialSpans := <-requests
	finalSpans := <-requests
	if len(partialSpans) != 1 {
		t.Fatalf("partial spans = %+v, want one selected observation", partialSpans)
	}
	if got, want := partialSpans[0].SpanID, agenttrace.StableSpanID(complete.Profile().ObservationPrefix, complete.TraceID, complete.TurnID, "0"); got != want {
		t.Fatalf("partial observation span id = %s, want %s", got, want)
	}
	if got, want := partialSpans[0].ParentSpanID, agenttrace.StableSpanID(complete.Profile().AgentSpanPrefix, complete.TraceID, complete.TurnID, ""); got != want {
		t.Fatalf("partial parent span id = %s, want future agent %s", got, want)
	}
	if got, want := len(finalSpans), len(complete.Observations)-1+3; got != want {
		t.Fatalf("final spans = %+v, count=%d want=%d", finalSpans, got, want)
	}
	for _, span := range finalSpans {
		if span.SpanID == partialSpans[0].SpanID {
			t.Fatalf("final request resent checkpointed observation: %+v", span)
		}
	}
	for _, span := range append(partialSpans, finalSpans...) {
		if span.Environment != environment || span.UserID != hostname {
			t.Fatalf("progressive identity mismatch: %#v", span)
		}
	}
}

func decodeOTLPSpans(t *testing.T, request *http.Request) []otlpSpan {
	t.Helper()
	raw, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatal(err)
	}
	var batch collectortrace.ExportTraceServiceRequest
	if err := proto.Unmarshal(raw, &batch); err != nil {
		t.Fatalf("decode OTLP protobuf: %v", err)
	}
	var spans []otlpSpan
	for _, resourceSpans := range batch.ResourceSpans {
		resourceAttributes := otlpAttributes(resourceSpans.Resource.Attributes)
		for _, scopeSpans := range resourceSpans.ScopeSpans {
			for _, span := range scopeSpans.Spans {
				attributes := otlpAttributes(span.Attributes)
				spans = append(spans, otlpSpan{
					Name:         span.Name,
					SpanID:       hex.EncodeToString(span.SpanId),
					ParentSpanID: hex.EncodeToString(span.ParentSpanId),
					Environment:  resourceAttributes["langfuse.environment"],
					UserID:       attributes["langfuse.user.id"],
				})
			}
		}
	}
	return spans
}

func otlpAttributes(attributes []*commonv1.KeyValue) map[string]string {
	result := make(map[string]string, len(attributes))
	for _, attribute := range attributes {
		result[attribute.Key] = attribute.Value.GetStringValue()
	}
	return result
}

// TEST-008
func TestOTLPHTTPExport(t *testing.T) {
	t.Parallel()
	// TEST-702

	var gotPath, gotAuth, gotVersion string
	var gotBody bool
	var gotSpans []otlpSpan
	scoreBatches := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/public/otel/v1/traces":
			gotPath = r.URL.Path
			gotAuth = r.Header.Get("Authorization")
			gotVersion = r.Header.Get("x-langfuse-ingestion-version")
			if r.ContentLength != 0 {
				gotBody = true
			}
			gotSpans = decodeOTLPSpans(t, r)
		case "/api/public/ingestion":
			scoreBatches++
			var batch scoreIngestionBatch
			if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
				t.Fatalf("decode score batch: %v", err)
			}
			if len(batch.Batch) != len(agenttrace.BuildDeterministicScores(completeTurn(t))) {
				t.Fatalf("score batch incomplete: %#v", batch)
			}
			writeScoreBatchSuccess(t, w, batch)
			return
		default:
			t.Fatalf("unexpected request %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := config.LangfuseConfig{
		Host:      server.URL,
		PublicKey: "pk-lf-test",
		SecretKey: "sk-lf-test",
	}
	turn := completeTurn(t)
	const environment = "repository--feature-one-a1b2c3"
	const hostname = "devbox-01"
	status, err := ExportSpans(context.Background(), cfg, turn, 0, true, environment, hostname, buildinfo.DefaultServiceName)
	if err != nil {
		t.Fatalf("ExportSpans: %v", err)
	}
	if err := CreateDeterministicScores(context.Background(), cfg, turn, environment); err != nil {
		t.Fatalf("CreateDeterministicScores: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	if gotPath != "/api/public/otel/v1/traces" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotAuth != "Basic cGstbGYtdGVzdDpzay1sZi10ZXN0" {
		t.Fatalf("auth = %q", gotAuth)
	}
	if gotVersion != "4" {
		t.Fatalf("ingestion version = %q", gotVersion)
	}
	if !gotBody {
		t.Fatal("empty OTLP body")
	}
	if len(gotSpans) == 0 {
		t.Fatal("no OTLP spans")
	}
	for _, span := range gotSpans {
		if span.Environment != environment || span.UserID != hostname {
			t.Fatalf("OTLP identity mismatch: %#v", span)
		}
	}
	if scoreBatches != 1 {
		t.Fatalf("score batches = %d", scoreBatches)
	}
}

func TestOTLPHTTPExportFailure(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	status, err := ExportSpans(ctx, config.LangfuseConfig{
		Host:      server.URL,
		PublicKey: "pk-lf-test",
		SecretKey: "sk-lf-test",
	}, completeTurn(t), 0, true, "default", "test-host", buildinfo.DefaultServiceName)
	if err == nil {
		t.Fatalf("ExportSpans status=%d err=nil, want error", status)
	}
}
