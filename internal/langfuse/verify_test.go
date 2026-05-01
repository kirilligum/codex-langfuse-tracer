package langfuse

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kirilligum/codex-langfuse-tracer/internal/config"
)

// TEST-009
func TestTraceVerificationClient(t *testing.T) {
	t.Parallel()

	turn := completeTurn(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/public/traces/"+turn.TraceID {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") == "" {
			t.Fatal("missing auth")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"input":  "",
			"output": "",
			"observations": []map[string]any{
				{"name": "codex.transcript", "input": turn.InputText(), "output": "not yet"},
				{"name": "codex.transcript", "input": turn.InputText(), "output": "Checks passed with sk-lf-<redacted> and gh<redacted> redacted."},
			},
		})
	}))
	defer server.Close()

	hasInput, hasOutput, err := VerifyTraceIO(context.Background(), config.LangfuseConfig{
		Host:      server.URL,
		PublicKey: "pk-lf-test",
		SecretKey: "sk-lf-test",
	}, turn, time.Second, time.Millisecond)
	if err != nil {
		t.Fatalf("VerifyTraceIO: %v", err)
	}
	if !hasInput || !hasOutput {
		t.Fatalf("verify result = %v/%v", hasInput, hasOutput)
	}
}
