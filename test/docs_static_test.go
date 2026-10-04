package test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TEST-014
func TestDocsAndRuntimeDoNotReferencePythonExporter(t *testing.T) {
	t.Parallel()

	if _, err := os.Stat(filepath.Join("..", "bin", "export_codex_session_to_langfuse.py")); !os.IsNotExist(err) {
		t.Fatalf("Python exporter still exists: %v", err)
	}
	for _, path := range []string{"AGENTS.md", "README.md", "TESTING.md", "systemd/codex-langfuse-watch.service"} {
		text := readRepoDoc(t, path)
		if strings.Contains(text, "export_codex_session_to_langfuse.py") || strings.Contains(text, "python3 -m py_compile") {
			t.Fatalf("%s still references the removed Python exporter", path)
		}
	}
}

// TEST-605
func TestDocsCompletedCodexVisibility(t *testing.T) {
	t.Parallel()
	readme := readRepoDoc(t, "README.md")
	testingDoc := readRepoDoc(t, "TESTING.md")
	for _, required := range []string{
		"finalizes turns with non-empty canonical input and output",
		"finalizes each turn with its full transcript",
		"does not read or update watcher checkpoints",
		"`processed_trace_ids`",
		"`pending_scores[trace_id]`",
		"at-least-once",
		"span_export_succeeded ... checkpoint=pending",
		"span_checkpoint_unconfirmed",
		"does not prove the backend has indexed the trace",
		"does not stream token deltas or unfinished tool observations",
	} {
		if !strings.Contains(readme, required) {
			t.Fatalf("README missing completed-turn contract %q", required)
		}
	}
	for _, required := range []string{
		"TestLegacyPendingScoreCheckpointReexportsTraceWithStableEnvironment",
		"TestLegacyPendingCheckpointBypassesSourceCache",
		"acknowledgement loss",
		"process termination before a durable checkpoint",
		"do not prove Collector durability or exactly-once delivery",
	} {
		if !strings.Contains(testingDoc, required) {
			t.Fatalf("TESTING missing retry contract %q", required)
		}
	}
}

// EVAL-007
func TestEvalDocsTraceContractCompleteness(t *testing.T) {
	t.Parallel()
	readme := readRepoDoc(t, "README.md")
	for _, required := range []string{
		"codex-langfuse-exporter",
		"codex.agent",
		"codex.transcript",
		"lmnr.span.input",
		"lmnr.span.output",
		"gen_ai.usage",
		"codex.tool.file_change",
		"systemd --user",
		"go test ./... -count=1",
	} {
		if !strings.Contains(readme, required) {
			t.Fatalf("README missing %q", required)
		}
	}
}

// TEST-108
func TestDocsTraceInsightMetadata(t *testing.T) {
	t.Parallel()
	readme := readRepoDoc(t, "README.md")
	for _, required := range []string{
		"verification_status",
		"verification_command_count",
		"changed_file_count",
		"changed_extensions",
		"touched_test_files",
		"command_kind",
		"duration_ms",
		"failure_type",
		"Hidden or encrypted reasoning blocks are omitted",
	} {
		if !strings.Contains(readme, required) {
			t.Fatalf("README missing %q", required)
		}
	}
}

// TEST-705
func TestDocsWorkspaceIdentity(t *testing.T) {
	t.Parallel()
	readme := readRepoDoc(t, "README.md")
	testingDoc := readRepoDoc(t, "TESTING.md")
	exampleConfig := readRepoDoc(t, filepath.Join("examples", "codex-config.toml"))
	for _, required := range []string{
		"--doctor",
		"--json",
		"deterministic turn summaries",
		"repository folder and export-time branch",
		"first six lowercase hexadecimal SHA-256",
		"A detached Git checkout uses `detached`",
		"uses `default`",
		"Linux runtime hostname",
		"not configurable through command-line flags",
		"version 3 export-state file",
		"Keep both in place during upgrades",
		"Never delete or rename the `.lock` sidecar",
	} {
		if !strings.Contains(readme, required) {
			t.Fatalf("README missing workspace or state contract %q", required)
		}
	}
	for _, required := range []string{"--doctor", "Collector health/metrics endpoints", "trace ID", "Laminar project UI"} {
		if !strings.Contains(testingDoc, required) {
			t.Fatalf("TESTING missing live identity check %q", required)
		}
	}
	if !strings.Contains(exampleConfig, "Workspace identity needs no configuration") {
		t.Fatal("example config does not state the single identity path")
	}
	for _, document := range []struct{ name, text string }{{"README", readme}, {"TESTING", testingDoc}, {"example config", exampleConfig}} {
		for _, forbidden := range []string{"LANGFUSE_USER_ID_MODE", "--environment", "folder(branch)@hostname", "path/to/repo(branch)@hostname"} {
			if strings.Contains(document.text, forbidden) {
				t.Fatalf("%s retains old identity override %q", document.name, forbidden)
			}
		}
	}
}

