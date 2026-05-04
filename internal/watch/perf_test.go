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

// TEST-019
// EVAL-005
func TestEvalWatchExportLatency(t *testing.T) {
	t.Parallel()

	root, statePath, rolloutPath := watchFixture(t)
	now := time.Date(2026, 5, 1, 10, 1, 0, 0, time.UTC)
	raw, err := os.ReadFile(rolloutPath)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 99; i++ {
		path := filepath.Join(filepath.Dir(rolloutPath), "rollout-old-"+time.Unix(int64(i), 0).Format("150405")+".jsonl")
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, now.Add(-10*time.Minute), now.Add(-10*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chtimes(rolloutPath, now.Add(-30*time.Second), now.Add(-30*time.Second)); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	_, exported, err := ScanOnce(context.Background(), ScanOptions{
		Root:      root,
		StatePath: statePath,
		Now:       now,
		Export: func(context.Context, agenttrace.Turn) (int, error) {
			return 200, nil
		},
	}, exportstate.State{Version: 1, ScanWatermarkNS: now.Add(-2 * time.Minute).UnixNano()})
	if err != nil {
		t.Fatal(err)
	}
	if exported != 1 {
		t.Fatalf("exported = %d, want 1", exported)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("scan latency = %s, want <= 5s", elapsed)
	}
}
