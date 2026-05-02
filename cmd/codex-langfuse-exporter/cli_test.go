package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kirilligum/codex-langfuse-tracer/internal/buildinfo"
	"github.com/kirilligum/codex-langfuse-tracer/internal/config"
	"github.com/kirilligum/codex-langfuse-tracer/internal/langfuse"
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
	if opts.Environment != buildinfo.DefaultEnvironment {
		t.Fatalf("environment = %q, want %q", opts.Environment, buildinfo.DefaultEnvironment)
	}
	if opts.ServiceName != buildinfo.DefaultServiceName {
		t.Fatalf("service name = %q, want %q", opts.ServiceName, buildinfo.DefaultServiceName)
	}
	if opts.PollIntervalSeconds != buildinfo.DefaultPollIntervalSeconds {
		t.Fatalf("poll interval = %v", opts.PollIntervalSeconds)
	}
	if opts.VerifyWaitSeconds != 25.0 || opts.VerifyIntervalSeconds != 3.0 {
		t.Fatalf("verify defaults = %v/%v", opts.VerifyWaitSeconds, opts.VerifyIntervalSeconds)
	}

	opts, err = parseArgs([]string{
		"--path", "/tmp/rollout.jsonl",
		"--turn-id", "turn-1",
		"--no-verify",
		"--verify-wait-seconds", "1.5",
		"--verify-interval-seconds", "0.25",
		"--quiet",
	})
	if err != nil {
		t.Fatalf("parse path mode: %v", err)
	}
	if opts.Path != "/tmp/rollout.jsonl" || opts.TurnID != "turn-1" || !opts.NoVerify || !opts.Quiet {
		t.Fatalf("path options not preserved: %+v", opts)
	}
	if opts.VerifyWaitSeconds != 1.5 || opts.VerifyIntervalSeconds != 0.25 {
		t.Fatalf("verify values not preserved: %+v", opts)
	}

	for _, args := range [][]string{
		{},
		{"--latest", "--watch"},
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
}

// TEST-406
func TestSyncModelPricingMode(t *testing.T) {
	home := t.TempDir()
	configPath := writeLangfuseConfig(t, home, "http://127.0.0.1")

	opts, err := parseArgs([]string{"--sync-model-pricing"})
	if err != nil {
		t.Fatalf("parse sync mode: %v", err)
	}
	if opts.Mode() != "sync-model-pricing" {
		t.Fatalf("mode = %q", opts.Mode())
	}
	for _, args := range [][]string{
		{"--sync-model-pricing", "--latest"},
		{"--sync-model-pricing", "--path", "/tmp/rollout.jsonl"},
		{"--sync-model-pricing", "--watch"},
	} {
		if _, err := parseArgs(args); err == nil {
			t.Fatalf("parseArgs(%v) succeeded, want mutually exclusive mode error", args)
		}
	}

	calls := 0
	oldSync := syncModelPricing
	syncModelPricing = func(ctx context.Context, cfg config.LangfuseConfig) (langfuse.ModelSyncSummary, error) {
		calls++
		if cfg.Host != "http://127.0.0.1" {
			t.Fatalf("cfg host = %q", cfg.Host)
		}
		return langfuse.ModelSyncSummary{Existing: 1, Created: 2}, nil
	}
	t.Cleanup(func() { syncModelPricing = oldSync })

	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"--sync-model-pricing", "--config", configPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run sync exit=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if calls != 1 {
		t.Fatalf("sync calls = %d, want 1", calls)
	}
	if !strings.Contains(stdout.String(), "model_pricing existing=1 created=2 conflicting=0") {
		t.Fatalf("missing sync summary stdout=%s", stdout.String())
	}

	rolloutPath := copyRolloutFixture(t, home, "complete-tools.jsonl")
	otelPosts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/public/otel/v1/traces":
			otelPosts++
			w.WriteHeader(http.StatusOK)
		case "/api/public/models":
			t.Fatalf("export mode called model sync endpoint")
		default:
			t.Fatalf("unexpected request %s", r.URL.Path)
		}
	}))
	defer server.Close()
	exportConfigPath := writeLangfuseConfig(t, home, server.URL)
	code = run(context.Background(), []string{"--path", rolloutPath, "--config", exportConfigPath, "--no-verify"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run export exit=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if calls != 1 {
		t.Fatalf("sync was called during export mode: %d", calls)
	}
	if otelPosts != 1 {
		t.Fatalf("otel posts = %d, want 1", otelPosts)
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
