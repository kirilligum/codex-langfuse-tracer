package watch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/kirilligum/codex-langfuse-tracer/internal/agenttrace"
	"github.com/kirilligum/codex-langfuse-tracer/internal/buildinfo"
	"github.com/kirilligum/codex-langfuse-tracer/internal/codextrace"
	"github.com/kirilligum/codex-langfuse-tracer/internal/exportstate"
)

// TEST-601
func TestIncompleteTurnWaitsForCompletion(t *testing.T) {
	t.Parallel()

	root, statePath, rolloutPath := incompleteWatchFixture(t)
	now := time.Date(2026, 5, 1, 11, 1, 0, 0, time.UTC)
	setMTime(t, rolloutPath, now.Add(-time.Second))
	state := exportstate.State{Version: exportstate.Version, ScanWatermarkNS: now.Add(-time.Minute).UnixNano()}
	if err := exportstate.Save(context.Background(), statePath, state); err != nil {
		t.Fatal(err)
	}
	parsed, err := codextrace.ParseTurns(rolloutPath)
	if err != nil {
		t.Fatal(err)
	}
	if isExportable(parsed[0]) {
		t.Fatal("fixture unexpectedly starts exportable")
	}
	exportCalls := 0
	scoreCalls := 0
	opts := ScanOptions{
		Root:      root,
		StatePath: statePath,
		Quiet:     true,
		ResolveWorkspace: func(_ context.Context, turn agenttrace.Turn) (agenttrace.Turn, string, error) {
			if !turn.Completed {
				t.Fatal("incomplete turn resolved before completion")
			}
			return turn, "default", nil
		},
		ExportSpans: func(context.Context, agenttrace.Turn, string) (int, error) {
			exportCalls++
			return 200, nil
		},
		ExportScores: func(context.Context, agenttrace.Turn, string) error {
			scoreCalls++
			return nil
		},
	}
	state, exported, err := ScanOnce(context.Background(), withScanNow(opts, now), state)
	if err != nil {
		t.Fatal(err)
	}
	traceID := incompleteTraceID(t)
	if exported != 0 || exportCalls != 0 || scoreCalls != 0 || state.HasProcessed(traceID) || state.PendingScoreEnvironment(traceID) != "" {
		t.Fatalf("incomplete turn changed export state: exported=%d exports=%d scores=%d state=%+v", exported, exportCalls, scoreCalls, state)
	}
	if state.ScanWatermarkNS != now.UnixNano() {
		t.Fatalf("watermark = %d, want %d", state.ScanWatermarkNS, now.UnixNano())
	}

	appendRolloutLine(t, rolloutPath, `{"timestamp":"2026-05-01T11:00:05Z","type":"event_msg","payload":{"type":"task_complete"}}`)
	setMTime(t, rolloutPath, now.Add(time.Second))
	state, exported, err = ScanOnce(context.Background(), withScanNow(opts, now.Add(2*time.Second)), state)
	if err != nil {
		t.Fatal(err)
	}
	if exported != 1 || exportCalls != 1 || scoreCalls != 1 || !state.HasProcessed(traceID) {
		t.Fatalf("completed turn was not exported once: exported=%d exports=%d scores=%d state=%+v", exported, exportCalls, scoreCalls, state)
	}
}

// TEST-604
func TestCompletedTurnScoreRetryUsesStableEnvironment(t *testing.T) {
	t.Parallel()

	root, statePath, rolloutPath := watchFixture(t)
	now := time.Date(2026, 5, 1, 11, 1, 0, 0, time.UTC)
	setMTime(t, rolloutPath, now.Add(-time.Second))
	state := exportstate.State{Version: exportstate.Version, ScanWatermarkNS: now.Add(-time.Minute).UnixNano()}
	if err := exportstate.Save(context.Background(), statePath, state); err != nil {
		t.Fatal(err)
	}
	const environment = "repository--feature-one-a1b2c3"
	spanCalls := 0
	scoreCalls := 0
	resolverCalls := 0
	scoreFailed := true
	var stdout bytes.Buffer
	opts := ScanOptions{
		Root:      root,
		StatePath: statePath,
		Stdout:    &stdout,
		ResolveWorkspace: func(_ context.Context, turn agenttrace.Turn) (agenttrace.Turn, string, error) {
			resolverCalls++
			return turn, environment, nil
		},
		ExportSpans: func(_ context.Context, turn agenttrace.Turn, gotEnvironment string) (int, error) {
			spanCalls++
			if !turn.Completed || turn.InputText() == "" || turn.OutputText() == "" || gotEnvironment != environment {
				t.Fatalf("invalid completed export turn=%+v environment=%q", turn, gotEnvironment)
			}
			return 202, nil
		},
		ExportScores: func(_ context.Context, _ agenttrace.Turn, gotEnvironment string) error {
			scoreCalls++
			if gotEnvironment != environment {
				t.Fatalf("score environment = %q, want %q", gotEnvironment, environment)
			}
			if scoreFailed {
				return errors.New("injected score failure")
			}
			return nil
		},
	}
	traceID := completeTraceID(t, rolloutPath)
	state, exported, err := ScanOnce(context.Background(), withScanNow(opts, now), state)
	if err != nil {
		t.Fatal(err)
	}
	if exported != 1 || spanCalls != 1 || scoreCalls != 1 || state.HasProcessed(traceID) || state.PendingScoreEnvironment(traceID) != environment {
		t.Fatalf("failed score checkpoint = exported:%d spans:%d scores:%d state:%+v", exported, spanCalls, scoreCalls, state)
	}
	if !strings.Contains(stdout.String(), "span_export_succeeded trace="+traceID+" status=202 checkpoint=pending") {
		t.Fatalf("initial successful span export was not diagnosed: %s", stdout.String())
	}

	scoreFailed = false
	stdout.Reset()
	state, exported, err = ScanOnce(context.Background(), withScanNow(opts, now.Add(time.Second)), state)
	if err != nil {
		t.Fatal(err)
	}
	if exported != 0 || spanCalls != 1 || scoreCalls != 2 || resolverCalls != 1 || !state.HasProcessed(traceID) || state.PendingScoreEnvironment(traceID) != "" {
		t.Fatalf("score retry re-exported or changed environment: exported:%d spans:%d scores:%d resolver:%d state:%+v", exported, spanCalls, scoreCalls, resolverCalls, state)
	}
	if strings.Contains(stdout.String(), "span_export_succeeded") {
		t.Fatalf("score-only retry logged a span send: %s", stdout.String())
	}
}

// TEST-704
func TestWatchEnvironmentPersistsOnlyAfterSuccessfulSpanExport(t *testing.T) {
	t.Parallel()

	root, statePath, rolloutPath := watchFixture(t)
	now := time.Date(2026, 5, 1, 11, 1, 0, 0, time.UTC)
	setMTime(t, rolloutPath, now.Add(-time.Second))
	state := exportstate.State{Version: exportstate.Version, ScanWatermarkNS: now.Add(-time.Minute).UnixNano()}
	if err := exportstate.Save(context.Background(), statePath, state); err != nil {
		t.Fatal(err)
	}
	const environment = "repository--feature-one-a1b2c3"
	traceID := completeTraceID(t, rolloutPath)
	state, _, err := ScanOnce(context.Background(), ScanOptions{
		Root:      root,
		StatePath: statePath,
		Quiet:     true,
		ResolveWorkspace: func(_ context.Context, turn agenttrace.Turn) (agenttrace.Turn, string, error) {
			return turn, environment, nil
		},
		ExportSpans: func(context.Context, agenttrace.Turn, string) (int, error) {
			return 0, errors.New("injected OTLP failure")
		},
		ExportScores: func(context.Context, agenttrace.Turn, string) error {
			t.Fatal("scores must not run after failed span export")
			return nil
		},
	}, state)
	if err != nil {
		t.Fatal(err)
	}
	if state.PendingScoreEnvironment(traceID) != "" {
		t.Fatalf("failed span export persisted pending score: %+v", state)
	}
}

