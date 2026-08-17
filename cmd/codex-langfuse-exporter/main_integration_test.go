package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kirilligum/codex-langfuse-tracer/internal/agenttrace"
	"github.com/kirilligum/codex-langfuse-tracer/internal/langfuse"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/protobuf/proto"
)

// TEST-704
func TestManualWorkspaceIdentity(t *testing.T) {
	home := t.TempDir()
	repository := filepath.Join(home, "Repository")
	nested := filepath.Join(repository, "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, repository, "init")
	runTestGit(t, repository,
		"-c", "user.name=Test",
		"-c", "user.email=test@example.com",
		"commit", "--allow-empty", "-m", "initial",
	)
	runTestGit(t, repository, "checkout", "-b", "Feature/One")

	rolloutPath := copyCodexSourceFixture(t, home, "complete-tools.jsonl")
	raw, err := os.ReadFile(rolloutPath)
	if err != nil {
		t.Fatal(err)
	}
	raw = []byte(strings.ReplaceAll(string(raw), "/tmp/codex-langfuse-fixture", nested))
	if err := os.WriteFile(rolloutPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	_, wantEnvironment, err := langfuse.ResolveWorkspace(context.Background(), agenttrace.Turn{CWD: nested})
	if err != nil {
		t.Fatal(err)
	}

	const wantHostname = "devbox-01"
	hostnameCalls := 0
	previousHostnameUserID := hostnameUserID
	hostnameUserID = func() (string, error) {
		hostnameCalls++
		return wantHostname, nil
	}
	t.Cleanup(func() { hostnameUserID = previousHostnameUserID })

	var spanEnvironments []string
	var spanUserIDs []string
	var scoreEnvironments []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/public/otel/v1/traces":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatal(err)
			}
			var request collectortrace.ExportTraceServiceRequest
			if err := proto.Unmarshal(body, &request); err != nil {
				t.Fatal(err)
			}
			for _, resourceSpans := range request.ResourceSpans {
				environment := testOTLPString(resourceSpans.Resource.Attributes, "langfuse.environment")
				for _, scopeSpans := range resourceSpans.ScopeSpans {
					for _, span := range scopeSpans.Spans {
						spanEnvironments = append(spanEnvironments, environment)
						spanUserIDs = append(spanUserIDs, testOTLPString(span.Attributes, "langfuse.user.id"))
					}
				}
			}
			w.WriteHeader(http.StatusOK)
		case "/api/public/ingestion":
			var batch struct {
				Batch []struct {
					Body struct {
						Environment string `json:"environment"`
					} `json:"body"`
				} `json:"batch"`
			}
			if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
				t.Fatal(err)
			}
			for _, event := range batch.Batch {
				scoreEnvironments = append(scoreEnvironments, event.Body.Environment)
			}
			successes := make([]map[string]any, len(batch.Batch))
			for index := range successes {
				successes[index] = map[string]any{"id": "accepted", "status": http.StatusCreated}
			}
			w.WriteHeader(http.StatusMultiStatus)
			_ = json.NewEncoder(w).Encode(map[string]any{"successes": successes, "errors": []any{}})
		case "/api/public/projects":
			_, _ = w.Write([]byte(`{"data":[{"id":"project-test"}]}`))
		default:
			t.Fatalf("unexpected request %s", r.URL.Path)
		}
	}))
	defer server.Close()

	configPath := writeLangfuseConfig(t, home, server.URL)
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"--path", rolloutPath, "--config", configPath, "--no-verify"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run exit=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if hostnameCalls != 1 {
		t.Fatalf("hostname calls = %d, want 1", hostnameCalls)
	}
	if len(spanEnvironments) == 0 || len(scoreEnvironments) == 0 {
		t.Fatalf("missing identity payloads spans=%v scores=%v", spanEnvironments, scoreEnvironments)
	}
	for _, environment := range append(spanEnvironments, scoreEnvironments...) {
		if environment != wantEnvironment {
			t.Fatalf("environment = %q, want %q", environment, wantEnvironment)
		}
	}
	for _, userID := range spanUserIDs {
		if userID != wantHostname {
			t.Fatalf("user id = %q, want %q", userID, wantHostname)
		}
	}
}

func testOTLPString(attributes []*commonv1.KeyValue, key string) string {
	for _, attribute := range attributes {
		if attribute.Key == key {
			return attribute.Value.GetStringValue()
		}
	}
	return ""
}

func runTestGit(t *testing.T, directory string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}

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
			scoreBatchCount := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/public/otel/v1/traces":
					postCount++
					w.WriteHeader(http.StatusOK)
					return
				case "/api/public/ingestion":
					scoreBatchCount++
					writeTestIngestionSuccess(t, w, r)
					return
				case "/api/public/projects":
					_, _ = w.Write([]byte(`{"data":[{"id":"project-test"}]}`))
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
			if scoreBatchCount != 1 {
				t.Fatalf("scoreBatchCount = %d, want 1", scoreBatchCount)
			}
			if !bytes.Contains(stdout.Bytes(), []byte("session_file="+sourcePath)) || !bytes.Contains(stdout.Bytes(), tc.wantOutput) || !bytes.Contains(stdout.Bytes(), []byte("trace_url="+server.URL+"/project/project-test/traces/")) {
				t.Fatalf("missing provider export stdout=%s", stdout.String())
			}
		})
	}
}

func TestManualExportCLIJSONOutput(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	rolloutPath := copyCodexSourceFixture(t, home, "complete-tools.jsonl")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/public/otel/v1/traces":
			w.WriteHeader(http.StatusOK)
		case "/api/public/ingestion":
			writeTestIngestionSuccess(t, w, r)
		case "/api/public/projects":
			_, _ = w.Write([]byte(`{"data":[{"id":"project-test"}]}`))
		default:
			t.Fatalf("unexpected request %s", r.URL.Path)
		}
	}))
	defer server.Close()
	configPath := writeLangfuseConfig(t, home, server.URL)

	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"--path", rolloutPath, "--config", configPath, "--no-verify", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run exit=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if bytes.Contains(stdout.Bytes(), []byte("session_file=")) {
		t.Fatalf("json output included plain text: %s", stdout.String())
	}
	var result exportResult
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &result); err != nil {
		t.Fatalf("parse json output: %v\n%s", err, stdout.String())
	}
	if result.TraceID == "" || result.Status != http.StatusOK || result.TraceURL != server.URL+"/project/project-test/traces/"+result.TraceID {
		t.Fatalf("json result = %+v", result)
	}
}

func writeTestIngestionSuccess(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	var body struct {
		Batch []struct {
			ID string `json:"id"`
		} `json:"batch"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatalf("decode ingestion batch: %v", err)
	}
	if len(body.Batch) == 0 {
		t.Fatal("empty ingestion batch")
	}
	successes := make([]map[string]any, 0, len(body.Batch))
	for _, event := range body.Batch {
		successes = append(successes, map[string]any{"id": event.ID, "status": http.StatusCreated})
	}
	w.WriteHeader(http.StatusMultiStatus)
	if err := json.NewEncoder(w).Encode(map[string]any{"successes": successes, "errors": []any{}}); err != nil {
		t.Fatalf("encode ingestion response: %v", err)
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
		case "/api/public/ingestion":
			writeTestIngestionSuccess(t, w, r)
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
