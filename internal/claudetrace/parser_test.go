package claudetrace

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kirilligum/codex-langfuse-tracer/internal/agenttrace"
)

// TEST-504
func TestClaudeParserNoTools(t *testing.T) {
	t.Parallel()

	turn := parseSingleExportable(t, "no-tools.jsonl")
	if turn.Provider != agenttrace.ProviderClaude {
		t.Fatalf("provider = %q", turn.Provider)
	}
	if turn.SessionID != "claude-no-tools" || turn.CWD != "/tmp/claude-no-tools" {
		t.Fatalf("identity = %+v", turn)
	}
	if turn.Model != "claude-haiku-4-5-20251001" {
		t.Fatalf("model = %q", turn.Model)
	}
	if turn.InputText() != "Summarize this project in one sentence." {
		t.Fatalf("input = %q", turn.InputText())
	}
	if turn.OutputText() != "The project exports coding-agent sessions to Langfuse." {
		t.Fatalf("output = %q", turn.OutputText())
	}
	if turn.TokenUsage == nil || turn.TokenUsage.InputTokens != 10 || turn.TokenUsage.OutputTokens != 9 || turn.TokenUsage.TotalTokens != 19 {
		t.Fatalf("token usage = %+v", turn.TokenUsage)
	}
	if len(turn.Observations) != 0 {
		t.Fatalf("observations = %#v, want none", turn.Observations)
	}
}

// TEST-504
func TestClaudeParserBashTool(t *testing.T) {
	t.Parallel()

	turn := parseSingleExportable(t, "bash-tool.jsonl")
	command := requireObservation(t, turn, agenttrace.ToolObservationName(agenttrace.ProviderClaude, agenttrace.ToolFamilyCommand))
	if command.Type != "tool" || command.Input != "printf hello" || command.Output != "hello" {
		t.Fatalf("command observation = %+v", command)
	}
	for key, want := range map[string]any{
		"tool_name":     "Bash",
		"call_id":       "toolu_bash_2",
		"status":        "success",
		"command_kind":  "other",
		"failure_type":  "none",
		"description":   "Print hello",
		"duration_ms":   nil,
		"tool_response": nil,
	} {
		if want == nil {
			if _, ok := command.Metadata[key]; ok {
				t.Fatalf("command metadata must omit %s: %#v", key, command.Metadata)
			}
			continue
		}
		if command.Metadata[key] != want {
			t.Fatalf("command metadata[%s] = %#v, want %#v; metadata=%#v", key, command.Metadata[key], want, command.Metadata)
		}
	}
	if turn.OutputText() != "hello" {
		t.Fatalf("final output = %q", turn.OutputText())
	}
}

// TEST-523
func TestClaudeParserCanonicalBashCommand(t *testing.T) {
	t.Parallel()

	turn := parseSingleExportable(t, "bash-tool.jsonl")
	command := requireObservation(t, turn, agenttrace.ToolObservationName(agenttrace.ProviderClaude, agenttrace.ToolFamilyCommand))
	if command.Metadata["tool_name"] != "Bash" || command.Metadata["command_kind"] != "other" || command.Metadata["failure_type"] != "none" {
		t.Fatalf("command metadata = %#v", command.Metadata)
	}
}

// TEST-524
func TestClaudeParserCanonicalFileChange(t *testing.T) {
	t.Parallel()

	turn := parseSingleExportable(t, "file-change-tool.jsonl")
	fileChange := requireObservation(t, turn, agenttrace.ToolObservationName(agenttrace.ProviderClaude, agenttrace.ToolFamilyFileChange))
	if fileChange.Metadata["tool_name"] != "Edit" || fileChange.Metadata["changed_file_count"] != 1 {
		t.Fatalf("file change metadata = %#v", fileChange.Metadata)
	}
	files, ok := fileChange.Metadata["changed_files"].([]string)
	if !ok || len(files) != 1 || files[0] != "README.md" {
		t.Fatalf("changed_files = %#v", fileChange.Metadata["changed_files"])
	}
	if fileChange.Metadata["file_change_types"].(map[string]string)["README.md"] != "update" {
		t.Fatalf("file_change_types = %#v", fileChange.Metadata["file_change_types"])
	}
}

