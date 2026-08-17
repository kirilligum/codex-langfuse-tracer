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
func TestProgressiveSuffixPlan(t *testing.T) {
	t.Parallel()

	turns, err := codextrace.ParseTurns(filepath.Join("..", "..", "testdata", "sources", "codex", "incomplete-turn.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 1 || len(turns[0].Observations) != 1 {
		t.Fatalf("fixture turns = %+v", turns)
	}
	turn := turns[0]

	plan, err := planTurn(turn, exportstate.TurnProgress{})
	if err != nil {
		t.Fatalf("plan initial incomplete: %v", err)
	}
	if !plan.ExportSpans || plan.FirstObservationIndex != 0 || plan.Final || plan.ExportScores {
		t.Fatalf("initial incomplete plan = %+v", plan)
	}

	plan, err = planTurn(turn, exportstate.TurnProgress{ExportedObservationCount: 1})
	if err != nil {
		t.Fatalf("plan unchanged incomplete: %v", err)
	}
	if plan.ExportSpans || plan.ExportScores {
		t.Fatalf("unchanged incomplete plan = %+v, want no work", plan)
	}

	turn.Completed = true
	turn.AssistantTexts = []string{"done"}
	plan, err = planTurn(turn, exportstate.TurnProgress{ExportedObservationCount: 1})
	if err != nil {
		t.Fatalf("plan final-only: %v", err)
	}
	if !plan.ExportSpans || plan.FirstObservationIndex != 1 || !plan.Final || plan.ExportScores {
		t.Fatalf("final-only plan = %+v", plan)
	}

	plan, err = planTurn(turn, exportstate.TurnProgress{ExportedObservationCount: 1, FinalSpansExported: true})
	if err != nil {
		t.Fatalf("plan scores: %v", err)
	}
	if plan.ExportSpans || !plan.ExportScores {
		t.Fatalf("score plan = %+v", plan)
	}

	if _, err := planTurn(turn, exportstate.TurnProgress{ExportedObservationCount: 2}); err == nil {
		t.Fatal("plan accepted a checkpoint beyond the parsed observation prefix")
	}
}

// TEST-604
func TestWatchProgressiveLifecycle(t *testing.T) {
	t.Parallel()

	root, statePath, rolloutPath := progressiveWatchFixture(t)
	now := time.Date(2026, 5, 1, 11, 1, 0, 0, time.UTC)
	setMTime(t, rolloutPath, now.Add(-time.Second))
	state := exportstate.State{Version: 2, ScanWatermarkNS: now.Add(-time.Minute).UnixNano()}
	if err := exportstate.Save(statePath, state); err != nil {
		t.Fatal(err)
	}

	type spanCall struct {
		First int
		Final bool
	}
	var spans []spanCall
	scores := 0
	opts := ScanOptions{
		ResolveWorkspace: testWorkspace,
		Root:             root,
		StatePath:        statePath,
		ExportSpans: func(_ context.Context, _ agenttrace.Turn, first int, final bool, _ string) (int, error) {
			spans = append(spans, spanCall{First: first, Final: final})
			return 200, nil
		},
		ExportScores: func(context.Context, agenttrace.Turn, string) error {
			scores++
			return nil
		},
	}
	state, exported, err := ScanOnce(context.Background(), withScanNow(opts, now), state)
	if err != nil {
		t.Fatal(err)
	}
	if exported != 1 || len(spans) != 1 || spans[0] != (spanCall{First: 0, Final: false}) || scores != 0 {
		t.Fatalf("partial lifecycle exported=%d spans=%+v scores=%d", exported, spans, scores)
	}
	if got := state.ProgressFor(incompleteTraceID(t)); got.ExportedObservationCount != 1 || got.FinalSpansExported {
		t.Fatalf("partial state = %+v", got)
	}

	state, exported, err = ScanOnce(context.Background(), withScanNow(opts, now.Add(time.Second)), state)
	if err != nil {
		t.Fatal(err)
	}
	if exported != 0 || len(spans) != 1 || scores != 0 {
		t.Fatalf("unchanged scan exported=%d spans=%+v scores=%d", exported, spans, scores)
	}

	appendRolloutLine(t, rolloutPath, `{"timestamp":"2026-05-01T11:00:05Z","type":"event_msg","payload":{"type":"task_complete","last_agent_message":"Partial answer"}}`)
	setMTime(t, rolloutPath, now.Add(1500*time.Millisecond))
	state, exported, err = ScanOnce(context.Background(), withScanNow(opts, now.Add(2*time.Second)), state)
	if err != nil {
		t.Fatal(err)
	}
	if exported != 1 || len(spans) != 2 || spans[1] != (spanCall{First: 1, Final: true}) || scores != 1 {
		t.Fatalf("final lifecycle exported=%d spans=%+v scores=%d", exported, spans, scores)
	}
	traceID := incompleteTraceID(t)
	if !state.HasProcessed(traceID) {
		t.Fatalf("final trace not processed: %+v", state)
	}
	if _, ok := state.TurnProgress[traceID]; ok {
		t.Fatalf("processed trace retained progress: %+v", state)
	}
}

// TEST-604
func TestWatchProgressiveFailureRetry(t *testing.T) {
	t.Parallel()

	root, statePath, rolloutPath := progressiveWatchFixture(t)
	now := time.Date(2026, 5, 1, 11, 1, 0, 0, time.UTC)
	setMTime(t, rolloutPath, now.Add(-time.Second))
	state := exportstate.State{Version: 2, ScanWatermarkNS: now.Add(-time.Minute).UnixNano()}
	if err := exportstate.Save(statePath, state); err != nil {
		t.Fatal(err)
	}

	spanAttempts := 0
	scoreAttempts := 0
	failSpan := true
	failScore := true
	opts := ScanOptions{
		ResolveWorkspace: testWorkspace,
		Root:             root,
		StatePath:        statePath,
		Quiet:            true,
		ExportSpans: func(context.Context, agenttrace.Turn, int, bool, string) (int, error) {
			spanAttempts++
			if failSpan {
				return 0, errors.New("injected span failure")
			}
			return 202, nil
		},
		ExportScores: func(context.Context, agenttrace.Turn, string) error {
			scoreAttempts++
			if failScore {
				return errors.New("injected score failure")
			}
			return nil
		},
	}

	state, _, err := ScanOnce(context.Background(), withScanNow(opts, now), state)
	if err != nil {
		t.Fatal(err)
	}
	if got := state.ProgressFor(incompleteTraceID(t)); got.ExportedObservationCount != 0 {
		t.Fatalf("failed partial advanced progress: %+v", got)
	}
	failSpan = false
	state, _, err = ScanOnce(context.Background(), withScanNow(opts, now.Add(time.Second)), state)
	if err != nil {
		t.Fatal(err)
	}
	if got := state.ProgressFor(incompleteTraceID(t)); got.ExportedObservationCount != 1 {
		t.Fatalf("retried partial progress: %+v", got)
	}

	appendRolloutLine(t, rolloutPath, `{"timestamp":"2026-05-01T11:00:05Z","type":"event_msg","payload":{"type":"task_complete","last_agent_message":"Partial answer"}}`)
	setMTime(t, rolloutPath, now.Add(1500*time.Millisecond))
	state, _, err = ScanOnce(context.Background(), withScanNow(opts, now.Add(2*time.Second)), state)
	if err != nil {
		t.Fatal(err)
	}
	traceID := incompleteTraceID(t)
	if got := state.ProgressFor(traceID); !got.FinalSpansExported {
		t.Fatalf("score failure lost final checkpoint: %+v", got)
	}
	spansAfterFinal := spanAttempts
	failScore = false
	state, _, err = ScanOnce(context.Background(), withScanNow(opts, now.Add(3*time.Second)), state)
	if err != nil {
		t.Fatal(err)
	}
	if spanAttempts != spansAfterFinal || scoreAttempts != 2 || !state.HasProcessed(traceID) {
		t.Fatalf("score retry spans=%d want=%d scores=%d state=%+v", spanAttempts, spansAfterFinal, scoreAttempts, state)
	}
}

// TEST-704
func TestWatchEnvironmentSnapshot(t *testing.T) {
	t.Parallel()

	legacyStatePath := filepath.Join(t.TempDir(), "legacy-state.json")
	if err := os.WriteFile(legacyStatePath, []byte("{\"version\":1}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WatchSessions(context.Background(), ScanOptions{StatePath: legacyStatePath, Quiet: true}); err == nil || !strings.Contains(err.Error(), "unsupported watch state version") {
		t.Fatalf("version 1 startup error = %v", err)
	}

	root, statePath, rolloutPath := progressiveWatchFixture(t)
	now := time.Date(2026, 5, 1, 11, 1, 0, 0, time.UTC)
	setMTime(t, rolloutPath, now.Add(-time.Second))
	state := exportstate.State{Version: 2, ScanWatermarkNS: now.Add(-time.Minute).UnixNano()}
	if err := exportstate.Save(statePath, state); err != nil {
		t.Fatal(err)
	}
	const environment = "repository--feature-one-a1b2c3"
	resolverCalls := 0
	state, _, err := ScanOnce(context.Background(), ScanOptions{
		Root:      root,
		StatePath: statePath,
		Now:       now,
		Quiet:     true,
		ResolveWorkspace: func(_ context.Context, turn agenttrace.Turn) (agenttrace.Turn, string, error) {
			resolverCalls++
			turn.GitBranch = "feature/one"
			return turn, environment, nil
		},
		ExportSpans: func(_ context.Context, _ agenttrace.Turn, _ int, _ bool, gotEnvironment string) (int, error) {
			if gotEnvironment != environment {
				t.Fatalf("span environment = %q", gotEnvironment)
			}
			persisted, err := exportstate.Load(statePath)
			if err != nil {
				t.Fatal(err)
			}
			if progress := persisted.ProgressFor(incompleteTraceID(t)); progress.Environment != environment {
				t.Fatalf("pre-network progress = %+v", progress)
			}
			return 0, errors.New("injected OTLP failure")
		},
		ExportScores: func(context.Context, agenttrace.Turn, string) error {
			t.Fatal("scores must not run after failed partial OTLP")
			return nil
		},
	}, state)
	if err != nil {
		t.Fatal(err)
	}
	if resolverCalls != 1 {
		t.Fatalf("resolver calls = %d, want 1", resolverCalls)
	}
	if progress := state.ProgressFor(incompleteTraceID(t)); progress.Environment != environment || progress.ExportedObservationCount != 0 {
		t.Fatalf("failed span progress = %+v", progress)
	}
}

// TEST-704
func TestWatchEnvironmentRetry(t *testing.T) {
	t.Parallel()

	root, statePath, rolloutPath := progressiveWatchFixture(t)
	now := time.Date(2026, 5, 1, 11, 1, 0, 0, time.UTC)
	setMTime(t, rolloutPath, now.Add(-time.Second))
	state := exportstate.State{Version: 2, ScanWatermarkNS: now.Add(-time.Minute).UnixNano()}
	if err := exportstate.Save(statePath, state); err != nil {
		t.Fatal(err)
	}

	const firstEnvironment = "repository--feature-one-a1b2c3"
	const changedEnvironment = "repository--feature-two-b2c3d4"
	resolverCalls := 0
	spanAttempts := 0
	scoreAttempts := 0
	var spanEnvironments []string
	var scoreEnvironments []string
	opts := ScanOptions{
		Root:      root,
		StatePath: statePath,
		Quiet:     true,
		ResolveWorkspace: func(_ context.Context, turn agenttrace.Turn) (agenttrace.Turn, string, error) {
			resolverCalls++
			if resolverCalls == 1 {
				turn.GitBranch = "feature/one"
				return turn, firstEnvironment, nil
			}
			turn.GitBranch = "feature/two"
			return turn, changedEnvironment, nil
		},
		ExportSpans: func(_ context.Context, _ agenttrace.Turn, _ int, _ bool, environment string) (int, error) {
			spanAttempts++
			spanEnvironments = append(spanEnvironments, environment)
			if spanAttempts == 1 {
				return 0, errors.New("injected first span failure")
			}
			return 202, nil
		},
		ExportScores: func(_ context.Context, _ agenttrace.Turn, environment string) error {
			scoreAttempts++
			scoreEnvironments = append(scoreEnvironments, environment)
			if scoreAttempts == 1 {
				return errors.New("injected first score failure")
			}
			return nil
		},
	}

	state, _, err := ScanOnce(context.Background(), withScanNow(opts, now), state)
	if err != nil {
		t.Fatal(err)
	}
	state, _, err = ScanOnce(context.Background(), withScanNow(opts, now.Add(time.Second)), state)
	if err != nil {
		t.Fatal(err)
	}
	appendRolloutLine(t, rolloutPath, `{"timestamp":"2026-05-01T11:00:05Z","type":"event_msg","payload":{"type":"task_complete","last_agent_message":"Partial answer"}}`)
	setMTime(t, rolloutPath, now.Add(1500*time.Millisecond))
	state, _, err = ScanOnce(context.Background(), withScanNow(opts, now.Add(2*time.Second)), state)
	if err != nil {
		t.Fatal(err)
	}
	resolverCallsBeforeScoreRetry := resolverCalls
	state, _, err = ScanOnce(context.Background(), withScanNow(opts, now.Add(3*time.Second)), state)
	if err != nil {
		t.Fatal(err)
	}
	if resolverCalls != resolverCallsBeforeScoreRetry {
		t.Fatalf("score-only retry resolved workspace: before=%d after=%d", resolverCallsBeforeScoreRetry, resolverCalls)
	}
	for _, environment := range append(spanEnvironments, scoreEnvironments...) {
		if environment != firstEnvironment {
			t.Fatalf("progressive environment changed: spans=%v scores=%v", spanEnvironments, scoreEnvironments)
		}
	}
	if spanAttempts != 3 || scoreAttempts != 2 || !state.HasProcessed(incompleteTraceID(t)) {
		t.Fatalf("retry lifecycle spans=%d scores=%d state=%+v", spanAttempts, scoreAttempts, state)
	}
}

func progressiveWatchFixture(t *testing.T) (root, statePath, rolloutPath string) {
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

func incompleteTraceID(t *testing.T) string {
	t.Helper()
	turns, err := codextrace.ParseTurns(filepath.Join("..", "..", "testdata", "sources", "codex", "incomplete-turn.jsonl"))
	if err != nil || len(turns) != 1 {
		t.Fatalf("parse incomplete fixture: turns=%d err=%v", len(turns), err)
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

	state := exportstate.State{Version: 2, ScanWatermarkNS: now.Add(-2 * time.Minute).UnixNano()}
	if err := exportstate.Save(statePath, state); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	exportCalls := 0
	state, exported, err := ScanOnce(context.Background(), ScanOptions{
		ResolveWorkspace: testWorkspace,
		Root:             root,
		StatePath:        statePath,
		Now:              now,
		Stderr:           &stderr,
		ExportSpans: func(context.Context, agenttrace.Turn, int, bool, string) (int, error) {
			exportCalls++
			return 0, errors.New("boom")
		},
		ExportScores: successfulScores,
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
		ResolveWorkspace: testWorkspace,
		Root:             root,
		StatePath:        statePath,
		Now:              now.Add(time.Minute),
		Stdout:           &stdout,
		Stderr:           &stderr,
		ExportSpans: func(context.Context, agenttrace.Turn, int, bool, string) (int, error) {
			exportCalls++
			return 200, nil
		},
		ExportScores: successfulScores,
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
		ResolveWorkspace: testWorkspace,
		Root:             root,
		StatePath:        statePath,
		Now:              now.Add(2 * time.Minute),
		ExportSpans: func(context.Context, agenttrace.Turn, int, bool, string) (int, error) {
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
		ResolveWorkspace:    testWorkspace,
		Root:                root,
		StatePath:           statePath,
		PollIntervalSeconds: 0.001,
		Quiet:               true,
		ExportSpans: func(context.Context, agenttrace.Turn, int, bool, string) (int, error) {
			t.Fatal("export should not run for empty canceled watch")
			return 0, nil
		},
		ExportScores: successfulScores,
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
	state := exportstate.State{Version: 2, ScanWatermarkNS: time.Date(2026, 5, 4, 11, 59, 0, 0, time.UTC).UnixNano()}
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
		ResolveWorkspace: testWorkspace,
		Root:             root,
		StatePath:        statePath,
		Now:              time.Date(2026, 5, 4, 12, 1, 0, 0, time.UTC),
		ExportSpans: func(_ context.Context, turn agenttrace.Turn, _ int, _ bool, _ string) (int, error) {
			exportedTraceIDs = append(exportedTraceIDs, turn.TraceID)
			return 202, nil
		},
		ExportScores: successfulScores,
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
		ResolveWorkspace: testWorkspace,
		Root:             root,
		StatePath:        statePath,
		Now:              time.Date(2026, 5, 4, 12, 2, 0, 0, time.UTC),
		ExportSpans: func(_ context.Context, turn agenttrace.Turn, _ int, _ bool, _ string) (int, error) {
			t.Fatalf("duplicate queued trace exported: %+v", turn)
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

// TEST-533
func TestWatchReloadsClaudeQueueFromHookState(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	statePath := filepath.Join(root, "langfuse-export-state.json")
	transcriptPath := filepath.Join(root, "claude-no-tools.jsonl")
	copyFile(t, filepath.Join("..", "..", "testdata", "sources", "claude", "no-tools.jsonl"), transcriptPath)
	initial := exportstate.State{
		Version:         2,
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
			ResolveWorkspace:    testWorkspace,
			Root:                root,
			StatePath:           statePath,
			PollIntervalSeconds: 0.01,
			Quiet:               true,
			ExportSpans: func(_ context.Context, turn agenttrace.Turn, _ int, _ bool, _ string) (int, error) {
				exported <- turn.TraceID
				cancel()
				return 202, nil
			},
			ExportScores: successfulScores,
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

func TestWaitBetweenExportsHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitBetweenExports(ctx, 60); !errors.Is(err, context.Canceled) {
		t.Fatalf("waitBetweenExports error = %v, want context canceled", err)
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
		Version:         2,
		ScanWatermarkNS: time.Date(2026, 5, 4, 11, 59, 0, 0, time.UTC).UnixNano(),
		Queue: []exportstate.QueueRequest{{
			Provider:   agenttrace.ProviderClaude,
			SourcePath: transcriptPath,
			SessionID:  "claude-no-tools",
			EnqueuedAt: time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano),
		}},
	}
	if err := exportstate.Save(statePath, state); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, exported, err := ScanOnce(context.Background(), ScanOptions{
		ResolveWorkspace: testWorkspace,
		Root:             root,
		StatePath:        statePath,
		Now:              time.Date(2026, 5, 4, 12, 1, 0, 0, time.UTC),
		Quiet:            true,
		ExportSpans: func(context.Context, agenttrace.Turn, int, bool, string) (int, error) {
			return 202, nil
		},
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

func successfulScores(context.Context, agenttrace.Turn, string) error {
	return nil
}

func testWorkspace(_ context.Context, turn agenttrace.Turn) (agenttrace.Turn, string, error) {
	return turn, "default", nil
}
