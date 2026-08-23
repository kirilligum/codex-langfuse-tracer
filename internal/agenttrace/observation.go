package agenttrace

func AddObservation(turn *Turn, name, timestamp, input, output string, metadata map[string]any, observationType string, duration any) {
	if input == "" && output == "" {
		return
	}
	if observationType == "" {
		observationType = "span"
	}
	startNS, endNS := ObservationBounds(StringOr(timestamp, StringOr(turn.EndTS, turn.StartTS)), duration)
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
