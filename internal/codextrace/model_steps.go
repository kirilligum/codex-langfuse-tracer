package codextrace

import (
	"github.com/kirilligum/codex-langfuse-tracer/internal/agenttrace"
	"strings"
)

type modelStep struct {
	start      string
	output     []string
	toolOutput []string
}

func (s *modelStep) consume(turn *agenttrace.Turn, kind string, payload map[string]any, timestamp string) {
	typ := agenttrace.StringValue(payload["type"])
	if kind == "response_item" {
		var text string
		switch typ {
		case "reasoning":
			text = agenttrace.ReasoningSummaryText(payload["summary"])
		case "message":
			if agenttrace.StringValue(payload["role"]) == "assistant" {
				text = textFromContent(payload["content"], "output_text")
			}
		case "function_call", "custom_tool_call", "tool_search_call":
			text = agenttrace.StableJSON(map[string]any{"tool": payload["name"], "call_id": payload["call_id"], "arguments": payload["arguments"], "input": payload["input"]})
		case "function_call_output", "custom_tool_call_output", "tool_search_output":
			s.toolOutput = append(s.toolOutput, agenttrace.StableJSON(payload["output"]))
		}
		if text != "" {
			if s.start == "" {
				s.start = timestamp
			}
			s.output = append(s.output, text)
		}
	}
	complete := kind == "event_msg" && (typ == "token_count" || typ == "task_complete")
	if !complete || len(s.output) == 0 {
		return
	}
	var usage *agenttrace.TokenUsage
	if typ == "token_count" {
		last := agenttrace.MapValue(agenttrace.MapValue(payload["info"])["last_token_usage"])
		if len(last) > 0 {
			usage = parseTokenUsage(last)
		}
	}
	input := turn.InputText()
	if len(turn.ModelCalls) > 0 && len(s.toolOutput) > 0 {
		input = strings.Join(s.toolOutput, "\n\n")
	}
	turn.ModelCalls = append(turn.ModelCalls, agenttrace.ModelCall{StartTS: s.start, EndTS: timestamp, Model: turn.Model, Input: input, Output: strings.Join(s.output, "\n\n"), Usage: usage})
	s.start = ""
	s.output = nil
	s.toolOutput = nil
}
