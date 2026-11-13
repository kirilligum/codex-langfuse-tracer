package watch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/kirilligum/codex-langfuse-tracer/internal/agenttrace"
	"github.com/kirilligum/codex-langfuse-tracer/internal/codextrace"
	"github.com/kirilligum/codex-langfuse-tracer/internal/exportstate"
	"github.com/kirilligum/codex-langfuse-tracer/internal/laminar"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// These synthetic source records go through the real parser, watcher state file,
// and span projection. The exporter only substitutes for the transport boundary;
// duplicate-safe backend accounting requires its separate live acceptance gate.
// TEST-HUMAN-INPUT: late source messages survive root acknowledgement and restart.
func TestHumanRequestLateHumanInputDoesNotRewriteCompletedRequest(t *testing.T) {
	for _, beforeCheckpoint := range []bool{false, true} {
		t.Run(fmt.Sprintf("late_before_root_checkpoint=%t", beforeCheckpoint), func(t *testing.T) {
			root, statePath, path := watchFixture(t)
			source := newRequestSource(t, path)
			source.add("session_meta", map[string]any{"id": "request-session", "model": "fixture-model", "cwd": root})
			source.start("e1", "user", "e1")
			source.user("initial", "e1", "original request", "user.text", "user")
			source.answer("e1", "original answer")
			source.tokens(11, 2)
			source.complete("e1", false)
			if beforeCheckpoint {
				source.user("late-early", "e1", "already finished", "user.text", "user")
			}
			state, _, err := exportstate.LoadOrCreate(context.Background(), statePath, exportstate.State{Version: exportstate.Version, RequestCutoverNS: 1})
			if err != nil {
				t.Fatal(err)
			}
			var accepted, attempted []*requestSpanRecorder
			fail := false
			opts := ScanOptions{Root: root, StatePath: statePath, ResolveWorkspace: testWorkspace,
				ExportSpans: func(ctx context.Context, turn agenttrace.Turn, environment string) (int, error) {
					recorder := &requestSpanRecorder{}
					if err := laminar.EmitSpans(ctx, turn, environment, "fixture-user", "fixture-capture", recorder); err != nil {
						return 0, err
					}
					attempted = append(attempted, recorder)
					if fail {
						return 0, fmt.Errorf("acknowledgement lost")
					}
					accepted = append(accepted, recorder)
					return 200, nil
				}}
			scan := func() {
				t.Helper()
				setMTime(t, path, source.now)
				opts.Now = source.now.Add(time.Nanosecond)
				if _, _, err := ScanOnce(context.Background(), opts, state); err != nil {
					t.Fatal(err)
				}
				loaded, err := exportstate.Load(statePath)
				if err != nil || loaded == nil {
					t.Fatalf("reload checkpoint: %v", err)
				}
				state = *loaded
			}
			scan()
			requestTrace := agenttrace.StableTraceID("codex-request-v1", "request-session", "turn:e1")
			if !state.HasProcessed(requestTrace) {
				t.Fatal("request root was not checkpointed")
			}
			before, err := os.ReadFile(statePath)
			if err != nil {
				t.Fatal(err)
			}
			source.user("late-1", "e1", "late clarification", "user.text", "user")
			fail = true
			attemptStart := len(attempted)
			scan()
			failedIDs := requestSpanIDs(attempted[attemptStart:])
			if len(failedIDs) != 1 {
				t.Fatalf("late input should emit one child, got %d spans", len(failedIDs))
			}
			after, err := os.ReadFile(statePath)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("lost acknowledgement changed checkpoint: %v", err)
			}
			fail = false
			attemptStart = len(attempted)
			scan()
			if !reflect.DeepEqual(failedIDs, requestSpanIDs(attempted[attemptStart:])) {
				t.Fatal("late input identity changed on restart/retry")
			}
			attemptStart = len(attempted)
			source.user("late-1", "e1", "late clarification", "user.text", "user")
			source.user("initial", "e1", "original request", "user.text", "user")
			scan()
			if len(attempted) != attemptStart {
				t.Fatal("accepted late message was sent again")
			}
			source.user("late-2", "e1", "late clarification", "user.text", "user")
			scan()
			if len(requestSpanIDs(attempted[attemptStart:])) != 1 {
				t.Fatal("distinct repeated human submission was not retained")
			}
			rootCount, lateCount := 0, 0
			var totalTokens int64
			for _, span := range collectRequestSpans(accepted) {
				if span.Name() == "Codex request" {
					rootCount++
					if requestSpanText(t, span, "lmnr.span.input") != "original request" || requestSpanText(t, span, "lmnr.span.output") != "original answer" {
						t.Fatal("late input rewrote the completed root")
					}
				}
				attrs := requestSpanAttributes(span)
				totalTokens += attrs["gen_ai.usage.input_tokens"].AsInt64() + attrs["gen_ai.usage.output_tokens"].AsInt64()
				if strings.HasPrefix(attrs["cli.human_message.id"].AsString(), "late-") {
					lateCount++
					if span.Name() != "human.input" || span.SpanContext().TraceID().String() != requestTrace || span.Parent().SpanID().String() != agenttrace.StableSpanID("request-root-v1", requestTrace, "turn:e1", "") || !attrs["cli.request.after_completion"].AsBool() {
						t.Fatal("late input lost its original request lineage")
					}
					if attrs["gen_ai.usage.input_tokens"].AsInt64() != 0 || attrs["gen_ai.usage.output_tokens"].AsInt64() != 0 || attrs["lmnr.association.properties.metadata.cli_input_preview"].AsString() != "" {
						t.Fatal("late input changed usage or trace preview")
					}
				}
			}
			if rootCount != 1 || lateCount != 2+boolToInt(beforeCheckpoint) {
				t.Fatalf("roots=%d late inputs=%d", rootCount, lateCount)
			}
			if totalTokens != 13 {
				t.Fatalf("late inputs changed model usage: %d", totalTokens)
			}
		})
	}
}

