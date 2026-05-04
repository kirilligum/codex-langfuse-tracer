package agenttrace

func completeFixtureTurn() Turn {
	return Turn{
		Provider:       ProviderCodex,
		SessionID:      "sess-complete",
		TurnID:         "turn-1",
		TraceID:        StableTraceID(ProviderCodex, "sess-complete", "turn-1"),
		StartTS:        "2026-05-01T10:00:00Z",
		EndTS:          "2026-05-01T10:00:10Z",
		UserMessages:   []string{"Summarize the repo and run checks"},
		AssistantTexts: []string{"Checks passed."},
		Completed:      true,
		TerminalEntries: []TerminalEntry{
			{Timestamp: "2026-05-01T10:00:01Z", Label: "user", Text: "Summarize the repo and run checks"},
			{Timestamp: "2026-05-01T10:00:09Z", Label: "assistant.final", Text: "Checks passed."},
		},
		Observations: []Observation{
			{
				Name:   ToolObservationName(ProviderCodex, ToolFamilyCommand),
				Type:   "tool",
				Input:  "printf 'ok\n'",
				Output: "ok",
				Metadata: map[string]any{
					"command_kind": "other",
					"failure_type": "none",
					"status":       "completed",
				},
			},
			{
				Name: ToolObservationName(ProviderCodex, ToolFamilyFileChange),
				Type: "tool",
				Metadata: map[string]any{
					"changed_files":      []string{"README.md"},
					"changed_file_count": 1,
				},
			},
			{
				Name: ToolObservationName(ProviderCodex, ToolFamilyMCP),
				Type: "tool",
				Metadata: map[string]any{
					"mcp_server": "github",
					"mcp_tool":   "issues/list",
				},
			},
			{Name: ToolObservationName(ProviderCodex, ToolFamilyWebSearch), Type: "tool", Metadata: map[string]any{}},
			{Name: ToolObservationName(ProviderCodex, ToolFamilyToolSearch), Type: "tool", Metadata: map[string]any{}},
		},
	}
}