func TestDocsExportStateLockUpgrade(t *testing.T) {
	t.Parallel()
	readme := readRepoDoc(t, "README.md")
	testingDoc := readRepoDoc(t, "TESTING.md")
	plan := readRepoDoc(t, filepath.Join("plans", "export-state-lock-recovery-plan.md"))
	for _, required := range []string{"older `O_EXCL` lock protocol", "pause new Claude Stop-hook invocations", "Never delete or rename the `.lock` sidecar"} {
		if !strings.Contains(readme, required) {
			t.Fatalf("README missing lock upgrade guidance %q", required)
		}
	}
	for _, required := range []string{
		"TestStateLockRecoversAfterKilledOwner",
		"TestStateInterruptedWritePreservesCommittedJSON",
		"TestStateWriteErrorsPreserveCommittedFile",
		"TestWatchRetriesPendingSpanCheckpointOnly",
		"TestClaudeHookLockTimeoutIsNotAcknowledged",
		"TestCLISignalCancelsStateWait",
		"TestInstallReportsPostStopFailureState",
		"go test -race ./internal/exportstate ./internal/claudehook ./internal/watch",
	} {
		if !strings.Contains(testingDoc, required) {
			t.Fatalf("TESTING missing lock regression %q", required)
		}
	}
	for _, required := range []string{"Legacy and new writers cannot safely coexist.", "TestStateLoadOrCreatePreservesEnqueueInEitherOrder", "TestWatchRetriesQueueRemovalAfterCheckpoint", "forced-kill tests against disposable state"} {
		if !strings.Contains(plan, required) {
			t.Fatalf("lock recovery plan missing %q", required)
		}
	}
}

func TestDocsLangfuseMCPVersionConstraint(t *testing.T) {
	t.Parallel()
	readme := readRepoDoc(t, "README.md")
	exampleConfig := readRepoDoc(t, filepath.Join("examples", "codex-config.toml"))
	for _, required := range []string{`"mcp>=1.28,<2"`, `"langfuse-mcp==0.10.0"`} {
		if !strings.Contains(exampleConfig, required) {
			t.Fatalf("example config missing %q", required)
		}
	}
	for _, required := range []string{"separately configured Langfuse MCP connection", "close during `initialize`", "incompatible MCP SDK v2"} {
		if !strings.Contains(readme, required) {
			t.Fatalf("README missing independent MCP compatibility note %q", required)
		}
	}
}

// TEST-204
func TestDocsNavigationFacetsAndFilters(t *testing.T) {
	t.Parallel()
	readme := readRepoDoc(t, "README.md")
	testingDoc := readRepoDoc(t, "TESTING.md")
	for _, required := range []string{
		"codex_insight.navigation",
		"claude_insight.navigation",
		"files:changed",
		"command_kind",
		"tool:command",
		"tool:file_change",
		"tool:web_search",
		"verification:not_run",
		"lmnr.association.properties.tags",
		"mcp:<server>",
		"exact MCP tool names",
		"gen_ai.*",
	} {
		if !strings.Contains(readme, required) {
			t.Fatalf("README missing Laminar filter/metadata contract %q", required)
		}
	}
	for _, forbidden := range []string{"Saved Views", "saved views", "Views -> Create Custom View", "reusable view"} {
		if strings.Contains(readme, forbidden) {
			t.Fatalf("README retains removed view guidance %q", forbidden)
		}
	}
	for _, required := range []string{"testdata/manifest.json", "raw OTLP fields", "Laminar span contract"} {
		if !strings.Contains(testingDoc+readme, required) {
			t.Fatalf("documentation missing contract detail %q", required)
		}
	}
}