func TestHumanRequestCaptureSurvivesContinuationLostAckAndRestart(t *testing.T) {
	root, statePath, path := watchFixture(t)
	source := newRequestSource(t, path)
	source.add("session_meta", map[string]any{"id": "request-session", "model": "fixture-model", "cwd": root})
	source.user("goal-control", "", "goal control", "user.goal", "harness")
	source.goal("active")
	source.start("e1", "goal", "e1")
	source.user("machine1", "e1", "machine continuation", "goal.internal_context", "harness")
	source.tool("lookup", "lookup-1", "{}")
	source.tokens(11, 2)
	source.add("response_item", map[string]any{"type": "function_call_output", "call_id": "lookup-1", "output": "lookup result"})
	source.answer("e1", "intermediate answer")
	source.tokens(5, 1)
	source.complete("e1", false)

	state, _, err := exportstate.LoadOrCreate(context.Background(), statePath, exportstate.State{Version: exportstate.Version, RequestCutoverNS: 1})
	if err != nil {
		t.Fatal(err)
	}
	var accepted, attempted []*requestSpanRecorder
	fail := false
	var log bytes.Buffer
	opts := ScanOptions{Root: root, StatePath: statePath, ResolveWorkspace: testWorkspace, Stdout: &log,
		ExportSpans: func(ctx context.Context, turn agenttrace.Turn, environment string) (int, error) {
			recorder := &requestSpanRecorder{}
			if err := laminar.EmitSpans(ctx, turn, environment, "fixture-user", "fixture-capture", recorder); err != nil {
				return 0, err
			}
			if len(recorder.spans) == 0 {
				t.Fatal("watcher dispatched an empty span batch")
			}
			attempted = append(attempted, recorder)
			if fail {
				return 0, fmt.Errorf("acknowledgement lost")
			}
			accepted = append(accepted, recorder)
			return 200, nil
		}}
	scan := func() {
		t.Helper()
		// Every pass loads committed state, like a fresh process. Retries inspect
		// the same source without advancing its mtime or its persisted watermark.
		setMTime(t, path, source.now)
		opts.Now = source.now.Add(time.Nanosecond)
		_, _, err := ScanOnce(context.Background(), opts, state)
		if err != nil {
			t.Fatal(err)
		}
		loaded, err := exportstate.Load(statePath)
		if err != nil || loaded == nil {
			t.Fatalf("reload committed checkpoint: %v", err)
		}
		state = *loaded
	}
	scan()
	requestTrace := agenttrace.StableTraceID("codex-request-v1", "request-session", "goal:request-session:100")
	if state.HasProcessed(requestTrace) || state.TurnProgress[requestTrace].HumanInputCount != 1 {
		t.Fatalf("active goal incorrectly finalized or input not checkpointed: %+v", state)
	}
	if !state.HasProcessed(agenttrace.StableTraceID("codex", "request-session", "e1")) {
		t.Fatal("completed execution was not checkpointed independently")
	}
	for _, span := range collectRequestSpans(accepted) {
		if span.Name() == "Codex request" {
			t.Fatal("execution completion prematurely closed active goal")
		}
	}

	source.start("e2", "goal", "e2")
	source.user("steer", "e2", "include the totals", "user.text", "user")
	source.tool("update_goal", "finish-goal", `{"status":"complete"}`)
	source.tokens(7, 2)
	source.add("response_item", map[string]any{"type": "function_call_output", "call_id": "finish-goal", "output": []any{map[string]any{"type": "text", "text": `{"goal":{"threadId":"request-session","createdAt":100,"objective":"prepare the report","status":"complete"},"remainingTokens":null,"completionBudgetReport":null}`}}})
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	fail = true
	startAttempt := len(attempted)
	scan()
	failedIDs := requestSpanIDs(attempted[startAttempt:])
	after, err := os.ReadFile(statePath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("lost acknowledgement modified committed state: %v", err)
	}
	fail = false
	startAttempt = len(attempted)
	scan()
	if !reflect.DeepEqual(failedIDs, requestSpanIDs(attempted[startAttempt:])) {
		t.Fatal("retry changed span identities")
	}
	if state.HasProcessed(requestTrace) {
		t.Fatal("goal status completed request before final execution ended")
	}
	source.answer("e2", "final report with totals")
	source.tokens(3, 1)
	source.complete("e2", false)
	scan()
	if !state.HasProcessed(requestTrace) || len(state.ProcessedTraceIDs) != 3 || len(state.TurnProgress) != 0 {
		t.Fatalf("request and executions not fully checkpointed: %+v", state)
	}
	spans := collectRequestSpans(accepted)
	ids := map[string]bool{}
	var requestRoot sdktrace.ReadOnlySpan
	executions := map[string]bool{}
	var tokens int64
	metadataOwners, inputCount, calls := 0, 0, 0
	for _, span := range spans {
		id := span.SpanContext().SpanID().String()
		if ids[id] || span.SpanContext().TraceID().String() != requestTrace {
			t.Fatalf("duplicate accepted span or split request trace: %s", span.Name())
		}
		ids[id] = true
		if span.Name() == "Codex request" {
			requestRoot = span
		}
		if strings.HasPrefix(span.Name(), "Execution ") {
			executions[id] = true
		}
		attrs := requestSpanAttributes(span)
		if attrs["cli.human_message.id"].AsString() != "" {
			inputCount++
		}
		for key := range attrs {
			if strings.HasPrefix(key, "lmnr.association.properties.metadata.") {
				metadataOwners++
				break
			}
		}
		tokens += attrs["gen_ai.usage.input_tokens"].AsInt64() + attrs["gen_ai.usage.output_tokens"].AsInt64()
		if attrs["lmnr.span.type"].AsString() == "LLM" {
			calls++
		}
	}
	if requestRoot == nil || len(executions) != 2 || metadataOwners != 1 || inputCount != 2 || calls != 4 || tokens != 32 {
		t.Fatalf("wrong request projection: root=%v executions=%d metadataOwners=%d inputs=%d calls=%d tokens=%d", requestRoot != nil, len(executions), metadataOwners, inputCount, calls, tokens)
	}
	attrs := requestSpanAttributes(requestRoot)
	if requestSpanText(t, requestRoot, "lmnr.span.input") != "prepare the report\n\ninclude the totals" || requestSpanText(t, requestRoot, "lmnr.span.output") != "final report with totals" {
		t.Fatalf("wrong human input/final answer: %+v", attrs)
	}
	rootID := requestRoot.SpanContext().SpanID().String()
	for _, span := range spans {
		parent := span.Parent().SpanID().String()
		if span == requestRoot {
			if span.Parent().IsValid() {
				t.Fatal("request root has a parent")
			}
		} else if executions[span.SpanContext().SpanID().String()] || requestSpanAttributes(span)["cli.human_message.id"].AsString() != "" {
			if parent != rootID {
				t.Fatalf("%s missing request parent", span.Name())
			}
		} else if !executions[parent] {
			t.Fatalf("%s missing execution parent", span.Name())
		}
	}
	if !strings.Contains(log.String(), "trace="+requestTrace) {
		t.Fatal("watcher does not expose the physical request trace identity")
	}
	if strings.Contains(log.String(), "trace="+agenttrace.StableTraceID("codex", "request-session", "e1")) {
		t.Fatal("watcher reports an execution work item as a Laminar trace")
	}
	count := len(attempted)
	scan()
	if len(attempted) != count {
		t.Fatal("unchanged completed source replayed after restart")
	}
}

