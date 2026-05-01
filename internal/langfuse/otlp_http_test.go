package langfuse

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kirilligum/codex-langfuse-tracer/internal/buildinfo"
	"github.com/kirilligum/codex-langfuse-tracer/internal/config"
)

// TEST-008
func TestOTLPHTTPExport(t *testing.T) {
	t.Parallel()

	var gotPath, gotAuth, gotVersion string
	var gotBody bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotVersion = r.Header.Get("x-langfuse-ingestion-version")
		if r.ContentLength != 0 {
			gotBody = true
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
}
