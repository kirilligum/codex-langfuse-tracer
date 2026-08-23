package exportstate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TEST-703
func TestVersion3State(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "state.json")
	state := State{
		ScanWatermarkNS:   42,
		ProcessedTraceIDs: []string{"b", "a", "a"},
		PendingScores:     map[string]string{"score-trace": "repository--feature-one-a1b2c3"},
	}
	if err := Save(path, state); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Version != Version || got.ScanWatermarkNS != 42 {
		t.Fatalf("state scalar mismatch: %+v", got)
	}
	if got.HasProcessed("missing") || !got.HasProcessed("a") || !got.HasProcessed("b") {
		t.Fatalf("dedupe lookup failed: %+v", got.ProcessedTraceIDs)
	}
	if environment := got.PendingScoreEnvironment("score-trace"); environment != "repository--feature-one-a1b2c3" {
		t.Fatalf("pending score environment = %q", environment)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	if document["version"] != float64(Version) {
		t.Fatalf("serialized version = %#v", document["version"])
	}
	if info, err := os.Stat(path); err != nil {
		t.Fatal(err)
	} else if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("state mode = %o, want 600", mode)
	}

	updated, err := Update(path, func(current *State) error {
		current.SetPendingScore("atomic-trace", "repository--main-b2c3d4")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if environment := updated.PendingScoreEnvironment("atomic-trace"); environment != "repository--main-b2c3d4" {
		t.Fatalf("atomic pending environment = %q", environment)
	}
	updated.AddProcessed("atomic-trace")
	if updated.PendingScoreEnvironment("atomic-trace") != "" {
		t.Fatalf("processed trace retained pending score: %+v", updated)
	}

	if err := os.WriteFile(path, []byte(`{"version":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "unsupported watch state version in "+path) {
		t.Fatalf("version 2 error = %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"version":4}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "unsupported watch state version in "+path) {
		t.Fatalf("version 4 error = %v", err)
	}
	if err := Save(path, State{Version: 2}); err == nil || !strings.Contains(err.Error(), "unsupported watch state version in "+path) {
		t.Fatalf("version 2 save error = %v", err)
	}
}