func TestHumanRequestInterleavedCommandEndUsesCallOwner(t *testing.T) {
	_, _, path := watchFixture(t)
	source := newRequestSource(t, path)
	source.add("session_meta", map[string]any{"id": "request-session"})
	source.start("first", "user", "first")
	source.user("first-input", "first", "run first command", "user.text", "user")
	source.tool("exec_command", "first-command", `{"cmd":"printf first"}`)
	source.start("second", "user", "second")
	source.user("second-input", "second", "run second command", "user.text", "user")
	source.tool("exec_command", "second-command", `{"cmd":"printf second"}`)
	for _, id := range []string{"second", "first"} {
		source.add("event_msg", map[string]any{"type": "exec_command_end", "call_id": id + "-command", "command": []string{"printf", id}, "aggregated_output": id + " result", "exit_code": 0})
		source.add("response_item", map[string]any{"type": "function_call_output", "call_id": id + "-command", "output": id + " result"})
		source.answer(id, id+" answer")
		source.complete(id, false)
	}
	turns, err := codextrace.ParseTurns(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"first", "second"} {
		var execution *agenttrace.Turn
		for i := range turns {
			if !turns[i].RequestRoot && turns[i].TurnID == id {
				execution = &turns[i]
			}
		}
		if execution == nil {
			t.Fatalf("missing execution %s", id)
		}
		commands := 0
		for _, observation := range execution.Observations {
			if observation.Name == "codex.tool.command" {
				commands++
				if !strings.Contains(observation.Output, id+" result") {
					t.Fatalf("execution %s received another command result: %+v", id, observation)
				}
			}
		}
		if commands != 1 || execution.PendingToolCount != 0 || execution.Request.Output != id+" answer" {
			t.Fatalf("execution %s lost call ownership: commands=%d pending=%d answer=%q", id, commands, execution.PendingToolCount, execution.Request.Output)
		}
	}
}