// TEST-011
func TestWatchScanSemantics(t *testing.T) {
	t.Parallel()

	root, statePath, rolloutPath := watchFixture(t)
	now := time.Date(2026, 5, 1, 10, 1, 0, 0, time.UTC)
	old := now.Add(-30 * time.Second)
	setMTime(t, rolloutPath, old)
	corrupt := filepath.Join(filepath.Dir(rolloutPath), "rollout-corrupt.jsonl")
	copyFile(t, filepath.Join("..", "..", "testdata", "sources", "codex", "corrupt-rollout.jsonl"), corrupt)
	setMTime(t, corrupt, old)
	state := exportstate.State{Version: exportstate.Version, ScanWatermarkNS: now.Add(-2 * time.Minute).UnixNano()}
	if err := exportstate.Save(context.Background(), statePath, state); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	exportCalls := 0
	state, exported, err := ScanOnce(context.Background(), ScanOptions{
		Root:             root,
		StatePath:        statePath,
		Now:              now,
		Stderr:           &stderr,
		ResolveWorkspace: testWorkspace,
		ExportSpans: func(context.Context, agenttrace.Turn, string) (int, error) {
			exportCalls++
			return 0, errors.New("boom")
		},
		ExportScores: successfulScores,
	}, state)
	if err != nil {
		t.Fatalf("ScanOnce failed export: %v", err)
	}
	if exported != 0 || exportCalls != 1 || state.ScanWatermarkNS != now.Add(-2*time.Minute).UnixNano() {
		t.Fatalf("failed export state exported=%d calls=%d state=%+v", exported, exportCalls, state)
	}
	if !bytes.Contains(stderr.Bytes(), []byte("warning: skipped unreadable rollout")) {
		t.Fatalf("missing corrupt warning: %s", stderr.String())
	}
	if err := os.Remove(corrupt); err != nil {
		t.Fatalf("remove corrupt fixture after warning: %v", err)
	}

	state, exported, err = ScanOnce(context.Background(), ScanOptions{
		Root:             root,
		StatePath:        statePath,
		Now:              now.Add(time.Minute),
		ResolveWorkspace: testWorkspace,
		ExportSpans: func(context.Context, agenttrace.Turn, string) (int, error) {
			exportCalls++
			return 200, nil
		},
		ExportScores: successfulScores,
	}, state)
	if err != nil {
		t.Fatalf("ScanOnce success: %v", err)
	}
	traceID := completeTraceID(t, rolloutPath)
	if exported != 1 || !state.HasProcessed(traceID) || state.ScanWatermarkNS != now.Add(time.Minute).UnixNano() {
		t.Fatalf("success state mismatch exported=%d state=%+v", exported, state)
	}

	state, exported, err = ScanOnce(context.Background(), ScanOptions{
		Root:             root,
		StatePath:        statePath,
		Now:              now.Add(2 * time.Minute),
		ResolveWorkspace: testWorkspace,
		ExportSpans: func(context.Context, agenttrace.Turn, string) (int, error) {
			t.Fatal("duplicate export callback should not run")
			return 0, nil
		},
		ExportScores: successfulScores,
	}, state)
	if err != nil {
		t.Fatalf("ScanOnce duplicate: %v", err)
	}
	if exported != 0 {
		t.Fatalf("duplicate exported = %d", exported)
	}
}

func TestWatchParseErrorDoesNotAdvanceWatermark(t *testing.T) {
	t.Parallel()

	root, statePath, rolloutPath := watchFixture(t)
	now := time.Date(2026, 5, 1, 10, 1, 0, 0, time.UTC)
	watermark := now.Add(-time.Minute).UnixNano()
	setMTime(t, rolloutPath, now.Add(-2*time.Minute))
	corrupt := filepath.Join(filepath.Dir(rolloutPath), "rollout-corrupt.jsonl")
	copyFile(t, filepath.Join("..", "..", "testdata", "sources", "codex", "corrupt-rollout.jsonl"), corrupt)
	setMTime(t, corrupt, now.Add(-30*time.Second))
	state := exportstate.State{Version: exportstate.Version, ScanWatermarkNS: watermark}
	if err := exportstate.Save(context.Background(), statePath, state); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer

	state, exported, err := ScanOnce(context.Background(), ScanOptions{
		Root:      root,
		StatePath: statePath,
		Now:       now,
		Stderr:    &stderr,
	}, state)
	if err != nil {
		t.Fatalf("ScanOnce: %v", err)
	}
	if exported != 0 || state.ScanWatermarkNS != watermark {
		t.Fatalf("corrupt rollout advanced scan: exported=%d watermark=%d want=%d", exported, state.ScanWatermarkNS, watermark)
	}
	if !bytes.Contains(stderr.Bytes(), []byte("warning: skipped unreadable rollout")) {
		t.Fatalf("missing corrupt-rollout diagnostic: %s", stderr.String())
	}
}

func TestWatchStatFailureRetainsWatermarkAndRecovers(t *testing.T) {
	t.Parallel()

	root, _, healthyPath := watchFixture(t)
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	watermark := now.Add(-2 * time.Minute).UnixNano()
	setMTime(t, healthyPath, now.Add(-20*time.Second))
	targetPath := filepath.Join(root, "restored-rollout.jsonl")
	unavailablePath := filepath.Join(filepath.Dir(healthyPath), "rollout-unavailable.jsonl")
	if err := os.Symlink(targetPath, unavailablePath); err != nil {
		t.Fatal(err)
	}
	state := exportstate.State{Version: exportstate.Version, ScanWatermarkNS: watermark}
	spanCalls := 0
	var stderr bytes.Buffer
	opts := ScanOptions{
		Root:   root,
		Quiet:  true,
		Stderr: &stderr,
		ResolveWorkspace: func(_ context.Context, turn agenttrace.Turn) (agenttrace.Turn, string, error) {
			return turn, "test", nil
		},
		ExportSpans: func(context.Context, agenttrace.Turn, string) (int, error) {
			spanCalls++
			return 200, nil
		},
		ExportScores: successfulScores,
	}

	state, exported, err := ScanOnce(context.Background(), withScanNow(opts, now), state)
	if err != nil {
		t.Fatalf("first ScanOnce: %v", err)
	}
	healthyTraceID := completeTraceID(t, healthyPath)
	if exported != 1 || spanCalls != 1 || !state.HasProcessed(healthyTraceID) {
		t.Fatalf("healthy sibling did not make progress: exported=%d spans=%d state=%+v", exported, spanCalls, state)
	}
	if state.ScanWatermarkNS != watermark {
		t.Fatalf("stat failure advanced watermark to %d, want %d", state.ScanWatermarkNS, watermark)
	}
	if !strings.Contains(stderr.String(), "ERROR: watch_scan_incomplete discovery_errors=0 stat_errors=1") {
		t.Fatalf("quiet scan did not report bounded incomplete status: %q", stderr.String())
	}

	copyFile(t, filepath.Join("..", "..", "testdata", "sources", "codex", "complete-no-tools.jsonl"), targetPath)
	setMTime(t, targetPath, now.Add(-30*time.Second))
	state, exported, err = ScanOnce(context.Background(), withScanNow(opts, now.Add(time.Minute)), state)
	if err != nil {
		t.Fatalf("recovery ScanOnce: %v", err)
	}
	if exported != 1 || spanCalls != 2 || state.ScanWatermarkNS != now.Add(time.Minute).UnixNano() {
		t.Fatalf("restored old-mtime source did not recover once: exported=%d spans=%d watermark=%d state=%+v", exported, spanCalls, state.ScanWatermarkNS, state)
	}
	if !state.HasProcessed(completeTraceID(t, targetPath)) {
		t.Fatal("restored source trace was not checkpointed")
	}
}

