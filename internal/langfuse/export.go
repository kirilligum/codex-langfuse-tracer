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

func ExportTurn(ctx context.Context, cfg config.LangfuseConfig, turn agenttrace.Turn, environment, serviceName string) (int, error) {
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
	if err := EmitTurn(ctx, turn, environment, serviceName, exporter); err != nil {
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

func EmitTurn(ctx context.Context, turn agenttrace.Turn, environment, serviceName string, exporter sdktrace.SpanExporter) error {
	ids, err := spanIDs(turn)
	if err != nil {
		return err
	}
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
	traceTags := agenttrace.BuildInsightRollup(turn).Tags()
	profile := turn.Profile()

	agentCtx, agent := tracer.Start(ctx, profile.AgentName,
		trace.WithTimestamp(parseTime(turn.StartTS)),
		trace.WithAttributes(turnAttributes(turn, environment, "agent", true, traceTags)...),
	)
	transcriptCtx, transcript := tracer.Start(agentCtx, profile.TranscriptName,
		trace.WithTimestamp(parseTime(turn.StartTS)),
		trace.WithAttributes(transcriptAttributes(turn, environment, traceTags)...),
	)
	transcript.End(trace.WithTimestamp(parseTime(turn.EndTS)))
	_ = transcriptCtx

	for index, observation := range turn.Observations {
		emitObservation(agentCtx, tracer, turn, observation, environment, strconv.Itoa(index), traceTags)
	}
	if terminal := agenttrace.TerminalObservation(turn); terminal != nil {
		emitObservation(agentCtx, tracer, turn, *terminal, environment, "terminal", traceTags)
	}
	agent.End(trace.WithTimestamp(parseTime(turn.EndTS)))
	return provider.Shutdown(ctx)
}

func emitObservation(ctx context.Context, tracer trace.Tracer, turn agenttrace.Turn, observation agenttrace.Observation, environment, key string, traceTags []string) {
	_, span := tracer.Start(ctx, observation.Name,
		trace.WithTimestamp(nsTime(observation.StartTimeUnixNS)),
		trace.WithAttributes(observationAttributes(turn, observation, environment, traceTags)...),
	)
	span.End(trace.WithTimestamp(nsTime(observation.EndTimeUnixNS)))
	_ = key
}

func spanIDs(turn agenttrace.Turn) ([]string, error) {
	profile := turn.Profile()
	ids := []string{
		agenttrace.StableSpanID(profile.AgentSpanPrefix, turn.TraceID, turn.TurnID, ""),
		agenttrace.StableSpanID(profile.TranscriptSpanPrefix, turn.TraceID, turn.TurnID, ""),
	}
	for index := range turn.Observations {
		ids = append(ids, agenttrace.StableSpanID(profile.ObservationPrefix, turn.TraceID, turn.TurnID, strconv.Itoa(index)))
	}
	if agenttrace.TerminalObservation(turn) != nil {
		ids = append(ids, agenttrace.StableSpanID(profile.ObservationPrefix, turn.TraceID, turn.TurnID, "terminal"))
	}
	return ids, nil
}

func baseObservationAttributes(turn agenttrace.Turn, environment, observationType, input, output string) []attribute.KeyValue {
	profile := turn.Profile()
	return []attribute.KeyValue{
		attribute.String("langfuse.trace.name", profile.TraceName),
		attribute.String("langfuse.trace.metadata.provider", profile.Provider),
		attribute.String("langfuse.session.id", turn.SessionID),
		attribute.String("langfuse.environment", environment),
		attribute.String("langfuse.version", buildinfo.Version),
		attribute.String("langfuse.release", buildinfo.Version),
		attribute.String("langfuse.observation.type", observationType),
		attribute.String("langfuse.observation.input", strconv.Quote(agenttrace.ExportText(input))),
		attribute.String("langfuse.observation.output", strconv.Quote(agenttrace.ExportText(output))),
	}
}

func turnAttributes(turn agenttrace.Turn, environment, observationType string, includeTraceIO bool, traceTags []string) []attribute.KeyValue {
	attrs := baseObservationAttributes(turn, environment, observationType, turn.InputText(), turn.OutputText())
	attrs = append(attrs, traceTagAttributes(traceTags)...)
	if includeTraceIO {
		attrs = append(attrs,
			attribute.String("langfuse.trace.input", agenttrace.ExportText(turn.InputText())),
			attribute.String("langfuse.trace.output", agenttrace.ExportText(turn.OutputText())),
		)
	}
	attrs = append(attrs, metadataAttributes(turn)...)
	if includeTraceIO {
		attrs = append(attrs, insightMetadataAttributes(turn)...)
	}
	return attrs
}

func transcriptAttributes(turn agenttrace.Turn, environment string, traceTags []string) []attribute.KeyValue {
	attrs := turnAttributes(turn, environment, "generation", false, traceTags)
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

func observationAttributes(turn agenttrace.Turn, observation agenttrace.Observation, environment string, traceTags []string) []attribute.KeyValue {
	attrs := baseObservationAttributes(turn, environment, observation.Type, observation.Input, observation.Output)
	attrs = append(attrs, traceTagAttributes(traceTags)...)
	attrs = append(attrs, metadataAttributes(turn)...)
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
	attrs := []attribute.KeyValue{
		attribute.String("langfuse.trace.metadata."+profile.MetadataPrefix+"_session_id", turn.SessionID),
		attribute.String("langfuse.trace.metadata."+profile.MetadataPrefix+"_turn_id", turn.TurnID),
		attribute.Bool("langfuse.trace.metadata."+profile.MetadataPrefix+"_transcript_exported", true),
		attribute.String("langfuse.observation.metadata.session_id", turn.SessionID),
		attribute.String("langfuse.observation.metadata.turn_id", turn.TurnID),
	}
	if turn.CWD != "" {
		attrs = append(attrs, attribute.String("langfuse.observation.metadata.cwd", turn.CWD))
	}
	return attrs
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

func FetchTrace(ctx context.Context, cfg config.LangfuseConfig, traceID string) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(cfg.Host, "/")+"/api/public/traces/"+traceID, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", AuthHeader(cfg))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("Langfuse trace fetch failed with HTTP %d", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	return body, nil
}

func VerifyTraceIO(ctx context.Context, cfg config.LangfuseConfig, turn agenttrace.Turn, timeout, interval time.Duration) (bool, bool, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		traceBody, err := FetchTrace(ctx, cfg, turn.TraceID)
		if err != nil {
			lastErr = err
		} else {
			hasInput, hasOutput := traceMatches(traceBody, turn.Profile().TranscriptName, agenttrace.ExportText(turn.InputText()), agenttrace.ExportText(turn.OutputText()))
			if hasInput && hasOutput {
				return true, true, nil
			}
		}
		if time.Now().After(deadline) {
			if lastErr != nil {
				return false, false, lastErr
			}
			return false, false, nil
		}
		select {
		case <-ctx.Done():
			return false, false, ctx.Err()
		case <-time.After(maxDuration(interval, 100*time.Millisecond)):
		}
	}
}

func traceMatches(traceBody map[string]any, transcriptName, input, output string) (bool, bool) {
	hasInput := stringValue(traceBody["input"]) == input
	hasOutput := stringValue(traceBody["output"]) == output
	for _, raw := range sliceValue(traceBody["observations"]) {
		observation := mapValue(raw)
		if stringValue(observation["name"]) != transcriptName {
			continue
		}
		hasInput = hasInput || stringValue(observation["input"]) == input
		hasOutput = hasOutput || stringValue(observation["output"]) == output
	}
	return hasInput, hasOutput
}

func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
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
