package watch

const memoryGateEOFTurnTraceID = "trace-new-at-eof"

const (
	memoryGateWorkerEnv         = "CODEX_LANGFUSE_MEMORY_GATE_WORKER"
	memoryGateLegacyWorkerEnv   = "CODEX_LANGFUSE_MEMORY_GATE_LEGACY_WORKER"
	memoryGateBaselineBinaryEnv = "CODEX_LANGFUSE_MEMORY_GATE_BASELINE_BINARY"
	memoryGateSpecEnv           = "CODEX_LANGFUSE_MEMORY_GATE_SPEC"
	memoryGateRootEnv           = "CODEX_LANGFUSE_MEMORY_GATE_ROOT"
	memoryGateStateEnv          = "CODEX_LANGFUSE_MEMORY_GATE_STATE"
	memoryGateTimeEnv           = "CODEX_LANGFUSE_MEMORY_GATE_TIME"
)

type memoryGateSpec struct {
	Name                  string `json:"name"`
	TargetBytes           int64  `json:"target_bytes"`
	MaxRecordBytes        int64  `json:"max_record_bytes,omitempty"`
	ExpectedSpanCalls     int    `json:"expected_span_calls"`
	ExpectedScoreCalls    int    `json:"expected_score_calls"`
	ExpectedSourceTurns   int    `json:"expected_source_turns"`
	ExpectedSelectedTurns int    `json:"expected_selected_turns"`
	ExpectedTraceID       string `json:"expected_trace_id,omitempty"`
	ExpectedObsPerTurn    int    `json:"expected_observations_per_turn,omitempty"`
	MinInputBytes         int    `json:"min_input_bytes,omitempty"`
	MinOutputBytes        int    `json:"min_output_bytes,omitempty"`
	CorruptSibling        bool   `json:"corrupt_sibling,omitempty"`
}
