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
// TEST-506
func TestManualProviderExportCLIIntegration(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		provider   string
		fixture    string
		extraArgs  []string
		wantOutput []byte
	}{
		{name: "codex", provider: "codex", fixture: "complete-tools.jsonl", extraArgs: []string{"--turn-id", "turn-1"}, wantOutput: []byte("exported trace=1e087e4ea8aa8d8e29e604d2cd8704d9 status=200")},
		{name: "claude", provider: "claude", fixture: "no-tools.jsonl", wantOutput: []byte("exported trace=")},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			home := t.TempDir()
			sourcePath := copyProviderSourceFixture(t, home, tc.provider, tc.fixture)
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

			configPath := writeLangfuseConfig(t, home, server.URL)
			args := []string{"--provider", tc.provider, "--path", sourcePath, "--config", configPath, "--no-verify"}
			args = append(args, tc.extraArgs...)
			var stdout, stderr bytes.Buffer
			code := run(context.Background(), args, &stdout, &stderr)
			if code != 0 {
				t.Fatalf("run exit=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
			}
			if postCount != 1 {
				t.Fatalf("postCount = %d, want 1", postCount)
			}
			if !bytes.Contains(stdout.Bytes(), []byte("session_file="+sourcePath)) || !bytes.Contains(stdout.Bytes(), tc.wantOutput) {
				t.Fatalf("missing provider export stdout=%s", stdout.String())
			}
		})
	}
}

func TestManualExportCLINoExportableTurns(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	rolloutPath := copyCodexSourceFixture(t, home, "incomplete-turn.jsonl")
	configPath := writeLangfuseConfig(t, home, "http://127.0.0.1")

	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"--path", rolloutPath, "--config", configPath, "--no-verify"}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("run succeeded for incomplete rollout stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
	if !bytes.Contains(stderr.Bytes(), []byte("No completed Codex turns with visible input/output found")) {
		t.Fatalf("missing no-exportable error stderr=%s", stderr.String())
	}
}

func TestManualExportCLIVerificationFailure(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	rolloutPath := copyCodexSourceFixture(t, home, "complete-no-tools.jsonl")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/public/otel/v1/traces":
			w.WriteHeader(http.StatusOK)
		case "/api/public/traces/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa":
			_, _ = w.Write([]byte(`{"input":"","output":"","observations":[]}`))
		default:
			t.Fatalf("unexpected request %s", r.URL.Path)
		}
	}))
	defer server.Close()
	configPath := writeLangfuseConfig(t, home, server.URL)

	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{
		"--path", rolloutPath,
		"--config", configPath,
		"--verify-wait-seconds", "0",
		"--verify-interval-seconds", "0",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("run succeeded despite verification miss stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
	if !bytes.Contains(stderr.Bytes(), []byte("did not show exported input/output before timeout")) {
		t.Fatalf("missing verification failure stderr=%s", stderr.String())
	}
}

func TestRunWatchCanceled(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex"))
	configPath := writeLangfuseConfig(t, home, "http://127.0.0.1")
	statePath := filepath.Join(home, "state.json")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var stdout, stderr bytes.Buffer
	code := run(ctx, []string{
		"--watch",
		"--config", configPath,
		"--state-file", statePath,
		"--poll-interval-seconds", "0.001",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("watch run succeeded with canceled context stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
	if !bytes.Contains(stderr.Bytes(), []byte("context canceled")) {
		t.Fatalf("missing canceled error stderr=%s", stderr.String())
	}
}

func copyCodexSourceFixture(t *testing.T, dir, name string) string {
	t.Helper()
	rolloutPath := filepath.Join(dir, name)
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "sources", "codex", name))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rolloutPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return rolloutPath
}

func copyClaudeSourceFixture(t *testing.T, dir, name string) string {
	t.Helper()
	return copyProviderSourceFixture(t, dir, "claude", name)
}

func copyProviderSourceFixture(t *testing.T, dir, provider, name string) string {
	t.Helper()
	sourcePath := filepath.Join(dir, name)
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "sources", provider, name))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourcePath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return sourcePath
}

func writeLangfuseConfig(t *testing.T, dir, host string) string {
	t.Helper()
	configPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(configPath, []byte(`
[mcp_servers.langfuse.env]
LANGFUSE_HOST = "`+host+`"
LANGFUSE_PUBLIC_KEY = "pk-lf-test"
LANGFUSE_SECRET_KEY = "sk-lf-test"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	return configPath
}
