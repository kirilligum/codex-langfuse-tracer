package exportstate

import (
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
	if err := Enqueue(path, req); err != nil {
		t.Fatalf("Enqueue first: %v", err)
	}
	if err := Enqueue(path, req); err != nil {
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
	if err := Save(path, *state); err != nil {
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
