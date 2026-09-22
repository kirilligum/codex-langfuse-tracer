package watch

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kirilligum/codex-langfuse-tracer/internal/agenttrace"
	"github.com/kirilligum/codex-langfuse-tracer/internal/exportstate"
)

// TEST-019
// TEST-609
// EVAL-005
// EVAL-601
func TestEvalWatchExportLatency(t *testing.T) {
	t.Parallel()

	root, statePath, rolloutPath := watchFixture(t)
	now := time.Date(2026, 5, 1, 11, 0, 8, 0, time.UTC)
	raw, err := os.ReadFile(rolloutPath)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 79; i++ {
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
	incompleteRaw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "sources", "codex", "incomplete-turn.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		fixture := strings.ReplaceAll(string(incompleteRaw), "sess-incomplete", "sess-progress-"+strconv.Itoa(i))
		fixture = strings.ReplaceAll(fixture, "turn-incomplete", "turn-progress-"+strconv.Itoa(i))
		path := filepath.Join(filepath.Dir(rolloutPath), "rollout-progress-"+strconv.Itoa(i)+".jsonl")
		if err := os.WriteFile(path, []byte(fixture), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, now.Add(-time.Second), now.Add(-time.Second)); err != nil {
			t.Fatal(err)
		}
	}

	batchesByTrace := map[string]int{}
	state := exportstate.State{Version: exportstate.Version, ScanWatermarkNS: now.Add(-2 * time.Minute).UnixNano()}
	if err := exportstate.Save(context.Background(), statePath, state); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, exported, err := ScanOnce(context.Background(), ScanOptions{
		ResolveWorkspace: testWorkspace,
		Root:             root,
		StatePath:        statePath,
		Now:              now,
		ExportSpans: func(_ context.Context, turn agenttrace.Turn, _ string) (int, error) {
			batchesByTrace[turn.TraceID]++
			return 200, nil
		},
		ExportScores: func(context.Context, agenttrace.Turn, string) error { return nil },
	}, state)
	if err != nil {
		t.Fatal(err)
	}
	if exported != 1 {
		t.Fatalf("exported = %d, want 1 completed turn", exported)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("scan latency = %s, want <= 5s", elapsed)
	}
	for traceID, batches := range batchesByTrace {
		if batches > 1 {
			t.Fatalf("trace %s emitted %d batches in one scan", traceID, batches)
		}
	}
	t.Logf("max_scan_wall=%s candidates=100 completed_only=true", time.Since(start))
}