// TEST-525
func TestClaudeParserCanonicalMCP(t *testing.T) {
	t.Parallel()

	turn := parseSingleExportable(t, "mcp-tool.jsonl")
	mcp := requireObservation(t, turn, agenttrace.ToolObservationName(agenttrace.ProviderClaude, agenttrace.ToolFamilyMCP))
	if mcp.Metadata["tool_name"] != "mcp__github__issues_list" || mcp.Metadata["mcp_server"] != "github" || mcp.Metadata["mcp_tool"] != "issues_list" {
		t.Fatalf("mcp metadata = %#v", mcp.Metadata)
	}
	tags := agenttrace.BuildInsightRollup(turn).Tags()
	if !slicesContains(tags, "mcp:github") || slicesContains(tags, "issues_list") {
		t.Fatalf("tags = %#v", tags)
	}
}

// TEST-504
func TestClaudeParserGenericTool(t *testing.T) {
	t.Parallel()

	turn := parseSingleExportable(t, "generic-tool.jsonl")
	generic := requireObservation(t, turn, "claude.tool.generic")
	if generic.Type != "tool" || !strings.Contains(generic.Input, `"file_path": "go.mod"`) || !strings.Contains(generic.Output, "codex-langfuse-tracer") {
		t.Fatalf("generic observation = %+v", generic)
	}
	if generic.Metadata["tool_name"] != "Read" || generic.Metadata["call_id"] != "toolu_read_1" || generic.Metadata["status"] != "success" {
		t.Fatalf("generic metadata = %#v", generic.Metadata)
	}
}

// TEST-504
func TestClaudeParserRealDerivedStructure(t *testing.T) {
	t.Parallel()

	basic := parseSingleExportable(t, "real-derived-structure-basic.jsonl")
	if basic.SessionID != "claude-real-basic" || basic.TurnID != "00000000-0000-4000-8000-000000000001" || basic.OutputText() != "claude-langfuse-smoke-test" {
		t.Fatalf("basic turn = %+v", basic)
	}
	bash := parseSingleExportable(t, "real-derived-structure-bash.jsonl")
	if requireObservation(t, bash, agenttrace.ToolObservationName(agenttrace.ProviderClaude, agenttrace.ToolFamilyCommand)).Input != "pwd" {
		t.Fatalf("real-derived bash observations = %#v", bash.Observations)
	}
}

// TEST-504
func TestClaudeParserLiveMetadataRecords(t *testing.T) {
	t.Parallel()

	turn := parseSingleExportable(t, "live-metadata-records.jsonl")
	if turn.SessionID != "claude-live-metadata" || turn.CWD != "/tmp/claude-live-metadata" {
		t.Fatalf("identity = %+v", turn)
	}
	if turn.InputText() != "Reply exactly: clt-live-fixture" {
		t.Fatalf("input = %q", turn.InputText())
	}
	if turn.OutputText() != "clt-live-fixture" {
		t.Fatalf("output = %q", turn.OutputText())
	}
	if turn.TurnID != "claude-live-user" {
		t.Fatalf("turn id = %q", turn.TurnID)
	}
	raw, err := json.Marshal(turn)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"SECRET_REASONING_DO_NOT_EXPORT", "fixture skill listing", "lastPrompt", "queue-operation"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("live metadata leaked %q in %s", forbidden, string(raw))
		}
	}
}

// TEST-529
func TestClaudeUsageDetailsPreserveCacheCategories(t *testing.T) {
	t.Parallel()

	turn := parseSingleExportable(t, "live-metadata-records.jsonl")
	if turn.TokenUsage == nil {
		t.Fatal("missing token usage")
	}
	if turn.TokenUsage.InputTokens != 22 || turn.TokenUsage.CacheCreationInputTokens != 5 || turn.TokenUsage.CacheReadInputTokens != 7 || turn.TokenUsage.OutputTokens != 3 || turn.TokenUsage.TotalTokens != 25 {
		t.Fatalf("token usage = %+v", turn.TokenUsage)
	}
	want := map[string]int{
		"input":                       10,
		"cache_creation_input_tokens": 5,
		"cache_read_input_tokens":     7,
		"output":                      3,
		"total":                       25,
	}
	if got := turn.TokenUsage.LangfuseUsageDetails(); !reflect.DeepEqual(got, want) {
		t.Fatalf("usage details = %#v, want %#v", got, want)
	}
}

