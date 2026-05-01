package test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kirilligum/codex-langfuse-tracer/internal/buildinfo"
	"github.com/kirilligum/codex-langfuse-tracer/internal/codextrace"
	"github.com/kirilligum/codex-langfuse-tracer/internal/tracecontract"
)

// TEST-018
func TestFullAcceptance(t *testing.T) {
	t.Parallel()

	if buildinfo.InstalledBinaryName != "codex-langfuse-exporter" {
		t.Fatalf("wrong binary name: %s", buildinfo.InstalledBinaryName)
	}
	turns, err := codextrace.ParseTurns(filepath.Join("..", "testdata", "rollouts", "complete-tools.jsonl"))
	if err != nil {
		t.Fatalf("ParseTurns: %v", err)
	}
	exportable := codextrace.ExportableTurns(turns)
	if len(exportable) != 1 {
		t.Fatalf("exportable turns = %d", len(exportable))
	}
	contract := tracecontract.FromTurn(exportable[0])
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
