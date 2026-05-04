package watch

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kirilligum/codex-langfuse-tracer/internal/agenttrace"
	"github.com/kirilligum/codex-langfuse-tracer/internal/buildinfo"
	"github.com/kirilligum/codex-langfuse-tracer/internal/exportstate"
)

// TEST-011
func TestWatchScanSemantics(t *testing.T) {
	t.Parallel()

	root, statePath, rolloutPath := watchFixture(t)
	now := time.Date(2026, 5, 1, 10, 1, 0, 0, time.UTC)
	old := now.Add(-30 * time.Second)
	if err := os.Chtimes(rolloutPath, old, old); err != nil {
		t.Fatal(err)
	}
	corrupt := filepath.Join(filepath.Dir(rolloutPath), "rollout-corrupt.jsonl")
	copyFile(t, filepath.Join("..", "..", "testdata", "sources", "codex", "corrupt-rollout.jsonl"), corrupt)
	if err := os.Chtimes(corrupt, old, old); err != nil {
		t.Fatal(err)
	}

	state := exportstate.State{Version: 1, ScanWatermarkNS: now.Add(-2 * time.Minute).UnixNano()}
	var stderr bytes.Buffer
	exportCalls := 0
	state, exported, err := ScanOnce(context.Background(), ScanOptions{
		Root:      root,
		StatePath: statePath,
		Now:       now,
		Stderr:    &stderr,
		Export: func(context.Context, agenttrace.Turn) (int, error) {
			exportCalls++
			return 0, errors.New("boom")
		},
	}, state)
	if err != nil {
		t.Fatalf("ScanOnce failed export: %v", err)
	}
	if exported != 0 || exportCalls != 1 {
		t.Fatalf("failed export count exported=%d calls=%d", exported, exportCalls)
	}
	if state.ScanWatermarkNS != now.Add(-2*time.Minute).UnixNano() {
		t.Fatalf("watermark advanced after failure: %d", state.ScanWatermarkNS)
	}
	if !bytes.Contains(stderr.Bytes(), []byte("warning: skipped unreadable rollout")) {
		t.Fatalf("missing corrupt warning: %s", stderr.String())
	}

	var stdout bytes.Buffer
	state, exported, err = ScanOnce(context.Background(), ScanOptions{
		Root:      root,
		StatePath: statePath,
		Now:       now.Add(time.Minute),
		Stdout:    &stdout,
		Stderr:    &stderr,
		Export: func(context.Context, agenttrace.Turn) (int, error) {
			exportCalls++
			return 200, nil
		},
	}, state)
	if err != nil {
		t.Fatalf("ScanOnce success: %v", err)
	}
	if exported != 1 || !state.HasProcessed("1e087e4ea8aa8d8e29e604d2cd8704d9") {
		t.Fatalf("success state mismatch exported=%d state=%+v", exported, state)
	}
	if state.ScanWatermarkNS != now.Add(time.Minute).UnixNano() {
		t.Fatalf("watermark not advanced after success")
	}

	state, exported, err = ScanOnce(context.Background(), ScanOptions{
		Root:      root,
		StatePath: statePath,
		Now:       now.Add(2 * time.Minute),
		Export: func(context.Context, agenttrace.Turn) (int, error) {
			t.Fatal("duplicate export callback should not run")
			return 0, nil
		},
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
	if state.ScanWatermarkNS != wantWatermark {
		t.Fatalf("watermark = %d, want %d", state.ScanWatermarkNS, wantWatermark)
	}
	if !bytes.Contains(stdout.Bytes(), []byte("initialized watch state")) {
		t.Fatalf("missing init log: %s", stdout.String())
	}
	loaded, err := exportstate.Load(statePath)
	if err != nil {
		t.Fatalf("LoadState after init: %v", err)
	}
	if loaded == nil || loaded.ScanWatermarkNS != wantWatermark {
		t.Fatalf("saved state mismatch: %+v", loaded)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = WatchSessions(ctx, ScanOptions{
		Root:                root,
		StatePath:           statePath,
		PollIntervalSeconds: 0.001,
		Quiet:               true,
		Export: func(context.Context, agenttrace.Turn) (int, error) {
			t.Fatal("export should not run for empty canceled watch")
			return 0, nil
		},
	})
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
	state := exportstate.State{Version: 1, ScanWatermarkNS: time.Date(2026, 5, 4, 11, 59, 0, 0, time.UTC).UnixNano()}
	state.Queue = []exportstate.QueueRequest{{
		Provider:   agenttrace.ProviderClaude,
		SourcePath: transcriptPath,
		SessionID:  "claude-no-tools",
		CWD:        root,
		EnqueuedAt: time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano),
	}}
	if err := exportstate.Save(statePath, state); err != nil {
		t.Fatal(err)
	}

	exportedTraceIDs := []string{}
	state, exported, err := ScanOnce(context.Background(), ScanOptions{
		Root:      root,
		StatePath: statePath,
		Now:       time.Date(2026, 5, 4, 12, 1, 0, 0, time.UTC),
		Export: func(_ context.Context, turn agenttrace.Turn) (int, error) {
			exportedTraceIDs = append(exportedTraceIDs, turn.TraceID)
			return 202, nil
		},
	}, state)
	if err != nil {
		t.Fatalf("ScanOnce: %v", err)
	}
	if exported != 1 || len(exportedTraceIDs) != 1 {
		t.Fatalf("exported = %d traces=%#v", exported, exportedTraceIDs)
	}
	if len(state.Queue) != 0 || !state.HasProcessed(exportedTraceIDs[0]) {
		t.Fatalf("state after drain = %+v", state)
	}

	state, exported, err = ScanOnce(context.Background(), ScanOptions{
		Root:      root,
		StatePath: statePath,
		Now:       time.Date(2026, 5, 4, 12, 2, 0, 0, time.UTC),
		Export: func(_ context.Context, turn agenttrace.Turn) (int, error) {
			t.Fatalf("duplicate queued trace exported: %+v", turn)
			return 0, nil
		},
	}, state)
	if err != nil {
		t.Fatalf("ScanOnce duplicate: %v", err)
	}
	if exported != 0 {
		t.Fatalf("duplicate exported = %d", exported)
	}
}

// TEST-533
func TestWatchReloadsClaudeQueueFromHookState(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	statePath := filepath.Join(root, "langfuse-export-state.json")
	transcriptPath := filepath.Join(root, "claude-no-tools.jsonl")
	copyFile(t, filepath.Join("..", "..", "testdata", "sources", "claude", "no-tools.jsonl"), transcriptPath)
	initial := exportstate.State{
		Version:         1,
		ScanWatermarkNS: time.Date(2026, 5, 4, 11, 59, 0, 0, time.UTC).UnixNano(),
	}
	if err := exportstate.Save(statePath, initial); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	exported := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		errCh <- WatchSessions(ctx, ScanOptions{
			Root:                root,
			StatePath:           statePath,
			PollIntervalSeconds: 0.01,
			Quiet:               true,
			Export: func(_ context.Context, turn agenttrace.Turn) (int, error) {
				exported <- turn.TraceID
				cancel()
				return 202, nil
			},
		})
	}()

	time.Sleep(50 * time.Millisecond)
	if err := exportstate.Enqueue(statePath, exportstate.QueueRequest{
		Provider:   agenttrace.ProviderClaude,
		SourcePath: transcriptPath,
		SessionID:  "claude-no-tools",
		CWD:        root,
		EnqueuedAt: time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano),
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

// EVAL-007
func TestEvalHookQueueDrainLatency(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	statePath := filepath.Join(root, "langfuse-export-state.json")
	transcriptPath := filepath.Join(root, "claude-no-tools.jsonl")
	copyFile(t, filepath.Join("..", "..", "testdata", "sources", "claude", "no-tools.jsonl"), transcriptPath)
	state := exportstate.State{
		Version:         1,
		ScanWatermarkNS: time.Date(2026, 5, 4, 11, 59, 0, 0, time.UTC).UnixNano(),
		Queue: []exportstate.QueueRequest{{
			Provider:   agenttrace.ProviderClaude,
			SourcePath: transcriptPath,
			SessionID:  "claude-no-tools",
			EnqueuedAt: time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano),
		}},
	}
	start := time.Now()
	_, exported, err := ScanOnce(context.Background(), ScanOptions{
		Root:      root,
		StatePath: statePath,
		Now:       time.Date(2026, 5, 4, 12, 1, 0, 0, time.UTC),
		Quiet:     true,
		Export: func(context.Context, agenttrace.Turn) (int, error) {
			return 202, nil
		},
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
