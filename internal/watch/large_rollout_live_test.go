package watch

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kirilligum/codex-langfuse-tracer/internal/agenttrace"
	"github.com/kirilligum/codex-langfuse-tracer/internal/exportstate"
)

// TestLiveCodexLargeRolloutFilteredScan reads a real large rollout using a
// temporary root and an in-memory copy of the export state. Export callbacks
// are stubs, so this test makes no remote requests and writes no production state.
func TestLiveCodexLargeRolloutFilteredScan(t *testing.T) {
	rolloutPath := os.Getenv("CODEX_LANGFUSE_LARGE_ROLLOUT_PATH")
	statePath := os.Getenv("CODEX_LANGFUSE_WATCH_STATE_PATH")
	if rolloutPath == "" || statePath == "" {
		t.Skip("set CODEX_LANGFUSE_LARGE_ROLLOUT_PATH and CODEX_LANGFUSE_WATCH_STATE_PATH to run the read-only large-rollout scan")
	}

	rolloutInfo, err := os.Stat(rolloutPath)
	if err != nil {
		t.Fatalf("stat rollout: %v", err)
	}
	if rolloutInfo.Size() < 100<<20 {
		t.Fatalf("rollout is %d bytes; this live check requires a file of at least 100 MiB", rolloutInfo.Size())
	}
	loadedState, err := exportstate.Load(statePath)
	if err != nil {
		t.Fatalf("load export state: %v", err)
	}
	state := *loadedState
	state.ScanWatermarkNS = rolloutInfo.ModTime().Add(-time.Nanosecond).UnixNano()

	root := t.TempDir()
	sessionDir := filepath.Join(root, "sessions", "live")
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		t.Fatalf("create temporary sessions directory: %v", err)
	}
	linkPath := filepath.Join(sessionDir, "rollout-live.jsonl")
	if err := os.Symlink(rolloutPath, linkPath); err != nil {
		t.Fatalf("link rollout into temporary root: %v", err)
	}

	now := rolloutInfo.ModTime().Add(time.Second)
	spanCalls := 0
	scoreCalls := 0
	state, exported, err := ScanOnce(context.Background(), ScanOptions{
		Root:      root,
		Now:       now,
		Quiet:     true,
		StatePath: "",
		ResolveWorkspace: func(_ context.Context, turn agenttrace.Turn) (agenttrace.Turn, string, error) {
			return turn, "large-rollout-probe", nil
		},
		ExportSpans: func(context.Context, agenttrace.Turn, string) (int, error) {
			spanCalls++
			return 200, nil
		},
		ExportScores: func(context.Context, agenttrace.Turn, string) error {
			scoreCalls++
			return nil
		},
	}, state)
	if err != nil {
		t.Fatalf("scan large rollout: %v", err)
	}
	if state.ScanWatermarkNS != now.UnixNano() {
		t.Fatalf("scan watermark = %d, want %d; rollout was not fully scanned", state.ScanWatermarkNS, now.UnixNano())
	}
	t.Logf("source_bytes=%d processed_traces=%d emitted_spans=%d span_calls=%d score_calls=%d", rolloutInfo.Size(), len(loadedState.ProcessedTraceIDs), exported, spanCalls, scoreCalls)
}
