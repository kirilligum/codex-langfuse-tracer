package watch

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kirilligum/codex-langfuse-tracer/internal/agenttrace"
	"github.com/kirilligum/codex-langfuse-tracer/internal/codextrace"
	"github.com/kirilligum/codex-langfuse-tracer/internal/exportstate"
)

// TestLiveCodexLargeRolloutFilteredScan scans a private stable snapshot. It
// reads but never modifies the supplied rollout or export state and has only
// local export callbacks.
func TestLiveCodexLargeRolloutFilteredScan(t *testing.T) {
	rolloutPath := os.Getenv("CODEX_LANGFUSE_LARGE_ROLLOUT_PATH")
	statePath := os.Getenv("CODEX_LANGFUSE_WATCH_STATE_PATH")
	if rolloutPath == "" || statePath == "" {
		t.Skip("set CODEX_LANGFUSE_LARGE_ROLLOUT_PATH and CODEX_LANGFUSE_WATCH_STATE_PATH to run the read-only large-rollout scan")
	}

	sourceBefore, err := os.Stat(rolloutPath)
	if err != nil {
		t.Fatalf("stat rollout: %v", err)
	}
	if sourceBefore.Size() < 100<<20 {
		t.Fatalf("rollout is %d bytes; this live check requires a file of at least 100 MiB", sourceBefore.Size())
	}
	loadedState, err := exportstate.Load(statePath)
	if err != nil {
		t.Fatalf("load export state: %v", err)
	}
	if loadedState == nil {
		t.Fatal("export state does not exist; the live check requires an existing version 3 state")
	}

	scratchParent := largeProbeScratchParent(t)
	privateDir, err := os.MkdirTemp(scratchParent, "codex-langfuse-large-probe-")
	if err != nil {
		t.Fatalf("create private disk-backed directory: %v", err)
	}
	if err := os.Chmod(privateDir, 0o700); err != nil {
		_ = os.RemoveAll(privateDir)
		t.Fatalf("protect private directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(privateDir) })

	sessionDir := filepath.Join(privateDir, "sessions", "snapshot")
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		t.Fatalf("create private Codex sessions directory: %v", err)
	}
	snapshotPath := filepath.Join(sessionDir, "rollout-snapshot.jsonl")
	source, err := os.Open(rolloutPath)
	if err != nil {
		t.Fatalf("open rollout: %v", err)
	}
	snapshot, err := os.OpenFile(snapshotPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		_ = source.Close()
		t.Fatalf("create private snapshot: %v", err)
	}
	if _, err := io.CopyBuffer(snapshot, source, make([]byte, 128<<10)); err != nil {
		_ = source.Close()
		_ = snapshot.Close()
		t.Fatalf("copy rollout snapshot: %v", err)
	}
	if err := snapshot.Sync(); err != nil {
		_ = source.Close()
		_ = snapshot.Close()
		t.Fatalf("sync private snapshot: %v", err)
	}
	if err := snapshot.Close(); err != nil {
		_ = source.Close()
		t.Fatalf("close private snapshot: %v", err)
	}
	openedInfo, statErr := source.Stat()
	pathInfo, pathErr := os.Stat(rolloutPath)
	closeErr := source.Close()
	if statErr != nil || pathErr != nil || closeErr != nil {
		t.Fatalf("verify stable source: file_stat=%v path_stat=%v close=%v", statErr, pathErr, closeErr)
	}
	if !os.SameFile(sourceBefore, openedInfo) || !os.SameFile(openedInfo, pathInfo) || sourceBefore.Size() != pathInfo.Size() || sourceBefore.ModTime() != pathInfo.ModTime() {
		t.Fatal("rollout identity, size or modification time changed while the private snapshot was copied")
	}
	snapshotInfo, err := os.Stat(snapshotPath)
	if err != nil {
		t.Fatalf("stat private snapshot: %v", err)
	}
	if snapshotInfo.Size() != sourceBefore.Size() {
		t.Fatalf("snapshot size=%d, source size=%d", snapshotInfo.Size(), sourceBefore.Size())
	}

	loadedProcessed := make(map[string]struct{}, len(loadedState.ProcessedTraceIDs))
	for _, traceID := range loadedState.ProcessedTraceIDs {
		loadedProcessed[traceID] = struct{}{}
	}
	pendingScores := make(map[string]string, len(loadedState.PendingScores))
	for traceID, environment := range loadedState.PendingScores {
		pendingScores[traceID] = environment
	}
	sourceTraceIDs := make(map[string]struct{})
	if _, err := codextrace.ParseTurnsFiltered(snapshotPath, func(traceID string) bool {
		sourceTraceIDs[traceID] = struct{}{}
		return false
	}); err != nil {
		t.Fatalf("inventory snapshot trace IDs without retaining turns: %v", err)
	}
	processedOverlap := 0
	pendingOverlap := make(map[string]string)
	for traceID := range sourceTraceIDs {
		if _, ok := loadedProcessed[traceID]; ok {
			processedOverlap++
			continue
		}
		if environment, ok := pendingScores[traceID]; ok {
			pendingOverlap[traceID] = environment
			continue
		}
		loadedProcessed[traceID] = struct{}{}
	}
	if len(sourceTraceIDs) == 0 {
		t.Fatal("snapshot contains no trace IDs; target-consumption oracle would be empty")
	}

	const markerTraceID = "codex-langfuse-large-rollout-probe-marker"
	if _, ok := loadedProcessed[markerTraceID]; ok {
		t.Fatal("synthetic marker collides with existing processed state")
	}
	if _, ok := pendingScores[markerTraceID]; ok {
		t.Fatal("synthetic marker collides with pending score state")
	}
	marker := strings.Join([]string{
		`{"timestamp":"2026-09-22T12:00:01Z","type":"turn_context","payload":{"turn_id":"large-probe-marker","trace_id":"codex-langfuse-large-rollout-probe-marker"}}`,
		`{"timestamp":"2026-09-22T12:00:02Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"large rollout probe input"}]}}`,
		`{"timestamp":"2026-09-22T12:00:03Z","type":"response_item","payload":{"type":"message","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"large rollout probe output"}]}}`,
		`{"timestamp":"2026-09-22T12:00:04Z","type":"event_msg","payload":{"type":"task_complete"}}`,
	}, "\n") + "\n"
	markerFile, err := os.OpenFile(snapshotPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open private snapshot for marker: %v", err)
	}
	if _, err := io.WriteString(markerFile, marker); err != nil {
		_ = markerFile.Close()
		t.Fatalf("append synthetic marker: %v", err)
	}
	if err := markerFile.Sync(); err != nil {
		_ = markerFile.Close()
		t.Fatalf("sync synthetic marker: %v", err)
	}
	if err := markerFile.Close(); err != nil {
		t.Fatalf("close synthetic marker: %v", err)
	}

	snapshotMTime := sourceBefore.ModTime()
	if err := os.Chtimes(snapshotPath, snapshotMTime, snapshotMTime); err != nil {
		t.Fatalf("set controlled private snapshot time: %v", err)
	}
	now := time.Now()
	if !now.After(snapshotMTime) {
		now = snapshotMTime.Add(time.Second)
	}
	state := exportstate.State{
		Version:           exportstate.Version,
		ScanWatermarkNS:   snapshotMTime.Add(-time.Nanosecond).UnixNano(),
		ProcessedTraceIDs: make([]string, 0, len(loadedProcessed)),
		PendingScores:     pendingOverlap,
		Queue:             nil,
	}
	for traceID := range loadedProcessed {
		state.ProcessedTraceIDs = append(state.ProcessedTraceIDs, traceID)
	}
	sort.Strings(state.ProcessedTraceIDs)

	firstSourceID := ""
	lastSourceID := ""
	for traceID := range sourceTraceIDs {
		if firstSourceID == "" || traceID < firstSourceID {
			firstSourceID = traceID
		}
		if lastSourceID == "" || traceID > lastSourceID {
			lastSourceID = traceID
		}
	}
	visited := make(map[string]int)
	markerVisits := 0
	targetParseCalls := 0
	deps := defaultScanDependencies()
	originalParse := deps.parse
	deps.parse = func(path string, include func(string) bool) ([]agenttrace.Turn, error) {
		if path != snapshotPath {
			return originalParse(path, include)
		}
		targetParseCalls++
		return originalParse(path, func(traceID string) bool {
			if _, ok := sourceTraceIDs[traceID]; ok {
				visited[traceID]++
			}
			if traceID == markerTraceID {
				markerVisits++
			}
			return include(traceID)
		})
	}

	spanCalls := 0
	scoreCalls := 0
	seenPendingScores := make(map[string]bool, len(pendingOverlap))
	opts := ScanOptions{
		Root:  privateDir,
		Now:   now,
		Quiet: true,
		ResolveWorkspace: func(_ context.Context, turn agenttrace.Turn) (agenttrace.Turn, string, error) {
			return turn, "large-rollout-probe", nil
		},
		ExportSpans: func(_ context.Context, turn agenttrace.Turn, environment string) (int, error) {
			if turn.TraceID != markerTraceID || environment != "large-rollout-probe" || turn.InputText() != "large rollout probe input" || turn.OutputText() != "large rollout probe output" {
				t.Errorf("unexpected span in local probe: trace=%q environment=%q", turn.TraceID, environment)
			}
			spanCalls++
			return 200, nil
		},
		ExportScores: func(_ context.Context, turn agenttrace.Turn, environment string) error {
			if turn.TraceID == markerTraceID {
				if environment != "large-rollout-probe" {
					t.Errorf("marker score environment=%q", environment)
				}
			} else if expected, ok := pendingOverlap[turn.TraceID]; !ok || expected != environment {
				t.Errorf("unexpected pending score trace=%q environment=%q", turn.TraceID, environment)
			} else {
				seenPendingScores[turn.TraceID] = true
			}
			scoreCalls++
			return nil
		},
	}
	state, exported, err := scanOnce(context.Background(), opts, state, newScanRuntime(), deps)
	if err != nil {
		t.Fatalf("scan private large rollout snapshot: %v", err)
	}
	if targetParseCalls != 1 || markerVisits != 1 || visited[firstSourceID] == 0 || visited[lastSourceID] == 0 || len(visited) != len(sourceTraceIDs) {
		t.Fatalf("target-consumption oracle failed: parser_calls=%d marker_visits=%d source_ids=%d visited=%d first=%d last=%d", targetParseCalls, markerVisits, len(sourceTraceIDs), len(visited), visited[firstSourceID], visited[lastSourceID])
	}
	if err := assertTargetConsumed(targetParseCalls, sourceTraceIDs, visited, markerVisits); err != nil {
		t.Fatal(err)
	}
	if spanCalls != 1 || scoreCalls != len(pendingOverlap)+1 || exported != 1 {
		t.Fatalf("probe callbacks: exported=%d spans=%d scores=%d pending_source_scores=%d", exported, spanCalls, scoreCalls, len(pendingOverlap))
	}
	if !state.HasProcessed(markerTraceID) || state.ScanWatermarkNS != now.UnixNano() || len(state.PendingScores) != 0 || len(seenPendingScores) != len(pendingOverlap) {
		t.Fatalf("probe state did not reach expected completion: watermark=%d processed_marker=%t pending=%d expected_pending_scores=%d", state.ScanWatermarkNS, state.HasProcessed(markerTraceID), len(state.PendingScores), len(pendingOverlap))
	}

	sourceAfter, err := os.Stat(rolloutPath)
	if err != nil {
		t.Fatalf("restat original rollout: %v", err)
	}
	if !os.SameFile(sourceBefore, sourceAfter) || sourceBefore.Size() != sourceAfter.Size() || sourceBefore.ModTime() != sourceAfter.ModTime() {
		t.Fatal("original rollout changed during read-only probe")
	}
	t.Logf("source_bytes=%d source_traces=%d original_processed_overlap=%d pending_source_scores=%d parser_calls=%d include_visits=%d exported=%d span_calls=%d score_calls=%d", sourceBefore.Size(), len(sourceTraceIDs), processedOverlap, len(pendingOverlap), targetParseCalls, len(visited)+markerVisits, exported, spanCalls, scoreCalls)
}

func largeProbeScratchParent(t *testing.T) string {
	t.Helper()
	candidates := []string{os.Getenv("CODEX_LANGFUSE_LARGE_ROLLOUT_SCRATCH"), "/var/tmp"}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		info, err := os.Stat(candidate)
		if err != nil || !info.IsDir() {
			continue
		}
		var filesystem syscall.Statfs_t
		if err := syscall.Statfs(candidate, &filesystem); err != nil {
			continue
		}
		const tmpfsMagic = 0x01021994
		if uint64(filesystem.Type) == tmpfsMagic {
			continue
		}
		return candidate
	}
	t.Fatal("no disk-backed scratch directory is available; set CODEX_LANGFUSE_LARGE_ROLLOUT_SCRATCH")
	return ""
}

