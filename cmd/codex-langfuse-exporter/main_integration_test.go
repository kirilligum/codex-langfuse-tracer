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
	"github.com/kirilligum/codex-langfuse-tracer/internal/laminar"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/protobuf/proto"
)

const testReceiverToken = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

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
	_, wantEnvironment, err := laminar.ResolveWorkspace(context.Background(), agenttrace.Turn{CWD: nested})
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
	var scoreCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/traces" || r.Header.Get("Authorization") != "Bearer "+testReceiverToken {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		var request collectortrace.ExportTraceServiceRequest
		if err := proto.Unmarshal(body, &request); err != nil {
			t.Fatal(err)
		}
		for _, resourceSpans := range request.ResourceSpans {
			for _, scopeSpans := range resourceSpans.ScopeSpans {
				for _, span := range scopeSpans.Spans {
					spanEnvironments = append(spanEnvironments, testOTLPString(span.Attributes, "lmnr.association.properties.metadata.environment"))
					spanUserIDs = append(spanUserIDs, testOTLPString(span.Attributes, "lmnr.association.properties.user_id"))
					if span.Name == "codex.agent" && testOTLPBool(span.Attributes, "lmnr.association.properties.metadata.codex_score_had_file_changes") {
						scoreCount++
					}
				}
			}
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	configPath := writeLaminarConfig(t, home, server.URL)
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"--path", rolloutPath, "--config", configPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run exit=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if hostnameCalls != 1 {
		t.Fatalf("hostname calls = %d, want 1", hostnameCalls)
	}
	if len(spanEnvironments) == 0 || scoreCount != 1 {
		t.Fatalf("missing environment or deterministic score metadata: environments=%v score_count=%d", spanEnvironments, scoreCount)
	}
	for _, environment := range spanEnvironments {
		if environment != wantEnvironment {
			t.Fatalf("environment = %q, want %q", environment, wantEnvironment)
		}
	}
	for _, userID := range spanUserIDs {
		if userID != wantHostname {
			t.Fatalf("user id = %q, want %q", userID, wantHostname)
		}
	}
	if !strings.Contains(stdout.String(), "collector_accepted trace=") {
		t.Fatalf("manual export did not state the actual acceptance boundary: %s", stdout.String())
	}
}

// TEST-015
// TEST-506
func TestManualProviderExportCLIIntegration(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		provider  string
		fixture   string
		wantTrace string
	}{
		{name: "codex", provider: "codex", fixture: "complete-tools.jsonl", wantTrace: "codex.agent"},
		{name: "claude", provider: "claude", fixture: "no-tools.jsonl", wantTrace: "claude.agent"},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			sourcePath := copyProviderSourceFixture(t, home, tc.provider, tc.fixture)
			postCount := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/traces" || r.Header.Get("Authorization") != "Bearer "+testReceiverToken {
					t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				postCount++
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Fatal(err)
				}
				var request collectortrace.ExportTraceServiceRequest
				if err := proto.Unmarshal(body, &request); err != nil {
					t.Fatal(err)
				}
				found := false
				for _, resourceSpans := range request.ResourceSpans {
					for _, scopeSpans := range resourceSpans.ScopeSpans {
						for _, span := range scopeSpans.Spans {
							if span.Name == tc.wantTrace {
								found = true
								if testOTLPString(span.Attributes, "lmnr.association.properties.metadata.provider") != tc.provider {
									t.Fatalf("%s root provider metadata missing", tc.name)
								}
							}
						}
					}
				}
				if !found {
					t.Fatalf("%s root span missing", tc.name)
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()

			configPath := writeLaminarConfig(t, home, server.URL)
			var stdout, stderr bytes.Buffer
			code := run(context.Background(), []string{"--provider", tc.provider, "--path", sourcePath, "--config", configPath}, &stdout, &stderr)
			if code != 0 {
				t.Fatalf("run exit=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
			}
			if postCount != 1 || !bytes.Contains(stdout.Bytes(), []byte("session_file="+sourcePath)) || !bytes.Contains(stdout.Bytes(), []byte("collector_accepted trace=")) {
				t.Fatalf("provider export request_count=%d stdout=%s stderr=%s", postCount, stdout.String(), stderr.String())
			}
			if bytes.Contains(stdout.Bytes(), []byte("verified=")) || bytes.Contains(stdout.Bytes(), []byte("trace_url=")) {
				t.Fatalf("CLI overstated Collector acceptance as backend visibility: %s", stdout.String())
			}
		})
	}
}

func TestManualExportCLIJSONOutput(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	rolloutPath := copyCodexSourceFixture(t, home, "complete-tools.jsonl")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/traces" {
			t.Fatalf("unexpected request %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	configPath := writeLaminarConfig(t, home, server.URL)

	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"--path", rolloutPath, "--config", configPath, "--json"}, &stdout, &stderr)
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
	if result.TraceID == "" || result.CollectorStatus != http.StatusOK {
		t.Fatalf("json result = %+v", result)
	}
	for _, retiredField := range []string{"trace_url", "verified_input", "verified_output"} {
		if strings.Contains(stdout.String(), retiredField) {
			t.Fatalf("JSON claims retired backend verification field %q: %s", retiredField, stdout.String())
		}
	}
}

func TestManualExportCLINoExportableTurns(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	rolloutPath := copyCodexSourceFixture(t, home, "incomplete-turn.jsonl")
	configPath := writeLaminarConfig(t, home, "http://127.0.0.1:14318")

	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"--path", rolloutPath, "--config", configPath}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("run succeeded for incomplete rollout stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
	if !bytes.Contains(stderr.Bytes(), []byte("No completed Codex turns with visible input/output found")) {
		t.Fatalf("missing no-exportable error stderr=%s", stderr.String())
	}
}

func TestManualExportFailsWhenCollectorRejectsBatch(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	rolloutPath := copyCodexSourceFixture(t, home, "complete-no-tools.jsonl")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "rejected", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	configPath := writeLaminarConfig(t, home, server.URL)

	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"--path", rolloutPath, "--config", configPath}, &stdout, &stderr)
	if code == 0 || strings.Contains(stdout.String(), "collector_accepted") {
		t.Fatalf("rejected Collector batch was reported successful: exit=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}

func TestRunWatchCanceled(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex"))
	configPath := writeLaminarConfig(t, home, "http://127.0.0.1:14318")
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
	if code != 0 {
		t.Fatalf("watch cancellation returned %d, want clean stop stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("clean watcher cancellation logged an error: %s", stderr.String())
	}
}

func copyCodexSourceFixture(t *testing.T, dir, name string) string {
	t.Helper()
	return copyProviderSourceFixture(t, dir, "codex", name)
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

func writeLaminarConfig(t *testing.T, dir, baseURL string) string {
	t.Helper()
	configDir := filepath.Join(dir, ".config", "lmnr")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Dir(configDir), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(configDir, "codex-tracer.json")
	contents, err := json.Marshal(map[string]string{"projectApiKey": testReceiverToken, "baseUrl": baseURL})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, append(contents, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	return configPath
}

func testOTLPString(attributes []*commonv1.KeyValue, key string) string {
	for _, attribute := range attributes {
		if attribute.Key == key {
			return attribute.Value.GetStringValue()
		}
	}
	return ""
}

func testOTLPBool(attributes []*commonv1.KeyValue, key string) bool {
	for _, attribute := range attributes {
		if attribute.Key == key {
			return attribute.Value.GetBoolValue()
		}
	}
	return false
}

func runTestGit(t *testing.T, directory string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}
