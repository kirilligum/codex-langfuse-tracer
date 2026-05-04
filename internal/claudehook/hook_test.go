package claudehook

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kirilligum/codex-langfuse-tracer/internal/agenttrace"
	"github.com/kirilligum/codex-langfuse-tracer/internal/exportstate"
)

// TEST-507
func TestClaudeHookEnqueuesStopOnly(t *testing.T) {
	t.Parallel()

	statePath := filepath.Join(t.TempDir(), "state.json")
	now := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	stop := `{"session_id":"claude-session","transcript_path":"/tmp/claude.jsonl","cwd":"/tmp/project","hook_event_name":"Stop"}`
	enqueued, err := Handle(bytes.NewBufferString(stop), statePath, now)
	if err != nil {
		t.Fatalf("Handle Stop: %v", err)
	}
	if !enqueued {
		t.Fatal("Stop hook did not enqueue")
	}
	state, err := exportstate.Load(statePath)
	if err != nil {
		t.Fatalf("Load state: %v", err)
	}
	if state == nil || len(state.Queue) != 1 {
		t.Fatalf("queue = %+v, want one request", state)
	}
	req := state.Queue[0]
	if req.Provider != agenttrace.ProviderClaude || req.SourcePath != "/tmp/claude.jsonl" || req.SessionID != "claude-session" || req.CWD != "/tmp/project" {
		t.Fatalf("queued request = %+v", req)
	}

	notification := `{"session_id":"claude-session","transcript_path":"/tmp/claude.jsonl","cwd":"/tmp/project","hook_event_name":"Notification"}`
	enqueued, err = Handle(bytes.NewBufferString(notification), statePath, now)
	if err != nil {
		t.Fatalf("Handle Notification: %v", err)
	}
	if enqueued {
		t.Fatal("non-Stop hook enqueued")
	}
	state, err = exportstate.Load(statePath)
	if err != nil {
		t.Fatalf("Load state after notification: %v", err)
	}
	if len(state.Queue) != 1 {
		t.Fatalf("queue after notification = %+v", state.Queue)
	}
}

func TestClaudeHookRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	_, err := Handle(bytes.NewBufferString(`{"hook_event_name":"Stop"}`), filepath.Join(t.TempDir(), "state.json"), time.Now())
	if err == nil || !strings.Contains(err.Error(), "transcript_path") {
		t.Fatalf("missing transcript error = %v", err)
	}
}

func TestClaudeHookNoLangfuseOrConfigImports(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join("hook.go"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, forbidden := range []string{"/internal/langfuse", "/internal/config"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("hook imports forbidden runtime package %s", forbidden)
		}
	}
}
