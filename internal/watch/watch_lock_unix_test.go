//go:build unix

package watch

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/kirilligum/codex-langfuse-tracer/internal/agenttrace"
	"github.com/kirilligum/codex-langfuse-tracer/internal/exportstate"
)

func TestWatchWaitsForStateWithoutRestarting(t *testing.T) {
	root, statePath, rolloutPath := watchFixture(t)
	now := time.Now().UTC()
	setMTime(t, rolloutPath, now.Add(-time.Second))
	lockFile := holdWatchStateLock(t, statePath)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stderr := &watchLockLogWriter{lines: make(chan string, 16)}
	spanCalls := make(chan struct{}, 2)
	var spanCount atomic.Int32
	errCh := make(chan error, 1)
	go func() {
		errCh <- WatchSessions(ctx, ScanOptions{
			Root: root, StatePath: statePath, Stderr: stderr, PollIntervalSeconds: 0.01,
			ResolveWorkspace: testWorkspace,
			ExportSpans: func(context.Context, agenttrace.Turn, string) (int, error) {
				spanCount.Add(1)
				spanCalls <- struct{}{}
				return 200, nil
			},
			ExportScores: successfulScores,
		})
	}()

	waitForWatchLockLog(t, stderr.lines, "ERROR: export state lock busy")
	if spanCount.Load() != 0 {
		t.Fatalf("watch exported %d traces before startup state acquisition", spanCount.Load())
	}
	select {
	case <-spanCalls:
		t.Fatal("watch scanned the source before its startup state operation acquired the lock")
	default:
	}
	releaseWatchStateLock(t, lockFile)

	select {
	case <-spanCalls:
	case err := <-errCh:
		t.Fatalf("watch exited before processing after lock release: %v", err)
	case <-time.After(6 * time.Second):
		t.Fatal("watch did not resume after lock release")
	}
	traceID := completeTraceID(t, rolloutPath)
	deadline := time.NewTimer(6 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer deadline.Stop()
	defer ticker.Stop()
	for {
		state, err := exportstate.Load(statePath)
		if err != nil {
			t.Fatal(err)
		}
		if state != nil && state.HasProcessed(traceID) {
			break
		}
		select {
		case err := <-errCh:
			t.Fatalf("watch exited before the trace checkpoint committed: %v", err)
		case <-deadline.C:
			t.Fatalf("trace checkpoint did not commit: %+v", state)
		case <-ticker.C:
		}
	}
	cancel()
	select {
	case err := <-errCh:
		if err == nil || err.Error() != context.Canceled.Error() {
			t.Fatalf("WatchSessions error = %v, want clean cancellation", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("watch did not stop after cancellation")
	}
	if got := spanCount.Load(); got != 1 {
		t.Fatalf("span export count = %d, want one after same-process recovery", got)
	}
	state, err := exportstate.Load(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if state == nil || !state.HasProcessed(traceID) {
		t.Fatalf("watch state after recovery = %+v", state)
	}
}

func TestWatchRetriesPendingCheckpointOnly(t *testing.T) {
	root, statePath, rolloutPath := watchFixture(t)
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	setMTime(t, rolloutPath, now.Add(-time.Second))
	initial := exportstate.State{Version: exportstate.Version, ScanWatermarkNS: now.Add(-time.Minute).UnixNano()}
	if err := exportstate.Save(context.Background(), statePath, initial); err != nil {
		t.Fatal(err)
	}
	busyWriter := &watchLockLogWriter{lines: make(chan string, 16)}
	locked := make(chan *os.File, 1)
	var spanCount, scoreCount atomic.Int32
	type result struct {
		state    exportstate.State
		exported int
		err      error
	}
	resultCh := make(chan result, 1)
	go func() {
		state, exported, err := ScanOnce(context.Background(), ScanOptions{
			Root: root, StatePath: statePath, Stderr: busyWriter, Now: now,
			ResolveWorkspace: testWorkspace,
			ExportSpans: func(context.Context, agenttrace.Turn, string) (int, error) {
				spanCount.Add(1)
				locked <- holdWatchStateLock(t, statePath)
				return 202, nil
			},
			ExportScores: func(context.Context, agenttrace.Turn, string) error {
				scoreCount.Add(1)
				return nil
			},
		}, initial)
		resultCh <- result{state: state, exported: exported, err: err}
	}()
	var lockFile *os.File
	select {
	case lockFile = <-locked:
	case <-time.After(6 * time.Second):
		t.Fatal("span export did not reach the state checkpoint contention barrier")
	}
	waitForWatchLockLog(t, busyWriter.lines, "ERROR: export state lock busy")
	if spanCount.Load() != 1 || scoreCount.Load() != 0 {
		t.Fatalf("callbacks while checkpoint waits: spans=%d scores=%d", spanCount.Load(), scoreCount.Load())
	}
	releaseWatchStateLock(t, lockFile)
	var got result
	select {
	case got = <-resultCh:
	case <-time.After(6 * time.Second):
		t.Fatal("state checkpoint did not finish after lock release")
	}
	if got.err != nil {
		t.Fatal(got.err)
	}
	traceID := completeTraceID(t, rolloutPath)
	if got.exported != 1 || spanCount.Load() != 1 || scoreCount.Load() != 1 || !got.state.HasProcessed(traceID) || got.state.PendingScoreEnvironment(traceID) != "" {
		t.Fatalf("checkpoint retry repeated or lost work: exported=%d spans=%d scores=%d state=%+v", got.exported, spanCount.Load(), scoreCount.Load(), got.state)
	}
	persisted, err := exportstate.Load(statePath)
	if err != nil || persisted == nil || !persisted.HasProcessed(traceID) {
		t.Fatalf("persisted state=%+v err=%v", persisted, err)
	}
}

func TestWatchRetriesQueueRemovalAfterCheckpoint(t *testing.T) {
	root := t.TempDir()
	statePath := filepath.Join(root, "state.json")
	transcriptPath := filepath.Join(root, "claude.jsonl")
	copyFile(t, filepath.Join("..", "..", "testdata", "sources", "claude", "no-tools.jsonl"), transcriptPath)
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	queued := exportstate.QueueRequest{
		Provider: agenttrace.ProviderClaude, SourcePath: transcriptPath, SessionID: "claude-session",
		EnqueuedAt: now.Format(time.RFC3339Nano),
	}
	initial := exportstate.State{Version: exportstate.Version, ScanWatermarkNS: now.Add(-time.Minute).UnixNano(), Queue: []exportstate.QueueRequest{queued}}
	if err := exportstate.Save(context.Background(), statePath, initial); err != nil {
		t.Fatal(err)
	}
	stdout := &watchScoredLockWriter{lockPath: statePath + ".lock", acquired: make(chan *os.File, 1)}
	stderr := &watchLockLogWriter{lines: make(chan string, 16)}
	var spanCount, scoreCount atomic.Int32
	type result struct {
		state    exportstate.State
		exported int
		err      error
	}
	resultCh := make(chan result, 1)
	go func() {
		state, exported, err := ScanOnce(context.Background(), ScanOptions{
			Root: root, StatePath: statePath, Stdout: stdout, Stderr: stderr, Now: now,
			ResolveWorkspace: testWorkspace,
			ExportSpans: func(context.Context, agenttrace.Turn, string) (int, error) {
				spanCount.Add(1)
				return 202, nil
			},
			ExportScores: func(context.Context, agenttrace.Turn, string) error {
				scoreCount.Add(1)
				return nil
			},
		}, initial)
		resultCh <- result{state: state, exported: exported, err: err}
	}()
	var lockFile *os.File
	select {
	case lockFile = <-stdout.acquired:
	case <-time.After(6 * time.Second):
		t.Fatal("scored callback did not reach the queue-removal contention barrier")
	}
	waitForWatchLockLog(t, stderr.lines, "ERROR: export state lock busy")
	if spanCount.Load() != 1 || scoreCount.Load() != 1 {
		t.Fatalf("callbacks while queue removal waits: spans=%d scores=%d", spanCount.Load(), scoreCount.Load())
	}
	pending, err := exportstate.Load(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if pending == nil || len(pending.ProcessedTraceIDs) != 1 || len(pending.Queue) != 1 {
		t.Fatalf("state before queue removal recovery = %+v", pending)
	}
	traceID := pending.ProcessedTraceIDs[0]
	releaseWatchStateLock(t, lockFile)
	var got result
	select {
	case got = <-resultCh:
	case <-time.After(6 * time.Second):
		t.Fatal("queued request did not finish after lock release")
	}
	if got.err != nil {
		t.Fatal(got.err)
	}
	if got.exported != 1 || spanCount.Load() != 1 || scoreCount.Load() != 1 || !got.state.HasProcessed(traceID) || len(got.state.Queue) != 0 || got.state.ScanWatermarkNS != now.UnixNano() {
		t.Fatalf("queue retry lost or repeated work: exported=%d spans=%d scores=%d state=%+v", got.exported, spanCount.Load(), scoreCount.Load(), got.state)
	}
}

type watchLockLogWriter struct {
	lines chan string
}

func (writer *watchLockLogWriter) Write(data []byte) (int, error) {
	line := string(bytes.Clone(data))
	select {
	case writer.lines <- line:
	default:
	}
	return len(data), nil
}

type watchScoredLockWriter struct {
	lockPath string
	acquired chan *os.File
	started  atomic.Bool
}

func (writer *watchScoredLockWriter) Write(data []byte) (int, error) {
	if bytes.Contains(data, []byte("scored trace=")) && writer.started.CompareAndSwap(false, true) {
		file, err := os.OpenFile(writer.lockPath, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			return 0, err
		}
		if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
			_ = file.Close()
			return 0, fmt.Errorf("hold state lock after scoring: %w", err)
		}
		writer.acquired <- file
	}
	return len(data), nil
}

func holdWatchStateLock(t *testing.T, statePath string) *os.File {
	t.Helper()
	file, err := os.OpenFile(statePath+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		t.Fatalf("hold state lock: %v", err)
	}
	return file
}

func releaseWatchStateLock(t *testing.T, file *os.File) {
	t.Helper()
	if err := unix.Flock(int(file.Fd()), unix.LOCK_UN); err != nil {
		t.Errorf("release state lock: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Errorf("close state lock: %v", err)
	}
}

func waitForWatchLockLog(t *testing.T, lines <-chan string, pattern string) string {
	t.Helper()
	timer := time.NewTimer(6 * time.Second)
	defer timer.Stop()
	for {
		select {
		case line := <-lines:
			if strings.Contains(line, pattern) {
				return line
			}
		case <-timer.C:
			t.Fatalf("timed out waiting for %q", pattern)
			return ""
		}
	}
}
