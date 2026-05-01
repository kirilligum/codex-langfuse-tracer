package codextrace

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

func ParseTurns(path string) ([]Turn, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	sessionID := ""
	sessionModel := ""
	sessionCWD := ""
	currentTurnID := ""
	turnOrder := []string{}
	turnsByID := map[string]*Turn{}
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

		itemType := stringValue(item["type"])
		timestamp := stringValue(item["timestamp"])
		payload := mapValue(item["payload"])

		switch itemType {
		case "session_meta":
			if value := stringValue(payload["id"]); value != "" {
				sessionID = value
			}
			if value := stringValue(payload["model"]); value != "" {
				sessionModel = value
			} else if value := stringValue(payload["default_model"]); value != "" {
				sessionModel = value
			}
			if value := stringValue(payload["cwd"]); value != "" {
				sessionCWD = value
			}
			continue
		case "turn_context":
			turnID := stringValue(payload["turn_id"])
			if turnID == "" {
				currentTurnID = ""
				continue
			}
			traceID := stringValue(payload["trace_id"])
			if traceID == "" {
				traceID = StableTraceID(sessionID, turnID)
			}
			currentTurnID = turnID
			turn := &Turn{
				SessionID: sessionID,
				TurnID:    turnID,
				TraceID:   traceID,
				StartTS:   timestampOrNow(timestamp),
				EndTS:     timestampOrNow(timestamp),
				CWD:       firstString(payload["cwd"], sessionCWD),
				Model:     firstString(payload["model"], sessionModel),
			}
			if _, exists := turnsByID[turnID]; !exists {
				turnOrder = append(turnOrder, turnID)
			}
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

	turns := make([]Turn, 0, len(turnOrder))
	for _, turnID := range turnOrder {
		turns = append(turns, *turnsByID[turnID])
	}
	return turns, nil
}

func parseEventMessage(turn *Turn, payload map[string]any, timestamp string, pendingCalls map[string]map[string]any, coveredCallIDs map[string]bool) {
	switch stringValue(payload["type"]) {
	case "user_message":
		appendUnique(&turn.UserMessages, payload["message"])
		addTerminalEntry(turn, timestamp, "user", stringValue(payload["message"]))
	case "agent_message":
		if stringValue(payload["phase"]) == "final_answer" {
			message := stringValue(payload["message"])
			appendUnique(&turn.AssistantTexts, message)
			addTerminalEntry(turn, timestamp, "assistant.final", message)
			if timestamp != "" {
				turn.EndTS = timestamp
			}
		} else {
			message := stringValue(payload["message"])
			addTerminalEntry(turn, timestamp, "assistant.commentary", message)
			addObservation(turn, "codex.message.commentary", timestamp, "", message, map[string]any{"phase": stringValue(payload["phase"])}, "span", nil)
		}
	case "task_complete":
		message := stringValue(payload["last_agent_message"])
		appendUnique(&turn.AssistantTexts, message)
		addTerminalEntry(turn, timestamp, "assistant.final", message)
		if timestamp != "" {
			turn.EndTS = timestamp
		}
		turn.Completed = true
	case "exec_command_end":
		callID := stringValue(payload["call_id"])
		if callID != "" {
			coveredCallIDs[callID] = true
		}
		output := commandOutput(payload)
		addTerminalEntry(turn, timestamp, "tool.exec_command", commandTerminalText(payload))
		addObservation(turn, "codex.tool.exec_command", timestamp, FormatCommand(payload["command"]), output, metadataWithoutLargeFields(payload, map[string]bool{
			"command": true, "stdout": true, "stderr": true, "aggregated_output": true, "formatted_output": true, "parsed_cmd": true,
		}), "tool", payload["duration"])
	case "patch_apply_end":
		callID := stringValue(payload["call_id"])
		if callID != "" {
			coveredCallIDs[callID] = true
		}
		patchInput := ""
		if pending := pendingCalls[callID]; pending != nil {
			if value := pending["input"]; value != nil {
				patchInput = StableJSON(value)
			} else {
				patchInput = StableJSON(pending["arguments"])
			}
		}
		metadata := metadataWithoutLargeFields(payload, map[string]bool{"stdout": true, "stderr": true, "changes": true})
		for key, value := range FileChangeMetadata(mapValue(payload["changes"])) {
			metadata[key] = value
		}
		output := patchOutput(payload)
		addTerminalEntry(turn, timestamp, "tool.apply_patch", toolTerminalText(patchInput, output))
		addObservation(turn, "codex.tool.apply_patch", timestamp, patchInput, output, metadata, "tool", nil)
	case "mcp_tool_call_end":
		callID := stringValue(payload["call_id"])
		if callID != "" {
			coveredCallIDs[callID] = true
		}
		input := StableJSON(payload["invocation"])
		output := StableJSON(payload["result"])
		addTerminalEntry(turn, timestamp, "tool.mcp", toolTerminalText(input, output))
		addObservation(turn, "codex.tool.mcp", timestamp, input, output, metadataWithoutLargeFields(payload, map[string]bool{"invocation": true, "result": true}), "tool", payload["duration"])
	case "web_search_end":
		callID := stringValue(payload["call_id"])
		if callID != "" {
			coveredCallIDs[callID] = true
		}
		input := StableJSON(map[string]any{"query": payload["query"], "action": payload["action"]})
		output := StableJSON(payload["action"])
		addTerminalEntry(turn, timestamp, "tool.web_search", toolTerminalText(input, output))
		addObservation(turn, "codex.tool.web_search", timestamp, input, output, metadataWithoutLargeFields(payload, map[string]bool{"query": true, "action": true}), "tool", nil)
	case "token_count":
		info := mapValue(payload["info"])
		usage := mapValue(info["last_token_usage"])
		if len(usage) == 0 {
			usage = mapValue(info["total_token_usage"])
		}
		if len(usage) > 0 {
			turn.TokenUsage = parseTokenUsage(usage)
		}
	case "context_compacted":
		addTerminalEntry(turn, timestamp, "system", "Context compacted")
	}
}

func parseResponseItem(turn *Turn, payload map[string]any, timestamp string, pendingCalls map[string]map[string]any, coveredCallIDs map[string]bool) {
	switch stringValue(payload["type"]) {
	case "message":
		switch stringValue(payload["role"]) {
		case "user":
			appendUnique(&turn.UserMessages, textFromContent(payload["content"], "input_text"))
		case "assistant":
			if stringValue(payload["phase"]) == "final_answer" {
				appendUnique(&turn.AssistantTexts, textFromContent(payload["content"], "output_text"))
				if timestamp != "" {
					turn.EndTS = timestamp
				}
			}
		}
	case "reasoning":
		summary := reasoningSummaryText(payload["summary"])
		if summary != "" {
			addTerminalEntry(turn, timestamp, "assistant.reasoning", summary)
			addObservation(turn, "codex.reasoning.summary", timestamp, "", summary, map[string]any{"response_item_type": "reasoning"}, "span", nil)
		}
	case "function_call", "custom_tool_call", "tool_search_call":
		callID := stringValue(payload["call_id"])
		if callID != "" {
			pendingCalls[callID] = payload
		}
	case "function_call_output", "custom_tool_call_output", "tool_search_output":
		callID := stringValue(payload["call_id"])
		if callID != "" && coveredCallIDs[callID] {
			return
		}
		pending := pendingCalls[callID]
		name := stringValue(pending["name"])
		if name == "" {
			name = strings.TrimSuffix(stringValue(payload["type"]), "_output")
		}
		observationName := "codex.tool." + name
		if stringValue(payload["type"]) == "tool_search_output" {
			observationName = "codex.tool.tool_search"
		}
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
		input := StableJSON(inputSource)
		output := StableJSON(outputSource)
		addTerminalEntry(turn, timestamp, strings.TrimPrefix(observationName, "codex."), toolTerminalText(input, output))
		addObservation(turn, observationName, timestamp, input, output, map[string]any{
			"call_id":            callID,
			"response_item_type": stringValue(payload["type"]),
			"status":             stringValue(payload["status"]),
		}, "tool", nil)
		if timestamp != "" && stringValue(payload["phase"]) == "final_answer" {
			if timestamp != "" {
				turn.EndTS = timestamp
			}
		}
	}
}

func parseTokenUsage(value map[string]any) *TokenUsage {
	return &TokenUsage{
		InputTokens:           intValue(value["input_tokens"]),
		OutputTokens:          intValue(value["output_tokens"]),
		TotalTokens:           intValue(value["total_tokens"]),
		CachedInputTokens:     intValue(value["cached_input_tokens"]),
		ReasoningOutputTokens: intValue(value["reasoning_output_tokens"]),
	}
}

func textFromContent(content any, textType string) string {
	var parts []string
	for _, item := range sliceValue(content) {
		entry := mapValue(item)
		if stringValue(entry["type"]) == textType {
			if text := stringValue(entry["text"]); text != "" {
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
