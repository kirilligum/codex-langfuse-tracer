package test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fixtureManifest struct {
	SchemaVersion int               `json:"schema_version"`
	Fixtures      []manifestFixture `json:"fixtures"`
}

type manifestFixture struct {
	ID         string   `json:"id"`
	Rollout    string   `json:"rollout"`
	Golden     string   `json:"golden"`
	Categories []string `json:"categories"`
}

func validateGoldenFixtures(t *testing.T) {
	t.Helper()
	manifestPath := filepath.Join("..", "testdata", "manifest.json")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}

	var manifest fixtureManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	if manifest.SchemaVersion != 1 {
		t.Fatalf("manifest schema_version = %d, want 1", manifest.SchemaVersion)
	}
	if len(manifest.Fixtures) == 0 {
		t.Fatal("manifest must define fixtures")
	}

	categories := map[string]bool{}
	seenIDs := map[string]bool{}
	for _, fixture := range manifest.Fixtures {
		if fixture.ID == "" {
			t.Fatal("fixture id is required")
		}
		if seenIDs[fixture.ID] {
			t.Fatalf("duplicate fixture id %q", fixture.ID)
		}
		seenIDs[fixture.ID] = true
		for _, category := range fixture.Categories {
			categories[category] = true
		}

		rolloutPath := filepath.Join("..", fixture.Rollout)
		if _, err := os.Stat(rolloutPath); err != nil {
			t.Fatalf("fixture %s rollout missing: %v", fixture.ID, err)
		}

		goldenPath := filepath.Join("..", fixture.Golden)
		goldenRaw, err := os.ReadFile(goldenPath)
		if err != nil {
			t.Fatalf("fixture %s golden missing: %v", fixture.ID, err)
		}
		if strings.Contains(string(goldenRaw), "resourceSpans") || strings.Contains(string(goldenRaw), "scopeSpans") {
			t.Fatalf("fixture %s golden contains raw OTLP transport fields", fixture.ID)
		}
		if strings.Contains(string(goldenRaw), "sk-lf-live-secret") || strings.Contains(string(goldenRaw), "ghp_live_secret") {
			t.Fatalf("fixture %s golden contains unredacted secret sentinel", fixture.ID)
		}

		var golden struct {
			SchemaVersion int `json:"schema_version"`
		}
		if err := json.Unmarshal(goldenRaw, &golden); err != nil {
			t.Fatalf("fixture %s golden invalid JSON: %v", fixture.ID, err)
		}
		if golden.SchemaVersion != 1 {
			t.Fatalf("fixture %s schema_version = %d, want 1", fixture.ID, golden.SchemaVersion)
		}
	}

	required := []string{
		"completed_turn",
		"incomplete_turn",
		"trace_preview",
		"terminal_stream",
		"commentary",
		"visible_reasoning_summary",
		"hidden_reasoning_exclusion",
		"exec_tool",
		"apply_patch_metadata",
		"mcp_tool",
		"web_search",
		"tool_search",
		"redaction",
		"truncation",
		"corrupt_rollout",
		"state_dedupe",
		"no_tools",
		"multi_turn",
		"failed_command",
		"verification_metadata",
		"missing_optional_fields",
		"unknown_event",
		"web_search_contract",
		"response_item_content",
		"tool_output_redaction",
	}
	for _, category := range required {
		if !categories[category] {
			t.Fatalf("manifest missing category %q", category)
		}
	}
}

func completeToolsGolden(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "testdata", "golden", "complete-tools.normalized.json"))
	if err != nil {
		t.Fatal(err)
	}
	var golden map[string]any
	if err := json.Unmarshal(raw, &golden); err != nil {
		t.Fatal(err)
	}
	return golden
}

func requireMap(t *testing.T, parent map[string]any, key string) map[string]any {
	t.Helper()
	value, ok := parent[key].(map[string]any)
	if !ok {
		t.Fatalf("%s = %#v, want object", key, parent[key])
	}
	return value
}

func requireObservationMetadata(t *testing.T, golden map[string]any, name string) map[string]any {
	t.Helper()
	observations, ok := golden["observations"].([]any)
	if !ok {
		t.Fatalf("observations = %#v, want array", golden["observations"])
	}
	for _, raw := range observations {
		observation, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("observation = %#v, want object", raw)
		}
		if observation["name"] == name {
			return requireMap(t, observation, "metadata")
		}
	}
	t.Fatalf("missing observation %s", name)
	return nil
}

func requireNoRawTransportOrDuration(t *testing.T, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, forbidden := range []string{"resourceSpans", "scopeSpans", `"duration"`} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("golden contains forbidden field %s in %s", forbidden, text)
		}
	}
}

func validateInsightFixtureCoverage(t *testing.T) {
	t.Helper()
	golden := completeToolsGolden(t)
	metadata := requireMap(t, golden, "metadata")
	for _, key := range []string{
		"tool_count",
		"command_count",
		"failed_command_count",
		"patch_count",
		"changed_file_count",
		"verification_command_count",
		"verification_status",
		"changed_extensions",
		"touched_test_files",
	} {
		if _, ok := metadata[key]; !ok {
			t.Fatalf("root metadata missing %q in %#v", key, metadata)
		}
	}
	if _, ok := metadata["changed_files"]; ok {
		t.Fatalf("root metadata must not include full changed_files: %#v", metadata)
	}

	commandMetadata := requireObservationMetadata(t, golden, "codex.tool.exec_command")
	for _, key := range []string{"command_kind", "status", "exit_code", "duration_ms", "failure_type"} {
		if _, ok := commandMetadata[key]; !ok {
			t.Fatalf("command metadata missing %q in %#v", key, commandMetadata)
		}
	}
	requireNoRawTransportOrDuration(t, golden)
}

// TEST-101
func TestGoldenInsightMetadataSchema(t *testing.T) {
	t.Parallel()
	validateInsightFixtureCoverage(t)
}

// TEST-020
func TestGoldenFixturesAreLanguageAgnostic(t *testing.T) {
	t.Parallel()
	validateGoldenFixtures(t)
}

// EVAL-101
func TestEvalInsightFixtureCoverage(t *testing.T) {
	t.Parallel()
	validateInsightFixtureCoverage(t)
}

// EVAL-009
func TestEvalGoldenFixtureCoverage(t *testing.T) {
	t.Parallel()
	validateGoldenFixtures(t)
}
