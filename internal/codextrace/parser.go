package codextrace

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/kirilligum/codex-langfuse-tracer/internal/agenttrace"
)

func ParseTurns(path string) ([]agenttrace.Turn, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	sessionID := ""
	sessionModel := ""
	sessionCWD := ""
	currentTurnID := ""
	turnOrder := []string{}
	turnsByID := map[string]*agenttrace.Turn{}
	pendingCalls := map[string]map[string]any{}
	coveredCallIDs := map[string]bool{}

	for lineNumber, line := range strings.Split(string(raw), "\n") {
		lineNumber++
		if strings.TrimSpace(line) == "" {
			continue
		}

		var item map[string]any
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			return nil, fmt.Errorf("%s:%d is not valid JSON: %w", path, lineNumber, err)
		}

		itemType := agenttrace.StringValue(item["type"])
		timestamp := agenttrace.StringValue(item["timestamp"])
		payload := agenttrace.MapValue(item["payload"])

		switch itemType {
		case "session_meta":
			if value := agenttrace.StringValue(payload["id"]); value != "" {
				sessionID = value
			}
			if value := agenttrace.StringValue(payload["model"]); value != "" {
				sessionModel = value
			} else if value := agenttrace.StringValue(payload["default_model"]); value != "" {
				sessionModel = value
			}
			if value := agenttrace.StringValue(payload["cwd"]); value != "" {
				sessionCWD = value
			}
			continue
		case "turn_context":
			turnID := agenttrace.StringValue(payload["turn_id"])
			if turnID == "" {
				currentTurnID = ""
				continue
			}
			traceID := agenttrace.StringValue(payload["trace_id"])
			if traceID == "" {
				traceID = agenttrace.StableTraceID(agenttrace.ProviderCodex, sessionID, turnID)
			}
			currentTurnID = turnID
			if existing := turnsByID[turnID]; existing != nil {
				if existing.TraceID == "" {
					existing.TraceID = traceID
				}
				if existing.CWD == "" {
					existing.CWD = agenttrace.StringOr(payload["cwd"], sessionCWD)
				}
				if existing.Model == "" {
					existing.Model = agenttrace.StringOr(payload["model"], sessionModel)
				}
				continue
			}
			turn := &agenttrace.Turn{
				Provider:  agenttrace.ProviderCodex,
				SessionID: sessionID,
				TurnID:    turnID,
				TraceID:   traceID,
				StartTS:   timestampOrNow(timestamp),
				EndTS:     timestampOrNow(timestamp),
				CWD:       agenttrace.StringOr(payload["cwd"], sessionCWD),
				Model:     agenttrace.StringOr(payload["model"], sessionModel),
			}
			turnOrder = append(turnOrder, turnID)
			turnsByID[turnID] = turn
			continue
		}

		if currentTurnID == "" {
			continue
		}
		turn := turnsByID[currentTurnID]
		if turn == nil {
			continue
		}

		switch itemType {
		case "event_msg":
			parseEventMessage(turn, payload, timestamp, pendingCalls, coveredCallIDs)
		case "response_item":
			parseResponseItem(turn, payload, timestamp, pendingCalls, coveredCallIDs)
		}
	}

	turns := make([]agenttrace.Turn, 0, len(turnOrder))
	for _, turnID := range turnOrder {
		turns = append(turns, *turnsByID[turnID])
	}
	return turns, nil
}

