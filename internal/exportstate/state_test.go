package exportstate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TEST-703
func TestVersion2State(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "state.json")
	state, err := Load(path)
	if err != nil {
		t.Fatalf("Load missing: %v", err)
	}
	if state != nil {
		t.Fatalf("missing state = %+v, want nil", state)
	}

	traceID := "progress-trace"
	want := State{
		ScanWatermarkNS:   42,
		ProcessedTraceIDs: []string{"b", "a", "a"},
		TurnProgress: map[string]TurnProgress{
			traceID: {
				ExportedObservationCount: 2,
				FinalSpansExported:       true,
				Environment:              "repository--feature-one-a1b2c3",
			},
		},
	}
	if err := Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Version != 2 || got.ScanWatermarkNS != 42 {
		t.Fatalf("state scalar mismatch: %+v", got)
	}
	if got.HasProcessed("missing") || !got.HasProcessed("a") || !got.HasProcessed("b") {
		t.Fatalf("dedupe lookup failed: %+v", got.ProcessedTraceIDs)
	}
	if progress := got.ProgressFor(traceID); progress.ExportedObservationCount != 2 || !progress.FinalSpansExported || progress.Environment != "repository--feature-one-a1b2c3" {
		t.Fatalf("loaded progress = %+v", progress)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	if document["version"] != float64(2) {
		t.Fatalf("serialized version = %#v", document["version"])
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("state mode = %o, want 600", mode)
	}

	updated, err := Update(path, func(current *State) error {
		current.SetProgress("atomic-trace", TurnProgress{
			ExportedObservationCount: 1,
			Environment:              "repository--main-b2c3d4",
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if progress := updated.ProgressFor("atomic-trace"); progress.Environment != "repository--main-b2c3d4" {
		t.Fatalf("atomic progress = %+v", progress)
	}

	updated.AddProcessed(traceID)
	if err := Save(path, updated); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded.TurnProgress[traceID]; ok {
		t.Fatalf("processed trace retained progress: %+v", loaded)
	}

	if err := os.WriteFile(path, []byte(`{"version":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "unsupported watch state version in "+path) {
		t.Fatalf("version 1 error = %v", err)
	}

	if err := os.WriteFile(path, []byte(`{"version":2,"turn_progress":{"trace":{"exported_observation_count":1}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "requires environment") {
		t.Fatalf("missing environment load error = %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"version":2,"processed_trace_ids":["trace"],"turn_progress":{"trace":{"exported_observation_count":1}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "requires environment") {
		t.Fatalf("processed trace with invalid progress error = %v", err)
	}
	if err := Save(path, State{TurnProgress: map[string]TurnProgress{"trace": {ExportedObservationCount: 1}}}); err == nil || !strings.Contains(err.Error(), "requires environment") {
		t.Fatalf("missing environment save error = %v", err)
	}
}
