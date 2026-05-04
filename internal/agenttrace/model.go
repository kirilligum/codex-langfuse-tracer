package agenttrace

import (
	"crypto/sha256"
	"fmt"
	"strings"
)

type Turn struct {
	Provider        string
	SessionID       string
	TurnID          string
	TraceID         string
	StartTS         string
	EndTS           string
	CWD             string
	Model           string
	UserMessages    []string
	AssistantTexts  []string
	TokenUsage      *TokenUsage
	Completed       bool
	TerminalEntries []TerminalEntry
	Observations    []Observation
}

const (
	ProviderCodex  = "codex"
	ProviderClaude = "claude"
)

const (
	ToolFamilyCommand    = "command"
	ToolFamilyFileChange = "file_change"
	ToolFamilyMCP        = "mcp"
	ToolFamilyWebSearch  = "web_search"
	ToolFamilyToolSearch = "tool_search"
	ToolFamilyGeneric    = "generic"
)

func ToolObservationName(provider, family string) string {
	if provider == "" {
		provider = ProviderCodex
	}
	return provider + ".tool." + NormalizeToolFamily(family)
}

func NormalizeToolFamily(family string) string {
	switch strings.ToLower(strings.TrimSpace(family)) {
	case ToolFamilyCommand:
		return ToolFamilyCommand
	case ToolFamilyFileChange:
		return ToolFamilyFileChange
	case ToolFamilyMCP:
		return ToolFamilyMCP
	case ToolFamilyWebSearch:
		return ToolFamilyWebSearch
	case ToolFamilyToolSearch:
		return ToolFamilyToolSearch
	default:
		return ToolFamilyGeneric
	}
}

type TokenUsage struct {
	InputTokens              int
	OutputTokens             int
	TotalTokens              int
	CachedInputTokens        int
	CacheReadInputTokens     int
	CacheCreationInputTokens int
	ReasoningOutputTokens    int
}

func (u TokenUsage) LangfuseUsageDetails() map[string]int {
	usage := map[string]int{}
	cacheRead := u.CachedInputTokens + u.CacheReadInputTokens
	input := u.InputTokens - cacheRead - u.CacheCreationInputTokens
	if input > 0 {
		usage["input"] = input
	}
	if u.CachedInputTokens > 0 {
		usage["input_cached_tokens"] = u.CachedInputTokens
	}
	if u.CacheReadInputTokens > 0 {
		usage["cache_read_input_tokens"] = u.CacheReadInputTokens
	}
	if u.CacheCreationInputTokens > 0 {
		usage["cache_creation_input_tokens"] = u.CacheCreationInputTokens
	}
	output := u.OutputTokens - u.ReasoningOutputTokens
	if output > 0 {
		usage["output"] = output
	}
	if u.ReasoningOutputTokens > 0 {
		usage["output_reasoning_tokens"] = u.ReasoningOutputTokens
	}
	if u.TotalTokens > 0 {
		usage["total"] = u.TotalTokens
	}
	if len(usage) == 0 {
		return nil
	}
	return usage
}

type TerminalEntry struct {
	Timestamp string
	Label     string
	Text      string
}

type Observation struct {
	Name            string
	StartTimeUnixNS string
	EndTimeUnixNS   string
	Type            string
	Input           string
	Output          string
	Metadata        map[string]any
}

func (t Turn) InputText() string {
	return joinedText(t.UserMessages)
}

func (t Turn) OutputText() string {
	return joinedText(t.AssistantTexts)
}

func joinedText(values []string) string {
	clean := make([]string, 0, len(values))
	for _, value := range values {
		if text := strings.TrimSpace(value); text != "" {
			clean = append(clean, text)
		}
	}
	return strings.TrimSpace(strings.Join(clean, "\n\n"))
}

func StableTraceID(provider, sessionID, turnID string) string {
	if provider == "" {
		provider = ProviderCodex
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s-turn:%s:%s", provider, sessionID, turnID)))
	return fmt.Sprintf("%x", sum)[:32]
}

func StableSpanID(prefix, traceID, turnID, key string) string {
	source := fmt.Sprintf("%s:%s:%s", prefix, traceID, turnID)
	if key != "" {
		source += ":" + key
	}
	sum := sha256.Sum256([]byte(source))
	return fmt.Sprintf("%x", sum)[:16]
}

func ExportableTurns(turns []Turn) []Turn {
	exportable := make([]Turn, 0, len(turns))
	for _, turn := range turns {
		if turn.Completed && turn.TraceID != "" && turn.InputText() != "" && turn.OutputText() != "" {
			exportable = append(exportable, turn)
		}
	}
	return exportable
}

func AppendUnique(values *[]string, value any) {
	text := strings.TrimSpace(StringValue(value))
	if text == "" {
		return
	}
	for _, existing := range *values {
		if existing == text {
			return
		}
	}
	*values = append(*values, text)
}
