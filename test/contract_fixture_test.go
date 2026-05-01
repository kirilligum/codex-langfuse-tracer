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
	}
	for _, category := range required {
		if !categories[category] {
			t.Fatalf("manifest missing category %q", category)
		}
	}
}

// TEST-020
func TestGoldenFixturesAreLanguageAgnostic(t *testing.T) {
	t.Parallel()
	validateGoldenFixtures(t)
}

// EVAL-009
func TestEvalGoldenFixtureCoverage(t *testing.T) {
	t.Parallel()
	validateGoldenFixtures(t)
}
