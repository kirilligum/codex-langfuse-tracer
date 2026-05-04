package codextrace

import (
	"path/filepath"
	"strings"
	"testing"
)

// TEST-012
func TestManualParseErrorsIncludePathAndLine(t *testing.T) {
	t.Parallel()

	path := filepath.Join("..", "..", "testdata", "sources", "codex", "corrupt-rollout.jsonl")
	_, err := ParseTurns(path)
	if err == nil {
		t.Fatal("ParseTurns(corrupt) succeeded, want error")
	}
	if !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), ":3") {
		t.Fatalf("error lacks path and line number: %v", err)
	}
}
