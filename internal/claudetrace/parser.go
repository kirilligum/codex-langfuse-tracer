package claudetrace

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/kirilligum/codex-langfuse-tracer/internal/agenttrace"
)

type transcriptRecord struct {
	Type      string        `json:"type"`
	SessionID string        `json:"sessionId"`
	CWD       string        `json:"cwd"`
	UUID      string        `json:"uuid"`
	Timestamp string        `json:"timestamp"`
	Subtype   string        `json:"subtype"`
	IsMeta    bool          `json:"isMeta"`
	Message   transcriptMsg `json:"message"`
	raw       map[string]any
}

type transcriptMsg struct {
	Role       string         `json:"role"`
	Model      string         `json:"model"`
	Content    any            `json:"content"`
	StopReason string         `json:"stop_reason"`
	Usage      map[string]any `json:"usage"`
}

type pendingTool struct {
	ID        string
	Name      string
	Input     map[string]any
	Timestamp string
}

func ParseTurns(path string) ([]agenttrace.Turn, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	turns := []agenttrace.Turn{}
	var current *agenttrace.Turn
	pending := map[string]pendingTool{}

	for index, line := range strings.Split(string(raw), "\n") {
		lineNumber := index + 1
		if strings.TrimSpace(line) == "" {
			continue
		}
		var record transcriptRecord
		if err := json.Unmarshal([]byte(line), &record.raw); err != nil {
			return nil, fmt.Errorf("%s:%d is not valid JSON: %w", path, lineNumber, err)
		}
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			return nil, fmt.Errorf("%s:%d is not valid Claude transcript record: %w", path, lineNumber, err)
		}

		switch record.Type {
		case "summary", "queue-operation", "attachment", "last-prompt", "permission-mode", "file-history-snapshot", "ai-title":
			continue
		case "system":
			if isKnownSystemMetadata(record.Subtype) {
				continue
			}
			return nil, fmt.Errorf("%s:%d unsupported Claude transcript record subtype %q for type %q", path, lineNumber, record.Subtype, record.Type)
		case "user":
			handleUserRecord(&turns, &current, pending, record)
		case "assistant":
			handleAssistantRecord(&turns, &current, pending, record)
		default:
			return nil, fmt.Errorf("%s:%d unsupported Claude transcript record type %q", path, lineNumber, record.Type)
		}
	}
	return turns, nil
}

func isKnownSystemMetadata(subtype string) bool {
	switch subtype {
	case "turn_duration", "stop_hook_summary", "away_summary":
		return true
	default:
		return false
	}
}

func handleUserRecord(turns *[]agenttrace.Turn, current **agenttrace.Turn, pending map[string]pendingTool, record transcriptRecord) {
	if record.IsMeta {
		return
	}
	parts := contentParts(record.Message.Content)
	hasPromptText := false
	for _, part := range parts {
		switch agenttrace.StringValue(part["type"]) {
		case "text":
			hasPromptText = true
		case "tool_result":
			if *current != nil {
				addToolResult(*current, pending, part, record.Timestamp)
			}
		}
	}
	if !hasPromptText {
		return
	}
	if *current == nil || (*current).Completed {
		turn := newTurn(len(*turns)+1, record)
		*turns = append(*turns, turn)
		*current = &(*turns)[len(*turns)-1]
	}
	for _, part := range parts {
		if agenttrace.StringValue(part["type"]) != "text" {
			continue
		}
		text := agenttrace.StringValue(part["text"])
		agenttrace.AppendUnique(&(*current).UserMessages, text)
		agenttrace.AddTerminalEntry(*current, record.Timestamp, "user", text)
	}
}

func handleAssistantRecord(turns *[]agenttrace.Turn, current **agenttrace.Turn, pending map[string]pendingTool, record transcriptRecord) {
	if *current == nil {
		turn := newTurn(len(*turns)+1, record)
		*turns = append(*turns, turn)
		*current = &(*turns)[len(*turns)-1]
	}
	if (*current).SessionID == "" {
		(*current).SessionID = record.SessionID
	}
	if (*current).CWD == "" {
		(*current).CWD = record.CWD
	}
	if record.Message.Model != "" {
		(*current).Model = record.Message.Model
	}
	if len(record.Message.Usage) > 0 {
		(*current).TokenUsage = parseUsage(record.Message.Usage)
	}
	for _, part := range contentParts(record.Message.Content) {
		switch agenttrace.StringValue(part["type"]) {
		case "text":
			text := agenttrace.StringValue(part["text"])
			if record.Message.StopReason == "tool_use" {
				agenttrace.AddTerminalEntry(*current, record.Timestamp, "assistant.commentary", text)
			} else {
				agenttrace.AppendUnique(&(*current).AssistantTexts, text)
				agenttrace.AddTerminalEntry(*current, record.Timestamp, "assistant.final", text)
			}
		case "tool_use":
			tool := pendingTool{
				ID:        agenttrace.StringValue(part["id"]),
				Name:      agenttrace.StringValue(part["name"]),
				Input:     agenttrace.MapValue(part["input"]),
				Timestamp: record.Timestamp,
			}
			if tool.ID != "" {
				pending[tool.ID] = tool
			}
		case "thinking", "redacted_thinking":
			continue
		}
	}
	if record.Message.StopReason == "end_turn" && (*current).OutputText() != "" {
		(*current).Completed = true
		if record.Timestamp != "" {
			(*current).EndTS = record.Timestamp
		}
	}
}