func assertTargetConsumed(parseCalls int, sourceIDs map[string]struct{}, visited map[string]int, markerVisits int) error {
	if parseCalls != 1 {
		return fmt.Errorf("target parser calls=%d, want 1", parseCalls)
	}
	if len(sourceIDs) == 0 || len(visited) != len(sourceIDs) {
		return fmt.Errorf("target source IDs=%d, visited=%d", len(sourceIDs), len(visited))
	}
	if markerVisits != 1 {
		return fmt.Errorf("target EOF marker visits=%d, want 1", markerVisits)
	}
	for traceID := range sourceIDs {
		if visited[traceID] == 0 {
			return fmt.Errorf("target did not visit a known source trace")
		}
	}
	return nil
}

func TestLargeRolloutProbeRejectsSkippedTarget(t *testing.T) {
	t.Parallel()

	root, _, rolloutPath := watchFixture(t)
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	setMTime(t, rolloutPath, now.Add(-2*time.Minute))
	parseCalls := 0
	deps := defaultScanDependencies()
	originalParse := deps.parse
	deps.parse = func(path string, include func(string) bool) ([]agenttrace.Turn, error) {
		parseCalls++
		return originalParse(path, include)
	}
	state := exportstate.State{Version: exportstate.Version, ScanWatermarkNS: now.Add(-time.Minute).UnixNano()}
	state, _, err := scanOnce(context.Background(), ScanOptions{Root: root, Now: now, Quiet: true}, state, newScanRuntime(), deps)
	if err != nil {
		t.Fatalf("ScanOnce: %v", err)
	}
	if state.ScanWatermarkNS != now.UnixNano() || parseCalls != 0 {
		t.Fatalf("negative control did not reproduce an mtime-skipped target: watermark=%d parses=%d", state.ScanWatermarkNS, parseCalls)
	}
	traceID := completeTraceID(t, rolloutPath)
	if err := assertTargetConsumed(parseCalls, map[string]struct{}{traceID: {}}, nil, 0); err == nil {
		t.Fatal("target-consumption oracle accepted a source excluded by mtime")
	}
}

