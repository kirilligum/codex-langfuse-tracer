package tracecontract

import (
	"github.com/kirilligum/codex-langfuse-tracer/internal/buildinfo"
	"github.com/kirilligum/codex-langfuse-tracer/internal/codextrace"
)

type Trace struct {
	SchemaVersion int            `json:"schema_version"`
	Name          string         `json:"name"`
	TraceID       string         `json:"trace_id,omitempty"`
	SessionID     string         `json:"session_id,omitempty"`
	TurnID        string         `json:"turn_id,omitempty"`
	Input         string         `json:"input,omitempty"`
	Output        string         `json:"output,omitempty"`
	Model         string         `json:"model,omitempty"`
	CWD           string         `json:"cwd,omitempty"`
	TokenUsage    map[string]int `json:"token_usage,omitempty"`
	Exportable    bool           `json:"exportable,omitempty"`
	ParseError    bool           `json:"parse_error,omitempty"`
	Observations  []Observation  `json:"observations"`
}

type Observation struct {
	Name           string         `json:"name"`
	Type           string         `json:"type"`
	Input          string         `json:"input"`
	Output         string         `json:"output,omitempty"`
	OutputContains []string       `json:"output_contains,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
}

func FromTurn(turn codextrace.Turn) Trace {
	trace := Trace{
		SchemaVersion: 1,
		Name:          buildinfo.TraceName,
		TraceID:       turn.TraceID,
		SessionID:     turn.SessionID,
		TurnID:        turn.TurnID,
		Input:         codextrace.ExportText(turn.InputText()),
		Output:        codextrace.ExportText(turn.OutputText()),
		Model:         turn.Model,
		CWD:           turn.CWD,
		Exportable:    true,
		Observations: []Observation{
			{Name: "codex.agent", Type: "agent", Input: codextrace.ExportText(turn.InputText()), Output: codextrace.ExportText(turn.OutputText())},
			{Name: "codex.transcript", Type: "generation", Input: codextrace.ExportText(turn.InputText()), Output: codextrace.ExportText(turn.OutputText())},
		},
	}
	if usage := usageDetails(turn); len(usage) > 0 {
		trace.TokenUsage = usage
	}
	for _, observation := range turn.Observations {
		trace.Observations = append(trace.Observations, normalizeObservation(observation))
	}
	if terminal := codextrace.TerminalObservation(turn); terminal != nil {
		trace.Observations = append(trace.Observations, normalizeObservation(*terminal))
	}
	return trace
}

func normalizeObservation(observation codextrace.Observation) Observation {
	metadata := map[string]any(nil)
	if len(observation.Metadata) > 0 {
		metadata = observation.Metadata
	}
	return Observation{
		Name:     observation.Name,
		Type:     observation.Type,
		Input:    codextrace.ExportText(observation.Input),
		Output:   codextrace.ExportText(observation.Output),
		Metadata: metadata,
	}
}

func usageDetails(turn codextrace.Turn) map[string]int {
	if turn.TokenUsage == nil {
		return nil
	}
	usage := map[string]int{}
	if turn.TokenUsage.InputTokens != 0 {
		usage["input"] = turn.TokenUsage.InputTokens
	}
	if turn.TokenUsage.OutputTokens != 0 {
		usage["output"] = turn.TokenUsage.OutputTokens
	}
	if turn.TokenUsage.TotalTokens != 0 {
		usage["total"] = turn.TokenUsage.TotalTokens
	}
	if turn.TokenUsage.CachedInputTokens != 0 {
		usage["cached_input"] = turn.TokenUsage.CachedInputTokens
	}
	if turn.TokenUsage.ReasoningOutputTokens != 0 {
		usage["reasoning_output"] = turn.TokenUsage.ReasoningOutputTokens
	}
	return usage
}
