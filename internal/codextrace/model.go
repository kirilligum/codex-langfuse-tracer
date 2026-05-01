package codextrace

import (
	"crypto/sha256"
	"fmt"
	"strings"
)

type Turn struct {
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

type TokenUsage struct {
	InputTokens           int
	OutputTokens          int
	TotalTokens           int
	CachedInputTokens     int
	ReasoningOutputTokens int
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

func StableTraceID(sessionID, turnID string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("codex-turn:%s:%s", sessionID, turnID)))
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

func appendUnique(values *[]string, value any) {
	text := strings.TrimSpace(stringValue(value))
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
