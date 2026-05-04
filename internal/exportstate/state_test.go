package exportstate

import (
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