func TestHumanRequestAttributionUsesExplicitLineage(t *testing.T) {
	_, _, path := watchFixture(t)
	source := newRequestSource(t, path)
	source.add("session_meta", map[string]any{"id": "request-session"})
	source.start("original", "user", "original")
	source.user("human", "original", "fix the issue", "user.text", "user")
	source.start("recovery", "daemon_recovery", "original")
	source.complete("original", false) // Delayed completion cannot close recovery.
	turns, err := codextrace.ParseTurns(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 3 || turns[0].Completed || len(turns[0].Request.ExecutionIDs) != 2 {
		t.Fatalf("late old completion closed successor: %+v", turns)
	}
	source.answer("recovery", "recovered answer")
	source.complete("recovery", false)
	source.start("followup", "user", "original") // Same root attribution is not steering.
	source.user("new-human", "followup", "another question", "user.text", "user")
	source.complete("followup", true)
	turns, err = codextrace.ParseTurns(path)
	if err != nil {
		t.Fatal(err)
	}
	var roots []agenttrace.Turn
	for _, turn := range turns {
		if turn.RequestRoot {
			roots = append(roots, turn)
		}
	}
	if len(roots) != 2 || roots[0].OutputText() != "recovered answer" || roots[1].Request.State != "cancelled" || roots[1].OutputText() != "" || !roots[1].Completed {
		t.Fatalf("recovery/followup/cancellation contract failed: %+v", roots)
	}
	for _, root := range roots {
		recorder := &requestSpanRecorder{}
		if err := laminar.EmitSpans(context.Background(), root, "fixture", "fixture-user", "fixture-capture", recorder); err != nil {
			t.Fatalf("completed request with optional output not exportable: %v", err)
		}
	}
}

func TestHumanRequestCutoverPreservesAcceptedLegacyPrefix(t *testing.T) {
	root, statePath, path := watchFixture(t)
	source := newRequestSource(t, path)
	source.add("session_meta", map[string]any{"id": "request-session", "cwd": root})
	source.start("old", "user", "old")
	source.user("old-input", "old", "old human input", "user.text", "user")
	source.tool("lookup", "old-tool", "{}")
	source.tokens(4, 1)
	source.add("response_item", map[string]any{"type": "function_call_output", "call_id": "old-tool", "output": "old result"})
	turns, err := codextrace.ParseTurns(path)
	if err != nil || len(turns) != 2 {
		t.Fatalf("parse active source: %v", err)
	}
	legacy := turns[1]
	legacy.Request = nil
	legacy.UserMessages = legacy.LegacyUserMessages
	var captures []*requestSpanRecorder
	opts := ScanOptions{Root: root, StatePath: statePath, ResolveWorkspace: testWorkspace, Quiet: true,
		ExportSpans: func(ctx context.Context, turn agenttrace.Turn, environment string) (int, error) {
			r := &requestSpanRecorder{}
			if err := laminar.EmitSpans(ctx, turn, environment, "fixture-user", "fixture-capture", r); err != nil {
				return 0, err
			}
			captures = append(captures, r)
			return 200, nil
		}}
	attempted := false
	state, _, failed, err := processTurn(context.Background(), opts, exportstate.State{Version: exportstate.Version}, legacy, path, &attempted)
	if err != nil || failed || !state.TurnProgress[legacy.TraceID].InputEmitted {
		t.Fatalf("seed installed legacy progress: failed=%v err=%v", failed, err)
	}
	legacyProgress := state.TurnProgress[legacy.TraceID]
	cutover := source.now.Add(time.Second)
	state, _, err = InitializeState(context.Background(), statePath, cutover, nil, true)
	if err != nil || state.RequestCutoverNS != cutover.UnixNano() || state.TurnProgress[legacy.TraceID] != legacyProgress {
		t.Fatalf("upgrade changed prior checkpoint: %+v err=%v", state, err)
	}
	restarted, _, err := InitializeState(context.Background(), statePath, cutover.Add(time.Hour), nil, true)
	if err != nil || !reflect.DeepEqual(state, restarted) {
		t.Fatalf("restart moved cutover or altered checkpoints: %v", err)
	}
	source.answer("old", "old answer")
	source.tokens(2, 1)
	source.complete("old", false)
	source.start("new", "user", "new")
	source.user("new-input", "new", "new human input", "user.text", "user")
	source.answer("new", "new answer")
	source.tokens(3, 1)
	source.complete("new", false)
	setMTime(t, path, source.now)
	opts.Now = source.now.Add(time.Second)
	state, _, err = ScanOnce(context.Background(), opts, state)
	if err != nil {
		t.Fatal(err)
	}
	newTrace := agenttrace.StableTraceID("codex-request-v1", "request-session", "turn:new")
	if !state.HasProcessed(legacy.TraceID) || !state.HasProcessed(newTrace) || len(state.TurnProgress) != 0 {
		t.Fatalf("cutover final checkpoints: %+v", state)
	}
	seen := map[string]bool{}
	oldRoots, newRoots := 0, 0
	for _, span := range collectRequestSpans(captures) {
		key := span.SpanContext().TraceID().String() + ":" + span.SpanContext().SpanID().String()
		if seen[key] {
			t.Fatal("upgrade replayed an accepted legacy span")
		}
		seen[key] = true
		switch span.SpanContext().TraceID().String() {
		case legacy.TraceID:
			if !span.Parent().IsValid() {
				oldRoots++
			}
		case newTrace:
			if span.Name() == "Codex request" {
				newRoots++
			}
		default:
			t.Fatalf("cutover changed legacy identity or split new request: %s", span.Name())
		}
	}
	if oldRoots != 1 || newRoots != 1 {
		t.Fatalf("wrong roots after cutover: legacy=%d request=%d", oldRoots, newRoots)
	}
}

func TestHumanRequestLateToolResultSurvivesCompletedExecutionCheckpoint(t *testing.T) {
	root, statePath, path := watchFixture(t)
	source := newRequestSource(t, path)
	source.add("session_meta", map[string]any{"id": "request-session", "cwd": root})
	source.start("cancelled", "user", "cancelled")
	source.user("human", "cancelled", "inspect this", "user.text", "user")
	source.tool("slow_lookup", "pending-call", "{}")
	source.tokens(6, 2)
	source.complete("cancelled", true)
	state, _, err := exportstate.LoadOrCreate(context.Background(), statePath, exportstate.State{Version: exportstate.Version, RequestCutoverNS: 1})
	if err != nil {
		t.Fatal(err)
	}
	var captures []*requestSpanRecorder
	opts := ScanOptions{Root: root, StatePath: statePath, ResolveWorkspace: testWorkspace, Quiet: true,
		ExportSpans: func(ctx context.Context, turn agenttrace.Turn, environment string) (int, error) {
			r := &requestSpanRecorder{}
			if err := laminar.EmitSpans(ctx, turn, environment, "fixture-user", "fixture-capture", r); err != nil {
				return 0, err
			}
			if len(r.spans) == 0 {
				t.Fatal("late-result checkpoint sent an empty batch")
			}
			captures = append(captures, r)
			return 200, nil
		}}
	scan := func() {
		t.Helper()
		setMTime(t, path, source.now)
		opts.Now = source.now.Add(time.Nanosecond)
		_, _, err := ScanOnce(context.Background(), opts, state)
		if err != nil {
			t.Fatal(err)
		}
		loaded, err := exportstate.Load(statePath)
		if err != nil || loaded == nil {
			t.Fatalf("load checkpoint: %v", err)
		}
		state = *loaded
	}
	scan()
	executionWorkID := agenttrace.StableTraceID("codex", "request-session", "cancelled")
	requestTrace := agenttrace.StableTraceID("codex-request-v1", "request-session", "turn:cancelled")
	if !state.HasProcessed(requestTrace) || state.HasProcessed(executionWorkID) || !state.TurnProgress[executionWorkID].ExecutionEmitted {
		t.Fatalf("pending result lost its checkpoint or cancelled request remains open: %+v", state)
	}
	beforeCount := len(captures)
	scan()
	if len(captures) != beforeCount {
		t.Fatal("waiting for result repeated the completed execution")
	}
	source.add("response_item", map[string]any{"type": "function_call_output", "call_id": "pending-call", "output": "late lookup result"})
	scan()
	if !state.HasProcessed(executionWorkID) || len(state.TurnProgress) != 0 {
		t.Fatalf("late result did not complete remaining checkpoint: %+v", state)
	}
	late := collectRequestSpans(captures[beforeCount:])
	if len(late) != 1 || !requestSpanAttributes(late[0])["cli.capture.late_result"].AsBool() || late[0].SpanContext().TraceID().String() != requestTrace {
		t.Fatalf("late result was dropped, repeated execution, or split trace: spans=%d", len(late))
	}
	if !strings.Contains(requestSpanText(t, late[0], "lmnr.span.output"), "late lookup result") {
		t.Fatal("late result content missing")
	}
	seen := map[string]bool{}
	var tokens int64
	var execution sdktrace.ReadOnlySpan
	for _, span := range collectRequestSpans(captures) {
		id := span.SpanContext().SpanID().String()
		if seen[id] {
			t.Fatal("late result repeated a previously acknowledged span")
		}
		seen[id] = true
		attrs := requestSpanAttributes(span)
		tokens += attrs["gen_ai.usage.input_tokens"].AsInt64() + attrs["gen_ai.usage.output_tokens"].AsInt64()
		if span.Name() == "Execution cancelled" {
			execution = span
		}
	}
	if tokens != 8 || execution == nil || late[0].Parent().SpanID() != execution.SpanContext().SpanID() {
		t.Fatal("late result changed usage or lost its original execution parent")
	}
	// Source completion, not capture batching, determines late-result identity.
	turns, err := codextrace.ParseTurns(path)
	if err != nil {
		t.Fatal(err)
	}
	var full []*requestSpanRecorder
	for _, turn := range turns {
		r := &requestSpanRecorder{}
		if err := laminar.EmitSpans(context.Background(), turn, "default", "fixture-user", "fixture-capture", r); err != nil {
			t.Fatal(err)
		}
		full = append(full, r)
	}
	if !reflect.DeepEqual(requestSpanIDs(captures), requestSpanIDs(full)) {
		t.Fatal("capture batching changed late-result span identities")
	}
}

type requestSpanRecorder struct{ spans []sdktrace.ReadOnlySpan }

func (r *requestSpanRecorder) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	r.spans = append(r.spans, spans...)
	return nil
}
func (*requestSpanRecorder) Shutdown(context.Context) error { return nil }

