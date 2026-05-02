package codextrace

import (
	"os"
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

// TEST-401
func TestMCPObservationMetadata(t *testing.T) {
	t.Parallel()
	validateMCPObservationMetadata(t)
}

func validateMCPObservationMetadata(t *testing.T) {
	t.Helper()
	turn := parseCompleteFixture(t)
	mcp := requireObservation(t, turn, "codex.tool.mcp")
	if mcp.Metadata["mcp_server"] != "github" {
		t.Fatalf("mcp_server = %#v, want github; metadata=%#v", mcp.Metadata["mcp_server"], mcp.Metadata)
	}
	if mcp.Metadata["mcp_tool"] != "issues/list" {
		t.Fatalf("mcp_tool = %#v, want issues/list; metadata=%#v", mcp.Metadata["mcp_tool"], mcp.Metadata)
	}
	for _, key := range []string{"invocation", "result", "duration"} {
		if _, ok := mcp.Metadata[key]; ok {
			t.Fatalf("MCP metadata must omit raw %s: %#v", key, mcp.Metadata)
		}
	}

	missing := parseRolloutText(t, `{"timestamp":"2026-05-01T10:00:00Z","type":"session_meta","payload":{"id":"sess-missing"}}
{"timestamp":"2026-05-01T10:00:01Z","type":"turn_context","payload":{"turn_id":"turn-missing"}}
{"timestamp":"2026-05-01T10:00:02Z","type":"event_msg","payload":{"type":"user_message","message":"Use MCP"}}
{"timestamp":"2026-05-01T10:00:03Z","type":"event_msg","payload":{"type":"mcp_tool_call_end","call_id":"mcp-missing","invocation":{},"result":{"ok":true}}}
{"timestamp":"2026-05-01T10:00:04Z","type":"event_msg","payload":{"type":"agent_message","phase":"final_answer","message":"Done"}}
{"timestamp":"2026-05-01T10:00:05Z","type":"event_msg","payload":{"type":"task_complete","last_agent_message":"Done"}}
`)
	missingMCP := requireObservation(t, missing, "codex.tool.mcp")
	if _, ok := missingMCP.Metadata["mcp_server"]; ok {
		t.Fatalf("missing server must omit mcp_server: %#v", missingMCP.Metadata)
	}
	if _, ok := missingMCP.Metadata["mcp_tool"]; ok {
		t.Fatalf("missing tool must omit mcp_tool: %#v", missingMCP.Metadata)
	}
}

// EVAL-401
func TestEvalMCPMetadataParsing(t *testing.T) {
	t.Parallel()
	validateMCPObservationMetadata(t)
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

func parseRolloutText(t *testing.T, text string) Turn {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatalf("write rollout: %v", err)
	}
	turns, err := ParseTurns(path)
	if err != nil {
		t.Fatalf("ParseTurns: %v", err)
	}
	exportable := ExportableTurns(turns)
	if len(exportable) != 1 {
		t.Fatalf("exportable turns = %d, want 1", len(exportable))
	}
	return exportable[0]
}

func requireObservation(t *testing.T, turn Turn, name string) Observation {
	t.Helper()
	for _, observation := range turn.Observations {
		if observation.Name == name {
			return observation
		}
	}
	t.Fatalf("missing observation %s in %#v", name, turn.Observations)
	return Observation{}
}
