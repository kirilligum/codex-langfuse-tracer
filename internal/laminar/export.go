package laminar

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kirilligum/codex-langfuse-tracer/internal/agenttrace"
	"github.com/kirilligum/codex-langfuse-tracer/internal/buildinfo"
	"github.com/kirilligum/codex-langfuse-tracer/internal/config"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"
)

const (
	laminarMetadataPrefix = "lmnr.association.properties.metadata."
	spanTypeAttribute     = "lmnr.span.type"
	spanInputAttribute    = "lmnr.span.input"
	spanOutputAttribute   = "lmnr.span.output"
)

// ExportSpans submits one complete turn to the host-local authenticated OTLP
// receiver. Its HTTP success means the persistent Collector queue accepted the
// batch; backend delivery is verified separately from the Collector metrics and
// Laminar project data.
func ExportSpans(ctx context.Context, cfg config.LaminarConfig, turn agenttrace.Turn, environment, userID, serviceName string) (int, error) {
	if cfg.BaseURL == "" || cfg.Token == "" {
		return 0, fmt.Errorf("Laminar receiver URL and token are required")
	}
	endpoint := strings.TrimRight(cfg.BaseURL, "/") + "/v1/traces"
	recorder := &statusRecorder{base: http.DefaultTransport}
	exporter, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpointURL(endpoint),
		otlptracehttp.WithTimeout(10*time.Second),
		otlptracehttp.WithHeaders(map[string]string{
			"Authorization": "Bearer " + cfg.Token,
		}),
		otlptracehttp.WithHTTPClient(&http.Client{Transport: recorder}),
	)
	if err != nil {
		return 0, err
	}
	if err := EmitSpans(ctx, turn, environment, userID, serviceName, exporter); err != nil {
		_ = exporter.Shutdown(ctx)
		return 0, err
	}
	if err := exporter.Shutdown(ctx); err != nil {
		return 0, err
	}
	status := recorder.StatusCode()
	if status < 200 || status > 299 {
		return status, fmt.Errorf("Laminar Collector rejected OTLP batch with HTTP %d", status)
	}
	return status, nil
}

// CheckReceiver validates the configured receiver path and bearer token with a
// valid empty OTLP request. It does not create a span or add an item to the
// persistent export queue.
func CheckReceiver(ctx context.Context, cfg config.LaminarConfig) (int, error) {
	if cfg.BaseURL == "" || cfg.Token == "" {
		return 0, fmt.Errorf("Laminar receiver URL and token are required")
	}
	body, err := proto.Marshal(&collectortrace.ExportTraceServiceRequest{})
	if err != nil {
		return 0, fmt.Errorf("encode empty OTLP receiver check: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(cfg.BaseURL, "/")+"/v1/traces", bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("create Laminar receiver check: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+cfg.Token)
	request.Header.Set("Content-Type", "application/x-protobuf")
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return 0, fmt.Errorf("Laminar receiver check failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return response.StatusCode, fmt.Errorf("Laminar receiver rejected check with HTTP %d", response.StatusCode)
	}
	return response.StatusCode, nil
}

type statusRecorder struct {
	base       http.RoundTripper
	mu         sync.Mutex
	statusCode int
}

func (s *statusRecorder) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := s.base.RoundTrip(req)
	if resp != nil {
		s.mu.Lock()
		s.statusCode = resp.StatusCode
		s.mu.Unlock()
	}
	return resp, err
}

func (s *statusRecorder) StatusCode() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.statusCode
}

func EmitSpans(ctx context.Context, turn agenttrace.Turn, environment, userID, serviceName string, exporter sdktrace.SpanExporter) error {
	return emitSpans(ctx, turn, environment, userID, serviceName, exporter)
}