func TestWatchDiscoveryFailureAllowsHealthyProgress(t *testing.T) {
	t.Parallel()

	root, _, rolloutPath := watchFixture(t)
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	watermark := now.Add(-time.Minute).UnixNano()
	setMTime(t, rolloutPath, now.Add(-10*time.Second))
	state := exportstate.State{Version: exportstate.Version, ScanWatermarkNS: watermark}
	deps := defaultScanDependencies()
	deps.discover = func(gotRoot string) ([]string, error) {
		paths, err := codextrace.SessionPaths(gotRoot)
		if err != nil {
			return paths, err
		}
		return paths, watchDiscoveryTestError{count: 2}
	}
	var stderr bytes.Buffer
	spanCalls := 0
	state, exported, err := scanOnce(context.Background(), ScanOptions{
		Root:             root,
		Now:              now,
		Quiet:            true,
		Stderr:           &stderr,
		ResolveWorkspace: testWorkspace,
		ExportSpans: func(context.Context, agenttrace.Turn, string) (int, error) {
			spanCalls++
			return 200, nil
		},
		ExportScores: successfulScores,
	}, state, newScanRuntime(), deps)
	if err != nil {
		t.Fatalf("scanOnce: %v", err)
	}
	if exported != 1 || spanCalls != 1 || !state.HasProcessed(completeTraceID(t, rolloutPath)) {
		t.Fatalf("healthy source did not progress during partial discovery: exported=%d spans=%d state=%+v", exported, spanCalls, state)
	}
	if state.ScanWatermarkNS != watermark {
		t.Fatalf("partial discovery advanced watermark to %d, want %d", state.ScanWatermarkNS, watermark)
	}
	if !strings.Contains(stderr.String(), "ERROR: watch_scan_incomplete discovery_errors=2") {
		t.Fatalf("quiet scan did not report discovery failure: %q", stderr.String())
	}
}

type watchDiscoveryTestError struct{ count int }

func (e watchDiscoveryTestError) Error() string { return "injected partial discovery failure" }

func (e watchDiscoveryTestError) ErrorCount() int { return e.count }

func TestWatchCancellationAfterParseDoesNotExportOrAdvance(t *testing.T) {
	t.Parallel()

	root, _, rolloutPath := watchFixture(t)
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	setMTime(t, rolloutPath, now.Add(-10*time.Second))
	watermark := now.Add(-time.Minute).UnixNano()
	state := exportstate.State{Version: exportstate.Version, ScanWatermarkNS: watermark}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	deps := defaultScanDependencies()
	originalParse := deps.parse
	deps.parse = func(path string, include func(string) bool) ([]agenttrace.Turn, error) {
		turns, err := originalParse(path, include)
		cancel()
		return turns, err
	}
	spanCalls := 0
	state, exported, err := scanOnce(ctx, ScanOptions{
		Root:             root,
		Now:              now,
		Quiet:            true,
		ResolveWorkspace: testWorkspace,
		ExportSpans: func(context.Context, agenttrace.Turn, string) (int, error) {
			spanCalls++
			return 200, nil
		},
		ExportScores: successfulScores,
	}, state, newScanRuntime(), deps)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("scan error = %v, want context cancellation", err)
	}
	if exported != 0 || spanCalls != 0 || state.ScanWatermarkNS != watermark || state.HasProcessed(completeTraceID(t, rolloutPath)) {
		t.Fatalf("cancelled scan exported or advanced: exported=%d spans=%d state=%+v", exported, spanCalls, state)
	}
}

