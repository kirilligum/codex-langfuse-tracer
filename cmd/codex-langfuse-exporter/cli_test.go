package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kirilligum/codex-langfuse-tracer/internal/buildinfo"
	"github.com/kirilligum/codex-langfuse-tracer/internal/exportstate"
)

// TEST-002
func TestCLIFlags(t *testing.T) {
	t.Parallel()

	opts, err := parseArgs([]string{"--latest"})
	if err != nil {
		t.Fatalf("parse latest: %v", err)
	}
	if !opts.Latest || opts.Mode() != "latest" {
		t.Fatalf("latest mode not selected: %+v", opts)
	}
	if opts.Provider != "codex" {
		t.Fatalf("default provider = %q, want codex", opts.Provider)
	}
	if opts.ServiceName != buildinfo.DefaultServiceName {
		t.Fatalf("service name = %q, want %q", opts.ServiceName, buildinfo.DefaultServiceName)
	}
	if opts.PollIntervalSeconds != buildinfo.DefaultPollIntervalSeconds {
		t.Fatalf("poll interval = %v", opts.PollIntervalSeconds)
	}
	opts, err = parseArgs([]string{
		"--path", "/tmp/rollout.jsonl",
		"--turn-id", "turn-1",
		"--quiet",
	})
	if err != nil {
		t.Fatalf("parse path mode: %v", err)
	}
	if opts.Path != "/tmp/rollout.jsonl" || opts.TurnID != "turn-1" || !opts.Quiet {
		t.Fatalf("path options not preserved: %+v", opts)
	}
	opts, err = parseArgs([]string{"--doctor", "--json"})
	if err != nil {
		t.Fatalf("parse doctor json: %v", err)
	}
	if !opts.Doctor || !opts.JSON || opts.Mode() != "doctor" {
		t.Fatalf("doctor options not preserved: %+v", opts)
	}

	for _, args := range [][]string{
		{},
		{"--latest", "--watch"},
		{"--doctor", "--latest"},
		{"--latest", "--session-id", "abc"},
		{"--path", "a", "--session-id", "abc"},
	} {
		_, err := parseArgs(args)
		if err == nil {
			t.Fatalf("parseArgs(%v) succeeded, want error", args)
		}
		if !strings.Contains(err.Error(), "exactly one source mode") {
			t.Fatalf("parseArgs(%v) error = %q", args, err)
		}
	}
	for _, args := range [][]string{
		{"--watch", "--json"},
		{"--check-receiver", "--json"},
		{"--claude-hook", "--json"},
	} {
		_, err := parseArgs(args)
		if err == nil || !strings.Contains(err.Error(), "--json is supported only") {
			t.Fatalf("parseArgs(%v) error = %v, want unsupported JSON mode error", args, err)
		}
	}
}

// TEST-704
func TestCLIIdentityFlags(t *testing.T) {
	t.Parallel()

	opts, err := parseArgs([]string{"--latest"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reflect.TypeOf(opts).FieldByName("Environment"); ok {
		t.Fatalf("options retains Environment: %+v", opts)
	}
	if _, err := parseArgs([]string{"--latest", "--environment", "staging"}); err == nil || !strings.Contains(err.Error(), "flag provided but not defined: -environment") {
		t.Fatalf("--environment error = %v", err)
	}
}

// TEST-506
func TestCLIProviderSelection(t *testing.T) {
	t.Parallel()

	opts, err := parseArgs([]string{"--provider", "claude", "--path", "/tmp/transcript.jsonl"})
	if err != nil {
		t.Fatalf("parse Claude path: %v", err)
	}
	if opts.Provider != "claude" || opts.Mode() != "path" || opts.Path != "/tmp/transcript.jsonl" {
		t.Fatalf("Claude provider options = %+v", opts)
	}
	for _, args := range [][]string{
		{"--provider", "unknown", "--path", "/tmp/source.jsonl"},
		{"--provider", "claude", "--latest"},
		{"--provider", "claude", "--session-id", "abc"},
		{"--provider", "claude", "--watch"},
		{"--provider", "claude", "--check-receiver"},
	} {
		_, err := parseArgs(args)
		if err == nil {
			t.Fatalf("parseArgs(%v) succeeded, want provider error", args)
		}
	}
}

// EVAL-006
func TestEvalProviderCLISurface(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"--latest"},
		{"--provider", "codex", "--latest"},
		{"--provider", "claude", "--path", "/tmp/transcript.jsonl"},
	} {
		if _, err := parseArgs(args); err != nil {
			t.Fatalf("parseArgs(%v): %v", args, err)
		}
	}
	if _, err := parseArgs([]string{"--provider", "claude", "--latest"}); err == nil || !strings.Contains(err.Error(), "Claude provider supports only --path") {
		t.Fatalf("Claude latest error = %v", err)
	}
}