func collectRequestSpans(recorders []*requestSpanRecorder) []sdktrace.ReadOnlySpan {
	var spans []sdktrace.ReadOnlySpan
	for _, r := range recorders {
		spans = append(spans, r.spans...)
	}
	return spans
}

func requestSpanIDs(recorders []*requestSpanRecorder) []string {
	var ids []string
	for _, span := range collectRequestSpans(recorders) {
		ids = append(ids, span.SpanContext().TraceID().String()+":"+span.SpanContext().SpanID().String())
	}
	sort.Strings(ids)
	return ids
}

func requestSpanAttributes(span sdktrace.ReadOnlySpan) map[string]attribute.Value {
	attrs := map[string]attribute.Value{}
	for _, attr := range span.Attributes() {
		attrs[string(attr.Key)] = attr.Value
	}
	return attrs
}

func requestSpanText(t *testing.T, span sdktrace.ReadOnlySpan, key string) string {
	t.Helper()
	raw := requestSpanAttributes(span)[key].AsString()
	if raw == "" {
		return ""
	}
	var text string
	if err := json.Unmarshal([]byte(raw), &text); err != nil {
		t.Fatalf("%s is not a JSON string: %v", key, err)
	}
	return text
}

type requestSource struct {
	t    *testing.T
	path string
	now  time.Time
}

