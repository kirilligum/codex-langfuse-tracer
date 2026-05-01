package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// TEST-015
func TestManualExportCLIIntegration(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	rolloutPath := filepath.Join(home, "rollout.jsonl")
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "rollouts", "complete-tools.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rolloutPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	postCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/public/otel/v1/traces" {
			postCount++
			w.WriteHeader(http.StatusOK)
			return
		}
		t.Fatalf("unexpected request %s", r.URL.Path)
	}))
	defer server.Close()

	configPath := filepath.Join(home, "config.toml")
	if err := os.WriteFile(configPath, []byte(`
[mcp_servers.langfuse.env]
LANGFUSE_HOST = "`+server.URL+`"
LANGFUSE_PUBLIC_KEY = "pk-lf-test"
LANGFUSE_SECRET_KEY = "sk-lf-test"
`), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"--path", rolloutPath, "--config", configPath, "--turn-id", "turn-1", "--no-verify"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run exit=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if postCount != 1 {
		t.Fatalf("postCount = %d, want 1", postCount)
	}
	if !bytes.Contains(stdout.Bytes(), []byte("exported trace=1e087e4ea8aa8d8e29e604d2cd8704d9 status=200")) {
		t.Fatalf("missing export line stdout=%s", stdout.String())
	}
}
