package codextrace

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// TEST-102
func TestInsightCommandClassification(t *testing.T) {
	t.Parallel()
	validateInsightCommandClassification(t)
}

func validateInsightCommandClassification(t *testing.T) {
	t.Helper()
	cases := []struct {
		command string
		want    string
	}{
		{command: "go test ./...", want: "test"},
		{command: "go build ./cmd/codex-langfuse-exporter", want: "build"},
		{command: "golangci-lint run ./...", want: "lint"},
		{command: "gofmt -w internal/codextrace/insight.go", want: "format"},
		{command: "git status --short", want: "git"},
		{command: "sed -n '1,80p' README.md", want: "read"},
		{command: "rg -n TODO internal", want: "search"},
		{command: "npm install", want: "install"},
		{command: "systemctl --user status codex-langfuse-watch.service", want: "systemd"},
		{command: "curl -fsS https://example.com", want: "network"},
		{command: "printf 'ok\n'", want: "other"},
	}

	for _, tc := range cases {
		if got := ClassifyCommand(tc.command); got != tc.want {
			t.Fatalf("ClassifyCommand(%q) = %q, want %q", tc.command, got, tc.want)
		}
	}
}

// TEST-103
func TestInsightFailureMetadata(t *testing.T) {
	t.Parallel()
	validateInsightFailureMetadata(t)
}

func validateInsightFailureMetadata(t *testing.T) {
	t.Helper()
	cases := []struct {
		name           string
		payload        map[string]any
		wantKind       string
		wantFailure    string
		wantDurationMS any
	}{
		{
			name: "success",
			payload: map[string]any{
				"command":   []any{"bash", "-lc", "go test ./..."},
				"status":    "completed",
				"exit_code": 0,
				"duration":  map[string]any{"secs": 1, "nanos": 250_000_000},
			},
			wantKind:       "test",
			wantFailure:    "none",
			wantDurationMS: 1250,
		},
		{
			name: "nonzero exit",
			payload: map[string]any{
				"command":   []any{"bash", "-lc", "go test ./..."},
				"status":    "completed",
				"exit_code": 2,
				"duration":  map[string]any{"secs": 0, "nanos": 500_000_000},
			},
			wantKind:       "test",
			wantFailure:    "nonzero_exit",
			wantDurationMS: 500,
		},
		{
			name: "timeout",
			payload: map[string]any{
				"command": []any{"bash", "-lc", "go test ./..."},
				"status":  "timed_out",
			},
			wantKind:    "test",
			wantFailure: "timeout",
		},
		{
			name:        "missing fields",
			payload:     map[string]any{},
			wantKind:    "other",
			wantFailure: "unknown",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := CommandInsightMetadata(tc.payload)
			if got["command_kind"] != tc.wantKind {
				t.Fatalf("command_kind = %#v, want %q", got["command_kind"], tc.wantKind)
			}
			if got["failure_type"] != tc.wantFailure {
				t.Fatalf("failure_type = %#v, want %q", got["failure_type"], tc.wantFailure)
			}
			if tc.wantDurationMS != nil && got["duration_ms"] != tc.wantDurationMS {
				t.Fatalf("duration_ms = %#v, want %#v", got["duration_ms"], tc.wantDurationMS)
			}
			if _, ok := got["duration"]; ok {
				t.Fatalf("raw duration must not be exported: %#v", got)
			}
		})
	}
}

// EVAL-102
func TestEvalInsightClassifierCoverage(t *testing.T) {
	t.Parallel()
	validateInsightCommandClassification(t)
	validateInsightFailureMetadata(t)
}