func emitSpans(ctx context.Context, turn agenttrace.Turn, environment, userID, serviceName string, exporter sdktrace.SpanExporter) error {
	if (!turn.Completed && !turn.ExportDelta) || turn.TraceID == "" || turn.InputText() == "" || (turn.Completed && turn.OutputText() == "") {
		return fmt.Errorf("span projection requires a completed turn with non-empty input and output")
	}
	if environment == "" || userID == "" || serviceName == "" {
		return fmt.Errorf("span projection requires environment, user id, and service name")
	}
	if turn.ExportDelta && (turn.FirstObservation < 0 || turn.FirstObservation > len(turn.Observations) || turn.FirstModelCall < 0 || turn.FirstModelCall > len(turn.ModelCalls)) {
		return fmt.Errorf("span projection has an invalid completed-step checkpoint")
	}
	ids := spanIDs(turn)
	traceID, err := trace.TraceIDFromHex(turn.TraceID)
	if err != nil {
		return err
	}
	rootID, err := trace.SpanIDFromHex(agenttrace.StableSpanID(turn.Profile().AgentSpanPrefix, turn.TraceID, turn.TurnID, ""))
	if err != nil {
		return err
	}
	res, err := resource.New(ctx, resource.WithAttributes(
		attribute.String("service.name", serviceName),
		attribute.String("service.version", buildinfo.Version),
		attribute.String("deployment.environment.name", environment),
	))
	if err != nil {
		return err
	}
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithIDGenerator(newFixedIDGenerator(turn.TraceID, ids)),
		sdktrace.WithBatcher(exporter),
	)
	tracer := provider.Tracer(buildinfo.ScopeName, trace.WithInstrumentationVersion(buildinfo.Version))
	profile := turn.Profile()
	traceTags := agenttrace.BuildTraceTags(turn)

	parentCtx := trace.ContextWithRemoteSpanContext(ctx, trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: rootID, TraceFlags: trace.FlagsSampled, Remote: true}))
	var agent trace.Span
	if turn.Completed {
		parentCtx, agent = tracer.Start(ctx, profile.AgentName,
			trace.WithTimestamp(parseTime(turn.StartTS)),
			trace.WithAttributes(rootAttributes(turn, environment, userID, traceTags)...),
		)
		setSpanPath(agent, rootID, profile.AgentName, profile.AgentName)
		_, transcript := tracer.Start(parentCtx, profile.TranscriptName,
			trace.WithTimestamp(parseTime(turn.StartTS)),
			trace.WithAttributes(transcriptAttributes(turn, environment, userID, traceTags)...),
		)
		setSpanPath(transcript, rootID, profile.AgentName, profile.TranscriptName)
		transcript.End(trace.WithTimestamp(parseTime(turn.EndTS)))
	}
	firstObservation, firstCall := 0, 0
	if turn.ExportDelta {
		firstObservation, firstCall = turn.FirstObservation, turn.FirstModelCall
	}
	for _, observation := range turn.Observations[firstObservation:] {
		emitObservation(parentCtx, tracer, turn, observation, environment, userID, traceTags)
	}
	for index, call := range turn.ModelCalls[firstCall:] {
		attrs := commonAttributes(turn, environment, userID, "LLM", call.Input, call.Output, traceTags)
		attrs = append(attrs, attribute.String("gen_ai.request.model", call.Model), attribute.String("gen_ai.system", modelProvider(profile.Provider)), attribute.Int(laminarMetadataPrefix+"model_step", firstCall+index+1), attribute.String(laminarMetadataPrefix+"capture", "transcript_model_step"))
		attrs = append(attrs, usageAttributes(call.Usage)...)
		name := fmt.Sprintf("%s.model.call.%d", profile.Provider, firstCall+index+1)
		_, span := tracer.Start(parentCtx, name, trace.WithTimestamp(parseTime(call.StartTS)), trace.WithAttributes(attrs...))
		setSpanPath(span, rootID, profile.AgentName, name)
		span.End(trace.WithTimestamp(parseTime(call.EndTS)))
	}
	if agent != nil {
		agent.End(trace.WithTimestamp(parseTime(turn.EndTS)))
	}
	return provider.Shutdown(ctx)
}

func emitObservation(ctx context.Context, tracer trace.Tracer, turn agenttrace.Turn, observation agenttrace.Observation, environment, userID string, traceTags []string) {
	options := []trace.SpanStartOption{
		trace.WithTimestamp(nsTime(observation.StartTimeUnixNS)),
		trace.WithAttributes(observationAttributes(turn, observation, environment, userID, traceTags)...),
	}
	_, span := tracer.Start(ctx, observation.Name, options...)
	setSpanPath(span, trace.SpanContextFromContext(ctx).SpanID(), turn.Profile().AgentName, observation.Name)
	if status := failedObservationStatusMessage(observation); status != "" {
		span.SetStatus(codes.Error, status)
	}
	span.End(trace.WithTimestamp(nsTime(observation.EndTimeUnixNS)))
}

func setSpanPath(span trace.Span, rootID trace.SpanID, rootName, name string) {
	uuid := func(id trace.SpanID) string {
		hex := id.String()
		return "00000000-0000-0000-" + hex[:4] + "-" + hex[4:]
	}
	ids, names := []string{uuid(rootID)}, []string{rootName}
	if span.SpanContext().SpanID() != rootID {
		ids, names = append(ids, uuid(span.SpanContext().SpanID())), append(names, name)
	}
	span.SetAttributes(attribute.StringSlice("lmnr.span.ids_path", ids), attribute.StringSlice("lmnr.span.path", names))
}

