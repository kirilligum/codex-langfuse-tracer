package watch

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
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
	if err := exportstate.Save(statePath, state); err != nil {
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
	if err := exportstate.Save(statePath, state); err != nil {
		t.Fatal(err)
	}
	const environment = "repository--feature-one-a1b2c3"
	spanCalls := 0
	scoreCalls := 0
	resolverCalls := 0
	scoreFailed := true
	opts := ScanOptions{
		Root:      root,
		StatePath: statePath,
		Quiet:     true,
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

	scoreFailed = false
	state, exported, err = ScanOnce(context.Background(), withScanNow(opts, now.Add(time.Second)), state)
	if err != nil {
		t.Fatal(err)
	}
	if exported != 0 || spanCalls != 1 || scoreCalls != 2 || resolverCalls != 1 || !state.HasProcessed(traceID) || state.PendingScoreEnvironment(traceID) != "" {
		t.Fatalf("score retry re-exported or changed environment: exported:%d spans:%d scores:%d resolver:%d state:%+v", exported, spanCalls, scoreCalls, resolverCalls, state)
	}
}

// TEST-704
func TestWatchEnvironmentPersistsOnlyAfterSuccessfulSpanExport(t *testing.T) {
	t.Parallel()

	root, statePath, rolloutPath := watchFixture(t)
	now := time.Date(2026, 5, 1, 11, 1, 0, 0, time.UTC)
	setMTime(t, rolloutPath, now.Add(-time.Second))
	state := exportstate.State{Version: exportstate.Version, ScanWatermarkNS: now.Add(-time.Minute).UnixNano()}
	if err := exportstate.Save(statePath, state); err != nil {
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
	if err := exportstate.Save(statePath, state); err != nil {
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

func TestInitializeStateAndWatchCancel(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	statePath := filepath.Join(root, "state.json")
	now := time.Date(2026, 5, 1, 10, 30, 0, 0, time.UTC)
	var stdout bytes.Buffer
	state, err := InitializeState(statePath, now, &stdout, false)
	if err != nil {
		t.Fatalf("InitializeState: %v", err)
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
	if err := exportstate.Save(statePath, state); err != nil {
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
	if err := exportstate.Save(statePath, exportstate.State{Version: exportstate.Version, ScanWatermarkNS: time.Date(2026, 5, 4, 11, 59, 0, 0, time.UTC).UnixNano()}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	exported := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		errCh <- WatchSessions(ctx, ScanOptions{
			Root: root, StatePath: statePath, ResolveWorkspace: testWorkspace, PollIntervalSeconds: 0.01, Quiet: true,
			ExportSpans: func(_ context.Context, turn agenttrace.Turn, _ string) (int, error) {
				exported <- turn.TraceID
				cancel()
				return 202, nil
			},
			ExportScores: successfulScores,
		})
	}()

	time.Sleep(50 * time.Millisecond)
	if err := exportstate.Enqueue(statePath, exportstate.QueueRequest{
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
	if err := <-errCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("WatchSessions error = %v", err)
	}
	loaded, err := exportstate.Load(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded == nil || len(loaded.Queue) != 0 || !loaded.HasProcessed(traceID) {
		t.Fatalf("state after reloaded queue drain = %+v trace=%s", loaded, traceID)
	}
}

func TestWaitBetweenExportsHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitBetweenExports(ctx, 60); !errors.Is(err, context.Canceled) {
		t.Fatalf("waitBetweenExports error = %v, want context canceled", err)
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
	if err := exportstate.Save(statePath, state); err != nil {
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
	if !bytes.Contains(stdout.Bytes(), []byte("exported trace=1e087e4ea8aa8d8e29e604d2cd8704d9 status=201 path=")) ||
		!bytes.Contains(stdout.Bytes(), []byte("scored trace=1e087e4ea8aa8d8e29e604d2cd8704d9 path=")) {
		t.Fatalf("success log missing: %s", stdout.String())
	}

	state = exportstate.State{Version: exportstate.Version, ScanWatermarkNS: now.Add(-2 * time.Minute).UnixNano()}
	if err := exportstate.Save(statePath, state); err != nil {
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
	logs := stdout.String() + stderr.String()
	for _, secretContent := range []string{"Summarize the repo", "Checks passed", "sk-lf-live-secret"} {
		if strings.Contains(logs, secretContent) {
			t.Fatalf("watch logs leaked %q: %s", secretContent, logs)
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
	if err := exportstate.Save(statePath, state); err != nil {
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