func parseEventMessage(turn *agenttrace.Turn, payload map[string]any, timestamp string, pendingCalls map[string]map[string]any, coveredCallIDs map[string]bool) {
	switch agenttrace.StringValue(payload["type"]) {
	case "user_message":
		agenttrace.AppendUnique(&turn.UserMessages, payload["message"])
		agenttrace.AddTerminalEntry(turn, timestamp, "user", agenttrace.StringValue(payload["message"]))
	case "agent_message":
		if agenttrace.StringValue(payload["phase"]) == "final_answer" {
			message := agenttrace.StringValue(payload["message"])
			agenttrace.AppendUnique(&turn.AssistantTexts, message)
			agenttrace.AddTerminalEntry(turn, timestamp, "assistant.final", message)
			if timestamp != "" {
				turn.EndTS = timestamp
			}
		} else {
			message := agenttrace.StringValue(payload["message"])
			agenttrace.AddTerminalEntry(turn, timestamp, "assistant.commentary", message)
			agenttrace.AddObservation(turn, "codex.message.commentary", timestamp, "", message, map[string]any{"phase": agenttrace.StringValue(payload["phase"])}, "span", nil)
		}
	case "task_complete":
		message := agenttrace.StringValue(payload["last_agent_message"])
		agenttrace.AppendUnique(&turn.AssistantTexts, message)
		agenttrace.AddTerminalEntry(turn, timestamp, "assistant.final", message)
		if timestamp != "" {
			turn.EndTS = timestamp
		}
		turn.Completed = true
	case "exec_command_end":
		callID := agenttrace.StringValue(payload["call_id"])
		if callID != "" {
			coveredCallIDs[callID] = true
		}
		output := agenttrace.CommandOutput(payload)
		metadata := agenttrace.MetadataWithoutLargeFields(payload, map[string]bool{
			"command": true, "stdout": true, "stderr": true, "aggregated_output": true, "formatted_output": true, "parsed_cmd": true, "duration": true,
		})
		for key, value := range agenttrace.CommandInsightMetadata(payload) {
			metadata[key] = value
		}
		metadata["tool_name"] = "exec_command"
		agenttrace.AddTerminalEntry(turn, timestamp, "tool.command", agenttrace.CommandTerminalText(payload))
		agenttrace.AddObservation(turn, agenttrace.ToolObservationName(agenttrace.ProviderCodex, agenttrace.ToolFamilyCommand), timestamp, agenttrace.FormatCommand(payload["command"]), output, metadata, "tool", payload["duration"])
	case "patch_apply_end":
		callID := agenttrace.StringValue(payload["call_id"])
		if callID != "" {
			coveredCallIDs[callID] = true
		}
		patchInput := ""
		if pending := pendingCalls[callID]; pending != nil {
			if value := pending["input"]; value != nil {
				patchInput = agenttrace.StableJSON(value)
			} else {
				patchInput = agenttrace.StableJSON(pending["arguments"])
			}
		}
		metadata := agenttrace.MetadataWithoutLargeFields(payload, map[string]bool{"stdout": true, "stderr": true, "changes": true})
		for key, value := range agenttrace.FileChangeMetadata(agenttrace.MapValue(payload["changes"])) {
			metadata[key] = value
		}
		metadata["tool_name"] = "apply_patch"
		output := agenttrace.PatchOutput(payload)
		agenttrace.AddTerminalEntry(turn, timestamp, "tool.file_change", agenttrace.ToolTerminalText(patchInput, output))
		agenttrace.AddObservation(turn, agenttrace.ToolObservationName(agenttrace.ProviderCodex, agenttrace.ToolFamilyFileChange), timestamp, patchInput, output, metadata, "tool", nil)
	case "mcp_tool_call_end":
		callID := agenttrace.StringValue(payload["call_id"])
		if callID != "" {
			coveredCallIDs[callID] = true
		}
		input := agenttrace.StableJSON(payload["invocation"])
		output := agenttrace.StableJSON(payload["result"])
		metadata := agenttrace.MetadataWithoutLargeFields(payload, map[string]bool{"invocation": true, "result": true, "duration": true})
		for key, value := range agenttrace.MCPToolMetadata(payload) {
			metadata[key] = value
		}
		metadata["tool_name"] = "mcp"
		agenttrace.AddTerminalEntry(turn, timestamp, "tool.mcp", agenttrace.ToolTerminalText(input, output))
		agenttrace.AddObservation(turn, agenttrace.ToolObservationName(agenttrace.ProviderCodex, agenttrace.ToolFamilyMCP), timestamp, input, output, metadata, "tool", payload["duration"])
	case "web_search_end":
		callID := agenttrace.StringValue(payload["call_id"])
		if callID != "" {
			coveredCallIDs[callID] = true
		}
		input := agenttrace.StableJSON(map[string]any{"query": payload["query"], "action": payload["action"]})
		output := agenttrace.StableJSON(payload["action"])
		metadata := agenttrace.MetadataWithoutLargeFields(payload, map[string]bool{"query": true, "action": true})
		metadata["tool_name"] = "web_search"
		agenttrace.AddTerminalEntry(turn, timestamp, "tool.web_search", agenttrace.ToolTerminalText(input, output))
		agenttrace.AddObservation(turn, agenttrace.ToolObservationName(agenttrace.ProviderCodex, agenttrace.ToolFamilyWebSearch), timestamp, input, output, metadata, "tool", nil)
	case "token_count":
		info := agenttrace.MapValue(payload["info"])
		usage := agenttrace.MapValue(info["last_token_usage"])
		if len(usage) == 0 {
			usage = agenttrace.MapValue(info["total_token_usage"])
		}
		if len(usage) > 0 {
			turn.TokenUsage = parseTokenUsage(usage)
		}
	case "context_compacted":
		agenttrace.AddTerminalEntry(turn, timestamp, "system", "Context compacted")
	}
}

