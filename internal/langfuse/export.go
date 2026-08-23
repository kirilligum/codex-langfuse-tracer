package langfuse

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kirilligum/codex-langfuse-tracer/internal/agenttrace"
	"github.com/kirilligum/codex-langfuse-tracer/internal/buildinfo"
	"github.com/kirilligum/codex-langfuse-tracer/internal/config"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

func AuthHeader(cfg config.LangfuseConfig) string {
	token := base64.StdEncoding.EncodeToString([]byte(cfg.PublicKey + ":" + cfg.SecretKey))
	return "Basic " + token
}

func ExportSpans(ctx context.Context, cfg config.LangfuseConfig, turn agenttrace.Turn, environment, userID, serviceName string) (int, error) {
	if !turn.Completed || turn.TraceID == "" || turn.InputText() == "" || turn.OutputText() == "" {
		return 0, fmt.Errorf("span export requires a completed turn with non-empty input and output")
	}
	recorder := &statusRecorder{base: http.DefaultTransport}
	exporter, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpointURL(strings.TrimRight(cfg.Host, "/")+"/api/public/otel/v1/traces"),
		otlptracehttp.WithHeaders(map[string]string{
			"Authorization":                AuthHeader(cfg),
			"x-langfuse-ingestion-version": "4",
		}),
		otlptracehttp.WithHTTPClient(&http.Client{Transport: recorder}),
	)
	if err != nil {
		return 0, err
	}
	if err := emitSpans(ctx, turn, environment, userID, serviceName, exporter); err != nil {
		_ = exporter.Shutdown(ctx)
		return 0, err
	}
	if err := exporter.Shutdown(ctx); err != nil {
		return 0, err
	}
	status := recorder.StatusCode()
	if status < 200 || status > 299 {
		return status, fmt.Errorf("Langfuse OTLP export failed with HTTP %d", status)
	}
	return status, nil
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
	if !turn.Completed || turn.TraceID == "" || turn.InputText() == "" || turn.OutputText() == "" {
		return fmt.Errorf("span projection requires a completed turn with non-empty input and output")
	}
	ids := spanIDs(turn)
	res, err := resource.New(ctx, resource.WithAttributes(
		attribute.String("service.name", serviceName),
		attribute.String("langfuse.environment", environment),
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

	parentCtx := ctx
	var agent trace.Span
	traceTags := agenttrace.BuildTraceTags(turn)
	parentCtx, agent = tracer.Start(ctx, profile.AgentName,
		trace.WithTimestamp(parseTime(turn.StartTS)),
		trace.WithAttributes(turnAttributes(turn, environment, userID, traceTags)...),
	)
	transcriptCtx, transcript := tracer.Start(parentCtx, profile.TranscriptName,
		trace.WithTimestamp(parseTime(turn.StartTS)),
		trace.WithAttributes(transcriptAttributes(turn, environment, userID, traceTags)...),
	)
	transcript.End(trace.WithTimestamp(parseTime(turn.EndTS)))
	_ = transcriptCtx
	for _, observation := range turn.Observations {
		emitObservation(parentCtx, tracer, turn, observation, environment, userID, traceTags)
	}
	agent.End(trace.WithTimestamp(parseTime(turn.EndTS)))
	return provider.Shutdown(ctx)
}

func emitObservation(ctx context.Context, tracer trace.Tracer, turn agenttrace.Turn, observation agenttrace.Observation, environment, userID string, traceTags []string) {
	_, span := tracer.Start(ctx, observation.Name,
		trace.WithTimestamp(nsTime(observation.StartTimeUnixNS)),
		trace.WithAttributes(observationAttributes(turn, observation, environment, userID, traceTags)...),
	)
	span.End(trace.WithTimestamp(nsTime(observation.EndTimeUnixNS)))
}

func spanIDs(turn agenttrace.Turn) []string {
	profile := turn.Profile()
	ids := make([]string, 0, len(turn.Observations)+2)
	ids = append(ids,
		agenttrace.StableSpanID(profile.AgentSpanPrefix, turn.TraceID, turn.TurnID, ""),
		agenttrace.StableSpanID(profile.TranscriptSpanPrefix, turn.TraceID, turn.TurnID, ""),
	)
	for index := range turn.Observations {
		ids = append(ids, agenttrace.StableSpanID(profile.ObservationPrefix, turn.TraceID, turn.TurnID, strconv.Itoa(index)))
	}
	return ids
}

func baseObservationAttributes(turn agenttrace.Turn, environment, userID, observationType, input, output string) []attribute.KeyValue {
	profile := turn.Profile()
	attrs := []attribute.KeyValue{
		attribute.String("langfuse.trace.name", profile.TraceName),
		attribute.String("langfuse.trace.metadata.provider", profile.Provider),
		attribute.String("langfuse.session.id", turn.SessionID),
		attribute.String("langfuse.environment", environment),
		attribute.String("langfuse.version", buildinfo.Version),
		attribute.String("langfuse.release", buildinfo.Version),
		attribute.String("langfuse.observation.type", observationType),
		attribute.String("langfuse.user.id", userID),
	}
	if value := agenttrace.ExportText(input); value != "" {
		attrs = append(attrs, attribute.String("langfuse.observation.input", strconv.Quote(value)))
	}
	if value := agenttrace.ExportText(output); value != "" {
		attrs = append(attrs, attribute.String("langfuse.observation.output", strconv.Quote(value)))
	}
	return attrs
}

func turnAttributes(turn agenttrace.Turn, environment, userID string, traceTags []string) []attribute.KeyValue {
	return projectionAttributes(turn, environment, userID, "agent", turn.InputText(), turn.OutputText(), traceTags, true)
}

func transcriptAttributes(turn agenttrace.Turn, environment, userID string, traceTags []string) []attribute.KeyValue {
	attrs := projectionAttributes(turn, environment, userID, "generation", turn.InputText(), turn.OutputText(), traceTags, false)
	if turn.Model != "" {
		attrs = append(attrs, attribute.String("langfuse.observation.model.name", turn.Model))
	}
	var usage map[string]int
	if turn.TokenUsage != nil {
		usage = turn.TokenUsage.LangfuseUsageDetails()
	}
	if len(usage) > 0 {
		attrs = append(attrs, attribute.String("langfuse.observation.usage_details", jsonString(usage)))
	}
	return attrs
}

func observationAttributes(turn agenttrace.Turn, observation agenttrace.Observation, environment, userID string, traceTags []string) []attribute.KeyValue {
	attrs := projectionAttributes(turn, environment, userID, observation.Type, observation.Input, observation.Output, traceTags, false)
	if len(observation.Metadata) > 0 {
		attrs = append(attrs, attribute.String("langfuse.observation.metadata", jsonString(observation.Metadata)))
	}
	if statusMessage := failedObservationStatusMessage(observation); statusMessage != "" {
		attrs = append(attrs,
			attribute.String("langfuse.observation.level", "ERROR"),
			attribute.String("langfuse.observation.status_message", statusMessage),
		)
	}
	return attrs
}

func projectionAttributes(turn agenttrace.Turn, environment, userID, observationType, input, output string, traceTags []string, includeInsights bool) []attribute.KeyValue {
	attrs := baseObservationAttributes(turn, environment, userID, observationType, input, output)
	attrs = append(attrs, traceTagAttributes(traceTags)...)
	attrs = append(attrs, metadataAttributes(turn)...)
	if includeInsights {
		attrs = append(attrs, insightMetadataAttributes(turn)...)
	}
	return attrs
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

func metadataAttributes(turn agenttrace.Turn) []attribute.KeyValue {
	profile := turn.Profile()
	attrs := stableMetadataAttributes(turn)
	attrs = append(attrs,
		attribute.Bool("langfuse.trace.metadata."+profile.MetadataPrefix+"_transcript_exported", true),
	)
	if turn.CWD != "" {
		attrs = append(attrs, attribute.String("langfuse.observation.metadata.cwd", turn.CWD))
	}
	if turn.GitBranch != "" {
		attrs = append(attrs,
			attribute.String("langfuse.trace.metadata.git_branch", turn.GitBranch),
			attribute.String("langfuse.observation.metadata.git_branch", turn.GitBranch),
		)
	}
	return attrs
}

func stableMetadataAttributes(turn agenttrace.Turn) []attribute.KeyValue {
	profile := turn.Profile()
	return []attribute.KeyValue{
		attribute.String("langfuse.trace.metadata."+profile.MetadataPrefix+"_session_id", turn.SessionID),
		attribute.String("langfuse.trace.metadata."+profile.MetadataPrefix+"_turn_id", turn.TurnID),
		attribute.String("langfuse.observation.metadata.session_id", turn.SessionID),
		attribute.String("langfuse.observation.metadata.turn_id", turn.TurnID),
	}
}

func traceTagAttributes(tags []string) []attribute.KeyValue {
	if len(tags) == 0 {
		return nil
	}
	return []attribute.KeyValue{attribute.StringSlice("langfuse.trace.tags", tags)}
}

func insightMetadataAttributes(turn agenttrace.Turn) []attribute.KeyValue {
	metadata := agenttrace.BuildInsightRollup(turn).Metadata()
	profile := turn.Profile()
	keys := make([]string, 0, len(metadata))
	for key := range metadata {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	attrs := make([]attribute.KeyValue, 0, len(keys))
	for _, key := range keys {
		attrKey := "langfuse.trace.metadata." + profile.InsightMetadataKey + "." + key
		switch value := metadata[key].(type) {
		case int:
			attrs = append(attrs, attribute.Int(attrKey, value))
		case string:
			attrs = append(attrs, attribute.String(attrKey, value))
		default:
			attrs = append(attrs, attribute.String(attrKey, jsonString(value)))
		}
	}
	return attrs
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
	return fmt.Sprint(value)
}

func mapValue(value any) map[string]any {
	if typed, ok := value.(map[string]any); ok {
		return typed
	}
	return map[string]any{}
}

func sliceValue(value any) []any {
	if typed, ok := value.([]any); ok {
		return typed
	}
	return nil
}
