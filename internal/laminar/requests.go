package laminar

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/kirilligum/codex-langfuse-tracer/internal/agenttrace"
	"github.com/kirilligum/codex-langfuse-tracer/internal/buildinfo"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

func requestRootID(r *agenttrace.Request) string {
	return agenttrace.StableSpanID("request-root-v1", r.TraceID, r.ID, "")
}
func executionID(t agenttrace.Turn) string {
	return agenttrace.StableSpanID("execution-v1", t.Request.TraceID, t.TurnID, "")
}

// Work-item identities remain execution-specific in the watcher. Only the
// exported TraceID and parent context join them to the human request.
func emitRequestProjection(ctx context.Context, t agenttrace.Turn, environment, userID, serviceName string, exporter sdktrace.SpanExporter) error {
	r := t.Request
	if environment == "" || userID == "" || serviceName == "" || r.ID == "" {
		return fmt.Errorf("request projection requires identity and workspace attributes")
	}
	if t.FirstHumanInput < 0 || t.FirstHumanInput > len(r.Inputs) || t.FirstObservation < 0 || t.FirstObservation > len(t.Observations) || t.FirstModelCall < 0 || t.FirstModelCall > len(t.ModelCalls) {
		return fmt.Errorf("request projection has an invalid completed-step checkpoint")
	}
	tid, err := trace.TraceIDFromHex(r.TraceID)
	if err != nil {
		return err
	}
	rootID, err := trace.SpanIDFromHex(requestRootID(r))
	if err != nil {
		return err
	}
	rootName := "Codex request"
	if !r.HasHumanInput {
		rootName = "Unattributed agent execution"
	}
	firstObservation, firstCall, firstInput := 0, 0, 0
	if t.ExportDelta {
		firstObservation = t.FirstObservation
		firstCall = t.FirstModelCall
		firstInput = t.FirstHumanInput
	}
	var ids []string
	completeRoot := t.Completed && !t.RequestInputOnly
	if t.RequestRoot {
		if completeRoot {
			ids = append(ids, rootID.String())
		}
		for _, input := range r.Inputs[firstInput:] {
			ids = append(ids, agenttrace.StableSpanID("human-input-v1", r.TraceID, r.ID, input.ID))
		}
		if completeRoot && !r.HasHumanInput {
			ids = append(ids, agenttrace.StableSpanID("request-source-v1", r.TraceID, r.ID, ""))
		}
	} else {
		if t.Completed && !t.ExecutionEmitted {
			ids = append(ids, executionID(t), agenttrace.StableSpanID("execution-transcript-v1", r.TraceID, t.TurnID, ""))
		}
		for i := firstObservation; i < len(t.Observations); i++ {
			ids = append(ids, agenttrace.StableSpanID("execution-observation-v1", r.TraceID, t.TurnID, strconv.Itoa(i)))
		}
		for i := firstCall; i < len(t.ModelCalls); i++ {
			ids = append(ids, agenttrace.StableSpanID("execution-model-v1", r.TraceID, t.TurnID, strconv.Itoa(i)))
		}
	}
	if len(ids) == 0 {
		return nil
	}
	res, err := resource.New(ctx, resource.WithAttributes(attribute.String("service.name", serviceName), attribute.String("service.version", buildinfo.Version), attribute.String("deployment.environment.name", environment)))
	if err != nil {
		return err
	}
	provider := sdktrace.NewTracerProvider(sdktrace.WithResource(res), sdktrace.WithIDGenerator(newFixedIDGenerator(r.TraceID, ids)), sdktrace.WithBatcher(exporter))
	tracer := provider.Tracer(buildinfo.ScopeName, trace.WithInstrumentationVersion(buildinfo.Version))
	parent := trace.ContextWithRemoteSpanContext(ctx, trace.NewSpanContext(trace.SpanContextConfig{TraceID: tid, SpanID: rootID, TraceFlags: trace.FlagsSampled, Remote: true}))
	// The request work item has no execution observations. Deriving outcome
	// tags from its empty slice would incorrectly claim that no work occurred.
	var tags []string
	if !t.RequestRoot {
		tags = agenttrace.BuildTraceTags(t)
	}
	common := func(typ, input, output string) []attribute.KeyValue {
		a := commonAttributes(t, environment, userID, typ, input, output, tags)
		return append(a, attribute.String("cli.request.id", r.ID), attribute.String(laminarMetadataPrefix+"cli_request_id", r.ID), attribute.String(laminarMetadataPrefix+"cli_capture_contract", "human-request-v1"))
	}
	if t.RequestRoot {
		var root trace.Span
		if completeRoot {
			a := spanMetadataAttributes(common("DEFAULT", t.InputText(), r.Output))
			a = append(a, attribute.String("cli.request.state", r.State))
			parent, root = tracer.Start(ctx, rootName, trace.WithTimestamp(parseTime(r.StartTS)), trace.WithAttributes(a...))
			setSpanPath(root, rootID, rootName, rootName)
		}
		for i, input := range r.Inputs[firstInput:] {
			index := firstInput + i
			name := "human.input"
			if index == 0 && !t.RequestInputOnly {
				name = "turn.input"
			}
			a := common("DEFAULT", input.Text, "")
			if index == 0 && !t.RequestInputOnly {
				preview := []rune(agenttrace.ExportText(input.Text))
				if len(preview) > 2048 {
					preview = preview[:2048]
				}
				a = append(a, attribute.String(laminarMetadataPrefix+"cli_input_preview", string(preview)), attribute.String(laminarMetadataPrefix+"cli_input_state", "human"))
			} else {
				a = spanMetadataAttributes(a)
			}
			a = append(a, attribute.String("cli.human_message.id", input.ID))
			if t.RequestInputOnly {
				a = append(a, attribute.Bool("cli.request.after_completion", true))
			}
			_, span := tracer.Start(parent, name, trace.WithTimestamp(parseTime(input.Timestamp)), trace.WithAttributes(a...))
			setSpanPath(span, rootID, rootName, name)
			span.End(trace.WithTimestamp(parseTime(input.Timestamp)))
		}
		if completeRoot && !r.HasHumanInput {
			a := append(common("DEFAULT", "", ""), attribute.String(laminarMetadataPrefix+"cli_input_state", "unattributed"))
			_, span := tracer.Start(parent, "request.source", trace.WithTimestamp(parseTime(r.StartTS)), trace.WithAttributes(a...))
			setSpanPath(span, rootID, rootName, "request.source")
			span.End(trace.WithTimestamp(parseTime(r.StartTS)))
		}
		if root != nil {
			root.End(trace.WithTimestamp(parseTime(r.FinishedAt)))
		}
		return provider.Shutdown(ctx)
	}
	executionSpanID, err := trace.SpanIDFromHex(executionID(t))
	if err != nil {
		return err
	}
	executionName := "Execution " + t.TurnID
	executionContext := trace.ContextWithRemoteSpanContext(ctx, trace.NewSpanContext(trace.SpanContextConfig{TraceID: tid, SpanID: executionSpanID, TraceFlags: trace.FlagsSampled, Remote: true}))
	var execution trace.Span
	if t.Completed && !t.ExecutionEmitted {
		input := t.InputText()
		if len(t.ModelMessages) > 0 {
			input = strings.Join(t.ModelMessages, "\n\n")
		}
		a := append(spanMetadataAttributes(common("DEFAULT", input, t.OutputText())), attribute.String("cli.execution.id", t.TurnID), attribute.String("cli.execution.trigger", t.ExecutionTrigger), attribute.Bool("cli.execution.completed", true), attribute.Bool("cli.request.after_completion", t.AfterCompletion), attribute.Bool("cli.execution.cancelled", t.Aborted))
		a = append(a, spanMetadataAttributes(insightMetadataAttributes(t))...)
		a = append(a, spanMetadataAttributes(deterministicScoreAttributes(t))...)
		executionContext, execution = tracer.Start(parent, executionName, trace.WithTimestamp(parseTime(t.StartTS)), trace.WithAttributes(a...))
		setRequestPath(execution, rootID, executionSpanID, rootName, executionName, executionName)
		_, transcript := tracer.Start(executionContext, t.Profile().TranscriptName, trace.WithTimestamp(parseTime(t.StartTS)), trace.WithAttributes(spanMetadataAttributes(common("DEFAULT", input, t.OutputText()))...))
		setRequestPath(transcript, rootID, executionSpanID, rootName, executionName, t.Profile().TranscriptName)
		transcript.End(trace.WithTimestamp(parseTime(t.EndTS)))
	}
	for _, observation := range t.Observations[firstObservation:] {
		_, span := tracer.Start(executionContext, observation.Name, trace.WithTimestamp(nsTime(observation.StartTimeUnixNS)), trace.WithAttributes(spanMetadataAttributes(observationAttributes(t, observation, environment, userID, tags))...))
		if observation.Metadata["late_result"] == true {
			span.SetAttributes(attribute.Bool("cli.capture.late_result", true))
		}
		if status := failedObservationStatusMessage(observation); status != "" {
			span.SetStatus(codes.Error, status)
		}
		setRequestPath(span, rootID, executionSpanID, rootName, executionName, observation.Name)
		span.End(trace.WithTimestamp(nsTime(observation.EndTimeUnixNS)))
	}
	for i, call := range t.ModelCalls[firstCall:] {
		name := fmt.Sprintf("codex.model.call.%d", firstCall+i+1)
		a := spanMetadataAttributes(common("LLM", call.Input, call.Output))
		a = append(a, usageAttributes(call.Usage)...)
		a = append(a, attribute.String("gen_ai.request.model", call.Model), attribute.String("gen_ai.system", modelProvider(t.Profile().Provider)))
		_, span := tracer.Start(executionContext, name, trace.WithTimestamp(parseTime(call.StartTS)), trace.WithAttributes(a...))
		setRequestPath(span, rootID, executionSpanID, rootName, executionName, name)
		span.End(trace.WithTimestamp(parseTime(call.EndTS)))
	}
	if execution != nil {
		execution.End(trace.WithTimestamp(parseTime(t.EndTS)))
	}
	return provider.Shutdown(ctx)
}

func setRequestPath(span trace.Span, root, execution trace.SpanID, rootName, executionName, name string) {
	uuid := func(id trace.SpanID) string { s := id.String(); return "00000000-0000-0000-" + s[:4] + "-" + s[4:] }
	ids, names := []string{uuid(root), uuid(execution)}, []string{rootName, executionName}
	if span.SpanContext().SpanID() != execution {
		ids = append(ids, uuid(span.SpanContext().SpanID()))
		names = append(names, name)
	}
	span.SetAttributes(attribute.StringSlice("lmnr.span.ids_path", ids), attribute.StringSlice("lmnr.span.path", names))
}