// TEST-104
func TestInsightRollup(t *testing.T) {
	t.Parallel()

	turn := parseCompleteFixture(t)
	rollup := BuildInsightRollup(turn)
	if rollup.ToolCount != 5 {
		t.Fatalf("ToolCount = %d, want 5", rollup.ToolCount)
	}
	if rollup.CommandCount != 1 || rollup.FailedCommandCount != 0 {
		t.Fatalf("command counts = %d/%d", rollup.CommandCount, rollup.FailedCommandCount)
	}
	if rollup.PatchCount != 1 || rollup.ChangedFileCount != 1 {
		t.Fatalf("patch counts = %d/%d", rollup.PatchCount, rollup.ChangedFileCount)
	}
	if rollup.VerificationCommandCount != 0 || rollup.VerificationStatus != "not_run" {
		t.Fatalf("verification = %d/%q", rollup.VerificationCommandCount, rollup.VerificationStatus)
	}
	if !reflect.DeepEqual(rollup.ChangedExtensions, []string{".md"}) {
		t.Fatalf("ChangedExtensions = %#v", rollup.ChangedExtensions)
	}
	if len(rollup.TouchedTestFiles) != 0 {
		t.Fatalf("TouchedTestFiles = %#v", rollup.TouchedTestFiles)
	}
}

func TestInsightRollupFailedVerification(t *testing.T) {
	t.Parallel()

	turns, err := ParseTurns(filepath.Join("..", "..", "testdata", "rollouts", "failed-command.jsonl"))
	if err != nil {
		t.Fatalf("ParseTurns: %v", err)
	}
	exportable := ExportableTurns(turns)
	if len(exportable) != 1 {
		t.Fatalf("exportable turns = %d, want 1", len(exportable))
	}
	rollup := BuildInsightRollup(exportable[0])
	if rollup.CommandCount != 2 || rollup.FailedCommandCount != 1 || rollup.VerificationCommandCount != 2 {
		t.Fatalf("command rollup mismatch: %+v", rollup)
	}
	if rollup.VerificationStatus != "failed" || rollup.LastVerificationCommand != "go test ./..." || rollup.LastVerificationStatus != "completed" {
		t.Fatalf("verification rollup mismatch: %+v", rollup)
	}
	if !reflect.DeepEqual(rollup.ChangedExtensions, []string{".go"}) {
		t.Fatalf("ChangedExtensions = %#v", rollup.ChangedExtensions)
	}
}

// TEST-302
func TestInsightCountMetadataSingleRepresentation(t *testing.T) {
	t.Parallel()

	turn := Turn{
		Observations: []Observation{
			{Name: "codex.tool.exec_command", Type: "tool", Input: "sed -n '1,80p' README.md", Metadata: map[string]any{}},
			{Name: "codex.tool.exec_command", Type: "tool", Input: "rg -n TODO internal", Metadata: map[string]any{}},
			{Name: "codex.tool.exec_command", Type: "tool", Input: "curl -fsS https://example.com", Metadata: map[string]any{}},
			{Name: "codex.tool.exec_command", Type: "tool", Input: "npm install", Metadata: map[string]any{}},
			{Name: "codex.tool.exec_command", Type: "tool", Input: "printf 'ok\n'", Metadata: map[string]any{}},
			{Name: "codex.tool.web_search", Type: "tool", Metadata: map[string]any{}},
			{Name: "codex.tool.mcp", Type: "tool", Metadata: map[string]any{}},
		},
	}
	metadata := BuildInsightRollup(turn).Metadata()
	want := map[string]any{
		"read_command_count":         1,
		"search_command_count":       1,
		"network_command_count":      1,
		"install_command_count":      1,
		"other_command_count":        1,
		"test_command_count":         0,
		"build_command_count":        0,
		"lint_command_count":         0,
		"format_command_count":       0,
		"git_command_count":          0,
		"systemd_command_count":      0,
		"exec_command_tool_count":    5,
		"apply_patch_tool_count":     0,
		"web_search_tool_count":      1,
		"mcp_tool_count":             1,
		"tool_search_tool_count":     0,
		"changed_file_count":         0,
		"verification_command_count": 0,
		"navigation":                 "command:install command:network command:other command:read command:search files:read_only tool:exec_command tool:mcp tool:web_search verification:not_applicable",
	}
	for key, want := range want {
		if got := metadata[key]; got != want {
			t.Fatalf("metadata[%q] = %#v, want %#v\nmetadata=%s", key, got, want, canonicalInsightJSON(metadata))
		}
	}
	requireNoDuplicateInsightFields(t, metadata)

	changedTurn := Turn{
		Observations: []Observation{
			{
				Name: "codex.tool.apply_patch",
				Type: "tool",
				Metadata: map[string]any{
					"changed_files": []string{"internal/codextrace/insight.go"},
				},
			},
		},
	}
	changedMetadata := BuildInsightRollup(changedTurn).Metadata()
	if changedMetadata["apply_patch_tool_count"] != 1 || changedMetadata["changed_file_count"] != 1 {
		t.Fatalf("changed file count metadata mismatch: %s", canonicalInsightJSON(changedMetadata))
	}
	if _, ok := changedMetadata["changed_files"]; ok {
		t.Fatalf("root metadata must omit changed_files: %s", canonicalInsightJSON(changedMetadata))
	}
	if changedMetadata["navigation"] != "files:changed tool:apply_patch verification:not_run" {
		t.Fatalf("changed navigation = %#v, metadata=%s", changedMetadata["navigation"], canonicalInsightJSON(changedMetadata))
	}
	requireNoDuplicateInsightFields(t, changedMetadata)
}

