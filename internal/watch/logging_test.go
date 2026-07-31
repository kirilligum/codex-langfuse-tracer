package watch

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/kirilligum/codex-langfuse-tracer/internal/agenttrace"
	"github.com/kirilligum/codex-langfuse-tracer/internal/exportstate"
)

// TEST-017
// TEST-604
func TestWatchLogs(t *testing.T) {
	t.Parallel()

	root, statePath, rolloutPath := watchFixture(t)
	now := time.Date(2026, 5, 1, 10, 1, 0, 0, time.UTC)
	if err := os.Chtimes(rolloutPath, now.Add(-30*time.Second), now.Add(-30*time.Second)); err != nil {
		t.Fatal(err)
	}
	state := exportstate.State{Version: 1, ScanWatermarkNS: now.Add(-2 * time.Minute).UnixNano()}
	if err := exportstate.Save(statePath, state); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	_, _, err := ScanOnce(context.Background(), ScanOptions{
		Root:      root,
		StatePath: statePath,
		Now:       now,
		Stdout:    &stdout,
		Stderr:    &stderr,
		ExportSpans: func(context.Context, agenttrace.Turn, int, bool) (int, error) {
			return 201, nil
		},
		ExportScores: successfulScores,
	}, state)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(stdout.Bytes(), []byte("exported trace=1e087e4ea8aa8d8e29e604d2cd8704d9 observations=0:")) ||
		!bytes.Contains(stdout.Bytes(), []byte("final=true status=201 path=")) {
		t.Fatalf("success log missing: %s", stdout.String())
	}

	state = exportstate.State{Version: 1, ScanWatermarkNS: now.Add(-2 * time.Minute).UnixNano()}
	stdout.Reset()
	stderr.Reset()
	if err := exportstate.Save(statePath, state); err != nil {
		t.Fatal(err)
	}
	_, _, err = ScanOnce(context.Background(), ScanOptions{
		Root:      root,
		StatePath: statePath,
		Now:       now,
		Stdout:    &stdout,
		Stderr:    &stderr,
		ExportSpans: func(context.Context, agenttrace.Turn, int, bool) (int, error) {
			return 0, errors.New("export failed")
		},
		ExportScores: successfulScores,
	}, state)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(stderr.Bytes(), []byte("ERROR: failed to export trace=1e087e4ea8aa8d8e29e604d2cd8704d9")) {
		t.Fatalf("failure log missing: %s", stderr.String())
	}
	logs := stdout.String() + stderr.String()
	for _, secretContent := range []string{"Summarize the repo", "Checks passed", "sk-lf-live-secret"} {
		if bytes.Contains([]byte(logs), []byte(secretContent)) {
			t.Fatalf("watch logs leaked %q: %s", secretContent, logs)
		}
	}
}
