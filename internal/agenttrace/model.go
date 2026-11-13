package agenttrace

import (
	"crypto/sha256"
	"fmt"
	"strings"
)

type Turn struct {
	Provider         string
	SessionID        string
	TurnID           string
	TraceID          string
	StartTS          string
	EndTS            string
	CWD              string
	GitBranch        string
	Model            string
	UserMessages     []string
	AssistantTexts   []string
	TokenUsage       *TokenUsage
	Completed        bool
	Observations     []Observation
	ModelCalls       []ModelCall `json:"-"`
	Repository       string      `json:"-"`
	ExportDelta      bool        `json:"-"`
	FirstObservation int         `json:"-"`
	FirstModelCall   int         `json:"-"`
	InputEmitted     bool        `json:"-"`
	// Authorship and model-role context are independent. These source details
	// belong to capture state, not the legacy serialized turn contract.
	SourceMessageIDs   []string `json:"-"`
	ModelMessages      []string `json:"-"`
	InputOrigin        string   `json:"-"`
	LegacyUserMessages []string `json:"-"`
	Request            *Request `json:"-"`
	RequestRoot        bool     `json:"-"`
	RequestInputOnly   bool     `json:"-"`
	ExecutionTrigger   string   `json:"-"`
	AfterCompletion    bool     `json:"-"`
	Aborted            bool     `json:"-"`
	FirstHumanInput    int      `json:"-"`
	PendingToolCount   int      `json:"-"`
	ExecutionEmitted   bool     `json:"-"`
}

// Request describes the source-observed human operation independently of a
// runtime execution. Its root is a separate export work item, using the same
// watcher/checkpoint path as executions rather than holding completed bodies.
type Request struct {
	ID            string
	TraceID       string
	StartTS       string
	EndTS         string
	FinishedAt    string
	State         string
	Inputs        []HumanInput
	LateInputs    []HumanInput
	Output        string
	GoalKey       string
	GoalStatus    string
	ExecutionIDs  []string
	HasHumanInput bool
}

type HumanInput struct {
	ID        string
	Text      string
	Timestamp string
}

// ModelCall describes a model step reconstructed from the visible transcript.
// It is separate from the turn transcript so aggregate usage is not counted twice.
type ModelCall struct {
	ID      string
	StartTS string
	EndTS   string
	Model   string
	Input   string
	Output  string
	Usage   *TokenUsage
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

func (u TokenUsage) CanonicalUsageDetails() map[string]int {
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

// TelemetryTraceID is the physical Laminar trace. TraceID remains the stable
// work-item identity used by existing watcher checkpoints.
func (t Turn) TelemetryTraceID() string {
	if t.Request != nil {
		return t.Request.TraceID
	}
	return t.TraceID
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
		if turn.Completed && turn.TraceID != "" && (turn.Request != nil || (turn.InputText() != "" && turn.OutputText() != "")) {
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
