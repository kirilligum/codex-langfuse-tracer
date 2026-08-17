package langfuse

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kirilligum/codex-langfuse-tracer/internal/agenttrace"
	"github.com/kirilligum/codex-langfuse-tracer/internal/config"
)

func TestCreateDeterministicScores(t *testing.T) {
	t.Parallel()
	// TEST-702

	turn := completeTurn(t)
	var gotBatch scoreIngestionBatch
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/public/ingestion" {
			t.Fatalf("unexpected request %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBatch); err != nil {
			t.Fatalf("decode score batch: %v", err)
		}
		writeScoreBatchSuccess(t, w, gotBatch)
	}))
	defer server.Close()

	cfg := config.LangfuseConfig{Host: server.URL, PublicKey: "pk-test", SecretKey: "sk-test"}
	const environment = "repository--feature-one-a1b2c3"
	if err := CreateDeterministicScores(context.Background(), cfg, turn, environment); err != nil {
		t.Fatalf("CreateDeterministicScores: %v", err)
	}
	if len(gotBatch.Batch) != len(agenttrace.BuildDeterministicScores(turn)) {
		t.Fatalf("events = %d, want %d", len(gotBatch.Batch), len(agenttrace.BuildDeterministicScores(turn)))
	}
	seen := map[string]scoreIngestionEvent{}
	for _, event := range gotBatch.Batch {
		seen[event.Body.Name] = event
		if event.ID == "" || event.Type != "score-create" || event.Timestamp == "" || event.Body.ID == "" || event.Body.TraceID != turn.TraceID || event.Body.Environment != environment {
			t.Fatalf("score event incomplete: %#v", event)
		}
	}
	if seen["verification_passed"].Body.DataType != agenttrace.ScoreDataTypeBoolean {
		t.Fatalf("verification_passed = %#v", seen["verification_passed"])
	}
	if seen["outcome"].Body.DataType != agenttrace.ScoreDataTypeCategorical || seen["outcome"].Body.Value == "" {
		t.Fatalf("outcome = %#v", seen["outcome"])
	}
	if seen["changed_file_count"].Body.DataType != agenttrace.ScoreDataTypeNumeric {
		t.Fatalf("changed_file_count = %#v", seen["changed_file_count"])
	}
}

func TestCreateDeterministicScoresRejectsBatchErrors(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusMultiStatus)
		_, _ = w.Write([]byte(`{"successes":[],"errors":[{"id":"event-id","status":400}]}`))
	}))
	defer server.Close()

	cfg := config.LangfuseConfig{Host: server.URL, PublicKey: "pk-test", SecretKey: "sk-test"}
	err := CreateDeterministicScores(context.Background(), cfg, completeTurn(t), "default")
	if err == nil || !strings.Contains(err.Error(), "accepted=0 errors=1") {
		t.Fatalf("CreateDeterministicScores error = %v", err)
	}
}

func writeScoreBatchSuccess(t *testing.T, w http.ResponseWriter, batch scoreIngestionBatch) {
	t.Helper()
	result := scoreIngestionResponse{Successes: make([]struct {
		ID     string `json:"id"`
		Status int    `json:"status"`
	}, 0, len(batch.Batch))}
	for _, event := range batch.Batch {
		result.Successes = append(result.Successes, struct {
			ID     string `json:"id"`
			Status int    `json:"status"`
		}{ID: event.ID, Status: http.StatusCreated})
	}
	w.WriteHeader(http.StatusMultiStatus)
	if err := json.NewEncoder(w).Encode(result); err != nil {
		t.Fatalf("encode score batch response: %v", err)
	}
}
