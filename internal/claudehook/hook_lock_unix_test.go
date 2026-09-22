//go:build unix

package claudehook

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/kirilligum/codex-langfuse-tracer/internal/exportstate"
)

func TestClaudeHookLockTimeoutIsNotAcknowledged(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	lockPath := statePath + ".lock"
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer lockFile.Close()
	if err := unix.Flock(int(lockFile.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatalf("hold export state lock: %v", err)
	}
	defer unix.Flock(int(lockFile.Fd()), unix.LOCK_UN)

	payload := `{"session_id":"claude-session","transcript_path":"/tmp/claude.jsonl","hook_event_name":"Stop"}`
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	enqueued, err := Handle(ctx, bytes.NewBufferString(payload), statePath, time.Now())
	if enqueued || !errors.Is(err, exportstate.ErrLockBusy) {
		t.Fatalf("Handle = (%v, %v), want false and lock contention", enqueued, err)
	}
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Fatalf("state after rejected hook = err %v, want no state file", err)
	}
	if err := unix.Flock(int(lockFile.Fd()), unix.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	enqueued, err = Handle(context.Background(), bytes.NewBufferString(payload), statePath, time.Now())
	if err != nil || !enqueued {
		t.Fatalf("hook retry after release = (%v, %v), want committed acknowledgement", enqueued, err)
	}
	state, err := exportstate.Load(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if state == nil || len(state.Queue) != 1 {
		t.Fatalf("hook retry queue = %+v, want one deduplicated request", state)
	}
}