func spanIDs(turn agenttrace.Turn) []string {
	profile := turn.Profile()
	ids := make([]string, 0, len(turn.Observations)+2)
	if turn.Completed {
		ids = append(ids,
			agenttrace.StableSpanID(profile.AgentSpanPrefix, turn.TraceID, turn.TurnID, ""),
			agenttrace.StableSpanID(profile.TranscriptSpanPrefix, turn.TraceID, turn.TurnID, ""),
		)
	}
	firstObservation, firstCall := 0, 0
	if turn.ExportDelta {
		firstObservation, firstCall = turn.FirstObservation, turn.FirstModelCall
	}
	for index := firstObservation; index < len(turn.Observations); index++ {
		ids = append(ids, agenttrace.StableSpanID(profile.ObservationPrefix, turn.TraceID, turn.TurnID, strconv.Itoa(index)))
	}
	for index := firstCall; index < len(turn.ModelCalls); index++ {
		ids = append(ids, agenttrace.StableSpanID(turn.Profile().Provider+"-model-call", turn.TraceID, turn.TurnID, strconv.Itoa(index)))
	}
	return ids
}

func rootAttributes(turn agenttrace.Turn, environment, userID string, tags []string) []attribute.KeyValue {
	attrs := commonAttributes(turn, environment, userID, "DEFAULT", turn.InputText(), turn.OutputText(), tags)
	rollup := agenttrace.BuildInsightRollup(turn)
	attrs = append(attrs, attribute.Int(laminarMetadataPrefix+"tool_count", rollup.ToolCount), attribute.Int(laminarMetadataPrefix+"command_count", rollup.CommandCount), attribute.Int(laminarMetadataPrefix+"failed_command_count", rollup.FailedCommandCount), attribute.Int(laminarMetadataPrefix+"changed_file_count", rollup.ChangedFileCount), attribute.Int(laminarMetadataPrefix+"verification_command_count", rollup.VerificationCommandCount), attribute.String(laminarMetadataPrefix+"verification_status", rollup.VerificationStatus), attribute.Int(laminarMetadataPrefix+"model_step_count", len(turn.ModelCalls)))
	attrs = append(attrs, attribute.String("gen_ai.operation.name", "invoke_agent"))
	attrs = append(attrs, insightMetadataAttributes(turn)...)
	attrs = append(attrs, deterministicScoreAttributes(turn)...)
	return attrs
}

func transcriptAttributes(turn agenttrace.Turn, environment, userID string, tags []string) []attribute.KeyValue {
	spanType := "LLM"
	if len(turn.ModelCalls) > 0 {
		spanType = "DEFAULT"
	}
	attrs := commonAttributes(turn, environment, userID, spanType, turn.InputText(), turn.OutputText(), tags)
	attrs = append(attrs,
		attribute.String("gen_ai.operation.name", "chat"),
		attribute.String("gen_ai.system", modelProvider(turn.Profile().Provider)),
	)
	if turn.Model != "" {
		attrs = append(attrs, attribute.String("gen_ai.request.model", turn.Model))
	}
	if len(turn.ModelCalls) == 0 {
		attrs = append(attrs, usageAttributes(turn.TokenUsage)...)
	}
	return attrs
}

func usageAttributes(usage *agenttrace.TokenUsage) []attribute.KeyValue {
	attrs := []attribute.KeyValue{}
	if usage != nil {
		if usage.InputTokens > 0 {
			attrs = append(attrs, attribute.Int("gen_ai.usage.input_tokens", usage.InputTokens))
		}
		if usage.OutputTokens > 0 {
			attrs = append(attrs, attribute.Int("gen_ai.usage.output_tokens", usage.OutputTokens))
		}
		cacheReadTokens := usage.CacheReadInputTokens + usage.CachedInputTokens
		if cacheReadTokens > 0 {
			attrs = append(attrs, attribute.Int("gen_ai.usage.cache_read.input_tokens", cacheReadTokens))
		}
		if usage.CacheCreationInputTokens > 0 {
			attrs = append(attrs, attribute.Int("gen_ai.usage.cache_creation.input_tokens", usage.CacheCreationInputTokens))
		}
		if usage.ReasoningOutputTokens > 0 {
			attrs = append(attrs, attribute.Int("gen_ai.usage.reasoning_tokens", usage.ReasoningOutputTokens))
		}
	}
	return attrs
}

func observationAttributes(turn agenttrace.Turn, observation agenttrace.Observation, environment, userID string, tags []string) []attribute.KeyValue {
	spanType := "DEFAULT"
	if observation.Type == "tool" || strings.Contains(observation.Name, ".tool.") {
		spanType = "TOOL"
	}
	attrs := commonAttributes(turn, environment, userID, spanType, observation.Input, observation.Output, tags)
	if failedObservationStatusMessage(observation) != "" {
		attrs = append(attrs, attribute.String("error.type", failedObservationStatusMessage(observation)))
	}
	keys := make([]string, 0, len(observation.Metadata))
	for key := range observation.Metadata {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		attrs = appendJSONAttribute(attrs, laminarMetadataPrefix+"observation_"+sanitizeMetadataKey(key), observation.Metadata[key])
	}
	return attrs
}