func TestWatchCorruptSourceDoesNotBlockHealthyTurn(t *testing.T) {
	t.Parallel()

	root, _, healthyPath := watchFixture(t)
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	watermark := now.Add(-time.Minute).UnixNano()
	setMTime(t, healthyPath, now.Add(-10*time.Second))
	corruptPath := filepath.Join(filepath.Dir(healthyPath), "rollout-000-corrupt.jsonl")
	if err := os.WriteFile(corruptPath, []byte("{\"type\":\"session_meta\"}\n{not-json}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	setMTime(t, corruptPath, now.Add(-20*time.Second))
	state := exportstate.State{Version: exportstate.Version, ScanWatermarkNS: watermark}
	runtime := newScanRuntime()
	deps := defaultScanDependencies()
	parseCalls := map[string]int{}
	originalParse := deps.parse
	deps.parse = func(path string, include func(string) bool) ([]agenttrace.Turn, error) {
		parseCalls[path]++
		return originalParse(path, include)
	}
	spanCalls := 0
	var stderr bytes.Buffer
	opts := ScanOptions{
		Root:             root,
		Quiet:            true,
		Stderr:           &stderr,
		ResolveWorkspace: testWorkspace,
		ExportSpans: func(context.Context, agenttrace.Turn, string) (int, error) {
			spanCalls++
			return 200, nil
		},
		ExportScores: successfulScores,
	}
	state, exported, err := scanOnce(context.Background(), withScanNow(opts, now), state, runtime, deps)
	if err != nil {
		t.Fatalf("first scanOnce: %v", err)
	}
	healthyTraceID := completeTraceID(t, healthyPath)
	if exported != 1 || spanCalls != 1 || !state.HasProcessed(healthyTraceID) {
		t.Fatalf("corrupt sibling blocked healthy trace: exported=%d spans=%d state=%+v", exported, spanCalls, state)
	}
	if state.ScanWatermarkNS != watermark {
		t.Fatalf("corrupt source advanced watermark to %d, want %d", state.ScanWatermarkNS, watermark)
	}
	if !strings.Contains(stderr.String(), "ERROR: watch_scan_incomplete discovery_errors=0 stat_errors=0 parse_errors=1") {
		t.Fatalf("quiet corrupt scan did not report parse failure: %q", stderr.String())
	}
	_, _, err = scanOnce(context.Background(), withScanNow(opts, now.Add(time.Second)), state, runtime, deps)
	if err != nil {
		t.Fatalf("deferred scanOnce: %v", err)
	}
	if parseCalls[healthyPath] != 1 || parseCalls[corruptPath] != 1 {
		t.Fatalf("unchanged sources were reparsed during deferred retry: parse calls=%v", parseCalls)
	}
}

func TestWatchParseRetryDeadlineAndRepair(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	sessionDir := filepath.Join(root, "sessions", "source")
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		t.Fatal(err)
	}
	corruptPath := filepath.Join(sessionDir, "rollout-retry.jsonl")
	if err := os.WriteFile(corruptPath, []byte("{not-json}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	setMTime(t, corruptPath, now.Add(-time.Second))
	watermark := now.Add(-time.Minute).UnixNano()
	state := exportstate.State{Version: exportstate.Version, ScanWatermarkNS: watermark}
	runtime := newScanRuntime()
	deps := defaultScanDependencies()
	parseCalls := 0
	originalParse := deps.parse
	deps.parse = func(path string, include func(string) bool) ([]agenttrace.Turn, error) {
		parseCalls++
		return originalParse(path, include)
	}
	spanCalls := 0
	var stderr bytes.Buffer
	opts := ScanOptions{
		Root:             root,
		Quiet:            true,
		Stderr:           &stderr,
		ResolveWorkspace: testWorkspace,
		ExportSpans: func(context.Context, agenttrace.Turn, string) (int, error) {
			spanCalls++
			return 200, nil
		},
		ExportScores: successfulScores,
	}
	for _, scanTime := range []time.Time{now, now.Add(29 * time.Second)} {
		var err error
		state, _, err = scanOnce(context.Background(), withScanNow(opts, scanTime), state, runtime, deps)
		if err != nil {
			t.Fatalf("scan at %s: %v", scanTime, err)
		}
		if state.ScanWatermarkNS != watermark {
			t.Fatalf("failed/deferred parse advanced watermark to %d, want %d", state.ScanWatermarkNS, watermark)
		}
	}
	if parseCalls != 1 {
		t.Fatalf("unchanged corrupt source parsed %d times before retry deadline, want 1", parseCalls)
	}
	state, _, err := scanOnce(context.Background(), withScanNow(opts, now.Add(30*time.Second)), state, runtime, deps)
	if err != nil {
		t.Fatalf("deadline scan: %v", err)
	}
	if parseCalls != 2 || state.ScanWatermarkNS != watermark {
		t.Fatalf("retry was not attempted at deadline: parses=%d watermark=%d", parseCalls, state.ScanWatermarkNS)
	}

	copyFile(t, filepath.Join("..", "..", "testdata", "sources", "codex", "complete-no-tools.jsonl"), corruptPath)
	setMTime(t, corruptPath, now.Add(31*time.Second))
	state, exported, err := scanOnce(context.Background(), withScanNow(opts, now.Add(32*time.Second)), state, runtime, deps)
	if err != nil {
		t.Fatalf("repair scan: %v", err)
	}
	if parseCalls != 3 || exported != 1 || spanCalls != 1 || state.ScanWatermarkNS != now.Add(32*time.Second).UnixNano() {
		t.Fatalf("changed source did not retry and recover immediately: parses=%d exported=%d spans=%d state=%+v", parseCalls, exported, spanCalls, state)
	}
}

func TestWatchCachedIncompleteTurnCompletesAfterAppend(t *testing.T) {
	t.Parallel()

	root, _, rolloutPath := incompleteWatchFixture(t)
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	setMTime(t, rolloutPath, now.Add(-10*time.Second))
	watermark := now.Add(-time.Minute).UnixNano()
	state := exportstate.State{Version: exportstate.Version, ScanWatermarkNS: watermark}
	runtime := newScanRuntime()
	deps := defaultScanDependencies()
	parseCalls := 0
	originalParse := deps.parse
	deps.parse = func(path string, include func(string) bool) ([]agenttrace.Turn, error) {
		parseCalls++
		return originalParse(path, include)
	}
	spanCalls := 0
	opts := ScanOptions{
		Root:             root,
		Quiet:            true,
		ResolveWorkspace: testWorkspace,
		ExportSpans: func(context.Context, agenttrace.Turn, string) (int, error) {
			spanCalls++
			return 200, nil
		},
		ExportScores: successfulScores,
	}
	state, exported, err := scanOnce(context.Background(), withScanNow(opts, now), state, runtime, deps)
	if err != nil {
		t.Fatalf("incomplete scan: %v", err)
	}
	if exported != 0 || parseCalls != 1 || state.ScanWatermarkNS != now.UnixNano() {
		t.Fatalf("incomplete turn was not held safely: exported=%d parses=%d state=%+v", exported, parseCalls, state)
	}

	appendRolloutLine(t, rolloutPath, `{"timestamp":"2026-09-22T12:00:01Z","type":"event_msg","payload":{"type":"task_complete"}}`)
	setMTime(t, rolloutPath, now.Add(time.Second))
	state, exported, err = scanOnce(context.Background(), withScanNow(opts, now.Add(2*time.Second)), state, runtime, deps)
	if err != nil {
		t.Fatalf("completion scan: %v", err)
	}
	if parseCalls != 2 || exported != 1 || spanCalls != 1 || !state.HasProcessed(incompleteTraceID(t)) {
		t.Fatalf("append did not invalidate successful cache: parses=%d exported=%d spans=%d state=%+v", parseCalls, exported, spanCalls, state)
	}
}

func TestWatchPendingScoreBypassesSuccessCache(t *testing.T) {
	t.Parallel()

	root, _, rolloutPath := watchFixture(t)
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	setMTime(t, rolloutPath, now.Add(-10*time.Second))
	traceID := completeTraceID(t, rolloutPath)
	state := exportstate.State{
		Version:         exportstate.Version,
		ScanWatermarkNS: now.Add(-time.Minute).UnixNano(),
		PendingScores:   map[string]string{traceID: "persisted-environment"},
	}
	runtime := newScanRuntime()
	spanCalls := 0
	scoreCalls := 0
	firstScoreFails := true
	opts := ScanOptions{
		Root:  root,
		Quiet: true,
		ExportSpans: func(context.Context, agenttrace.Turn, string) (int, error) {
			spanCalls++
			return 200, nil
		},
		ExportScores: func(_ context.Context, turn agenttrace.Turn, environment string) error {
			scoreCalls++
			if turn.TraceID != traceID || environment != "persisted-environment" {
				t.Errorf("score retry turn=%s environment=%q", turn.TraceID, environment)
			}
			if firstScoreFails {
				return errors.New("injected score failure")
			}
			return nil
		},
	}
	state, _, err := scanOnce(context.Background(), withScanNow(opts, now), state, runtime, defaultScanDependencies())
	if err != nil {
		t.Fatalf("first score scan: %v", err)
	}
	if spanCalls != 0 || scoreCalls != 1 || state.PendingScoreEnvironment(traceID) != "persisted-environment" || state.ScanWatermarkNS >= now.UnixNano() {
		t.Fatalf("failed pending score was not retained: spans=%d scores=%d state=%+v", spanCalls, scoreCalls, state)
	}
	firstScoreFails = false
	state, _, err = scanOnce(context.Background(), withScanNow(opts, now.Add(time.Second)), state, runtime, defaultScanDependencies())
	if err != nil {
		t.Fatalf("score retry scan: %v", err)
	}
	if spanCalls != 0 || scoreCalls != 2 || !state.HasProcessed(traceID) || state.PendingScoreEnvironment(traceID) != "" {
		t.Fatalf("pending score retry resent spans or failed to checkpoint: spans=%d scores=%d state=%+v", spanCalls, scoreCalls, state)
	}
}

func TestWatchChangedDuringParseDoesNotCacheOrAdvance(t *testing.T) {
	t.Parallel()

	root, _, rolloutPath := watchFixture(t)
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	setMTime(t, rolloutPath, now.Add(-10*time.Second))
	watermark := now.Add(-time.Minute).UnixNano()
	state := exportstate.State{Version: exportstate.Version, ScanWatermarkNS: watermark}
	runtime := newScanRuntime()
	deps := defaultScanDependencies()
	originalParse := deps.parse
	parseCalls := 0
	deps.parse = func(path string, include func(string) bool) ([]agenttrace.Turn, error) {
		parseCalls++
		turns, err := originalParse(path, include)
		if parseCalls == 1 && err == nil {
			for _, line := range []string{
				`{"timestamp":"2026-09-22T12:00:01Z","type":"turn_context","payload":{"turn_id":"second-turn","trace_id":"trace-second-turn"}}`,
				`{"timestamp":"2026-09-22T12:00:02Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"second input"}]}}`,
				`{"timestamp":"2026-09-22T12:00:03Z","type":"response_item","payload":{"type":"message","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"second output"}]}}`,
				`{"timestamp":"2026-09-22T12:00:04Z","type":"event_msg","payload":{"type":"task_complete"}}`,
			} {
				appendRolloutLine(t, path, line)
			}
			setMTime(t, path, now.Add(time.Second))
		}
		return turns, err
	}
	spanCalls := 0
	var stderr bytes.Buffer
	opts := ScanOptions{
		Root:             root,
		Quiet:            true,
		Stderr:           &stderr,
		ResolveWorkspace: testWorkspace,
		ExportSpans: func(context.Context, agenttrace.Turn, string) (int, error) {
			spanCalls++
			return 200, nil
		},
		ExportScores: successfulScores,
	}
	state, exported, err := scanOnce(context.Background(), withScanNow(opts, now), state, runtime, deps)
	if err != nil {
		t.Fatalf("first changed-source scan: %v", err)
	}
	if exported != 1 || spanCalls != 1 || state.ScanWatermarkNS != watermark || !strings.Contains(stderr.String(), "changed_sources=1") {
		t.Fatalf("mutation during parse was not held for retry: exported=%d spans=%d state=%+v stderr=%q", exported, spanCalls, state, stderr.String())
	}
	state, exported, err = scanOnce(context.Background(), withScanNow(opts, now.Add(2*time.Second)), state, runtime, deps)
	if err != nil {
		t.Fatalf("second changed-source scan: %v", err)
	}
	if parseCalls != 2 || exported != 1 || spanCalls != 2 || !state.HasProcessed("trace-second-turn") || state.ScanWatermarkNS != now.Add(2*time.Second).UnixNano() {
		t.Fatalf("changed source was cached or lost: parses=%d exported=%d spans=%d state=%+v", parseCalls, exported, spanCalls, state)
	}
}

func TestWatchScanCacheEvictsOldestMetadataEntry(t *testing.T) {
	runtime := newScanRuntime()
	path := filepath.Join(t.TempDir(), "source")
	if err := os.WriteFile(path, []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index <= scanCacheCapacity; index++ {
		runtime.put(scanCacheEntry{path: fmt.Sprintf("source-%04d", index), info: info})
	}
	if len(runtime.entries) != scanCacheCapacity || runtime.get("source-0000") != nil {
		t.Fatalf("cache did not evict oldest entry: entries=%d oldest=%+v", len(runtime.entries), runtime.get("source-0000"))
	}
	if runtime.get(fmt.Sprintf("source-%04d", scanCacheCapacity)) == nil {
		t.Fatal("cache evicted newest source")
	}
}

func TestWatchCacheEvictionRereadsWithoutRepeatingCompletedWork(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	sessionDir := filepath.Join(root, "sessions", "source")
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	watermark := now.Add(-time.Minute).UnixNano()
	state := exportstate.State{Version: exportstate.Version, ScanWatermarkNS: watermark}
	paths := make([]string, scanCacheCapacity+1)
	for index := range paths {
		path := filepath.Join(sessionDir, fmt.Sprintf("rollout-%04d.jsonl", index))
		traceID := fmt.Sprintf("trace-cache-eviction-%04d", index)
		source := strings.Join([]string{
			`{"timestamp":"2026-09-22T12:00:00Z","type":"session_meta","payload":{"id":"cache-eviction"}}`,
			fmt.Sprintf(`{"timestamp":"2026-09-22T12:00:01Z","type":"turn_context","payload":{"turn_id":"turn-%04d","trace_id":%q}}`, index, traceID),
			`{"timestamp":"2026-09-22T12:00:02Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"input"}]}}`,
			`{"timestamp":"2026-09-22T12:00:03Z","type":"response_item","payload":{"type":"message","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"output"}]}}`,
			`{"timestamp":"2026-09-22T12:00:04Z","type":"event_msg","payload":{"type":"task_complete"}}`,
		}, "\n") + "\n"
		if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
		setMTime(t, path, now.Add(-10*time.Second))
		paths[index] = path
	}
	corruptPath := filepath.Join(sessionDir, "rollout-z-corrupt.jsonl")
	if err := os.WriteFile(corruptPath, []byte("{not-json}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	setMTime(t, corruptPath, now.Add(-20*time.Second))
	runtime := newScanRuntime()
	deps := defaultScanDependencies()
	parseCalls := make(map[string]int, len(paths)+1)
	originalParse := deps.parse
	deps.parse = func(path string, include func(string) bool) ([]agenttrace.Turn, error) {
		parseCalls[path]++
		return originalParse(path, include)
	}
	spanCalls := 0
	opts := ScanOptions{
		Root:  root,
		Quiet: true,
		ResolveWorkspace: func(_ context.Context, turn agenttrace.Turn) (agenttrace.Turn, string, error) {
			return turn, "cache-eviction", nil
		},
		ExportSpans: func(context.Context, agenttrace.Turn, string) (int, error) {
			spanCalls++
			return 200, nil
		},
		ExportScores: successfulScores,
	}
	state, exported, err := scanOnce(context.Background(), withScanNow(opts, now), state, runtime, deps)
	if err != nil {
		t.Fatalf("first scan: %v", err)
	}
	if exported != len(paths) || spanCalls != len(paths) || state.ScanWatermarkNS != watermark {
		t.Fatalf("first scan lost work or advanced on corruption: exported=%d spans=%d watermark=%d", exported, spanCalls, state.ScanWatermarkNS)
	}
	for _, path := range paths {
		if parseCalls[path] != 1 {
			t.Fatalf("source %s first parse count=%d, want 1", filepath.Base(path), parseCalls[path])
		}
	}
	state, exported, err = scanOnce(context.Background(), withScanNow(opts, now.Add(time.Second)), state, runtime, deps)
	if err != nil {
		t.Fatalf("second scan after eviction: %v", err)
	}
	if exported != 0 || spanCalls != len(paths) || parseCalls[paths[0]] < 2 || parseCalls[corruptPath] < 1 {
		t.Fatalf("eviction did not cause a safe reread: exported=%d spans=%d first=%d corrupt=%d", exported, spanCalls, parseCalls[paths[0]], parseCalls[corruptPath])
	}
	for _, path := range paths {
		if parseCalls[path] < 1 || parseCalls[path] > 2 {
			t.Fatalf("source %s had unexpected parse count %d", filepath.Base(path), parseCalls[path])
		}
	}
	if parseCalls[corruptPath] > 2 {
		t.Fatalf("corrupt source retried too often after eviction: %d", parseCalls[corruptPath])
	}
}

func TestWatchRestartDoesNotDuplicateDurablyProcessedTrace(t *testing.T) {
	t.Parallel()

	root, _, rolloutPath := watchFixture(t)
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	setMTime(t, rolloutPath, now.Add(-10*time.Second))
	traceID := completeTraceID(t, rolloutPath)
	state := exportstate.State{
		Version:           exportstate.Version,
		ScanWatermarkNS:   now.Add(-time.Minute).UnixNano(),
		ProcessedTraceIDs: []string{traceID},
	}
	spanCalls := 0
	state, exported, err := ScanOnce(context.Background(), ScanOptions{
		Root:  root,
		Now:   now,
		Quiet: true,
		ExportSpans: func(context.Context, agenttrace.Turn, string) (int, error) {
			spanCalls++
			return 200, nil
		},
		ExportScores: successfulScores,
	}, state)
	if err != nil {
		t.Fatalf("restarted scan: %v", err)
	}
	if exported != 0 || spanCalls != 0 || !state.HasProcessed(traceID) || state.ScanWatermarkNS != now.UnixNano() {
		t.Fatalf("restart did not honor durable processed checkpoint: exported=%d spans=%d state=%+v", exported, spanCalls, state)
	}
}

func TestWatchMissingSessionsRootIsIncompleteAndDrainsQueue(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	watermark := now.Add(-time.Minute).UnixNano()
	queuedPath := filepath.Join("..", "..", "testdata", "sources", "codex", "complete-tools.jsonl")
	state := exportstate.State{
		Version:         exportstate.Version,
		ScanWatermarkNS: watermark,
		Queue: []exportstate.QueueRequest{{
			Provider:   agenttrace.ProviderCodex,
			SourcePath: queuedPath,
			EnqueuedAt: now.Format(time.RFC3339Nano),
		}},
	}
	var stderr bytes.Buffer
	spanCalls := 0
	state, exported, err := ScanOnce(context.Background(), ScanOptions{
		Root:             root,
		Now:              now,
		Quiet:            true,
		Stderr:           &stderr,
		ResolveWorkspace: testWorkspace,
		ExportSpans: func(context.Context, agenttrace.Turn, string) (int, error) {
			spanCalls++
			return 200, nil
		},
		ExportScores: successfulScores,
	}, state)
	if err != nil {
		t.Fatalf("ScanOnce: %v", err)
	}
	if exported != 1 || spanCalls != 1 || len(state.Queue) != 0 {
		t.Fatalf("queue was blocked by missing Codex root: exported=%d spans=%d queue=%d", exported, spanCalls, len(state.Queue))
	}
	if state.ScanWatermarkNS != watermark || !strings.Contains(stderr.String(), "ERROR: watch_scan_incomplete discovery_errors=1") {
		t.Fatalf("missing root did not remain visibly incomplete: watermark=%d stderr=%q", state.ScanWatermarkNS, stderr.String())
	}
}

func TestWatchFiltersProcessedTurnsBeforeRetainingObservations(t *testing.T) {
	t.Parallel()

	const processedTurns = 2000
	root, _, rolloutPath := watchFixture(t)
	now := time.Date(2026, 5, 1, 10, 1, 0, 0, time.UTC)
	var source strings.Builder
	source.WriteString(`{"timestamp":"2026-05-01T10:00:00Z","type":"session_meta","payload":{"id":"sess-watch-filter"}}` + "\n")
	message := strings.Repeat("already processed response ", 24)
	processedTraceIDs := make([]string, 0, processedTurns)
	for index := 0; index < processedTurns; index++ {
		turnID := fmt.Sprintf("processed-%d", index)
		traceID := agenttrace.StableTraceID(agenttrace.ProviderCodex, "sess-watch-filter", turnID)
		processedTraceIDs = append(processedTraceIDs, traceID)
		fmt.Fprintf(&source, `{"timestamp":"2026-05-01T10:00:01Z","type":"turn_context","payload":{"turn_id":%q,"trace_id":%q}}`+"\n", turnID, traceID)
		fmt.Fprintf(&source, `{"timestamp":"2026-05-01T10:00:02Z","type":"event_msg","payload":{"type":"agent_message","phase":"commentary","message":%q}}`+"\n", message)
	}
	newTurnID := "new-turn"
	newTraceID := agenttrace.StableTraceID(agenttrace.ProviderCodex, "sess-watch-filter", newTurnID)
	fmt.Fprintf(&source, `{"timestamp":"2026-05-01T10:00:03Z","type":"turn_context","payload":{"turn_id":%q,"trace_id":%q}}`+"\n", newTurnID, newTraceID)
	source.WriteString(`{"timestamp":"2026-05-01T10:00:04Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"new input"}]}}` + "\n")
	source.WriteString(`{"timestamp":"2026-05-01T10:00:05Z","type":"response_item","payload":{"type":"message","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"new output"}]}}` + "\n")
	source.WriteString(`{"timestamp":"2026-05-01T10:00:06Z","type":"event_msg","payload":{"type":"task_complete"}}` + "\n")
	if err := os.WriteFile(rolloutPath, []byte(source.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	setMTime(t, rolloutPath, now.Add(-30*time.Second))
	sort.Strings(processedTraceIDs)
	state := exportstate.State{
		Version:           exportstate.Version,
		ScanWatermarkNS:   now.Add(-time.Minute).UnixNano(),
		ProcessedTraceIDs: processedTraceIDs,
	}
	spanCalls := 0
	scoreCalls := 0
	state, exported, err := ScanOnce(context.Background(), ScanOptions{
		Root:             root,
		Now:              now,
		Quiet:            true,
		ResolveWorkspace: testWorkspace,
		ExportSpans: func(_ context.Context, turn agenttrace.Turn, _ string) (int, error) {
			spanCalls++
			if turn.TraceID != newTraceID {
				t.Errorf("unexpected span export for trace %s", turn.TraceID)
			}
			return 200, nil
		},
		ExportScores: func(_ context.Context, turn agenttrace.Turn, _ string) error {
			scoreCalls++
			if turn.TraceID != newTraceID {
				t.Errorf("unexpected score export for trace %s", turn.TraceID)
			}
			return nil
		},
	}, state)
	if err != nil {
		t.Fatalf("ScanOnce: %v", err)
	}
	if exported != 1 || spanCalls != 1 || scoreCalls != 1 || !state.HasProcessed(newTraceID) {
		t.Fatalf("processed-turn filtering failed: exported=%d spans=%d scores=%d state=%+v", exported, spanCalls, scoreCalls, state)
	}
}

func TestInitializeStateAndWatchCancel(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	statePath := filepath.Join(root, "state.json")
	now := time.Date(2026, 5, 1, 10, 30, 0, 0, time.UTC)
	var stdout bytes.Buffer
	state, created, err := InitializeState(context.Background(), statePath, now, &stdout, false)
	if err != nil {
		t.Fatalf("InitializeState: %v", err)
	}
	if !created {
		t.Fatal("InitializeState did not report creating a missing state file")
	}
	wantWatermark := now.Add(-time.Duration(buildinfo.DefaultInitialLookbackSecs) * time.Second).UnixNano()
	if state.Version != exportstate.Version || state.ScanWatermarkNS != wantWatermark {
		t.Fatalf("initialized state = %+v", state)
	}
	if !bytes.Contains(stdout.Bytes(), []byte("initialized watch state")) {
		t.Fatalf("missing init log: %s", stdout.String())
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = WatchSessions(ctx, ScanOptions{Root: root, StatePath: statePath, Quiet: true, PollIntervalSeconds: 0.001})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("WatchSessions canceled error = %v", err)
	}
}

// TEST-507
func TestWatchDrainsClaudeQueue(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	statePath := filepath.Join(root, "langfuse-export-state.json")
	transcriptPath := filepath.Join(root, "claude-no-tools.jsonl")
	copyFile(t, filepath.Join("..", "..", "testdata", "sources", "claude", "no-tools.jsonl"), transcriptPath)
	state := exportstate.State{Version: exportstate.Version, ScanWatermarkNS: time.Date(2026, 5, 4, 11, 59, 0, 0, time.UTC).UnixNano(), Queue: []exportstate.QueueRequest{{
		Provider: agenttrace.ProviderClaude, SourcePath: transcriptPath, SessionID: "claude-no-tools", CWD: root,
		EnqueuedAt: time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano),
	}},
	}
	if err := exportstate.Save(context.Background(), statePath, state); err != nil {
		t.Fatal(err)
	}
	exportedTraceIDs := []string{}
	state, exported, err := ScanOnce(context.Background(), ScanOptions{
		Root: root, StatePath: statePath, ResolveWorkspace: testWorkspace,
		Now: time.Date(2026, 5, 4, 12, 1, 0, 0, time.UTC),
		ExportSpans: func(_ context.Context, turn agenttrace.Turn, _ string) (int, error) {
			exportedTraceIDs = append(exportedTraceIDs, turn.TraceID)
			return 202, nil
		},
		ExportScores: successfulScores,
	}, state)
	if err != nil {
		t.Fatalf("ScanOnce: %v", err)
	}
	if exported != 1 || len(exportedTraceIDs) != 1 || len(state.Queue) != 0 || !state.HasProcessed(exportedTraceIDs[0]) {
		t.Fatalf("state after queue drain exported=%d traces=%#v state=%+v", exported, exportedTraceIDs, state)
	}
}

// TEST-533
func TestWatchReloadsClaudeQueueFromHookState(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	statePath := filepath.Join(root, "langfuse-export-state.json")
	transcriptPath := filepath.Join(root, "claude-no-tools.jsonl")
	copyFile(t, filepath.Join("..", "..", "testdata", "sources", "claude", "no-tools.jsonl"), transcriptPath)
	if err := exportstate.Save(context.Background(), statePath, exportstate.State{Version: exportstate.Version, ScanWatermarkNS: time.Date(2026, 5, 4, 11, 59, 0, 0, time.UTC).UnixNano()}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	exported := make(chan string, 1)
	errCh := make(chan error, 1)
	watchReady := make(chan struct{}, 1)
	spanCalls := 0
	scoreCalls := 0
	go func() {
		errCh <- WatchSessions(ctx, ScanOptions{
			Root: root, StatePath: statePath, ResolveWorkspace: testWorkspace, PollIntervalSeconds: 0.01, Stdout: watcherReadyWriter(watchReady),
			ExportSpans: func(_ context.Context, turn agenttrace.Turn, _ string) (int, error) {
				spanCalls++
				exported <- turn.TraceID
				return 202, nil
			},
			ExportScores: func(context.Context, agenttrace.Turn, string) error {
				scoreCalls++
				return nil
			},
		})
	}()

	select {
	case <-watchReady:
	case err := <-errCh:
		t.Fatalf("WatchSessions exited before startup: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("WatchSessions did not finish startup")
	}
	if err := exportstate.Enqueue(context.Background(), statePath, exportstate.QueueRequest{
		Provider: agenttrace.ProviderClaude, SourcePath: transcriptPath, SessionID: "claude-no-tools",
		CWD: root, EnqueuedAt: time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	var traceID string
	select {
	case traceID = <-exported:
	case err := <-errCh:
		t.Fatalf("WatchSessions exited before queued export: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("WatchSessions did not reload and drain queued Claude request")
	}
	deadline := time.NewTimer(2 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer deadline.Stop()
	defer ticker.Stop()
	for {
		loaded, err := exportstate.Load(statePath)
		if err != nil {
			t.Fatal(err)
		}
		if loaded.HasProcessed(traceID) && len(loaded.Queue) == 0 {
			break
		}
		select {
		case err := <-errCh:
			t.Fatalf("WatchSessions exited before queue commit: %v", err)
		case <-deadline.C:
			t.Fatalf("queued request did not commit: %+v", loaded)
		case <-ticker.C:
		}
	}
	cancel()
	if err := <-errCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("WatchSessions error = %v", err)
	}
	if spanCalls != 1 || scoreCalls != 1 {
		t.Fatalf("span calls=%d score calls=%d, want one each", spanCalls, scoreCalls)
	}
	loaded, err := exportstate.Load(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded == nil || len(loaded.Queue) != 0 || !loaded.HasProcessed(traceID) {
		t.Fatalf("state after reloaded queue drain = %+v trace=%s", loaded, traceID)
	}
}

type watcherReadyWriter chan struct{}

func (writer watcherReadyWriter) Write(data []byte) (int, error) {
	if bytes.Contains(data, []byte("watching ")) {
		select {
		case writer <- struct{}{}:
		default:
		}
	}
	return len(data), nil
}

func TestWaitBetweenExportsHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitBetweenExports(ctx, 60); !errors.Is(err, context.Canceled) {
		t.Fatalf("waitBetweenExports error = %v, want context canceled", err)
	}
}

func TestWatchLockBackoffLogThrottleAndShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	previous := exportstate.State{Version: exportstate.Version, ScanWatermarkNS: 123, ProcessedTraceIDs: []string{"kept"}}
	var stderr, stdout bytes.Buffer
	fakeNow := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	var delays []time.Duration
	policy := stateLockRetryPolicy{
		initialDelay: time.Second,
		maximumDelay: 30 * time.Second,
		logInterval:  time.Minute,
		now:          func() time.Time { return fakeNow },
		wait: func(ctx context.Context, delay time.Duration) error {
			delays = append(delays, delay)
			fakeNow = fakeNow.Add(delay)
			if len(delays) == 7 {
				cancel()
				return ctx.Err()
			}
			return nil
		},
	}
	operationCalls := 0
	got, err := retryStateOperationWithPolicy(ctx, ScanOptions{StatePath: "/tmp/state.json", Stdout: &stdout, Stderr: &stderr}, previous, func() (exportstate.State, error) {
		operationCalls++
		return exportstate.State{}, &exportstate.LockBusyError{Path: "/tmp/state.json.lock", Waited: 2 * time.Second}
	}, policy)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("retry result error = %v, want context canceled", err)
	}
	if operationCalls != 7 {
		t.Fatalf("operation calls = %d, want 7", operationCalls)
	}
	wantDelays := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second, 30 * time.Second}
	if len(delays) != len(wantDelays) {
		t.Fatalf("retry delays = %v, want %v", delays, wantDelays)
	}
	for index := range wantDelays {
		if delays[index] != wantDelays[index] {
			t.Fatalf("retry delays = %v, want %v", delays, wantDelays)
		}
	}
	if got.ScanWatermarkNS != previous.ScanWatermarkNS || !got.HasProcessed("kept") {
		t.Fatalf("canceled retry changed prior state: %+v", got)
	}
	if count := strings.Count(stderr.String(), "ERROR: export state lock busy"); count != 2 {
		t.Fatalf("contention log count = %d, want first timeout and one after 60 seconds: %s", count, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("canceled retry reported recovery: %s", stdout.String())
	}

	fakeNow = time.Date(2026, 9, 21, 13, 0, 0, 0, time.UTC)
	delays = nil
	stderr.Reset()
	stdout.Reset()
	policy.wait = func(_ context.Context, delay time.Duration) error {
		delays = append(delays, delay)
		fakeNow = fakeNow.Add(delay)
		return nil
	}
	recoveryCalls := 0
	got, err = retryStateOperationWithPolicy(context.Background(), ScanOptions{StatePath: "/tmp/state.json", Stdout: &stdout, Stderr: &stderr}, previous, func() (exportstate.State, error) {
		recoveryCalls++
		if recoveryCalls < 3 {
			return exportstate.State{}, &exportstate.LockBusyError{Path: "/tmp/state.json.lock", Waited: 2 * time.Second}
		}
		return exportstate.State{Version: exportstate.Version, ScanWatermarkNS: 456}, nil
	}, policy)
	if err != nil || got.ScanWatermarkNS != 456 || recoveryCalls != 3 {
		t.Fatalf("successful recovery state=%+v calls=%d err=%v", got, recoveryCalls, err)
	}
	if len(delays) != 2 || delays[0] != time.Second || delays[1] != 2*time.Second {
		t.Fatalf("successful recovery delays = %v, want [1s 2s]", delays)
	}
	if !strings.Contains(stdout.String(), "export state lock recovered path=/tmp/state.json waited=3s") {
		t.Fatalf("recovery log = %q", stdout.String())
	}
	secondOperationCalls := 0
	secondDelays := make([]time.Duration, 0, 1)
	policy.wait = func(_ context.Context, delay time.Duration) error {
		secondDelays = append(secondDelays, delay)
		fakeNow = fakeNow.Add(delay)
		return nil
	}
	_, err = retryStateOperationWithPolicy(context.Background(), ScanOptions{StatePath: "/tmp/state.json", Quiet: true}, got, func() (exportstate.State, error) {
		secondOperationCalls++
		if secondOperationCalls == 1 {
			return exportstate.State{}, &exportstate.LockBusyError{Path: "/tmp/state.json.lock", Waited: 2 * time.Second}
		}
		return got, nil
	}, policy)
	if err != nil || len(secondDelays) != 1 || secondDelays[0] != time.Second {
		t.Fatalf("retry delay did not reset after recovery: delays=%v err=%v", secondDelays, err)
	}
}

// TEST-017
// TEST-604
func TestWatchLogs(t *testing.T) {
	t.Parallel()

	root, statePath, rolloutPath := watchFixture(t)
	now := time.Date(2026, 5, 1, 10, 1, 0, 0, time.UTC)
	setMTime(t, rolloutPath, now.Add(-30*time.Second))
	state := exportstate.State{Version: exportstate.Version, ScanWatermarkNS: now.Add(-2 * time.Minute).UnixNano()}
	if err := exportstate.Save(context.Background(), statePath, state); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	_, _, err := ScanOnce(context.Background(), ScanOptions{
		Root: root, StatePath: statePath, ResolveWorkspace: testWorkspace, Now: now, Stdout: &stdout, Stderr: &stderr,
		ExportSpans:  func(context.Context, agenttrace.Turn, string) (int, error) { return 201, nil },
		ExportScores: successfulScores,
	}, state)
	if err != nil {
		t.Fatal(err)
	}
	successLog := stdout.String()
	spanSuccess := strings.Index(successLog, "span_export_succeeded trace=1e087e4ea8aa8d8e29e604d2cd8704d9 status=201 checkpoint=pending")
	exported := strings.Index(successLog, "exported trace=1e087e4ea8aa8d8e29e604d2cd8704d9 status=201 path=")
	scored := strings.Index(successLog, "scored trace=1e087e4ea8aa8d8e29e604d2cd8704d9 path=")
	if spanSuccess < 0 || exported < 0 || scored < 0 || spanSuccess >= exported || exported >= scored ||
		!bytes.Contains(stdout.Bytes(), []byte("scored trace=1e087e4ea8aa8d8e29e604d2cd8704d9 path=")) {
		t.Fatalf("success log order is wrong: %s", successLog)
	}

	state = exportstate.State{Version: exportstate.Version, ScanWatermarkNS: now.Add(-2 * time.Minute).UnixNano()}
	if err := exportstate.Save(context.Background(), statePath, state); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	_, _, err = ScanOnce(context.Background(), ScanOptions{
		Root: root, StatePath: statePath, ResolveWorkspace: testWorkspace, Now: now, Stdout: &stdout, Stderr: &stderr,
		ExportSpans:  func(context.Context, agenttrace.Turn, string) (int, error) { return 0, errors.New("export failed") },
		ExportScores: successfulScores,
	}, state)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(stderr.Bytes(), []byte("ERROR: failed to export trace=1e087e4ea8aa8d8e29e604d2cd8704d9")) {
		t.Fatalf("failure log missing: %s", stderr.String())
	}
	if strings.Contains(stdout.String()+stderr.String(), "span_export_succeeded") || strings.Contains(stdout.String()+stderr.String(), "span_checkpoint_unconfirmed") {
		t.Fatalf("failed span callback emitted a success/checkpoint diagnostic: stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
	logs := stdout.String() + stderr.String()
	for _, secretContent := range []string{"Summarize the repo", "Checks passed", "sk-lf-live-secret"} {
		if strings.Contains(logs, secretContent) {
			t.Fatalf("watch logs leaked %q: %s", secretContent, logs)
		}
	}

	if err := exportstate.Save(context.Background(), statePath, state); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	_, _, err = ScanOnce(context.Background(), ScanOptions{
		Root: root, StatePath: statePath, ResolveWorkspace: testWorkspace, Now: now, Stdout: &stdout, Stderr: &stderr, Quiet: true,
		ExportSpans:  func(context.Context, agenttrace.Turn, string) (int, error) { return 201, nil },
		ExportScores: successfulScores,
	}, state)
	if err != nil {
		t.Fatal(err)
	}
	if stdout.Len() != 0 || strings.Contains(stderr.String(), "span_export_succeeded") {
		t.Fatalf("quiet success emitted a success diagnostic: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

// TEST-706
func TestWatchSpanCheckpointFailureLogs(t *testing.T) {
	t.Parallel()

	for _, quiet := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "quiet"}[quiet], func(t *testing.T) {
			t.Parallel()
			root, statePath, rolloutPath := watchFixture(t)
			now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
			setMTime(t, rolloutPath, now.Add(-time.Second))
			initial := exportstate.State{Version: exportstate.Version, ScanWatermarkNS: now.Add(-time.Minute).UnixNano(), ProcessedTraceIDs: []string{"previous-trace"}}
			if err := exportstate.Save(context.Background(), statePath, initial); err != nil {
				t.Fatal(err)
			}
			initialBytes, err := os.ReadFile(statePath)
			if err != nil {
				t.Fatal(err)
			}

			ctx, cancel := context.WithCancel(context.Background())
			var stdout, stderr bytes.Buffer
			spanCalls, scoreCalls := 0, 0
			_, _, scanErr := ScanOnce(ctx, ScanOptions{
				Root: root, StatePath: statePath, ResolveWorkspace: testWorkspace, Now: now, Quiet: quiet, Stdout: &stdout, Stderr: &stderr,
				ExportSpans: func(context.Context, agenttrace.Turn, string) (int, error) {
					spanCalls++
					cancel()
					return 202, nil
				},
				ExportScores: func(context.Context, agenttrace.Turn, string) error {
					scoreCalls++
					return nil
				},
			}, initial)
			cancel()
			if !errors.Is(scanErr, context.Canceled) {
				t.Fatalf("ScanOnce error = %v, want context canceled", scanErr)
			}
			if spanCalls != 1 || scoreCalls != 0 {
				t.Fatalf("callbacks = spans:%d scores:%d, want spans:1 scores:0", spanCalls, scoreCalls)
			}
			traceID := completeTraceID(t, rolloutPath)
			expectedError := "ERROR: span_checkpoint_unconfirmed trace=" + traceID + " export_result=success replay_possible=true"
			if strings.Count(stderr.String(), expectedError) != 1 {
				t.Fatalf("checkpoint diagnostic = %q, want exactly one %q", stderr.String(), expectedError)
			}
			if strings.Contains(stdout.String(), "exported trace=") || strings.Contains(stdout.String(), "scored trace=") {
				t.Fatalf("checkpoint failure logged a completed checkpoint: %q", stdout.String())
			}
			if got := strings.Count(stdout.String(), "span_export_succeeded trace="+traceID+" status=202 checkpoint=pending"); got != map[bool]int{false: 1, true: 0}[quiet] {
				t.Fatalf("span success diagnostic count = %d in quiet=%v; log=%q", got, quiet, stdout.String())
			}
			for _, private := range []string{"Summarize the repo", "Checks passed", "sk-lf-live-secret"} {
				if strings.Contains(stdout.String()+stderr.String(), private) {
					t.Fatalf("checkpoint diagnostics contain private content %q", private)
				}
			}
			afterBytes, err := os.ReadFile(statePath)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(afterBytes, initialBytes) {
				t.Fatalf("checkpoint error mutated durable state: before=%s after=%s", initialBytes, afterBytes)
			}
		})
	}
}

type watchEventLogWriter chan string

func (writer watchEventLogWriter) Write(data []byte) (int, error) {
	line := strings.TrimSpace(string(bytes.Clone(data)))
	writer <- line
	return len(data), nil
}

func waitForWatchEvent(t *testing.T, lines <-chan string, prefix string) string {
	t.Helper()
	timer := time.NewTimer(6 * time.Second)
	defer timer.Stop()
	for {
		select {
		case line := <-lines:
			if strings.HasPrefix(line, prefix) {
				return line
			}
		case <-timer.C:
			t.Fatalf("timed out waiting for watch event %q", prefix)
		}
	}
}

func drainWatchEvents(lines <-chan string) []string {
	var result []string
	for {
		select {
		case line := <-lines:
			result = append(result, line)
		default:
			return result
		}
	}
}

// EVAL-007
func TestEvalHookQueueDrainLatency(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	statePath := filepath.Join(root, "langfuse-export-state.json")
	transcriptPath := filepath.Join(root, "claude-no-tools.jsonl")
	copyFile(t, filepath.Join("..", "..", "testdata", "sources", "claude", "no-tools.jsonl"), transcriptPath)
	state := exportstate.State{Version: exportstate.Version, ScanWatermarkNS: time.Date(2026, 5, 4, 11, 59, 0, 0, time.UTC).UnixNano(), Queue: []exportstate.QueueRequest{{
		Provider: agenttrace.ProviderClaude, SourcePath: transcriptPath, SessionID: "claude-no-tools",
		EnqueuedAt: time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano),
	}}}
	if err := exportstate.Save(context.Background(), statePath, state); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, exported, err := ScanOnce(context.Background(), ScanOptions{
		Root: root, StatePath: statePath, ResolveWorkspace: testWorkspace,
		Now: time.Date(2026, 5, 4, 12, 1, 0, 0, time.UTC), Quiet: true,
		ExportSpans:  func(context.Context, agenttrace.Turn, string) (int, error) { return 202, nil },
		ExportScores: successfulScores,
	}, state)
	if err != nil {
		t.Fatalf("ScanOnce: %v", err)
	}
	if exported != 1 {
		t.Fatalf("exported = %d, want 1", exported)
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("queue drain latency = %s, want <= 200ms", elapsed)
	}
}

func incompleteWatchFixture(t *testing.T) (root, statePath, rolloutPath string) {
	t.Helper()
	root = t.TempDir()
	sessionDir := filepath.Join(root, "sessions", "2026", "05", "01")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rolloutPath = filepath.Join(sessionDir, "rollout-incomplete.jsonl")
	copyFile(t, filepath.Join("..", "..", "testdata", "sources", "codex", "incomplete-turn.jsonl"), rolloutPath)
	statePath = filepath.Join(root, "langfuse-export-state.json")
	return root, statePath, rolloutPath
}

func watchFixture(t *testing.T) (root, statePath, rolloutPath string) {
	t.Helper()
	root = t.TempDir()
	sessionDir := filepath.Join(root, "sessions", "2026", "05", "01")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rolloutPath = filepath.Join(sessionDir, "rollout-complete-tools.jsonl")
	copyFile(t, filepath.Join("..", "..", "testdata", "sources", "codex", "complete-tools.jsonl"), rolloutPath)
	statePath = filepath.Join(root, "langfuse-export-state.json")
	return root, statePath, rolloutPath
}

func incompleteTraceID(t *testing.T) string {
	t.Helper()
	turns, err := codextrace.ParseTurns(filepath.Join("..", "..", "testdata", "sources", "codex", "incomplete-turn.jsonl"))
	if err != nil || len(turns) != 1 {
		t.Fatalf("parse incomplete fixture: turns=%d err=%v", len(turns), err)
	}
	return turns[0].TraceID
}

func completeTraceID(t *testing.T, path string) string {
	t.Helper()
	turns, err := codextrace.ParseTurns(path)
	if err != nil || len(turns) != 1 {
		t.Fatalf("parse complete fixture: turns=%d err=%v", len(turns), err)
	}
	return turns[0].TraceID
}

func withScanNow(opts ScanOptions, now time.Time) ScanOptions {
	opts.Now = now
	return opts
}

func setMTime(t *testing.T, path string, timestamp time.Time) {
	t.Helper()
	if err := os.Chtimes(path, timestamp, timestamp); err != nil {
		t.Fatal(err)
	}
}

func appendRolloutLine(t *testing.T, path, line string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(line + "\n"); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func successfulScores(context.Context, agenttrace.Turn, string) error {
	return nil
}

func testWorkspace(_ context.Context, turn agenttrace.Turn) (agenttrace.Turn, string, error) {
	return turn, "default", nil
}
