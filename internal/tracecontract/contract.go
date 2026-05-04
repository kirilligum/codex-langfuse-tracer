package tracecontract

import "github.com/kirilligum/codex-langfuse-tracer/internal/agenttrace"

type Trace struct {
	SchemaVersion int            `json:"schema_version"`
	Name          string         `json:"name"`
	Provider      string         `json:"provider,omitempty"`
	TraceID       string         `json:"trace_id,omitempty"`
	SessionID     string         `json:"session_id,omitempty"`
	TurnID        string         `json:"turn_id,omitempty"`
	Input         string         `json:"input,omitempty"`
	Output        string         `json:"output,omitempty"`
	Model         string         `json:"model,omitempty"`
	CWD           string         `json:"cwd,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
	Tags          []string       `json:"tags,omitempty"`
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

func FromTurn(turn agenttrace.Turn) Trace {
	rollup := agenttrace.BuildInsightRollup(turn)
	profile := turn.Profile()
	trace := Trace{
		SchemaVersion: 1,
		Name:          profile.TraceName,
		Provider:      profile.Provider,
		TraceID:       turn.TraceID,
		SessionID:     turn.SessionID,
		TurnID:        turn.TurnID,
		Input:         agenttrace.ExportText(turn.InputText()),
		Output:        agenttrace.ExportText(turn.OutputText()),
		Model:         turn.Model,
		CWD:           turn.CWD,
		Metadata:      rollup.Metadata(),
		Tags:          rollup.Tags(),
		Exportable:    true,
		Observations: []Observation{
			{Name: profile.AgentName, Type: "agent", Input: agenttrace.ExportText(turn.InputText()), Output: agenttrace.ExportText(turn.OutputText())},
			{Name: profile.TranscriptName, Type: "generation", Input: agenttrace.ExportText(turn.InputText()), Output: agenttrace.ExportText(turn.OutputText())},
		},
	}
	if turn.TokenUsage != nil {
		if usage := turn.TokenUsage.LangfuseUsageDetails(); len(usage) > 0 {
			trace.TokenUsage = usage
		}
	}
	for _, observation := range turn.Observations {
		trace.Observations = append(trace.Observations, normalizeObservation(observation))
	}
	if terminal := agenttrace.TerminalObservation(turn); terminal != nil {
		trace.Observations = append(trace.Observations, normalizeObservation(*terminal))
	}
	return trace
}

func normalizeObservation(observation agenttrace.Observation) Observation {
	metadata := map[string]any(nil)
	if len(observation.Metadata) > 0 {
		metadata = observation.Metadata
	}
	return Observation{
		Name:     observation.Name,
		Type:     observation.Type,
		Input:    agenttrace.ExportText(observation.Input),
		Output:   agenttrace.ExportText(observation.Output),
		Metadata: metadata,
	}
}