// TEST-405
func TestDocsTagsAndMCPUsage(t *testing.T) {
	t.Parallel()
	readme := readRepoDoc(t, "README.md")
	testingDoc := readRepoDoc(t, "TESTING.md")
	installScript := readRepoDoc(t, "install.sh")
	for _, required := range []string{
		"lmnr.association.properties.tags",
		"Navigation is emitted as",
		"`mcp:<server>`",
		"claude.tool.mcp",
		"codex.tool.mcp",
		"exact MCP tool names",
		"internal/providers/providers.go",
		"codex-langfuse-watch.service",
		"~/.codex/bin/codex-langfuse-exporter --path",
	} {
		if !strings.Contains(readme, required) {
			t.Fatalf("README missing %q", required)
		}
	}
	for _, required := range []string{"TestLaminarSpanProjectionPreservesTraceContextAndMetadata", "TestFullAcceptanceLaminarTagsAndMCP", "TestInsightTagFacets"} {
		if !strings.Contains(testingDoc, required) {
			t.Fatalf("TESTING missing Laminar tag coverage %q", required)
		}
	}
	if !strings.Contains(installScript, "codex-langfuse-watch.service") {
		t.Fatal("install.sh missing watcher service restart")
	}
}

// TEST-408
func TestDocsNoLocalLaminarCostCalculation(t *testing.T) {
	t.Parallel()
	readme := readRepoDoc(t, "README.md")
	for _, required := range []string{"calculate cost locally", "not submitted as native Laminar evaluator score records", "local Collector accepts the batch"} {
		if !strings.Contains(strings.ToLower(readme), strings.ToLower(required)) {
			t.Fatalf("README missing backend ownership boundary %q", required)
		}
	}
	for _, forbidden := range []string{"--sync-model-pricing", "/api/public/otel", "/api/public/ingestion", "cost_details"} {
		if strings.Contains(readme, forbidden) {
			t.Fatalf("README retains removed direct-backend feature %q", forbidden)
		}
	}
}

// TEST-514
func TestDocsCodingAgentIntegrationGuide(t *testing.T) {
	t.Parallel()
	readme := readRepoDoc(t, "README.md")
	testingDoc := readRepoDoc(t, "TESTING.md")
	for _, required := range []string{
		"To add a provider",
		"internal/<provider>trace",
		"agenttrace.Turn",
		"internal/providers/providers.go",
		"testdata/sources/<provider>/*.jsonl",
		"testdata/manifest.json",
		"Hooks should enqueue state only",
		"watcher remains the single automatic export path",
	} {
		if !strings.Contains(readme, required) {
			t.Fatalf("README missing provider integration text %q", required)
		}
	}
	for _, required := range []string{"go test ./... -count=1", "testdata/manifest.json", "Laminar span contract"} {
		if !strings.Contains(testingDoc, required) {
			t.Fatalf("TESTING missing provider contract %q", required)
		}
	}
}

// TEST-508
func TestDocsClaudeSupportContract(t *testing.T) {
	t.Parallel()
	readme := readRepoDoc(t, "README.md")
	testingDoc := readRepoDoc(t, "TESTING.md")
	agents := readRepoDoc(t, "AGENTS.md")
	for _, required := range []string{"Claude Code's Stop and completed-tool hooks", "--provider claude --path", "--claude-hook", "claude.agent", "claude.transcript", "claude.tool.command", "claude.tool.file_change", "claude.tool.mcp", "claude.tool.generic", "does not run Claude or modify its settings"} {
		if !strings.Contains(readme, required) {
			t.Fatalf("README missing Claude support contract %q", required)
		}
	}
	for _, required := range []string{"separate benign Claude Code turn", "already-installed Stop hook", "Do not manually export that transcript", "Laminar project"} {
		if !strings.Contains(testingDoc, required) {
			t.Fatalf("TESTING missing Claude acceptance detail %q", required)
		}
	}
	for _, required := range []string{"Do not add Claude polling", "do not add local token-price multiplication", "Do not mutate Claude settings automatically"} {
		if !strings.Contains(agents, required) {
			t.Fatalf("AGENTS missing Claude boundary %q", required)
		}
	}
}

