package watch

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kirilligum/codex-langfuse-tracer/internal/agenttrace"
	"github.com/kirilligum/codex-langfuse-tracer/internal/buildinfo"
	"github.com/kirilligum/codex-langfuse-tracer/internal/config"
	"github.com/kirilligum/codex-langfuse-tracer/internal/exportstate"
	"github.com/kirilligum/codex-langfuse-tracer/internal/langfuse"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"
)

type deliveryOTLPServer struct {
	server             *httptest.Server
	holdAcknowledgment atomic.Bool
	firstRecorded      chan struct{}
	mu                 sync.Mutex
	requests           [][]string
	errors             chan error
}

func newDeliveryOTLPServer(t *testing.T, holdAcknowledgment bool) *deliveryOTLPServer {
	t.Helper()
	result := &deliveryOTLPServer{
		firstRecorded: make(chan struct{}, 1),
		errors:        make(chan error, 8),
	}
	result.holdAcknowledgment.Store(holdAcknowledgment)
	result.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/public/otel/v1/traces" {
			result.errors <- errors.New("unexpected OTLP path")
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}
		identities, err := deliverySpanIdentities(r.Body)
		if err != nil {
			result.errors <- err
			http.Error(w, "invalid OTLP payload", http.StatusBadRequest)
			return
		}
		result.mu.Lock()
		result.requests = append(result.requests, identities)
		result.mu.Unlock()
		select {
		case result.firstRecorded <- struct{}{}:
		default:
		}
		if result.holdAcknowledgment.Load() {
			<-r.Context().Done()
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(result.server.Close)
	return result
}

func deliverySpanIdentities(body io.Reader) ([]string, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return nil, err
	}
	var request collectortrace.ExportTraceServiceRequest
	if err := proto.Unmarshal(data, &request); err != nil {
		return nil, err
	}
	identities := make([]string, 0)
	for _, resourceSpans := range request.ResourceSpans {
		for _, scopeSpans := range resourceSpans.ScopeSpans {
			for _, span := range scopeSpans.Spans {
				identities = append(identities, hex.EncodeToString(span.TraceId)+":"+hex.EncodeToString(span.SpanId))
			}
		}
	}
	sort.Strings(identities)
	if len(identities) == 0 {
		return nil, errors.New("OTLP payload contains no spans")
	}
	return identities, nil
}

func (server *deliveryOTLPServer) requestSnapshot() [][]string {
	server.mu.Lock()
	defer server.mu.Unlock()
	result := make([][]string, len(server.requests))
	for i, request := range server.requests {
		result[i] = append([]string(nil), request...)
	}
	return result
}

func (server *deliveryOTLPServer) checkErrors(t *testing.T) {
	t.Helper()
	select {
	case err := <-server.errors:
		t.Fatalf("OTLP mock handler: %v", err)
	default:
	}
}

func deliveryConfig(host string) config.LangfuseConfig {
	return config.LangfuseConfig{Host: host, PublicKey: "pk-lf-delivery-test", SecretKey: "sk-lf-delivery-test"}
}

func deliveryScanOptions(root, statePath, host string, now time.Time, scores *atomic.Int32) ScanOptions {
	return ScanOptions{
		Root: root, StatePath: statePath, Now: now,
		ResolveWorkspace: testWorkspace,
		ExportSpans: func(ctx context.Context, turn agenttrace.Turn, environment string) (int, error) {
			return langfuse.ExportSpans(ctx, deliveryConfig(host), turn, environment, "delivery-test-host", buildinfo.DefaultServiceName)
		},
		ExportScores: func(context.Context, agenttrace.Turn, string) error {
			scores.Add(1)
			return nil
		},
	}
}

