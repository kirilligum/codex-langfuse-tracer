package agenttrace

type ProviderProfile struct {
	Provider             string
	TraceName            string
	AgentName            string
	TranscriptName       string
	TerminalName         string
	AgentSpanPrefix      string
	TranscriptSpanPrefix string
	ObservationPrefix    string
	MetadataPrefix       string
	InsightMetadataKey   string
}

var providerProfiles = map[string]ProviderProfile{
	ProviderCodex: {
		Provider:             ProviderCodex,
		TraceName:            "codex.turn.transcript",
		AgentName:            "codex.agent",
		TranscriptName:       "codex.transcript",
		TerminalName:         "codex.terminal",
		AgentSpanPrefix:      "codex-agent",
		TranscriptSpanPrefix: "codex-transcript",
		ObservationPrefix:    "codex-observation",
		MetadataPrefix:       "codex",
		InsightMetadataKey:   "codex_insight",
	},
	ProviderClaude: {
		Provider:             ProviderClaude,
		TraceName:            "claude.turn.transcript",
		AgentName:            "claude.agent",
		TranscriptName:       "claude.transcript",
		TerminalName:         "claude.terminal",
		AgentSpanPrefix:      "claude-agent",
		TranscriptSpanPrefix: "claude-transcript",
		ObservationPrefix:    "claude-observation",
		MetadataPrefix:       "claude",
		InsightMetadataKey:   "claude_insight",
	},
}

func Profile(provider string) ProviderProfile {
	if profile, ok := providerProfiles[provider]; ok {
		return profile
	}
	return providerProfiles[ProviderCodex]
}

func (t Turn) Profile() ProviderProfile {
	return Profile(t.Provider)
}
