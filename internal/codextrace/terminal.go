package codextrace

import (
	"fmt"
	"strings"
)

func addTerminalEntry(turn *Turn, timestamp, label, text string) {
	clean := strings.TrimSpace(text)
	if clean == "" {
		return
	}
	if len(turn.TerminalEntries) > 0 {
		last := turn.TerminalEntries[len(turn.TerminalEntries)-1]
		if last.Label == label && last.Text == clean {
			return
		}
	}
	turn.TerminalEntries = append(turn.TerminalEntries, TerminalEntry{
		Timestamp: firstString(timestamp, firstString(turn.EndTS, turn.StartTS)),
		Label:     label,
		Text:      clean,
	})
}

func addObservation(turn *Turn, name, timestamp, input, output string, metadata map[string]any, observationType string, duration any) {
	if input == "" && output == "" {
		return
	}
	if observationType == "" {
		observationType = "span"
	}
	startNS, endNS := ObservationBounds(firstString(timestamp, firstString(turn.EndTS, turn.StartTS)), duration)
	turn.Observations = append(turn.Observations, Observation{
		Name:            name,
		StartTimeUnixNS: startNS,
		EndTimeUnixNS:   endNS,
		Type:            observationType,
		Input:           input,
		Output:          output,
		Metadata:        metadata,
	})
}

func TerminalObservation(turn Turn) *Observation {
	if len(turn.TerminalEntries) == 0 {
		return nil
	}
	parts := make([]string, 0, len(turn.TerminalEntries))
	for _, entry := range turn.TerminalEntries {
		parts = append(parts, fmt.Sprintf("## %s %s\n%s", entry.Timestamp, entry.Label, entry.Text))
	}
	startNS, _ := ObservationBounds(turn.TerminalEntries[0].Timestamp, nil)
	_, endNS := ObservationBounds(turn.TerminalEntries[len(turn.TerminalEntries)-1].Timestamp, nil)
	return &Observation{
		Name:            "codex.terminal",
		StartTimeUnixNS: startNS,
		EndTimeUnixNS:   endNS,
		Type:            "span",
		Output:          strings.Join(parts, "\n\n"),
		Metadata: map[string]any{
			"event_count": len(turn.TerminalEntries),
			"turn_id":     turn.TurnID,
		},
	}
}