func TestLargeRolloutTargetConsumptionOracle(t *testing.T) {
	t.Parallel()

	sourceIDs := map[string]struct{}{"source-first": {}, "source-last": {}}
	completeVisits := map[string]int{"source-first": 1, "source-last": 1}
	if err := assertTargetConsumed(1, sourceIDs, completeVisits, 1); err != nil {
		t.Fatalf("complete positive control rejected: %v", err)
	}
	for _, testCase := range []struct {
		name       string
		parseCalls int
		visited    map[string]int
		markers    int
	}{
		{name: "mtime skipped source", visited: completeVisits, markers: 1},
		{name: "source not fully consumed", parseCalls: 1, visited: map[string]int{"source-first": 1}, markers: 1},
		{name: "EOF marker not consumed", parseCalls: 1, visited: completeVisits},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if err := assertTargetConsumed(testCase.parseCalls, sourceIDs, testCase.visited, testCase.markers); err == nil {
				t.Fatal("target-consumption oracle accepted an incomplete target")
			}
		})
	}
}

func TestLargeRolloutProbeRejectsMalformedTail(t *testing.T) {
	t.Parallel()

	root, _, rolloutPath := watchFixture(t)
	appendRolloutLine(t, rolloutPath, "{not-json}")
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	setMTime(t, rolloutPath, now.Add(-time.Second))
	watermark := now.Add(-time.Minute).UnixNano()
	state := exportstate.State{Version: exportstate.Version, ScanWatermarkNS: watermark}
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
		t.Fatalf("ScanOnce: %v", err)
	}
	if exported != 0 || spanCalls != 0 || state.ScanWatermarkNS != watermark {
		t.Fatalf("malformed tail was accepted as a complete target: exported=%d spans=%d state=%+v", exported, spanCalls, state)
	}
}
