package claudehook

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/kirilligum/codex-langfuse-tracer/internal/agenttrace"
	"github.com/kirilligum/codex-langfuse-tracer/internal/exportstate"
)

type Input struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	CWD            string `json:"cwd"`
	HookEventName  string `json:"hook_event_name"`
}

func Handle(input io.Reader, statePath string, now time.Time) (bool, error) {
	var hook Input
	if err := json.NewDecoder(input).Decode(&hook); err != nil {
		return false, fmt.Errorf("invalid Claude hook JSON: %w", err)
	}
	if hook.HookEventName != "Stop" {
		return false, nil
	}
	if hook.TranscriptPath == "" {
		return false, fmt.Errorf("Claude Stop hook missing transcript_path")
	}
	if hook.SessionID == "" {
		return false, fmt.Errorf("Claude Stop hook missing session_id")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return true, exportstate.Enqueue(statePath, exportstate.QueueRequest{
		Provider:   agenttrace.ProviderClaude,
		SourcePath: hook.TranscriptPath,
		SessionID:  hook.SessionID,
		CWD:        hook.CWD,
		EnqueuedAt: now.UTC().Format(time.RFC3339Nano),
	})
}
