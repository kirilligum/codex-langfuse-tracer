package exportstate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TEST-010
func TestStateLoadSaveAndDedupe(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "state.json")
	state, err := Load(path)
	if err != nil {
		t.Fatalf("LoadState missing: %v", err)
	}
	if state != nil {
		t.Fatalf("missing state = %+v, want nil", state)
	}

	want := State{Version: 1, ScanWatermarkNS: 42, ProcessedTraceIDs: []string{"b", "a", "a"}}
	if err := Save(path, want); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if got.Version != 1 || got.ScanWatermarkNS != 42 {
		t.Fatalf("state scalar mismatch: %+v", got)
	}
	if got.HasProcessed("missing") || !got.HasProcessed("a") || !got.HasProcessed("b") {
		t.Fatalf("dedupe lookup failed: %+v", got.ProcessedTraceIDs)
	}

	if err := os.WriteFile(path, []byte(`{"version":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("LoadState accepted unsupported version")
	}
}

// TEST-602
func TestTurnProgressLifecycle(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("{\"version\":1,\"scan_watermark_ns\":42}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	state, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if state == nil || state.TurnProgress == nil || len(state.TurnProgress) != 0 {
		t.Fatalf("missing turn_progress did not normalize to an empty map: %+v", state)
	}

	traceID := "progress-trace"
	state.SetProgress(traceID, TurnProgress{ExportedObservationCount: 2, FinalSpansExported: true})
	if err := Save(path, *state); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.ProgressFor(traceID); got.ExportedObservationCount != 2 || !got.FinalSpansExported {
		t.Fatalf("loaded progress = %+v", got)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	if _, ok := document["turn_progress"]; !ok {
		t.Fatalf("serialized state omits turn_progress: %s", raw)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("state mode = %o, want 600", got)
	}

	loaded.AddProcessed(traceID)
	if _, ok := loaded.TurnProgress[traceID]; ok {
		t.Fatalf("processed trace retained progress: %+v", loaded)
	}
}