// TEST-406
func TestCheckReceiverMode(t *testing.T) {
	home := t.TempDir()
	var requests atomic.Int32
	var receiverCheckComplete atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/v1/traces" || r.Header.Get("Authorization") != "Bearer "+strings.Repeat("a", 64) {
			t.Fatalf("unexpected receiver check: %s %s", r.Method, r.URL.Path)
		}
		payload, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read receiver request: %v", err)
		}
		if !receiverCheckComplete.Load() && len(payload) != 0 {
			t.Fatalf("empty OTLP receiver check unexpectedly carries data: %d bytes", len(payload))
		}
		if receiverCheckComplete.Load() && len(payload) == 0 {
			t.Fatal("manual trace export unexpectedly carries an empty body")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	configPath := writeLaminarConfig(t, home, server.URL)

	opts, err := parseArgs([]string{"--check-receiver"})
	if err != nil || opts.Mode() != "check-receiver" {
		t.Fatalf("receiver check options=%+v err=%v", opts, err)
	}
	for _, args := range [][]string{
		{"--check-receiver", "--latest"},
		{"--check-receiver", "--watch"},
	} {
		if _, err := parseArgs(args); err == nil {
			t.Fatalf("parseArgs(%v) succeeded, want mutually exclusive mode error", args)
		}
	}

	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"--check-receiver", "--config", configPath}, &stdout, &stderr)
	if code != 0 || requests.Load() != 1 || !strings.Contains(stdout.String(), "receiver_accepted status=200") {
		t.Fatalf("check receiver exit=%d requests=%d stdout=%s stderr=%s", code, requests.Load(), stdout.String(), stderr.String())
	}

	receiverCheckComplete.Store(true)
	rolloutPath := copyCodexSourceFixture(t, home, "complete-tools.jsonl")
	requests.Store(0)
	code = run(context.Background(), []string{"--path", rolloutPath, "--config", configPath}, &stdout, &stderr)
	if code != 0 || requests.Load() != 1 {
		t.Fatalf("export through Laminar exit=%d requests=%d stdout=%s stderr=%s", code, requests.Load(), stdout.String(), stderr.String())
	}
}

