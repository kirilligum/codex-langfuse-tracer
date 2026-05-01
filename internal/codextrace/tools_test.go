package codextrace

import (
	"path/filepath"
	"testing"
)

// TEST-005
func TestToolObservationParity(t *testing.T) {
	t.Parallel()

	turn := parseCompleteFixture(t)
	byName := map[string]Observation{}
	for _, observation := range turn.Observations {
		byName[observation.Name] = observation
	}

	for _, name := range []string{
		"codex.tool.exec_command",
		"codex.tool.apply_patch",
		"codex.tool.mcp",
		"codex.tool.web_search",
		"codex.tool.tool_search",
	} {
		obs, ok := byName[name]
		if !ok {
			t.Fatalf("missing observation %s in %#v", name, turn.Observations)
		}
		if obs.Type != "tool" {
			t.Fatalf("%s type = %q", name, obs.Type)
		}
		if obs.Input == "" || obs.Output == "" {
			t.Fatalf("%s input/output missing: %+v", name, obs)
		}
	}

	patch := byName["codex.tool.apply_patch"]
	if patch.Metadata["changed_file_count"] != 1 {
		t.Fatalf("changed_file_count = %#v", patch.Metadata["changed_file_count"])
	}
	files, ok := patch.Metadata["changed_files"].([]string)
	if !ok || len(files) != 1 || files[0] != "README.md" {
		t.Fatalf("changed_files = %#v", patch.Metadata["changed_files"])
	}
	if patch.Metadata["file_change_types"].(map[string]string)["README.md"] != "update" {
		t.Fatalf("file_change_types = %#v", patch.Metadata["file_change_types"])
	}
}

func parseCompleteFixture(t *testing.T) Turn {
	t.Helper()
	turns, err := ParseTurns(filepath.Join("..", "..", "testdata", "rollouts", "complete-tools.jsonl"))
	if err != nil {
		t.Fatalf("ParseTurns: %v", err)
	}
	exportable := ExportableTurns(turns)
	if len(exportable) != 1 {
		t.Fatalf("exportable turns = %d", len(exportable))
	}
	return exportable[0]
}
