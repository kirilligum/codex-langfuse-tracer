package test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/kirilligum/codex-langfuse-tracer/internal/buildinfo"
	"github.com/kirilligum/codex-langfuse-tracer/internal/codextrace"
	"github.com/kirilligum/codex-langfuse-tracer/internal/langfuse"
	"github.com/kirilligum/codex-langfuse-tracer/internal/tracecontract"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// TEST-018
func TestFullAcceptance(t *testing.T) {
	t.Parallel()

	if buildinfo.InstalledBinaryName != "codex-langfuse-exporter" {
		t.Fatalf("wrong binary name: %s", buildinfo.InstalledBinaryName)
	}
	contract := contractFromFixture(t, "complete-tools")
	if contract.SchemaVersion != 1 || contract.Name != buildinfo.TraceName {
		t.Fatalf("bad contract identity: %+v", contract)
	}
	if contract.Input == "" || contract.Output == "" || strings.Contains(contract.Output, "sk-lf-live-secret") {
		t.Fatalf("bad contract input/output: %+v", contract)
	}
	if len(contract.Observations) < 8 {
		t.Fatalf("too few observations: %d", len(contract.Observations))
	}
	for _, key := range []string{"verification_status", "verification_command_count", "changed_file_count", "changed_extensions", "tool_count"} {
		if _, ok := contract.Metadata[key]; !ok {
			t.Fatalf("contract metadata missing %s in %#v", key, contract.Metadata)
		}
	}
	if _, ok := contract.Metadata["changed_files"]; ok {
		t.Fatalf("root metadata must not include changed_files: %#v", contract.Metadata)
	}
	commandMetadata := map[string]any(nil)
	for _, observation := range contract.Observations {
		if observation.Name == "codex.tool.exec_command" {
			commandMetadata = observation.Metadata
			break
		}
	}
	if commandMetadata == nil {
		t.Fatal("missing exec command observation metadata")
	}
	for _, key := range []string{"command_kind", "duration_ms", "failure_type"} {
		if _, ok := commandMetadata[key]; !ok {
			t.Fatalf("command metadata missing %s in %#v", key, commandMetadata)
		}
	}

	service, err := os.ReadFile(filepath.Join("..", "systemd", "codex-langfuse-watch.service"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(service), ".codex/bin/codex-langfuse-exporter --watch") {
		t.Fatalf("service does not run Go exporter:\n%s", service)
	}
	if _, err := os.Stat(filepath.Join("..", "bin", "export_codex_session_to_langfuse.py")); !os.IsNotExist(err) {
		t.Fatalf("Python exporter still present: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if ctx.Err() == nil {
		t.Fatal("context sanity check failed")
	}
}

// TEST-305
func TestFullAcceptanceLangfuseFilterCostContract(t *testing.T) {
	t.Parallel()

	complete := contractFromFixture(t, "complete-tools")
	if complete.Model != "gpt-5.5" {
		t.Fatalf("model = %q", complete.Model)
	}
	if complete.TokenUsage["input"] != 100 || complete.TokenUsage["output"] != 40 || complete.TokenUsage["total"] != 140 {
		t.Fatalf("token usage = %#v", complete.TokenUsage)
	}
	for key, want := range map[string]int{
		"changed_file_count":      1,
		"other_command_count":     1,
		"exec_command_tool_count": 1,
		"apply_patch_tool_count":  1,
		"web_search_tool_count":   1,
		"mcp_tool_count":          1,
		"tool_search_tool_count":  1,
	} {
		requireMetadataInt(t, complete.Metadata, key, want)
	}
	if complete.Metadata["navigation"] != "command:other files:changed tool:apply_patch tool:exec_command tool:mcp tool:tool_search tool:web_search verification:not_run" {
		t.Fatalf("navigation = %#v", complete.Metadata["navigation"])
	}
	requireNoForbiddenContractKeys(t, complete.Metadata)
	for _, observation := range complete.Observations {
		requireNoForbiddenContractKeys(t, observation.Metadata)
	}

	failed := contractFromFixture(t, "failed-command")
	requireMetadataInt(t, failed.Metadata, "failed_command_count", 1)
	if failed.Metadata["verification_status"] != "failed" {
		t.Fatalf("verification_status = %#v", failed.Metadata["verification_status"])
	}
	requireNoForbiddenContractKeys(t, failed.Metadata)
}

// TEST-406
func TestFullAcceptanceLangfuseTagsAndMCP(t *testing.T) {
	t.Parallel()

	completeTurn := turnFromFixture(t, "complete-tools")
	complete := tracecontract.FromTurn(completeTurn)
	for _, tag := range []string{"command:other", "files:changed", "mcp:github", "tool:mcp", "tool:web_search", "verification:not_run"} {
		if !slices.Contains(complete.Tags, tag) {
			t.Fatalf("complete tags missing %q in %#v", tag, complete.Tags)
		}
	}
	for _, tag := range complete.Tags {
		if strings.Contains(tag, "issues/list") || strings.Contains(tag, "/tmp/") || strings.Contains(tag, "sess-complete") {
			t.Fatalf("forbidden value leaked into tag %q", tag)
		}
	}
	mcpMetadata := map[string]any(nil)
	for _, observation := range complete.Observations {
		if observation.Name == "codex.tool.mcp" {
			mcpMetadata = observation.Metadata
			break
		}
	}
	if mcpMetadata["mcp_server"] != "github" || mcpMetadata["mcp_tool"] != "issues/list" {
		t.Fatalf("MCP metadata = %#v", mcpMetadata)
	}

	noTools := contractFromFixture(t, "complete-no-tools")
	for _, tag := range noTools.Tags {
		if strings.HasPrefix(tag, "mcp:") || tag == "tool:mcp" {
			t.Fatalf("no-tools fixture has MCP tag in %#v", noTools.Tags)
		}
	}

	spans := emitAcceptanceSpans(t, completeTurn)
	rawTags, err := json.Marshal(codextrace.BuildInsightRollup(completeTurn).Tags())
	if err != nil {
		t.Fatal(err)
	}
	wantTags := string(rawTags)
	for _, name := range []string{"codex.agent", "codex.transcript", "codex.tool.mcp"} {
		span := spans.byName(name)
		if span.name == "" {
			t.Fatalf("missing span %s", name)
		}
		if span.attributes["langfuse.trace.tags"] != wantTags {
			t.Fatalf("%s tags = %q want %q", name, span.attributes["langfuse.trace.tags"], wantTags)
		}
	}
	for _, span := range spans {
		if span.name == "codex.agent" {
			continue
		}
		for key := range span.attributes {
			if strings.HasPrefix(key, "langfuse.trace.metadata.codex_insight.") {
				t.Fatalf("%s repeats root insight metadata %s", span.name, key)
			}
		}
	}

	service, err := os.ReadFile(filepath.Join("..", "systemd", "codex-langfuse-watch.service"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(service), ".codex/bin/codex-langfuse-exporter --watch") {
		t.Fatalf("service does not run watcher exporter:\n%s", service)
	}
}

func contractFromFixture(t *testing.T, name string) tracecontract.Trace {
	t.Helper()
	return tracecontract.FromTurn(turnFromFixture(t, name))
}

func turnFromFixture(t *testing.T, name string) codextrace.Turn {
	t.Helper()
	turns, err := codextrace.ParseTurns(filepath.Join("..", "testdata", "rollouts", name+".jsonl"))
	if err != nil {
		t.Fatalf("ParseTurns(%s): %v", name, err)
	}
	exportable := codextrace.ExportableTurns(turns)
	if len(exportable) != 1 {
		t.Fatalf("%s exportable turns = %d", name, len(exportable))
	}
	return exportable[0]
}

func requireMetadataInt(t *testing.T, metadata map[string]any, key string, want int) {
	t.Helper()
	if metadata[key] != want {
		t.Fatalf("metadata[%s] = %#v, want %d\nmetadata=%s", key, metadata[key], want, canonicalJSON(metadata))
	}
}

type acceptanceExporter struct {
	mu    sync.Mutex
	spans []sdktrace.ReadOnlySpan
}

func (e *acceptanceExporter) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.spans = append(e.spans, spans...)
	return nil
}

func (e *acceptanceExporter) Shutdown(context.Context) error {
	return nil
}

func emitAcceptanceSpans(t *testing.T, turn codextrace.Turn) acceptanceSpans {
	t.Helper()
	exporter := &acceptanceExporter{}
	if err := langfuse.EmitTurn(context.Background(), turn, buildinfo.DefaultEnvironment, buildinfo.DefaultServiceName, exporter); err != nil {
		t.Fatalf("EmitTurn: %v", err)
	}
	exporter.mu.Lock()
	defer exporter.mu.Unlock()
	result := make(acceptanceSpans, 0, len(exporter.spans))
	for _, span := range exporter.spans {
		attrs := map[string]string{}
		for _, attr := range span.Attributes() {
			attrs[string(attr.Key)] = attr.Value.Emit()
		}
		result = append(result, acceptanceSpan{name: span.Name(), attributes: attrs})
	}
	return result
}

type acceptanceSpan struct {
	name       string
	attributes map[string]string
}

type acceptanceSpans []acceptanceSpan

func (s acceptanceSpans) byName(name string) acceptanceSpan {
	for _, span := range s {
		if span.name == name {
			return span
		}
	}
	return acceptanceSpan{}
}
