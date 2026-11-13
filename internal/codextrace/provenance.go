package codextrace

import (
	"strings"

	"github.com/kirilligum/codex-langfuse-tracer/internal/agenttrace"
)

// recordUserMessage keeps the model's user-role context independently from
// human Input. Work records authorship for each content item, including mixed
// envelopes containing both harness material and actual human text.
func recordUserMessage(turn *agenttrace.Turn, payload map[string]any) {
	id := agenttrace.StringValue(payload["id"])
	if id != "" {
		for _, seen := range turn.SourceMessageIDs {
			if seen == id {
				return
			}
		}
	}
	text := textFromContent(payload["content"], "input_text")
	if text == "" {
		return
	}
	if id != "" {
		turn.SourceMessageIDs = append(turn.SourceMessageIDs, id)
	}
	turn.ModelMessages = append(turn.ModelMessages, text)
	// Preserve the installed turn projection for an in-flight upgrade fence.
	turn.LegacyUserMessages = []string{text}
	meta := agenttrace.MapValue(payload["internal_chat_message_metadata_passthrough"])
	kinds := agenttrace.SliceValue(meta["content_item_kinds"])
	metadata := agenttrace.SliceValue(meta["content_item_metadata"])
	origin := "legacy"
	if len(kinds) > 0 || len(metadata) > 0 {
		var human []any
		for i, item := range agenttrace.SliceValue(payload["content"]) {
			kind, author := "", ""
			if i < len(kinds) {
				kind = agenttrace.StringValue(kinds[i])
			}
			if i < len(metadata) {
				author = agenttrace.StringValue(agenttrace.MapValue(agenttrace.MapValue(metadata[i])["provenance"])["type"])
			}
			if author == "user" || (author == "" && kind == "user.text") {
				human = append(human, item)
			}
		}
		text = textFromContent(human, "input_text")
		origin = "human"
	} else {
		for _, prefix := range []string{"<environment_context>", "<user_instructions>", "<ENVIRONMENT_CONTEXT>", "<USER_INSTRUCTIONS>", "<codex_internal_context"} {
			if strings.HasPrefix(strings.TrimSpace(text), prefix) {
				return
			}
		}
	}
	if text == "" {
		return
	}
	turn.UserMessages = append(turn.UserMessages, text)
	turn.InputOrigin = origin
}