// TEST-504
func TestClaudeParserIncompleteAndCorrupt(t *testing.T) {
	t.Parallel()

	turns, err := ParseTurns(filepath.Join("..", "..", "testdata", "sources", "claude", "incomplete.jsonl"))
	if err != nil {
		t.Fatalf("incomplete parse error: %v", err)
	}
	if got := agenttrace.ExportableTurns(turns); len(got) != 0 {
		t.Fatalf("incomplete exportable turns = %d, want 0", len(got))
	}

	_, err = ParseTurns(filepath.Join("..", "..", "testdata", "sources", "claude", "corrupt.jsonl"))
	if err == nil {
		t.Fatal("corrupt parse succeeded")
	}
	if text := err.Error(); !strings.Contains(text, "corrupt.jsonl:2") || !strings.Contains(text, "not valid JSON") {
		t.Fatalf("corrupt error = %q", text)
	}
}

// TEST-504
func TestClaudeParserOmitsThinking(t *testing.T) {
	t.Parallel()

	turn := parseSingleExportable(t, "thinking.jsonl")
	raw, err := json.Marshal(turn)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"SECRET_REASONING_DO_NOT_EXPORT", "encrypted-thinking-data", "redacted_thinking"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("thinking content leaked %q in %s", forbidden, string(raw))
		}
	}
	if turn.OutputText() != "Final answer only." {
		t.Fatalf("output = %q", turn.OutputText())
	}
}

// TEST-504
func TestClaudeParserProviderSeparatedIDs(t *testing.T) {
	t.Parallel()

	turn := parseSingleExportable(t, "no-tools.jsonl")
	claudeID := agenttrace.StableTraceID(agenttrace.ProviderClaude, turn.SessionID, turn.TurnID)
	codexID := agenttrace.StableTraceID(agenttrace.ProviderCodex, turn.SessionID, turn.TurnID)
	if turn.TraceID != claudeID {
		t.Fatalf("trace id = %q, want %q", turn.TraceID, claudeID)
	}
	if claudeID == codexID {
		t.Fatalf("provider IDs collide: %s", claudeID)
	}
}

// EVAL-004
func TestEvalClaudeParserDeterminismAndLatency(t *testing.T) {
	t.Parallel()

	fixtures := []string{
		"real-derived-structure-basic.jsonl",
		"real-derived-structure-bash.jsonl",
		"no-tools.jsonl",
		"bash-tool.jsonl",
		"generic-tool.jsonl",
		"file-change-tool.jsonl",
		"mcp-tool.jsonl",
		"incomplete.jsonl",
		"thinking.jsonl",
		"live-metadata-records.jsonl",
	}
	first := map[string]string{}
	start := time.Now()
	for i := 0; i < 20; i++ {
		for _, fixture := range fixtures {
			turns, err := ParseTurns(filepath.Join("..", "..", "testdata", "sources", "claude", fixture))
			if err != nil {
				t.Fatalf("ParseTurns(%s): %v", fixture, err)
			}
			raw, err := json.Marshal(turns)
			if err != nil {
				t.Fatal(err)
			}
			if i == 0 {
				first[fixture] = string(raw)
			} else if string(raw) != first[fixture] {
				t.Fatalf("%s parse is nondeterministic", fixture)
			}
		}
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("fixture corpus parse latency = %s, want <= 200ms", elapsed)
	}
}

func slicesContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func parseSingleExportable(t *testing.T, fixture string) agenttrace.Turn {
	t.Helper()
	turns, err := ParseTurns(filepath.Join("..", "..", "testdata", "sources", "claude", fixture))
	if err != nil {
		t.Fatalf("ParseTurns(%s): %v", fixture, err)
	}
	exportable := agenttrace.ExportableTurns(turns)
	if len(exportable) != 1 {
		t.Fatalf("%s exportable turns = %d, want 1; turns=%#v", fixture, len(exportable), turns)
	}
	return exportable[0]
}

func requireObservation(t *testing.T, turn agenttrace.Turn, name string) agenttrace.Observation {
	t.Helper()
	for _, observation := range turn.Observations {
		if observation.Name == name {
			return observation
		}
	}
	t.Fatalf("missing observation %s in %#v", name, turn.Observations)
	return agenttrace.Observation{}
}