func newRequestSource(t *testing.T, path string) *requestSource {
	t.Helper()
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	return &requestSource{t: t, path: path, now: time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)}
}

func (s *requestSource) add(kind string, payload map[string]any) {
	s.t.Helper()
	s.now = s.now.Add(time.Second)
	raw, err := json.Marshal(map[string]any{"type": kind, "timestamp": s.now.Format(time.RFC3339Nano), "payload": payload})
	if err != nil {
		s.t.Fatal(err)
	}
	appendRolloutLine(s.t, s.path, string(raw))
}
func (s *requestSource) start(id, trigger, root string) {
	s.add("event_msg", map[string]any{"type": "task_started", "turn_id": id, "turn_attribution": map[string]any{"turn_trigger": trigger, "root_turn_id": root}})
	s.add("turn_context", map[string]any{"turn_id": id, "model": "fixture-model"})
}
func (s *requestSource) user(id, execution, text, kind, author string) {
	s.add("response_item", map[string]any{"type": "message", "role": "user", "id": id, "content": []any{map[string]any{"type": "input_text", "text": text}}, "internal_chat_message_metadata_passthrough": map[string]any{"turn_id": execution, "content_item_kinds": []string{kind}, "content_item_metadata": []any{map[string]any{"provenance": map[string]any{"type": author}}}}})
}
func (s *requestSource) goal(status string) {
	s.add("event_msg", map[string]any{"type": "thread_goal_updated", "goal": map[string]any{"threadId": "request-session", "createdAt": 100, "objective": "prepare the report", "status": status}})
}
func (s *requestSource) answer(id, text string) {
	s.add("response_item", map[string]any{"type": "message", "role": "assistant", "turn_id": id, "phase": "final_answer", "content": []any{map[string]any{"type": "output_text", "text": text}}})
}
func (s *requestSource) tool(name, callID, arguments string) {
	s.add("response_item", map[string]any{"type": "function_call", "name": name, "call_id": callID, "arguments": arguments})
}
func (s *requestSource) tokens(input, output int) {
	s.add("event_msg", map[string]any{"type": "token_count", "info": map[string]any{"last_token_usage": map[string]any{"input_tokens": input, "output_tokens": output, "total_tokens": input + output}}})
}
func (s *requestSource) complete(id string, aborted bool) {
	kind := "task_complete"
	if aborted {
		kind = "turn_aborted"
	}
	s.add("event_msg", map[string]any{"type": kind, "turn_id": id})
}
