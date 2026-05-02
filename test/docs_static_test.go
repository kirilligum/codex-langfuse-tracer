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
		"codex_insight.navigation",
		"files:read_only",
		"files:changed",
		"command:search",
		"command:read",
		"command:network",
		"command:install",
		"tool:web_search",
		"verification:failed",
		"langfuse.observation.model.name",
		"langfuse.observation.usage_details",
		"cost_details",
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
		"TestInsightCountMetadataSingleRepresentation",
		"TestGoldenLangfuseSingleRepresentation",
		"TestCountMetadataExportedOnAgent",
		"TestDocsNavigationFacetsAndSavedViews",
	} {
		if !strings.Contains(testingDoc, required) {
			t.Fatalf("TESTING missing %q", required)
		}
	}
}

// TEST-405
func TestDocsTagsAndMCPUsage(t *testing.T) {
	t.Parallel()

	readme := readRepoDoc(t, "README.md")
	testingDoc := readRepoDoc(t, "TESTING.md")
	installScript := readRepoDoc(t, "install.sh")
	for _, required := range []string{
		"langfuse.trace.tags",
		"codex_insight.navigation values plus observed mcp:<server>",
		"mcp_server",
		"mcp_tool",
		"codex.tool.mcp",
		"issues/list",
		"future watcher exports",
		"explicit re-export",
		"codex-langfuse-watch.service",
		"~/.codex/bin/codex-langfuse-exporter --path",
	} {
		if !strings.Contains(readme, required) {
			t.Fatalf("README missing %q", required)
		}
	}
	for _, required := range []string{
		"TestDocsTagsAndMCPUsage",
		"TestLangfuseTraceTagsExportedOnSpans",
		"TestGoldenLangfuseTagsContract",
		"TestInsightTagFacets",
	} {
		if !strings.Contains(testingDoc, required) {
			t.Fatalf("TESTING missing %q", required)
		}
	}
	if !strings.Contains(installScript, "codex-langfuse-watch.service") {
		t.Fatalf("install.sh missing service restart")
	}
}

// TEST-408
func TestDocsLangfuseCostPricing(t *testing.T) {
	t.Parallel()

	readme := readRepoDoc(t, "README.md")
	for _, required := range []string{
		"--sync-model-pricing",
		"https://openai.com/api/pricing/",
		"2026-05-02",
		"gpt-5.5",
		"gpt-5.4",
		"gpt-5.4-mini",
		"input_cached_tokens",
		"output_reasoning_tokens",
		"When OpenAI pricing changes",
		"internal/langfuse/models.go",
		"Do not add fallback local cost multiplication",
		"install.sh",
		"explicit re-export",
		"~/.codex/bin/codex-langfuse-exporter --session-id",
	} {
		if !strings.Contains(readme, required) {
			t.Fatalf("README missing %q", required)
		}
	}
}

// TEST-409
func TestNoLocalCostDetailsOrDirectIngestionShortcut(t *testing.T) {
	t.Parallel()

	for _, pattern := range []string{
		filepath.Join("..", "internal", "langfuse", "*.go"),
		filepath.Join("..", "cmd", "codex-langfuse-exporter", "*.go"),
	} {
		paths, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range paths {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			text := string(raw)
			if strings.Contains(text, "cost_details") {
				t.Fatalf("%s emits local cost_details", path)
			}
			if strings.Contains(text, "/api/public/ingestion") {
				t.Fatalf("%s uses direct ingestion export path", path)
			}
		}
	}
	if !strings.Contains(readRepoDoc(t, "README.md"), "/api/public/otel/v1/traces") {
		t.Fatal("README must keep OTLP as the trace export path")
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
