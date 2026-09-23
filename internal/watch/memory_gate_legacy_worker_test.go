package watch

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/kirilligum/codex-langfuse-tracer/internal/agenttrace"
	"github.com/kirilligum/codex-langfuse-tracer/internal/exportstate"
)

const memoryGateInstalledBaselineRevision = "03139fa58eb57c9a66b24c76903847d9c1330ea3"

// Set only when compiling the detached installed-baseline test binary. The
// legacy worker checks this linker-stamped identity before measuring anything.
var memoryGateLegacySourceRevision = "not-stamped-as-installed-baseline"

// TestLegacyWatchMemoryEnvelope is copied into a temporary worktree at the
// installed baseline revision and run against the exact fixtures prepared by
// TestWatchMemoryEnvelope. It intentionally uses only the stable public scan
// API so the candidate and baseline process the same source and state.
func TestLegacyWatchMemoryEnvelope(t *testing.T) {
	if os.Getenv(memoryGateLegacyWorkerEnv) != "1" {
		t.Skip("run only from the comparative memory gate")
	}
	if memoryGateLegacySourceRevision != memoryGateInstalledBaselineRevision {
		t.Fatalf("baseline worker binary identity=%q, want installed source revision %q", memoryGateLegacySourceRevision, memoryGateInstalledBaselineRevision)
	}
	t.Logf("baseline_source_revision=%s", memoryGateLegacySourceRevision)
	var spec memoryGateSpec
	raw, err := os.ReadFile(os.Getenv(memoryGateSpecEnv))
	if err != nil {
		t.Fatalf("read legacy worker spec: %v", err)
	}
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("decode legacy worker spec: %v", err)
	}
	state, err := exportstate.Load(os.Getenv(memoryGateStateEnv))
	if err != nil || state == nil {
		t.Fatalf("load baseline memory worker state: state=%t err=%v", state != nil, err)
	}
	now, err := time.Parse(time.RFC3339Nano, os.Getenv(memoryGateTimeEnv))
	if err != nil {
		t.Fatalf("parse baseline memory worker time: %v", err)
	}
	spanCalls := 0
	scoreCalls := 0
	opts := ScanOptions{
		Root:  os.Getenv(memoryGateRootEnv),
		Now:   now,
		Quiet: true,
		ResolveWorkspace: func(_ context.Context, turn agenttrace.Turn) (agenttrace.Turn, string, error) {
			return turn, "memory-test", nil
		},
		ExportSpans: func(_ context.Context, turn agenttrace.Turn, _ string) (int, error) {
			spanCalls++
			if !turn.Completed || turn.InputText() == "" || turn.OutputText() == "" {
				t.Errorf("baseline case %s exported an incomplete projection: completed=%t input_bytes=%d output_bytes=%d", spec.Name, turn.Completed, len(turn.InputText()), len(turn.OutputText()))
			}
			if spec.ExpectedTraceID != "" && turn.TraceID != spec.ExpectedTraceID {
				t.Errorf("baseline case %s span trace=%q, want %q", spec.Name, turn.TraceID, spec.ExpectedTraceID)
			}
			if spec.Name == "U100" && len(turn.InputText()) < spec.MinInputBytes {
				t.Errorf("U100 selected input was truncated: bytes=%d", len(turn.InputText()))
			}
			if spec.Name == "T100" && (len(turn.Observations) != spec.ExpectedObsPerTurn || len(turn.InputText()) < spec.MinInputBytes) {
				t.Errorf("T100 projection changed: input_bytes=%d observations=%d", len(turn.InputText()), len(turn.Observations))
			}
			if spec.Name == "R16" && (len(turn.Observations) == 0 || len(turn.Observations[0].Output) < spec.MinOutputBytes) {
				t.Errorf("R16 large record was truncated: observations=%d", len(turn.Observations))
			}
			return 200, nil
		},
		ExportScores: func(_ context.Context, turn agenttrace.Turn, environment string) error {
			scoreCalls++
			if spec.ExpectedTraceID != "" && turn.TraceID != spec.ExpectedTraceID {
				t.Errorf("baseline case %s score trace=%q, want %q", spec.Name, turn.TraceID, spec.ExpectedTraceID)
			}
			if spec.Name == "S100" && environment != "persisted-score-environment" {
				t.Errorf("S100 environment=%q", environment)
			}
			return nil
		},
	}
	scannedState, exported, err := ScanOnce(context.Background(), opts, *state)
	if err != nil {
		t.Fatalf("baseline scan case %s: %v", spec.Name, err)
	}
	if spanCalls != spec.ExpectedSpanCalls || scoreCalls != spec.ExpectedScoreCalls || exported != spec.ExpectedSpanCalls {
		t.Fatalf("baseline case %s callbacks: spans=%d scores=%d exported=%d; want %d/%d/%d", spec.Name, spanCalls, scoreCalls, exported, spec.ExpectedSpanCalls, spec.ExpectedScoreCalls, spec.ExpectedSpanCalls)
	}
	wantWatermark := now.UnixNano()
	if spec.CorruptSibling {
		wantWatermark = now.Add(-time.Minute).UnixNano()
	}
	if scannedState.ScanWatermarkNS != wantWatermark {
		t.Fatalf("baseline case %s watermark=%d, want %d", spec.Name, scannedState.ScanWatermarkNS, wantWatermark)
	}
	if spec.CorruptSibling {
		second := opts
		second.Now = now.Add(time.Second)
		scannedState, exported, err = ScanOnce(context.Background(), second, scannedState)
		if err != nil {
			t.Fatalf("baseline C100 second scan: %v", err)
		}
		if exported != 0 || spanCalls != spec.ExpectedSpanCalls || scoreCalls != spec.ExpectedScoreCalls || scannedState.ScanWatermarkNS != wantWatermark {
			t.Fatalf("baseline C100 repeated scan: exported=%d spans=%d scores=%d watermark=%d", exported, spanCalls, scoreCalls, scannedState.ScanWatermarkNS)
		}
	}
	t.Logf("memory_case name=%s scans=%d fixture_source_turns=%d fixture_selected_turns=%d fixture_processed_turns=%d spans=%d scores=%d processed_state=%d pending_scores=%d parse_visits=unavailable_on_baseline", spec.Name, 1+boolToInt(spec.CorruptSibling), spec.ExpectedSourceTurns, spec.ExpectedSelectedTurns, spec.ExpectedSourceTurns-spec.ExpectedSelectedTurns, spanCalls, scoreCalls, len(scannedState.ProcessedTraceIDs), len(scannedState.PendingScores))
}
