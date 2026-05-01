package buildinfo

const (
	Version                    = "0.1.0"
	ScopeName                  = "codex-transcript-exporter"
	DefaultEnvironment         = "default"
	DefaultServiceName         = "codex_transcript_exporter"
	DefaultInitialLookbackSecs = 300
	DefaultPollIntervalSeconds = 5.0
	DefaultStateFileName       = "langfuse-export-state.json"
	MaxFieldChars              = 50000
	TraceName                  = "codex.turn.transcript"
	InstalledBinaryName        = "codex-langfuse-exporter"
	InstalledServiceName       = "codex-langfuse-watch.service"
)
