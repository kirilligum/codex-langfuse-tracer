package watch

import (
	"context"
	"fmt"
	"github.com/kirilligum/codex-langfuse-tracer/internal/agenttrace"
	"github.com/kirilligum/codex-langfuse-tracer/internal/exportstate"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProgressiveCheckpointSurvivesRetryRestartAndCompletion(t *testing.T) {
	root, statePath, path := watchFixture(t)
	_ = root
	_ = path
	state := exportstate.State{Version: exportstate.Version}
	traceID := agenttrace.StableTraceID("codex", "s", "t")
	turn := agenttrace.Turn{Provider: "codex", SessionID: "s", TurnID: "t", TraceID: traceID, StartTS: time.Now().Format(time.RFC3339Nano), UserMessages: []string{"input"}, Observations: []agenttrace.Observation{{Name: "codex.tool.command", Input: "ls", Output: "ok"}}}
	attempts := 0
	fail := true
	opts := ScanOptions{StatePath: statePath, Quiet: true, ResolveWorkspace: testWorkspace, ExportSpans: func(_ context.Context, delta agenttrace.Turn, _ string) (int, error) {
		attempts++
		if delta.FirstObservation != 0 || !delta.ExportDelta {
			t.Fatalf("unexpected initial delta: %+v", delta)
		}
		if fail {
			return 503, fmt.Errorf("unavailable")
		}
		return 200, nil
	}}
	attempted := false
	state, _, failed, err := processTurn(context.Background(), opts, state, turn, filepath.Base(path), &attempted)
	if err != nil || !failed || len(state.TurnProgress) != 0 {
		t.Fatalf("failed batch checkpointed: %+v err=%v", state, err)
	}
	fail = false
	attempted = false
	state, _, failed, err = processTurn(context.Background(), opts, state, turn, path, &attempted)
	if err != nil || failed || state.TurnProgress[traceID].ObservationCount != 1 || state.HasProcessed(traceID) {
		t.Fatalf("progress checkpoint failed: %+v err=%v", state, err)
	}
	loaded, err := exportstate.Load(statePath)
	if err != nil {
		t.Fatal(err)
	}
	state = *loaded
	attempted = false
	state, _, _, err = processTurn(context.Background(), opts, state, turn, path, &attempted)
	if err != nil || attempts != 2 {
		t.Fatalf("unchanged prefix resent: attempts=%d err=%v", attempts, err)
	}
	turn.Completed = true
	turn.AssistantTexts = []string{"done"}
	turn.EndTS = turn.StartTS
	opts.ExportSpans = func(_ context.Context, delta agenttrace.Turn, _ string) (int, error) {
		if !delta.Completed || delta.FirstObservation != 1 {
			t.Fatalf("wrong final delta: %+v", delta)
		}
		return 200, nil
	}
	state, completed, _, err := processTurn(context.Background(), opts, state, turn, path, &attempted)
	if err != nil || completed != 1 || !state.HasProcessed(traceID) || len(state.TurnProgress) != 0 {
		t.Fatalf("final checkpoint: %+v err=%v", state, err)
	}
}

// TEST-002: a prompt-only turn must pass the existing watcher checkpoint path.
func TestPromptOnlyProgressSurvivesRetryRestartAndCompletion(t *testing.T) {
	for _, provider := range []string{"codex", "claude"} {
		t.Run(provider, func(t *testing.T) {
			_, statePath, path := watchFixture(t)
			traceID := agenttrace.StableTraceID(provider, "s", "t")
			turn := agenttrace.Turn{Provider: provider, SessionID: "s", TurnID: "t", TraceID: traceID,
				StartTS: "2026-10-04T12:00:00Z", UserMessages: []string{"input"}}
			state := exportstate.State{Version: exportstate.Version}
			attempts := 0
			fail := true
			opts := ScanOptions{StatePath: statePath, Quiet: true, ResolveWorkspace: testWorkspace,
				ExportSpans: func(_ context.Context, delta agenttrace.Turn, _ string) (int, error) {
					attempts++
					if delta.TraceID != traceID || !delta.ExportDelta {
						t.Fatalf("wrong prompt delta: %+v", delta)
					}
					if delta.InputEmitted != (attempts == 3) {
						t.Fatalf("wrong input acknowledgement on attempt %d: %+v", attempts, delta)
					}
					if fail {
						return 503, fmt.Errorf("unavailable")
					}
					return 200, nil
				},
			}
			attempted := false
			var failed bool
			var err error
			state, _, failed, err = processTurn(context.Background(), opts, state, turn, path, &attempted)
			if err != nil || !failed || len(state.TurnProgress) != 0 || attempts != 1 {
				t.Fatalf("failed input advanced state: attempts=%d state=%+v err=%v", attempts, state, err)
			}
			fail = false
			attempted = false
			state, _, failed, err = processTurn(context.Background(), opts, state, turn, path, &attempted)
			if err != nil || failed || attempts != 2 {
				t.Fatalf("input not accepted: attempts=%d state=%+v err=%v", attempts, state, err)
			}
			if !state.TurnProgress[traceID].InputEmitted {
				t.Fatal("accepted input was not checkpointed")
			}
			loaded, err := exportstate.Load(statePath)
			if err != nil {
				t.Fatal(err)
			}
			if loaded == nil {
				t.Fatal("progress was not saved")
			}
			state = *loaded
			if !state.TurnProgress[traceID].InputEmitted {
				t.Fatal("accepted input was lost on restart")
			}
			attempted = false
			state, _, failed, err = processTurn(context.Background(), opts, state, turn, path, &attempted)
			if err != nil || failed || attempts != 2 {
				t.Fatalf("unchanged input replayed: attempts=%d err=%v", attempts, err)
			}
			turn.Completed = true
			turn.EndTS = "2026-10-04T12:00:01Z"
			turn.AssistantTexts = []string{"done"}
			attempted = false
			state, completed, failed, err := processTurn(context.Background(), opts, state, turn, path, &attempted)
			if err != nil || failed || completed != 1 || attempts != 3 || !state.HasProcessed(traceID) {
				t.Fatalf("finalization: attempts=%d state=%+v err=%v", attempts, state, err)
			}
		})
	}
}

func TestClaudeToolHookExportsStepsAndStopFinalizesWithoutPolling(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "claude.jsonl")
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "sources", "claude", "bash-tool.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if err := os.WriteFile(path, []byte(strings.Join(lines[:3], "\n")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	request := exportstate.QueueRequest{Provider: "claude", SourcePath: path, SessionID: "claude-bash-tool"}
	state := exportstate.State{Version: exportstate.Version, Queue: []exportstate.QueueRequest{request}}
	calls := 0
	opts := ScanOptions{Quiet: true, ResolveWorkspace: testWorkspace, ExportSpans: func(_ context.Context, delta agenttrace.Turn, _ string) (int, error) {
		calls++
		if calls == 1 && (delta.Completed || len(delta.Observations) != 1 || len(delta.ModelCalls) != 1) {
			t.Fatalf("wrong live delta: %+v", delta)
		}
		if calls == 2 && (!delta.Completed || delta.FirstObservation != 1 || delta.FirstModelCall != 1) {
			t.Fatalf("wrong final delta: %+v", delta)
		}
		return 200, nil
	}}
	attempted := false
	state, completed, err := drainQueue(context.Background(), opts, state, &attempted)
	if err != nil || completed != 0 || len(state.Queue) != 0 || len(state.TurnProgress) != 1 || calls != 1 {
		t.Fatalf("live queue: %+v calls=%d err=%v", state, calls, err)
	}
	state.Queue = []exportstate.QueueRequest{request}
	attempted = false
	state, _, err = drainQueue(context.Background(), opts, state, &attempted)
	if err != nil || len(state.Queue) != 0 || calls != 1 {
		t.Fatalf("unchanged event replayed: calls=%d err=%v", calls, err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	state.Queue = []exportstate.QueueRequest{request}
	attempted = false
	state, completed, err = drainQueue(context.Background(), opts, state, &attempted)
	if err != nil || completed != 1 || len(state.TurnProgress) != 0 || len(state.Queue) != 0 || calls != 2 {
		t.Fatalf("final queue: %+v calls=%d err=%v", state, calls, err)
	}
}