func newTurn(index int, record transcriptRecord) agenttrace.Turn {
	turnID := record.UUID
	if turnID == "" {
		turnID = fmt.Sprintf("turn-%d", index)
	}
	timestamp := timestampOrZero(record.Timestamp)
	return agenttrace.Turn{
		Provider:  agenttrace.ProviderClaude,
		SessionID: record.SessionID,
		TurnID:    turnID,
		TraceID:   agenttrace.StableTraceID(agenttrace.ProviderClaude, record.SessionID, turnID),
		StartTS:   timestamp,
		EndTS:     timestamp,
		CWD:       record.CWD,
	}
}

func addToolResult(turn *agenttrace.Turn, pending map[string]pendingTool, part map[string]any, timestamp string) {
	callID := agenttrace.StringValue(part["tool_use_id"])
	tool, ok := pending[callID]
	if !ok {
		return
	}
	delete(pending, callID)

	isError := boolValue(part["is_error"])
	status := "success"
	failureType := "none"
	if isError {
		status = "error"
		failureType = "nonzero_exit"
	}
	output := toolResultText(part["content"])
	metadata := map[string]any{
		"tool_name": tool.Name,
		"call_id":   callID,
		"status":    status,
		"is_error":  isError,
	}
	name := agenttrace.ToolObservationName(agenttrace.ProviderClaude, agenttrace.ToolFamilyGeneric)
	input := agenttrace.StableJSON(tool.Input)
	if isBashTool(tool.Name) {
		name = agenttrace.ToolObservationName(agenttrace.ProviderClaude, agenttrace.ToolFamilyCommand)
		input = agenttrace.StringValue(tool.Input["command"])
		metadata["command_kind"] = agenttrace.ClassifyCommand(input)
		metadata["failure_type"] = failureType
		if description := agenttrace.StringValue(tool.Input["description"]); description != "" {
			metadata["description"] = description
		}
	} else if mcpMetadata, ok := mcpToolMetadata(tool.Name); ok {
		name = agenttrace.ToolObservationName(agenttrace.ProviderClaude, agenttrace.ToolFamilyMCP)
		for key, value := range mcpMetadata {
			metadata[key] = value
		}
	} else if fileMetadata, ok := fileChangeMetadata(tool.Name, tool.Input); ok {
		name = agenttrace.ToolObservationName(agenttrace.ProviderClaude, agenttrace.ToolFamilyFileChange)
		for key, value := range fileMetadata {
			metadata[key] = value
		}
	}
	agenttrace.AddTerminalEntry(turn, timestamp, strings.TrimPrefix(name, "claude."), agenttrace.ToolTerminalText(input, output))
	agenttrace.AddObservation(turn, name, timestamp, input, output, metadata, "tool", nil)
}

func parseUsage(usage map[string]any) *agenttrace.TokenUsage {
	cacheRead := agenttrace.IntValue(usage["cache_read_input_tokens"])
	cacheCreation := agenttrace.IntValue(usage["cache_creation_input_tokens"])
	input := agenttrace.IntValue(usage["input_tokens"]) + cacheRead + cacheCreation
	output := agenttrace.IntValue(usage["output_tokens"])
	return &agenttrace.TokenUsage{
		InputTokens:              input,
		OutputTokens:             output,
		TotalTokens:              input + output,
		CacheReadInputTokens:     cacheRead,
		CacheCreationInputTokens: cacheCreation,
	}
}

func isBashTool(name string) bool {
	return strings.EqualFold(name, "Bash")
}

func mcpToolMetadata(name string) (map[string]any, bool) {
	parts := strings.Split(name, "__")
	if len(parts) < 3 || parts[0] != "mcp" {
		return nil, false
	}
	server := strings.TrimSpace(parts[1])
	tool := strings.TrimSpace(strings.Join(parts[2:], "__"))
	if server == "" || tool == "" {
		return nil, false
	}
	return agenttrace.MCPToolMetadata(map[string]any{"invocation": map[string]any{"server": server, "tool": tool}}), true
}

func fileChangeMetadata(toolName string, input map[string]any) (map[string]any, bool) {
	path := agenttrace.StringValue(input["file_path"])
	if path == "" {
		path = agenttrace.StringValue(input["path"])
	}
	if path == "" {
		path = agenttrace.StringValue(input["notebook_path"])
	}
	if path == "" {
		return nil, false
	}
	changeType := "update"
	switch toolName {
	case "Write":
		changeType = "write"
	case "Edit", "MultiEdit", "NotebookEdit":
		changeType = "update"
	default:
		return nil, false
	}
	return agenttrace.FileChangeMetadata(map[string]any{path: map[string]any{"type": changeType}}), true
}

func contentParts(value any) []map[string]any {
	switch typed := value.(type) {
	case []any:
		parts := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			part := agenttrace.MapValue(item)
			if len(part) > 0 {
				parts = append(parts, part)
			}
		}
		return parts
	case string:
		if strings.TrimSpace(typed) == "" {
			return nil
		}
		return []map[string]any{{"type": "text", "text": typed}}
	default:
		return nil
	}
}

func toolResultText(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case []any:
		var parts []string
		for _, item := range typed {
			entry := agenttrace.MapValue(item)
			if text := agenttrace.StringValue(entry["text"]); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.TrimSpace(strings.Join(parts, "\n"))
	default:
		return agenttrace.StableJSON(value)
	}
}

func boolValue(value any) bool {
	typed, _ := value.(bool)
	return typed
}

func timestampOrZero(value string) string {
	if value != "" {
		return value
	}
	return "1970-01-01T00:00:00Z"
}