func commonAttributes(turn agenttrace.Turn, environment, userID, spanType, input, output string, tags []string) []attribute.KeyValue {
	profile := turn.Profile()
	attrs := []attribute.KeyValue{
		attribute.String(spanTypeAttribute, spanType),
		attribute.String("lmnr.association.properties.session_id", turn.SessionID),
		attribute.String("lmnr.association.properties.user_id", userID),
		attribute.String("lmnr.association.properties.metadata.environment", environment),
		attribute.String(laminarMetadataPrefix+"provider", profile.Provider),
		attribute.String(laminarMetadataPrefix+"turn_id", turn.TurnID),
		attribute.String(laminarMetadataPrefix+"version", buildinfo.Version),
	}
	hostname, _ := os.Hostname()
	attrs = append(attrs, attribute.String(laminarMetadataPrefix+"hostname", hostname))
	if turn.Repository != "" {
		attrs = append(attrs, attribute.String(laminarMetadataPrefix+"repo", turn.Repository))
	}
	if len(tags) > 0 {
		attrs = append(attrs, attribute.StringSlice("lmnr.association.properties.tags", tags))
	}
	if turn.CWD != "" {
		attrs = append(attrs, attribute.String(laminarMetadataPrefix+"cwd", turn.CWD))
	}
	if turn.GitBranch != "" {
		attrs = append(attrs, attribute.String(laminarMetadataPrefix+"git_branch", turn.GitBranch))
	}
	if text := agenttrace.ExportText(input); text != "" {
		attrs = append(attrs, attribute.String(spanInputAttribute, jsonString(text)))
	}
	if text := agenttrace.ExportText(output); text != "" {
		attrs = append(attrs, attribute.String(spanOutputAttribute, jsonString(text)))
	}
	return attrs
}

func insightMetadataAttributes(turn agenttrace.Turn) []attribute.KeyValue {
	metadata := agenttrace.BuildInsightRollup(turn).Metadata()
	keys := make([]string, 0, len(metadata))
	for key := range metadata {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	attrs := make([]attribute.KeyValue, 0, len(keys))
	for _, key := range keys {
		attrs = appendJSONAttribute(attrs, laminarMetadataPrefix+turn.Profile().MetadataPrefix+"_insight_"+sanitizeMetadataKey(key), metadata[key])
	}
	return attrs
}

func deterministicScoreAttributes(turn agenttrace.Turn) []attribute.KeyValue {
	scores := agenttrace.BuildDeterministicScores(turn)
	attrs := make([]attribute.KeyValue, 0, len(scores)*3)
	for _, score := range scores {
		key := laminarMetadataPrefix + "codex_score_" + sanitizeMetadataKey(score.Name)
		value := score.Value
		if score.DataType == agenttrace.ScoreDataTypeBoolean {
			numeric, ok := value.(int)
			if !ok || (numeric != 0 && numeric != 1) {
				continue
			}
			value = numeric == 1
		}
		attrs = appendJSONAttribute(attrs, key, value)
		attrs = append(attrs, attribute.String(key+"_data_type", score.DataType))
		attrs = append(attrs, attribute.String(key+"_comment", score.Comment))
	}
	return attrs
}

func appendJSONAttribute(attrs []attribute.KeyValue, key string, value any) []attribute.KeyValue {
	switch value := value.(type) {
	case bool:
		return append(attrs, attribute.Bool(key, value))
	case int:
		return append(attrs, attribute.Int(key, value))
	case int64:
		return append(attrs, attribute.Int64(key, value))
	case float64:
		return append(attrs, attribute.Float64(key, value))
	case string:
		return append(attrs, attribute.String(key, value))
	case []string:
		return append(attrs, attribute.StringSlice(key, value))
	default:
		return append(attrs, attribute.String(key, jsonString(value)))
	}
}

func sanitizeMetadataKey(value string) string {
	var result strings.Builder
	for _, r := range strings.ToLower(value) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' {
			result.WriteRune(r)
		} else {
			result.WriteByte('_')
		}
	}
	return strings.Trim(result.String(), "_")
}

func modelProvider(provider string) string {
	if provider == agenttrace.ProviderClaude {
		return "anthropic"
	}
	return "openai"
}

func failedObservationStatusMessage(observation agenttrace.Observation) string {
	if observation.Type != "tool" {
		return ""
	}
	failureType := stringValue(observation.Metadata["failure_type"])
	if failureType == "nonzero_exit" || failureType == "timeout" {
		return failureType
	}
	return ""
}

func jsonString(value any) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}

func parseTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Unix(0, 0).UTC()
	}
	return parsed
}

func nsTime(value string) time.Time {
	ns, _ := strconv.ParseInt(value, 10, 64)
	return time.Unix(0, ns).UTC()
}

func stringValue(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}