// TEST-530
func TestDocsCanonicalSemanticToolFamilies(t *testing.T) {
	t.Parallel()
	readme := readRepoDoc(t, "README.md")
	for _, required := range []string{"codex.tool.command", "codex.tool.file_change", "codex.tool.mcp", "codex.tool.web_search", "codex.tool.tool_search", "claude.tool.command", "claude.tool.file_change", "claude.tool.mcp", "claude.tool.generic", "Tool metadata includes observed command kind"} {
		if !strings.Contains(readme, required) {
			t.Fatalf("README missing canonical tool family %q", required)
		}
	}
	for _, forbidden := range []string{
		strings.Join([]string{"codex", "tool", "exec_command"}, "."),
		strings.Join([]string{"codex", "tool", "apply_patch"}, "."),
		strings.Join([]string{"claude", "tool", "bash"}, "."),
		strings.Join([]string{"tool", "bash"}, ":"),
		strings.Join([]string{"patch", "count"}, "_"),
	} {
		if strings.Contains(readme, forbidden) {
			t.Fatalf("README contains provider-native semantic family %q", forbidden)
		}
	}
}

// EVAL-008
func TestEvalDocsClaudeContractCompleteness(t *testing.T) {
	t.Parallel()
	readme := readRepoDoc(t, "README.md")
	testingDoc := readRepoDoc(t, "TESTING.md")
	for _, required := range []string{"Claude Code's Stop and completed-tool hooks", "hooks queue the transcript path", "Hidden or encrypted reasoning blocks are omitted", "The exporter does not run Claude or modify its settings"} {
		if !strings.Contains(readme, required) {
			t.Fatalf("README missing Claude completeness phrase %q", required)
		}
	}
	for _, required := range []string{"For Claude acceptance", "Do not manually export that transcript", "Confirm the watcher drains the queued request"} {
		if !strings.Contains(testingDoc, required) {
			t.Fatalf("TESTING missing Claude acceptance phrase %q", required)
		}
	}
}

// TEST-620
func TestDocsPerformanceGateSeparation(t *testing.T) {
	t.Parallel()
	testingDoc := readRepoDoc(t, "TESTING.md")
	handoff := readRepoDoc(t, filepath.Join("plans", "multi-machine-tracing-gateway-handoff.md"))
	for _, required := range []string{"Benchmark(InsightRollup|ClaudeParserCorpus)", "TestEvalWatchExportLatency|TestEvalHookQueueDrainLatency", "performance-test-stability.md", "memory matrix", "768 MiB"} {
		if !strings.Contains(testingDoc, required) {
			t.Fatalf("TESTING missing performance/resource gate %q", required)
		}
	}
	for _, retired := range []string{"TestEvalInsightRollupLatency", "TestEvalClaudeParserDeterminismAndLatency"} {
		if strings.Contains(testingDoc, retired) {
			t.Fatalf("TESTING still references retired scheduler-sensitive test %q", retired)
		}
	}
	for _, required := range []string{"benchmarks are non-binding engineering evidence", "binding watcher and Claude queue latency thresholds remain release-blocking"} {
		if !strings.Contains(handoff, required) {
			t.Fatalf("gateway handoff missing performance closeout %q", required)
		}
	}
}

// TEST-409
func TestNoLocalCostDetailsOrDirectIngestionShortcut(t *testing.T) {
	t.Parallel()
	for _, root := range []string{filepath.Join("..", "internal", "laminar"), filepath.Join("..", "cmd", "codex-langfuse-exporter")} {
		entries, err := os.ReadDir(root)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.IsDir() || strings.HasSuffix(entry.Name(), "_test.go") || !strings.HasSuffix(entry.Name(), ".go") {
				continue
			}
			path := filepath.Join(root, entry.Name())
			text := readRepoDoc(t, filepath.Join(strings.TrimPrefix(root, "../"), entry.Name()))
			for _, forbidden := range []string{"cost_details", "/api/public/ingestion", "/api/public/otel"} {
				if strings.Contains(text, forbidden) {
					t.Fatalf("%s uses removed backend-specific path %q", path, forbidden)
				}
			}
		}
	}
	readme := readRepoDoc(t, "README.md")
	for _, forbidden := range []string{"/api/public/otel", "/api/public/ingestion", "cost_details"} {
		if strings.Contains(readme, forbidden) {
			t.Fatalf("README includes a direct backend path %q", forbidden)
		}
	}
}

func readRepoDoc(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", path))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
