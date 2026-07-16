package langfuse

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kirilligum/codex-langfuse-tracer/internal/agenttrace"
	"github.com/kirilligum/codex-langfuse-tracer/internal/buildinfo"
	"github.com/kirilligum/codex-langfuse-tracer/internal/config"
)

// TEST-008
func TestOTLPHTTPExport(t *testing.T) {
	t.Parallel()

	var gotPath, gotAuth, gotVersion string
	var gotBody bool
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

	status, err := ExportTurn(context.Background(), config.LangfuseConfig{
		Host:      server.URL,
		PublicKey: "pk-lf-test",
		SecretKey: "sk-lf-test",
	}, completeTurn(t), buildinfo.DefaultEnvironment, buildinfo.DefaultServiceName)
	if err != nil {
		t.Fatalf("ExportTurn: %v", err)
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
	status, err := ExportTurn(ctx, config.LangfuseConfig{
		Host:      server.URL,
		PublicKey: "pk-lf-test",
		SecretKey: "sk-lf-test",
	}, completeTurn(t), buildinfo.DefaultEnvironment, buildinfo.DefaultServiceName)
	if err == nil {
		t.Fatalf("ExportTurn status=%d err=nil, want error", status)
	}
}