// TEST-708
func TestWatchRetriesAfterLostAcknowledgement(t *testing.T) {
	t.Parallel()

	root, statePath, rolloutPath := watchFixture(t)
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	setMTime(t, rolloutPath, now.Add(-time.Second))
	initial := exportstate.State{Version: exportstate.Version, ScanWatermarkNS: now.Add(-time.Minute).UnixNano()}
	if err := exportstate.Save(context.Background(), statePath, initial); err != nil {
		t.Fatal(err)
	}
	initialBytes, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}

	mock := newDeliveryOTLPServer(t, true)
	var scoreCalls atomic.Int32
	opts := deliveryScanOptions(root, statePath, mock.server.URL, now, &scoreCalls)
	logicalAttempts := 0
	underlyingExport := opts.ExportSpans
	opts.ExportSpans = func(ctx context.Context, turn agenttrace.Turn, environment string) (int, error) {
		logicalAttempts++
		if logicalAttempts != 1 {
			return underlyingExport(ctx, turn, environment)
		}
		requestCtx, cancelRequest := context.WithCancel(ctx)
		cancelDone := make(chan struct{})
		go func() {
			defer close(cancelDone)
			select {
			case <-mock.firstRecorded:
				cancelRequest()
			case <-ctx.Done():
				cancelRequest()
			}
		}()
		status, exportErr := langfuse.ExportSpans(requestCtx, deliveryConfig(mock.server.URL), turn, environment, "delivery-test-host", buildinfo.DefaultServiceName)
		cancelRequest()
		<-cancelDone
		return status, exportErr
	}

	state, _, err := ScanOnce(context.Background(), opts, initial)
	if err != nil {
		t.Fatalf("first retryable scan: %v", err)
	}
	mock.checkErrors(t)
	firstRequests := mock.requestSnapshot()
	if len(firstRequests) == 0 {
		t.Fatal("mock receiver did not record the request before its acknowledgement was canceled")
	}
	if logicalAttempts != 1 || scoreCalls.Load() != 0 {
		t.Fatalf("first scan callbacks = spans:%d scores:%d, want spans:1 scores:0", logicalAttempts, scoreCalls.Load())
	}
	if state.HasProcessed(completeTraceID(t, rolloutPath)) || state.PendingScoreEnvironment(completeTraceID(t, rolloutPath)) != "" {
		t.Fatalf("first scan advanced in-memory checkpoints after lost acknowledgement: %+v", state)
	}
	if !reflect.DeepEqual(state, initial) {
		t.Fatalf("first scan changed returned state after lost acknowledgement: got=%+v want=%+v", state, initial)
	}
	stateBytes, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(stateBytes, initialBytes) {
		t.Fatalf("first scan changed durable state after lost acknowledgement: got=%s want=%s", stateBytes, initialBytes)
	}

	mock.holdAcknowledgment.Store(false)
	state, _, err = ScanOnce(context.Background(), opts, state)
	if err != nil {
		t.Fatalf("recovery scan: %v", err)
	}
	mock.checkErrors(t)
	if logicalAttempts != 2 || scoreCalls.Load() != 1 {
		t.Fatalf("recovery callbacks = spans:%d scores:%d, want spans:2 total scores:1", logicalAttempts, scoreCalls.Load())
	}
	traceID := completeTraceID(t, rolloutPath)
	if !state.HasProcessed(traceID) || state.PendingScoreEnvironment(traceID) != "" {
		t.Fatalf("recovery did not commit processed state: %+v", state)
	}
	requests := mock.requestSnapshot()
	if len(requests) <= len(firstRequests) {
		t.Fatalf("recovery did not submit another request: before=%d after=%d", len(firstRequests), len(requests))
	}
	for _, request := range requests {
		if !reflect.DeepEqual(request, firstRequests[0]) {
			t.Fatalf("retry changed deterministic span identities: first=%v current=%v", firstRequests[0], request)
		}
	}

	_, exported, err := ScanOnce(context.Background(), withScanNow(opts, now.Add(time.Second)), state)
	if err != nil || exported != 0 || logicalAttempts != 2 || scoreCalls.Load() != 1 || len(mock.requestSnapshot()) != len(requests) {
		t.Fatalf("processed scan performed additional work: exported=%d attempts=%d scores=%d requests=%d err=%v", exported, logicalAttempts, scoreCalls.Load(), len(mock.requestSnapshot()), err)
	}
}
