package exportstate

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/kirilligum/codex-langfuse-tracer/internal/agenttrace"
)

// TEST-507
func TestExportStateQueueDedupe(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "state.json")
	enqueuedAt := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	req := QueueRequest{
		Provider:   agenttrace.ProviderClaude,
		SourcePath: "/tmp/claude.jsonl",
		SessionID:  "claude-session",
		CWD:        "/tmp/project",
		EnqueuedAt: enqueuedAt.Format(time.RFC3339Nano),
	}
	if err := Enqueue(context.Background(), path, req); err != nil {
		t.Fatalf("Enqueue first: %v", err)
	}
	if err := Enqueue(context.Background(), path, req); err != nil {
		t.Fatalf("Enqueue duplicate: %v", err)
	}
	state, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if state == nil || len(state.Queue) != 1 {
		t.Fatalf("queue = %+v, want one request", state)
	}
	if state.ScanWatermarkNS != enqueuedAt.UnixNano() {
		t.Fatalf("scan watermark = %d, want %d", state.ScanWatermarkNS, enqueuedAt.UnixNano())
	}
	state.AddProcessed(agenttrace.StableTraceID(agenttrace.ProviderClaude, "session", "turn"))
	if err := Save(context.Background(), path, *state); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load saved: %v", err)
	}
	if !loaded.HasProcessed(agenttrace.StableTraceID(agenttrace.ProviderClaude, "session", "turn")) {
		t.Fatalf("processed trace not persisted: %+v", loaded)
	}
}

// TEST-602
func TestStateUpdatePreservesQueue(t *testing.T) {
	t.Parallel()
	// TEST-703

	path := filepath.Join(t.TempDir(), "state.json")
	if err := Save(context.Background(), path, State{Version: Version, ScanWatermarkNS: 10}); err != nil {
		t.Fatal(err)
	}
	stale, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	request := QueueRequest{
		Provider:   agenttrace.ProviderClaude,
		SourcePath: "/tmp/queued-while-watching.jsonl",
		EnqueuedAt: time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano),
	}
	if err := Enqueue(context.Background(), path, request); err != nil {
		t.Fatal(err)
	}

	stale.SetPendingScore("trace-progress", "stale--main-a1b2c3")
	updated, err := Update(context.Background(), path, func(current *State) error {
		current.SetPendingScore("trace-progress", "repository--main-b2c3d4")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Queue) != 1 || updated.Queue[0].SourcePath != request.SourcePath {
		t.Fatalf("atomic update lost queue: %+v", updated)
	}
	if got := updated.PendingScoreEnvironment("trace-progress"); got != "repository--main-b2c3d4" {
		t.Fatalf("pending score environment = %q", got)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Queue) != 1 || loaded.PendingScoreEnvironment("trace-progress") != "repository--main-b2c3d4" {
		t.Fatalf("persisted state = %+v", loaded)
	}
}
