package test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kirilligum/codex-langfuse-tracer/internal/agenttrace"
)

type fixtureManifest struct {
	SchemaVersion int               `json:"schema_version"`
	Fixtures      []manifestFixture `json:"fixtures"`
}

type manifestFixture struct {
	ID           string   `json:"id"`
	Provider     string   `json:"provider"`
	Source       string   `json:"source"`
	SourceFormat string   `json:"source_format"`
	Golden       string   `json:"golden"`
	Categories   []string `json:"categories"`
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
		if fixture.Provider != "codex" && fixture.Provider != "claude" {
			t.Fatalf("fixture %s provider = %q, want codex or claude", fixture.ID, fixture.Provider)
		}
		if fixture.Source == "" {
			t.Fatalf("fixture %s source is required", fixture.ID)
		}
		if fixture.SourceFormat == "" {
			t.Fatalf("fixture %s source_format is required", fixture.ID)
		}
		if strings.Contains(fixture.Source, filepath.ToSlash(filepath.Join("testdata", "rollouts"))+"/") {
			t.Fatalf("fixture %s source uses legacy rollout path %s", fixture.ID, fixture.Source)
		}
		if !strings.HasPrefix(fixture.Source, "testdata/sources/"+fixture.Provider+"/") {
			t.Fatalf("fixture %s source %s must live under testdata/sources/%s", fixture.ID, fixture.Source, fixture.Provider)
		}
		for _, category := range fixture.Categories {
			categories[category] = true
		}

		sourcePath := filepath.Join("..", fixture.Source)
		if _, err := os.Stat(sourcePath); err != nil {
			t.Fatalf("fixture %s source missing: %v", fixture.ID, err)
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
		"command_tool",
		"file_change_metadata",
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

	manifestFiles := 0
	if err := filepath.WalkDir(filepath.Join("..", "testdata"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && filepath.Base(path) == "manifest.json" {
			manifestFiles++
		}
		return nil
	}); err != nil {
		t.Fatalf("scan manifests: %v", err)
	}
	if manifestFiles != 1 {
		t.Fatalf("manifest file count = %d, want 1", manifestFiles)
	}
}

// TEST-501
func TestFixtureManifestProviderSources(t *testing.T) {
	t.Parallel()
	validateGoldenFixtures(t)
}

// EVAL-001
func TestEvalClaudeFixtureCoverage(t *testing.T) {
	t.Parallel()

	manifest := loadManifestForFixtures(t)
	claudeCount := 0
	realDerivedCount := 0
	requiredCategories := map[string]bool{
		"no_tools":                      false,
		"claude_command_tool":           false,
		"claude_file_change_tool":       false,
		"claude_mcp_tool":               false,
		"claude_generic_tool":           false,
		"incomplete_turn":               false,
		"corrupt_transcript":            false,
		"hidden_reasoning_exclusion":    false,
		"claude_live_metadata":          false,
		"claude_real_derived_structure": false,
	}
	providerCoverage := map[string]bool{}
	sourceFormatCoverage := map[string]bool{}
	for _, fixture := range manifest.Fixtures {
		providerCoverage[fixture.Provider] = true
		sourceFormatCoverage[fixture.SourceFormat] = true
		if fixture.Provider != "claude" {
			continue
		}
		claudeCount++
		for _, category := range fixture.Categories {
			if category == "claude_real_derived_structure" {
				realDerivedCount++
			}
			if _, ok := requiredCategories[category]; ok {
				requiredCategories[category] = true
			}
		}
	}
	if claudeCount < 5 {
		t.Fatalf("claude fixture count = %d, want >= 5", claudeCount)
	}
	if realDerivedCount < 2 {
		t.Fatalf("claude real-derived structure fixture count = %d, want >= 2", realDerivedCount)
	}
	for category, covered := range requiredCategories {
		if !covered {
			t.Fatalf("claude fixture coverage missing category %q", category)
		}
	}
	for _, provider := range []string{"codex", "claude"} {
		if !providerCoverage[provider] {
			t.Fatalf("provider coverage missing %q", provider)
		}
	}
	for _, sourceFormat := range []string{"codex_rollout", "claude_transcript"} {
		if !sourceFormatCoverage[sourceFormat] {
			t.Fatalf("source format coverage missing %q", sourceFormat)
		}
	}
}

func loadManifestForFixtures(t *testing.T) fixtureManifest {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "testdata", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest fixtureManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	return manifest
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
		"changed_file_count",
		"command_tool_count",
		"file_change_tool_count",
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

	commandMetadata := requireObservationMetadata(t, golden, agenttrace.ToolObservationName(agenttrace.ProviderCodex, agenttrace.ToolFamilyCommand))
	for _, key := range []string{"tool_name", "command_kind", "status", "exit_code", "duration_ms", "failure_type"} {
		if _, ok := commandMetadata[key]; !ok {
			t.Fatalf("command metadata missing %q in %#v", key, commandMetadata)
		}
	}
	requireNoRawTransportOrDuration(t, golden)
}

func validateSingleRepresentationFixtureCoverage(t *testing.T) {
	t.Helper()
	golden := completeToolsGolden(t)
	metadata := requireMap(t, golden, "metadata")
	want := map[string]any{
		"other_command_count":    float64(1),
		"search_command_count":   float64(0),
		"read_command_count":     float64(0),
		"network_command_count":  float64(0),
		"install_command_count":  float64(0),
		"file_change_tool_count": float64(1),
		"command_tool_count":     float64(1),
		"web_search_tool_count":  float64(1),
		"mcp_tool_count":         float64(1),
		"tool_search_tool_count": float64(1),
		"navigation":             "command:other files:changed tool:command tool:file_change tool:mcp tool:tool_search tool:web_search verification:not_run",
	}
	for key, value := range want {
		if canonicalJSON(metadata[key]) != canonicalJSON(value) {
			t.Fatalf("metadata[%s] = %s want %s\nmetadata=%s", key, canonicalJSON(metadata[key]), canonicalJSON(value), canonicalJSON(metadata))
		}
	}
	for _, kind := range []string{"test", "build", "lint", "format", "git", "systemd"} {
		countKey := kind + "_command_count"
		if canonicalJSON(metadata[countKey]) != "0" {
			t.Fatalf("%s = %s, want 0", countKey, canonicalJSON(metadata[countKey]))
		}
	}
	requireNoForbiddenContractKeys(t, golden)
	requireNoRawTransportOrDuration(t, golden)
	commandMetadata := requireObservationMetadata(t, golden, agenttrace.ToolObservationName(agenttrace.ProviderCodex, agenttrace.ToolFamilyCommand))
	if commandMetadata["command_kind"] != "other" {
		t.Fatalf("command_kind = %#v, want other", commandMetadata["command_kind"])
	}
	if _, ok := commandMetadata["duration_ms"]; !ok {
		t.Fatalf("command metadata missing duration_ms: %#v", commandMetadata)
	}
	fileChangeMetadata := requireObservationMetadata(t, golden, agenttrace.ToolObservationName(agenttrace.ProviderCodex, agenttrace.ToolFamilyFileChange))
	if _, ok := fileChangeMetadata["changed_files"]; !ok {
		t.Fatalf("file-change metadata missing changed_files: %#v", fileChangeMetadata)
	}
}

func validateGoldenLangfuseTagsContract(t *testing.T) {
	t.Helper()
	complete := completeToolsGolden(t)
	tags, ok := complete["tags"].([]any)
	if !ok {
		t.Fatalf("complete-tools tags = %#v, want array", complete["tags"])
	}
	want := []string{
		"command:other",
		"files:changed",
		"mcp:github",
		"tool:command",
		"tool:file_change",
		"tool:mcp",
		"tool:tool_search",
		"tool:web_search",
		"verification:not_run",
	}
	if canonicalJSON(tags) != canonicalJSON(want) {
		t.Fatalf("complete-tools tags = %s want %s", canonicalJSON(tags), canonicalJSON(want))
	}
	raw := canonicalJSON(tags)
	for _, forbidden := range []string{"issues/list", "/tmp/", "sess-complete", "turn-1", "sk-lf-", "ghp_"} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("tags contain forbidden substring %q in %s", forbidden, raw)
		}
	}

	noToolsRaw, err := os.ReadFile(filepath.Join("..", "testdata", "golden", "complete-no-tools.normalized.json"))
	if err != nil {
		t.Fatal(err)
	}
	var noTools map[string]any
	if err := json.Unmarshal(noToolsRaw, &noTools); err != nil {
		t.Fatal(err)
	}
	noToolTags, ok := noTools["tags"].([]any)
	if !ok {
		t.Fatalf("complete-no-tools tags = %#v, want array", noTools["tags"])
	}
	for _, rawTag := range noToolTags {
		tag, ok := rawTag.(string)
		if !ok {
			t.Fatalf("tag = %#v, want string", rawTag)
		}
		if strings.HasPrefix(tag, "mcp:") || tag == "tool:mcp" {
			t.Fatalf("no-tools fixture has MCP tag in %#v", noTools["tags"])
		}
	}
}