func TestDoctorMode(t *testing.T) {
	home := t.TempDir()
	statePath := filepath.Join(home, "state.json")
	if err := exportstate.Save(context.Background(), statePath, exportstate.State{Version: exportstate.Version, ProcessedTraceIDs: []string{"trace-1"}}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/traces":
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected request %s", r.URL.Path)
		}
	}))
	defer server.Close()
	configPath := writeLaminarConfig(t, home, server.URL)
	oldHealth, oldMetrics := checkCollectorHealth, checkCollectorMetrics
	checkCollectorHealth = func(context.Context) (int, error) { return http.StatusOK, nil }
	checkCollectorMetrics = func(context.Context) (int, error) { return http.StatusOK, nil }
	t.Cleanup(func() { checkCollectorHealth, checkCollectorMetrics = oldHealth, oldMetrics })

	oldRunCommand := runCommand
	journalOutput := "all quiet\n"
	var journalError error
	runCommand = func(_ context.Context, name string, args ...string) ([]byte, error) {
		switch name {
		case "systemctl":
			return []byte("active\n"), nil
		case "journalctl":
			return []byte(journalOutput), journalError
		default:
			t.Fatalf("unexpected command %s %v", name, args)
			return nil, nil
		}
	}
	t.Cleanup(func() { runCommand = oldRunCommand })

	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"--doctor", "--config", configPath, "--state-file", statePath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("doctor exit=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	for _, want := range []string{"doctor receiver_auth ok", "doctor collector_health ok", "doctor collector_metrics ok", "doctor watcher ok", "doctor state ok", "doctor result ok"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("doctor output missing %q in %s", want, stdout.String())
		}
	}

	stdout.Reset()
	code = run(context.Background(), []string{"--doctor", "--json", "--config", configPath, "--state-file", statePath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("doctor json exit=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var result doctorResult
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &result); err != nil {
		t.Fatalf("parse doctor json: %v\n%s", err, stdout.String())
	}
	if !result.OK || len(result.Checks) == 0 {
		t.Fatalf("doctor json result = %+v", result)
	}

	journalOutput = "ERROR: watch_scan_incomplete discovery_errors=1 stat_errors=0 parse_errors=0 delivery_errors=0 changed_sources=0 watermark_advanced=false\n"
	stdout.Reset()
	code = run(context.Background(), []string{"--doctor", "--config", configPath, "--state-file", statePath}, &stdout, &stderr)
	if code == 0 || !strings.Contains(stdout.String(), "doctor recent_errors fail count=1") {
		t.Fatalf("doctor did not fail on incomplete scan: exit=%d stdout=%s", code, stdout.String())
	}

	if err := exportstate.Save(context.Background(), statePath, exportstate.State{
		Version:       exportstate.Version,
		PendingScores: map[string]string{"trace-pending": "test-environment"},
	}); err != nil {
		t.Fatal(err)
	}
	journalOutput = "all quiet\n"
	stdout.Reset()
	code = run(context.Background(), []string{"--doctor", "--config", configPath, "--state-file", statePath}, &stdout, &stderr)
	if code == 0 || !strings.Contains(stdout.String(), "doctor state_legacy_pending_traces fail pending_traces=1") {
		t.Fatalf("doctor did not report pending legacy traces: exit=%d stdout=%s", code, stdout.String())
	}

	if err := exportstate.Save(context.Background(), statePath, exportstate.State{Version: exportstate.Version}); err != nil {
		t.Fatal(err)
	}
	journalError = errors.New("injected journal read failure")
	stdout.Reset()
	code = run(context.Background(), []string{"--doctor", "--config", configPath, "--state-file", statePath}, &stdout, &stderr)
	if code == 0 || !strings.Contains(stdout.String(), "doctor recent_errors fail journal unavailable") {
		t.Fatalf("doctor treated unavailable journal as healthy: exit=%d stdout=%s", code, stdout.String())
	}
}

func TestProviderDispatchRejectsUnknownProvider(t *testing.T) {
	t.Parallel()

	_, err := parseProviderTurns("unknown", "/tmp/source.jsonl")
	if err == nil || !errors.Is(err, errUnsupportedProvider) {
		t.Fatalf("unknown provider error = %v", err)
	}
}

// TEST-507
func TestClaudeHookCLIModeDoesNotLoadConfig(t *testing.T) {
	t.Parallel()

	statePath := filepath.Join(t.TempDir(), "state.json")
	oldStdin := stdin
	stdin = strings.NewReader(`{"session_id":"claude-cli","transcript_path":"/tmp/claude-cli.jsonl","cwd":"/tmp/project","hook_event_name":"Stop"}`)
	t.Cleanup(func() { stdin = oldStdin })

	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"--claude-hook", "--state-file", statePath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run hook exit=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	state, err := exportstate.Load(statePath)
	if err != nil {
		t.Fatalf("Load state: %v", err)
	}
	if state == nil || len(state.Queue) != 1 || state.Queue[0].SourcePath != "/tmp/claude-cli.jsonl" {
		t.Fatalf("hook queue = %+v", state)
	}
}

func TestSelectedSessionPathExplicitAndInvalidModes(t *testing.T) {
	t.Parallel()

	path, err := selectedSessionPath(options{Path: "/tmp/rollout.jsonl"})
	if err != nil {
		t.Fatalf("selectedSessionPath(path): %v", err)
	}
	if path != "/tmp/rollout.jsonl" {
		t.Fatalf("selected path = %q", path)
	}
	if _, err := selectedSessionPath(options{}); err == nil {
		t.Fatal("selectedSessionPath accepted empty mode")
	}
}

func TestSelectedSessionPathLatestAndSessionID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	sessionDir := filepath.Join(home, "sessions", "2026", "05", "01")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(sessionDir, "rollout-old-session.jsonl")
	newPath := filepath.Join(sessionDir, "rollout-target-session.jsonl")
	for _, path := range []string{oldPath, newPath} {
		if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	oldTime := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	newTime := oldTime.Add(time.Minute)
	if err := os.Chtimes(oldPath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(newPath, newTime, newTime); err != nil {
		t.Fatal(err)
	}

	latest, err := selectedSessionPath(options{Latest: true})
	if err != nil {
		t.Fatalf("selectedSessionPath(latest): %v", err)
	}
	if latest != newPath {
		t.Fatalf("latest = %q, want %q", latest, newPath)
	}
	byID, err := selectedSessionPath(options{SessionID: "target-session"})
	if err != nil {
		t.Fatalf("selectedSessionPath(session-id): %v", err)
	}
	if byID != newPath {
		t.Fatalf("byID = %q, want %q", byID, newPath)
	}
}

// EVAL-001
func TestEvalBuildAndPackageGraph(t *testing.T) {
	t.Parallel()
	opts, err := parseArgs([]string{"--watch"})
	if err != nil {
		t.Fatalf("parse watch: %v", err)
	}
	if opts.Mode() != "watch" {
		t.Fatalf("mode = %q", opts.Mode())
	}
}
