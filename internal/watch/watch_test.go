package watch

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kirilligum/codex-langfuse-tracer/internal/codextrace"
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
	copyFile(t, filepath.Join("..", "..", "testdata", "rollouts", "corrupt-rollout.jsonl"), corrupt)
	if err := os.Chtimes(corrupt, old, old); err != nil {
		t.Fatal(err)
	}

	state := State{Version: 1, ScanWatermarkNS: now.Add(-2 * time.Minute).UnixNano()}
	var stderr bytes.Buffer
	exportCalls := 0
	state, exported, err := ScanOnce(context.Background(), ScanOptions{
		Root:      root,
		StatePath: statePath,
		Now:       now,
		Stderr:    &stderr,
		Export: func(context.Context, codextrace.Turn) (int, error) {
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
		Export: func(context.Context, codextrace.Turn) (int, error) {
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
		Export: func(context.Context, codextrace.Turn) (int, error) {
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

func watchFixture(t *testing.T) (root, statePath, rolloutPath string) {
	t.Helper()
	root = t.TempDir()
	sessionDir := filepath.Join(root, "sessions", "2026", "05", "01")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rolloutPath = filepath.Join(sessionDir, "rollout-complete-tools.jsonl")
	copyFile(t, filepath.Join("..", "..", "testdata", "rollouts", "complete-tools.jsonl"), rolloutPath)
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
