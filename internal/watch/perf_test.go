package watch

import (
	"context"
	"os"
	"path/filepath"
	"sort"
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

	logicalLatencies := make([]time.Duration, 0, 20)
	batchesByTrace := map[string]int{}
	state := exportstate.State{Version: 2, ScanWatermarkNS: now.Add(-2 * time.Minute).UnixNano()}
	if err := exportstate.Save(statePath, state); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, exported, err := ScanOnce(context.Background(), ScanOptions{
		ResolveWorkspace: testWorkspace,
		Root:             root,
		StatePath:        statePath,
		Now:              now,
		ExportSpans: func(_ context.Context, turn agenttrace.Turn, _ int, _ bool, _ string) (int, error) {
			batchesByTrace[turn.TraceID]++
			if !turn.Completed {
				endNS, parseErr := strconv.ParseInt(turn.Observations[len(turn.Observations)-1].EndTimeUnixNS, 10, 64)
				if parseErr != nil {
					t.Fatalf("parse observation end: %v", parseErr)
				}
				logicalLatencies = append(logicalLatencies, now.Sub(time.Unix(0, endNS)))
			}
			return 200, nil
		},
		ExportScores: func(context.Context, agenttrace.Turn, string) error { return nil },
	}, state)
	if err != nil {
		t.Fatal(err)
	}
	if exported != 21 {
		t.Fatalf("exported = %d, want 21", exported)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("scan latency = %s, want <= 5s", elapsed)
	}
	if len(logicalLatencies) != 20 {
		t.Fatalf("logical latency samples = %d, want 20", len(logicalLatencies))
	}
	sort.Slice(logicalLatencies, func(i, j int) bool { return logicalLatencies[i] < logicalLatencies[j] })
	p95 := logicalLatencies[18]
	if p95 > 10*time.Second {
		t.Fatalf("logical p95 eligibility latency = %s, want <= 10s", p95)
	}
	for traceID, batches := range batchesByTrace {
		if batches > 1 {
			t.Fatalf("trace %s emitted %d batches in one scan", traceID, batches)
		}
	}
	t.Logf("logical_p95=%s max_scan_wall=%s candidates=100 progressive=20", p95, time.Since(start))
}
