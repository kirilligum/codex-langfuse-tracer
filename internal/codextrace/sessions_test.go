package codextrace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSessionPathSelection(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	sessionDir := filepath.Join(root, "sessions", "2026", "05", "01")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	first := filepath.Join(sessionDir, "rollout-2026-05-01T10-00-00-abc123.jsonl")
	second := filepath.Join(sessionDir, "rollout-2026-05-01T10-01-00-def456.jsonl")
	if err := os.WriteFile(first, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	found, err := FindSessionByID("abc123", root)
	if err != nil {
		t.Fatalf("FindSessionByID: %v", err)
	}
	if found != first {
		t.Fatalf("found = %q, want %q", found, first)
	}

	latest, err := LatestSession(root)
	if err != nil {
		t.Fatalf("LatestSession: %v", err)
	}
	if latest != second && latest != first {
		t.Fatalf("latest = %q", latest)
	}
}