func parseResponseItem(turn *agenttrace.Turn, payload map[string]any, timestamp string, pendingCalls map[string]map[string]any, coveredCallIDs map[string]bool) {
	switch agenttrace.StringValue(payload["type"]) {
	case "message":
		switch agenttrace.StringValue(payload["role"]) {
		case "user":
			agenttrace.AppendUnique(&turn.UserMessages, textFromContent(payload["content"], "input_text"))
		case "assistant":
			if agenttrace.StringValue(payload["phase"]) == "final_answer" {
				agenttrace.AppendUnique(&turn.AssistantTexts, textFromContent(payload["content"], "output_text"))
				if timestamp != "" {
					turn.EndTS = timestamp
				}
			}
		}
	case "reasoning":
		summary := agenttrace.ReasoningSummaryText(payload["summary"])
		if summary != "" {
			agenttrace.AddTerminalEntry(turn, timestamp, "assistant.reasoning", summary)
			agenttrace.AddObservation(turn, "codex.reasoning.summary", timestamp, "", summary, map[string]any{"response_item_type": "reasoning"}, "span", nil)
		}
	case "function_call", "custom_tool_call", "tool_search_call":
		callID := agenttrace.StringValue(payload["call_id"])
		if callID != "" {
			pendingCalls[callID] = payload
		}
	case "function_call_output", "custom_tool_call_output", "tool_search_output":
		callID := agenttrace.StringValue(payload["call_id"])
		if callID != "" && coveredCallIDs[callID] {
			return
		}
		pending := pendingCalls[callID]
		name := agenttrace.StringValue(pending["name"])
		if name == "" {
			name = strings.TrimSuffix(agenttrace.StringValue(payload["type"]), "_output")
		}
		family := agenttrace.ToolFamilyGeneric
		if agenttrace.StringValue(payload["type"]) == "tool_search_output" {
			family = agenttrace.ToolFamilyToolSearch
			name = "tool_search"
		}
		observationName := agenttrace.ToolObservationName(agenttrace.ProviderCodex, family)
		inputSource := pending["arguments"]
		if inputSource == nil {
			inputSource = pending["input"]
		}
		if inputSource == nil {
			inputSource = pending["execution"]
		}
		outputSource := payload["output"]
		if outputSource == nil {
			outputSource = payload["tools"]
		}
		input := agenttrace.StableJSON(inputSource)
		output := agenttrace.StableJSON(outputSource)
		agenttrace.AddTerminalEntry(turn, timestamp, strings.TrimPrefix(observationName, "codex."), agenttrace.ToolTerminalText(input, output))
		agenttrace.AddObservation(turn, observationName, timestamp, input, output, map[string]any{
			"call_id":            callID,
			"response_item_type": agenttrace.StringValue(payload["type"]),
			"status":             agenttrace.StringValue(payload["status"]),
			"tool_name":          name,
		}, "tool", nil)
		if timestamp != "" && agenttrace.StringValue(payload["phase"]) == "final_answer" {
			if timestamp != "" {
				turn.EndTS = timestamp
			}
		}
	}
}

func parseTokenUsage(value map[string]any) *agenttrace.TokenUsage {
	return &agenttrace.TokenUsage{
		InputTokens:           agenttrace.IntValue(value["input_tokens"]),
		OutputTokens:          agenttrace.IntValue(value["output_tokens"]),
		TotalTokens:           agenttrace.IntValue(value["total_tokens"]),
		CachedInputTokens:     agenttrace.IntValue(value["cached_input_tokens"]),
		ReasoningOutputTokens: agenttrace.IntValue(value["reasoning_output_tokens"]),
	}
}

func textFromContent(content any, textType string) string {
	var parts []string
	for _, item := range agenttrace.SliceValue(content) {
		entry := agenttrace.MapValue(item)
		if agenttrace.StringValue(entry["type"]) == textType {
			if text := agenttrace.StringValue(entry["text"]); text != "" {
				parts = append(parts, text)
			}
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func timestampOrNow(value string) string {
	if value != "" {
		return value
	}
	return time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
}
