package watch

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kirilligum/codex-langfuse-tracer/internal/agenttrace"
	"github.com/kirilligum/codex-langfuse-tracer/internal/codextrace"
	"github.com/kirilligum/codex-langfuse-tracer/internal/exportstate"
)

func TestWatchShrunkenPrefixPreservesCheckpointAndContinues(t *testing.T) {
	for _, field := range []string{"observations", "model_calls"} {
		t.Run(field, func(t *testing.T) {
			root, statePath, path := watchFixture(t)
			now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
			setMTime(t, path, now.Add(-time.Second))
			turns, err := codextrace.ParseTurns(path)
			if err != nil || len(turns) != 1 {
				t.Fatalf("parse fixture: turns=%d err=%v", len(turns), err)
			}
			parent := turns[0]
			progress := exportstate.TurnProgress{
				ObservationCount: len(parent.Observations),
				ModelCallCount:   len(parent.ModelCalls),
				InputEmitted:     true,
				Environment:      "saved-workspace",
			}
			if field == "observations" {
				progress.ObservationCount++
			} else {
				progress.ModelCallCount++
			}
			state := exportstate.State{
				Version:         exportstate.Version,
				ScanWatermarkNS: now.Add(-time.Minute).UnixNano(),
				TurnProgress:    map[string]exportstate.TurnProgress{parent.TraceID: progress},
			}
			if err := exportstate.Save(context.Background(), statePath, state); err != nil {
				t.Fatal(err)
			}
			forkCopy := parent
			forkCopy.Completed = false
			healthy := parent
			healthy.TraceID = "healthy-turn"
			var stderr bytes.Buffer
			exports := 0
			opts := ScanOptions{
				Root: root, StatePath: statePath, Now: now, Stderr: &stderr,
				ResolveWorkspace: testWorkspace,
				ExportSpans: func(_ context.Context, turn agenttrace.Turn, _ string) (int, error) {
					if turn.TraceID != healthy.TraceID {
						t.Fatal("shorter source replayed an accepted turn")
					}
					exports++
					return 200, nil
				},
			}
			deps := defaultScanDependencies()
			deps.discover = func(string) ([]string, error) { return []string{path}, nil }
			deps.parse = func(string, func(string) bool) ([]agenttrace.Turn, error) {
				return []agenttrace.Turn{forkCopy, healthy}, nil
			}
			runtime := newScanRuntime()
			got, exported, err := scanOnce(context.Background(), opts, state, runtime, deps)
			if err != nil || exports != 1 || exported != 1 || !got.HasProcessed(healthy.TraceID) {
				t.Fatalf("healthy turn blocked: exports=%d exported=%d err=%v", exports, exported, err)
			}
			persisted, err := exportstate.Load(statePath)
			if err != nil || persisted == nil {
				t.Fatalf("load checkpoint: %v", err)
			}
			if persisted.TurnProgress[parent.TraceID] != progress || persisted.HasProcessed(parent.TraceID) || persisted.ScanWatermarkNS != state.ScanWatermarkNS {
				t.Fatal("mismatched source changed its checkpoint or advanced the watermark")
			}
			if runtime.get(path) != nil || !strings.Contains(stderr.String(), "observation_prefix_shrank") || !strings.Contains(stderr.String(), "watermark_advanced=false") {
				t.Fatal("mismatched source must remain visible and eligible for recovery")
			}
			// A later complete source can resume from the original accepted prefix.
			recovered := parent
			recovered.Observations = append(recovered.Observations, agenttrace.Observation{})
			recovered.ModelCalls = append(recovered.ModelCalls, agenttrace.ModelCall{})
			opts.ExportSpans = func(_ context.Context, turn agenttrace.Turn, environment string) (int, error) {
				if turn.FirstObservation != progress.ObservationCount || turn.FirstModelCall != progress.ModelCallCount || !turn.InputEmitted || environment != progress.Environment {
					t.Fatal("recovery did not preserve accepted offsets and workspace")
				}
				return 200, nil
			}
			attempted := false
			got, emitted, failed, err := processTurn(context.Background(), opts, got, recovered, path, &attempted)
			if err != nil || failed || emitted != 1 || !got.HasProcessed(parent.TraceID) {
				t.Fatalf("complete source did not recover: emitted=%d failed=%t err=%v", emitted, failed, err)
			}
		})
	}
}

func TestWatchQueuedShrunkenPrefixDoesNotBlockCodexScan(t *testing.T) {
	root, statePath, path := watchFixture(t)
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	setMTime(t, path, now.Add(-time.Second))
	traceID := completeTraceID(t, path)
	progress := exportstate.TurnProgress{ObservationCount: 1000, InputEmitted: true, Environment: "saved"}
	state := exportstate.State{
		Version:         exportstate.Version,
		ScanWatermarkNS: now.Add(-time.Minute).UnixNano(),
		TurnProgress:    map[string]exportstate.TurnProgress{traceID: progress},
		Queue:           []exportstate.QueueRequest{{Provider: agenttrace.ProviderCodex, SourcePath: path}},
	}
	if err := exportstate.Save(context.Background(), statePath, state); err != nil {
		t.Fatal(err)
	}
	scanned := false
	deps := defaultScanDependencies()
	deps.discover = func(string) ([]string, error) { scanned = true; return nil, nil }
	opts := ScanOptions{Root: root, StatePath: statePath, Now: now}
	got, _, err := scanOnce(context.Background(), opts, state, newScanRuntime(), deps)
	if err != nil || !scanned || len(got.Queue) != 1 || got.TurnProgress[traceID] != progress {
		t.Fatalf("queued mismatch blocked scanning or lost state: scanned=%t queue=%d err=%v", scanned, len(got.Queue), err)
	}
}