// TEST-105
func TestInsightRollupDeterminism(t *testing.T) {
	t.Parallel()

	changes := map[string]any{
		"z_test.go": map[string]any{"type": "update"},
		"README":    map[string]any{"type": "update"},
		"a.go":      map[string]any{"type": "add"},
	}
	fileMetadata := FileChangeMetadata(changes)
	if got := fileMetadata["changed_files"]; !reflect.DeepEqual(got, []string{"README", "a.go", "z_test.go"}) {
		t.Fatalf("FileChangeMetadata changed_files = %#v", got)
	}

	turn := Turn{
		Observations: []Observation{
			{
				Name: "codex.tool.apply_patch",
				Type: "tool",
				Metadata: map[string]any{
					"changed_files": []string{"z_test.go", "README", "a.go", "z_test.go"},
				},
			},
		},
	}
	first := BuildInsightRollup(turn).Metadata()
	second := BuildInsightRollup(turn).Metadata()
	if _, ok := first["changed_files"]; ok {
		t.Fatalf("root metadata must omit changed_files: %#v", first)
	}
	if !reflect.DeepEqual(first["changed_extensions"], []string{".go"}) {
		t.Fatalf("changed_extensions = %#v", first["changed_extensions"])
	}
	if !reflect.DeepEqual(first["touched_test_files"], []string{"z_test.go"}) {
		t.Fatalf("touched_test_files = %#v", first["touched_test_files"])
	}
	if canonicalInsightJSON(first) != canonicalInsightJSON(second) {
		t.Fatalf("rollup is nondeterministic\nfirst=%s\nsecond=%s", canonicalInsightJSON(first), canonicalInsightJSON(second))
	}
}

// EVAL-103
func TestEvalInsightRollupLatency(t *testing.T) {
	t.Parallel()

	turn := parseCompleteFixture(t)
	start := time.Now()
	for i := 0; i < 100; i++ {
		_ = BuildInsightRollup(turn).Metadata()
	}
	if elapsed := time.Since(start); elapsed > 10*time.Millisecond {
		t.Fatalf("100 rollups took %s, want <= 10ms", elapsed)
	}
}

func requireNoDuplicateInsightFields(t *testing.T, metadata map[string]any) {
	t.Helper()
	for key := range metadata {
		if strings.HasPrefix(key, "ran_") || strings.HasPrefix(key, "used_") || key == "has_file_changes" || key == "is_read_only" || key == "command_kinds" || key == "web_search_count" || key == "trace_facets" || key == "navigation_facets" {
			t.Fatalf("metadata contains duplicate navigation field %q in %s", key, canonicalInsightJSON(metadata))
		}
	}
}

func canonicalInsightJSON(value any) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}
