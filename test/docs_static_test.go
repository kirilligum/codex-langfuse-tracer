package test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TEST-014
func TestDocsAndRuntimeDoNotReferencePythonExporter(t *testing.T) {
	t.Parallel()

	if _, err := os.Stat(filepath.Join("..", "bin", "export_codex_session_to_langfuse.py")); !os.IsNotExist(err) {
		t.Fatalf("Python exporter still exists: %v", err)
	}
	for _, path := range []string{
		"README.md",
		"PROJECT_CONTEXT.md",
		"systemd/codex-langfuse-watch.service",
	} {
		raw, err := os.ReadFile(filepath.Join("..", path))
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		if strings.Contains(text, "export_codex_session_to_langfuse.py") || strings.Contains(text, "python3 -m py_compile") {
			t.Fatalf("%s still references Python exporter", path)
		}
	}
}

// EVAL-007
func TestEvalDocsTraceContractCompleteness(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, required := range []string{
		"codex-langfuse-exporter",
		"codex.agent",
		"codex.transcript",
		"codex.terminal",
		"codex.tool.apply_patch",
		"systemd --user",
		"go test ./...",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("README missing %q", required)
		}
	}
}