func requireNoForbiddenContractKeys(t *testing.T, value any) {
	t.Helper()
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if isForbiddenContractKey(key) {
				t.Fatalf("golden contains forbidden projection key %q in %s", key, canonicalJSON(value))
			}
			requireNoForbiddenContractKeys(t, child)
		}
	case []any:
		for _, child := range typed {
			requireNoForbiddenContractKeys(t, child)
		}
	}
}

func isForbiddenContractKey(key string) bool {
	switch key {
	case "has_file_changes", "is_read_only", "command_kinds", "web_search_count", "trace_facets", "navigation_facets", "cost_details", "available_tool_names":
		return true
	default:
		return strings.HasPrefix(key, "ran_") || strings.HasPrefix(key, "used_")
	}
}

// TEST-101
func TestGoldenInsightMetadataSchema(t *testing.T) {
	t.Parallel()
	validateInsightFixtureCoverage(t)
}

// TEST-304
func TestGoldenLangfuseSingleRepresentation(t *testing.T) {
	t.Parallel()
	validateSingleRepresentationFixtureCoverage(t)
}

// TEST-403
func TestGoldenLangfuseTagsContract(t *testing.T) {
	t.Parallel()
	validateGoldenLangfuseTagsContract(t)
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

// EVAL-403
func TestEvalGoldenFixtureCoverageForLangfuseTags(t *testing.T) {
	t.Parallel()
	validateGoldenLangfuseTagsContract(t)
}

// EVAL-009
func TestEvalGoldenFixtureCoverage(t *testing.T) {
	t.Parallel()
	validateGoldenFixtures(t)
}
