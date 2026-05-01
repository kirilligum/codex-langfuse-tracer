package langfuse

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/kirilligum/codex-langfuse-tracer/internal/buildinfo"
	"github.com/kirilligum/codex-langfuse-tracer/internal/codextrace"
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

func ExportTurn(ctx context.Context, cfg config.LangfuseConfig, turn codextrace.Turn, environment, serviceName string) (int, error) {
	exporter, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpointURL(strings.TrimRight(cfg.Host, "/")+"/api/public/otel/v1/traces"),
		otlptracehttp.WithHeaders(map[string]string{
			"Authorization":                AuthHeader(cfg),
			"x-langfuse-ingestion-version": "4",
		}),
	)
	if err != nil {
		return 0, err
	}
	defer func() { _ = exporter.Shutdown(ctx) }()
	if err := EmitTurn(ctx, turn, environment, serviceName, exporter); err != nil {
		return 0, err
	}
	return http.StatusOK, nil
}

func EmitTurn(ctx context.Context, turn codextrace.Turn, environment, serviceName string, exporter sdktrace.SpanExporter) error {
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
	defer func() { _ = provider.Shutdown(ctx) }()
	tracer := provider.Tracer(buildinfo.ScopeName, trace.WithInstrumentationVersion(buildinfo.Version))

	agentCtx, agent := tracer.Start(ctx, "codex.agent",
		trace.WithTimestamp(parseTime(turn.StartTS)),
		trace.WithAttributes(turnAttributes(turn, environment, "agent", true)...),
	)
	transcriptCtx, transcript := tracer.Start(agentCtx, "codex.transcript",
		trace.WithTimestamp(parseTime(turn.StartTS)),
		trace.WithAttributes(transcriptAttributes(turn, environment)...),
	)
	transcript.End(trace.WithTimestamp(parseTime(turn.EndTS)))
	_ = transcriptCtx

	for index, observation := range turn.Observations {
		emitObservation(agentCtx, tracer, turn, observation, environment, strconv.Itoa(index))
	}
	if terminal := codextrace.TerminalObservation(turn); terminal != nil {
		emitObservation(agentCtx, tracer, turn, *terminal, environment, "terminal")
	}
	agent.End(trace.WithTimestamp(parseTime(turn.EndTS)))
	return nil
}

func emitObservation(ctx context.Context, tracer trace.Tracer, turn codextrace.Turn, observation codextrace.Observation, environment, key string) {
	_, span := tracer.Start(ctx, observation.Name,
		trace.WithTimestamp(nsTime(observation.StartTimeUnixNS)),
		trace.WithAttributes(observationAttributes(turn, observation, environment)...),
	)
	span.End(trace.WithTimestamp(nsTime(observation.EndTimeUnixNS)))
	_ = key
}

func spanIDs(turn codextrace.Turn) ([]string, error) {
	ids := []string{
		codextrace.StableSpanID("codex-agent", turn.TraceID, turn.TurnID, ""),
		codextrace.StableSpanID("codex-transcript", turn.TraceID, turn.TurnID, ""),
	}
	for index := range turn.Observations {
		ids = append(ids, codextrace.StableSpanID("codex-observation", turn.TraceID, turn.TurnID, strconv.Itoa(index)))
	}
	if codextrace.TerminalObservation(turn) != nil {
		ids = append(ids, codextrace.StableSpanID("codex-observation", turn.TraceID, turn.TurnID, "terminal"))
	}
	return ids, nil
}

func baseObservationAttributes(turn codextrace.Turn, environment, observationType, input, output string) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("langfuse.trace.name", buildinfo.TraceName),
		attribute.String("langfuse.session.id", turn.SessionID),
		attribute.String("langfuse.environment", environment),
		attribute.String("langfuse.observation.type", observationType),
		attribute.String("langfuse.observation.input", strconv.Quote(codextrace.ExportText(input))),
		attribute.String("langfuse.observation.output", strconv.Quote(codextrace.ExportText(output))),
	}
}

func turnAttributes(turn codextrace.Turn, environment, observationType string, includeTraceIO bool) []attribute.KeyValue {
	attrs := baseObservationAttributes(turn, environment, observationType, turn.InputText(), turn.OutputText())
	if includeTraceIO {
		attrs = append(attrs,
			attribute.String("langfuse.trace.input", codextrace.ExportText(turn.InputText())),
			attribute.String("langfuse.trace.output", codextrace.ExportText(turn.OutputText())),
		)
	}
	attrs = append(attrs, metadataAttributes(turn)...)
	return attrs
}

func transcriptAttributes(turn codextrace.Turn, environment string) []attribute.KeyValue {
	attrs := turnAttributes(turn, environment, "generation", false)
	usage := map[string]int{}
	if turn.TokenUsage != nil {
		if turn.TokenUsage.InputTokens != 0 {
			usage["input"] = turn.TokenUsage.InputTokens
		}
		if turn.TokenUsage.OutputTokens != 0 {
			usage["output"] = turn.TokenUsage.OutputTokens
		}
		if turn.TokenUsage.TotalTokens != 0 {
			usage["total"] = turn.TokenUsage.TotalTokens
		}
		if turn.TokenUsage.CachedInputTokens != 0 {
			usage["cached_input"] = turn.TokenUsage.CachedInputTokens
		}
		if turn.TokenUsage.ReasoningOutputTokens != 0 {
			usage["reasoning_output"] = turn.TokenUsage.ReasoningOutputTokens
		}
	}
	if len(usage) > 0 {
		attrs = append(attrs, attribute.String("langfuse.observation.usage_details", jsonString(usage)))
	}
	return attrs
}

func observationAttributes(turn codextrace.Turn, observation codextrace.Observation, environment string) []attribute.KeyValue {
	attrs := baseObservationAttributes(turn, environment, observation.Type, observation.Input, observation.Output)
	attrs = append(attrs, metadataAttributes(turn)...)
	if len(observation.Metadata) > 0 {
		attrs = append(attrs, attribute.String("langfuse.observation.metadata", jsonString(observation.Metadata)))
	}
	return attrs
}

func metadataAttributes(turn codextrace.Turn) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String("langfuse.trace.metadata.codex_session_id", turn.SessionID),
		attribute.String("langfuse.trace.metadata.codex_turn_id", turn.TurnID),
		attribute.Bool("langfuse.trace.metadata.codex_transcript_exported", true),
		attribute.String("langfuse.observation.metadata.session_id", turn.SessionID),
		attribute.String("langfuse.observation.metadata.turn_id", turn.TurnID),
	}
	if turn.CWD != "" {
		attrs = append(attrs, attribute.String("langfuse.observation.metadata.cwd", turn.CWD))
	}
	if turn.Model != "" {
		attrs = append(attrs, attribute.String("langfuse.observation.metadata.model", turn.Model))
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

func VerifyTraceIO(ctx context.Context, cfg config.LangfuseConfig, turn codextrace.Turn, timeout, interval time.Duration) (bool, bool, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		traceBody, err := FetchTrace(ctx, cfg, turn.TraceID)
		if err != nil {
			lastErr = err
		} else {
			hasInput, hasOutput := traceMatches(traceBody, codextrace.ExportText(turn.InputText()), codextrace.ExportText(turn.OutputText()))
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

func traceMatches(traceBody map[string]any, input, output string) (bool, bool) {
	hasInput := stringValue(traceBody["input"]) == input
	hasOutput := stringValue(traceBody["output"]) == output
	for _, raw := range sliceValue(traceBody["observations"]) {
		observation := mapValue(raw)
		if stringValue(observation["name"]) != "codex.transcript" {
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
