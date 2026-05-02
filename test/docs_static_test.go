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
		"AGENTS.md",
		"README.md",
		"TESTING.md",
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

// TEST-108
func TestDocsTraceInsightMetadata(t *testing.T) {
	t.Parallel()

	required := []string{
		"verification_status",
		"verification_command_count",
		"changed_file_count",
		"changed_extensions",
		"touched_test_files",
		"command_kind",
		"duration_ms",
		"failure_type",
		"hidden chain-of-thought",
	}
	for _, path := range []string{"README.md"} {
		raw, err := os.ReadFile(filepath.Join("..", path))
		if err != nil {
			t.Fatal(err)
		}
		text := strings.ToLower(string(raw))
		for _, value := range required {
			if !strings.Contains(text, value) {
				t.Fatalf("%s missing %q", path, value)
			}
		}
	}
}

// TEST-204
func TestDocsNavigationFacetsAndSavedViews(t *testing.T) {
	t.Parallel()

	readme := readRepoDoc(t, "README.md")
	testingDoc := readRepoDoc(t, "TESTING.md")
	for _, required := range []string{
		"has_file_changes",
		"is_read_only",
		"ran_search_command",
		"ran_read_command",
		"ran_network_command",
		"ran_install_command",
		"used_web_search",
		"command_kind",
		"Views -> Create Custom View",
		"Traces: read only",
		"Observations: command search",
	} {
		if !strings.Contains(readme, required) {
			t.Fatalf("README missing %q", required)
		}
	}
	for _, required := range []string{
		"no observed local file changes",
		"always-on",
		"trace filters",
		"observation filters",
	} {
		if !strings.Contains(strings.ToLower(readme), required) {
			t.Fatalf("README missing %q", required)
		}
	}
	for _, required := range []string{
		"TestInsightNavigationFacets",
		"TestGoldenNavigationFacetsMetadataSchema",
		"TestNavigationFacetsMetadataExportedOnAgent",
		"TestDocsNavigationFacetsAndSavedViews",
	} {
		if !strings.Contains(testingDoc, required) {
			t.Fatalf("TESTING missing %q", required)
		}
	}
}

func readRepoDoc(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", path))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
