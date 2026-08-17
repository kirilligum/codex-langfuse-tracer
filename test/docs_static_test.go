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
	for _, path := range []string{
		"AGENTS.md",
		"README.md",
		"TESTING.md",
		"systemd/codex-langfuse-watch.service",
	} {
		raw, err := os.ReadFile(filepath.Join("..", path))
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		if strings.Contains(text, "export_codex_session_to_langfuse.py") || strings.Contains(text, "python3 -m py_compile") {
			t.Fatalf("%s still references Python exporter", path)
		}
	}
}

// TEST-605
func TestDocsProgressiveCodexVisibility(t *testing.T) {
	t.Parallel()

	readme := readRepoDoc(t, "README.md")
	testingDoc := readRepoDoc(t, "TESTING.md")
	agentNotes := readRepoDoc(t, "AGENTS.md")
	for _, required := range []string{
		"unfinished Codex turns",
		"first completed observation",
		"five-second polling cycle",
		"`turn_progress`",
		"`exported_observation_count`",
		"`final_spans_exported`",
		"at-least-once",
		"does not stream tokens or partial assistant text",
		"currently configured Langfuse target",
	} {
		if !strings.Contains(readme, required) {
			t.Fatalf("README missing progressive contract %q", required)
		}
	}
	for _, required := range []string{
		"TestIncompleteObservationPrefixStability|TestProgressiveSuffixPlan",
		"TestVersion2State|TestStateUpdatePreservesQueue",
		"TestOTLPProgressiveThenFinal|TestProgressiveSpanAttributes",
		"TestWatchProgressiveLifecycle|TestWatchProgressiveFailureRetry|TestWatchLogs",
		"TestEvalWatchExportLatency",
	} {
		if !strings.Contains(testingDoc, required) {
			t.Fatalf("TESTING missing progressive command fragment %q", required)
		}
	}
	for _, forbiddenAlternative := range []string{
		"native Codex OTEL path",
		"wrapper export path",
		"per-file observation fanout",
	} {
		if !strings.Contains(agentNotes, forbiddenAlternative) {
			t.Fatalf("AGENTS missing canonical-path constraint %q", forbiddenAlternative)
		}
	}
}

// EVAL-007
func TestEvalDocsTraceContractCompleteness(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, required := range []string{
		"codex-langfuse-exporter",
		"codex.agent",
		"codex.transcript",
		"codex.terminal",
		"codex.tool.file_change",
		"systemd --user",
		"go test ./...",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("README missing %q", required)
		}
	}
}

// TEST-108
func TestDocsTraceInsightMetadata(t *testing.T) {
	t.Parallel()

	required := []string{
		"verification_status",
		"verification_command_count",
		"changed_file_count",
		"changed_extensions",
		"touched_test_files",
		"command_kind",
		"duration_ms",
		"failure_type",
		"hidden chain-of-thought",
	}
	for _, path := range []string{"README.md"} {
		raw, err := os.ReadFile(filepath.Join("..", path))
		if err != nil {
			t.Fatal(err)
		}
		text := strings.ToLower(string(raw))
		for _, value := range required {
			if !strings.Contains(text, value) {
				t.Fatalf("%s missing %q", path, value)
			}
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
		"trace_url",
		"deterministic trace-level Langfuse scores",
		"They do not make extra LLM calls",
		"`langfuse.environment`",
		"`repository-folder--branch-<hash>`",
		"first six lowercase hexadecimal SHA-256",
		"export-time branch",
		"detached HEAD uses `detached`",
		"Non-Git, missing, unreadable, or timed-out working directories use `default`",
		"`langfuse.user.id`",
		"Linux runtime hostname",
		"Identity fields are not configurable",
		"version 2",
		"systemctl --user stop codex-langfuse-watch.service",
		"rm -- ~/.codex/langfuse-export-state.json",
		"It is the only required service-start step",
	} {
		if !strings.Contains(readme, required) {
			t.Fatalf("README missing %q", required)
		}
	}
	stopIndex := strings.Index(readme, "systemctl --user stop codex-langfuse-watch.service")
	removeIndex := strings.Index(readme, "rm -- ~/.codex/langfuse-export-state.json")
	installIndex := strings.Index(readme, "./install.sh")
	if stopIndex >= removeIndex || removeIndex >= installIndex {
		t.Fatal("README must document the version 1 cutover as stop, remove state, then install")
	}
	if strings.Contains(readme, "systemctl --user start codex-langfuse-watch.service") {
		t.Fatal("README must use install.sh as the only service-start path")
	}
	for _, required := range []string{
		"TestDoctorMode",
		"TestManualExportCLIJSONOutput",
		"TestDeterministicScores",
		"TestCreateDeterministicScores",
		"TestDocsWorkspaceIdentity",
		"TestWorkspaceIdentity",
		"TestManualWorkspaceIdentity",
		"TestWatchEnvironmentSnapshot|TestWatchEnvironmentRetry",
		"LIVE_LANGFUSE_IDENTITY_TRACE_ID",
		"LIVE_LANGFUSE_HOSTNAME",
		"LIVE_LANGFUSE_ENVIRONMENT",
		"TestLiveWorkspaceIdentityTrace",
	} {
		if !strings.Contains(testingDoc, required) {
			t.Fatalf("TESTING missing %q", required)
		}
	}
	if !strings.Contains(exampleConfig, "Workspace identity needs no configuration") {
		t.Fatal("example config does not state the single identity path")
	}
	for _, document := range []struct {
		name string
		text string
	}{
		{name: "README", text: readme},
		{name: "TESTING", text: testingDoc},
		{name: "example config", text: exampleConfig},
	} {
		for _, forbidden := range []string{
			strings.Join([]string{"LANGFUSE", "USER", "ID", "MODE"}, "_"),
			strings.Join([]string{"--", "environment"}, ""),
			"folder(branch)@hostname",
			"path/to/repo(branch)@hostname",
		} {
			if strings.Contains(document.text, forbidden) {
				t.Fatalf("%s retains legacy identity surface %q", document.name, forbidden)
			}
		}
	}
}

func TestDocsLangfuseMCPVersionConstraint(t *testing.T) {
	t.Parallel()

	readme := readRepoDoc(t, "README.md")
	exampleConfig := readRepoDoc(t, filepath.Join("examples", "codex-config.toml"))
	for _, required := range []string{
		`"mcp>=1.28,<2"`,
		`"langfuse-mcp==0.10.0"`,
	} {
		if !strings.Contains(exampleConfig, required) {
			t.Fatalf("example config missing %q", required)
		}
	}
	for _, required := range []string{
		"closed during `initialize`",
		"`mcp>=1.28,<2`",
		"incompatible MCP SDK v2",
	} {
		if !strings.Contains(readme, required) {
			t.Fatalf("README missing %q", required)
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
		"files:read_only",
		"files:changed",
		"command:search",
		"command:read",
		"command:network",
		"command:install",
		"tool:command",
		"tool:file_change",
		"tool:web_search",
		"verification:failed",
		"langfuse.observation.model.name",
		"langfuse.observation.usage_details",
		"cost_details",
		"command_kind",
		"Trace tags are the primary reusable trace filters",
		"mcp:<server>",
		"Observations: command search",
	} {
		if !strings.Contains(readme, required) {
			t.Fatalf("README missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"Saved Views",
		"saved views",
		"Views -> Create Custom View",
		"reusable view",
	} {
		if strings.Contains(readme, forbidden) {
			t.Fatalf("README still contains saved-view wording %q", forbidden)
		}
	}
	for _, required := range []string{
		"no observed local file changes",
		"always-on",
		"trace tags",
		"observation filters",
	} {
		if !strings.Contains(strings.ToLower(readme), required) {
			t.Fatalf("README missing %q", required)
		}
	}
	for _, required := range []string{
		"go test ./internal/agenttrace -run TestInsightCountMetadataSingleRepresentation -count=1",
		"TestGoldenLangfuseSingleRepresentation",
		"TestCountMetadataExportedOnAgent",
		"TestDocsNavigationFacetsAndFilters",
	} {
		if !strings.Contains(testingDoc, required) {
			t.Fatalf("TESTING missing %q", required)
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
		"langfuse.trace.tags",
		"active provider's insight navigation values plus observed `mcp:<server>` values",
		"mcp_server",
		"mcp_tool",
		"codex.tool.mcp",
		"claude.tool.mcp",
		"issues/list",
		"internal/agenttrace/TAG_RULES.md",
		"future watcher exports",
		"explicit re-export",
		"codex-langfuse-watch.service",
		"~/.codex/bin/codex-langfuse-exporter --path",
	} {
		if !strings.Contains(readme, required) {
			t.Fatalf("README missing %q", required)
		}
	}
	for _, required := range []string{
		"TestDocsTagsAndMCPUsage",
		"TestLangfuseTraceTagsExportedOnSpans",
		"TestGoldenLangfuseTagsContract",
		"TestInsightTagFacets",
		"TestDocsNavigationFacetsAndFilters",
	} {
		if !strings.Contains(testingDoc, required) {
			t.Fatalf("TESTING missing %q", required)
		}
	}
	if !strings.Contains(installScript, "codex-langfuse-watch.service") {
		t.Fatalf("install.sh missing service restart")
	}
}

// TEST-408
func TestDocsLangfuseCostPricing(t *testing.T) {
	t.Parallel()

	readme := readRepoDoc(t, "README.md")
	for _, required := range []string{
		"--sync-model-pricing",
		"https://openai.com/api/pricing/",
		"2026-05-02",
		"gpt-5.5",
		"gpt-5.4",
		"gpt-5.4-mini",
		"gpt-5.3-codex-spark",
		"claude-opus-4-7",
		"claude-sonnet-4-6",
		"claude-haiku-4-5-20251001",
		"https://developers.openai.com/api/docs/models/gpt-5.3-codex",
		"https://platform.claude.com/docs/en/about-claude/pricing",
		"2026-05-05",
		"input_cached_tokens",
		"output_reasoning_tokens",
		"cache_creation_input_tokens",
		"cache_read_input_tokens",
		"When provider pricing changes",
		"internal/langfuse/models.go",
		"Do not add fallback local cost multiplication",
		"install.sh",
		"explicit re-export",
		"~/.codex/bin/codex-langfuse-exporter --session-id",
	} {
		if !strings.Contains(readme, required) {
			t.Fatalf("README missing %q", required)
		}
	}
}

// TEST-514
func TestDocsCodingAgentIntegrationGuide(t *testing.T) {
	t.Parallel()

	readme := readRepoDoc(t, "README.md")
	testingDoc := readRepoDoc(t, "TESTING.md")
	for _, required := range []string{
		"### Adding A Coding Agent",
		"Gemini CLI",
		"OpenCode",
		"Goose",
		"source transcript/log -> internal/<provider>trace -> agenttrace.Turn -> tracecontract.Trace -> langfuse.EmitSpans",
		"internal/providers/providers.go",
		"testdata/sources/<provider>/*.jsonl",
		"testdata/manifest.json",
		"go test ./test -run TestGoldenTraceContract -count=1",
		"exportstate.QueueRequest",
		"`--watch` remains the only automatic exporter",
		"Do not add provider wrapper execution",
		"placeholder providers without real fixtures",
	} {
		if !strings.Contains(readme, required) {
			t.Fatalf("README missing coding-agent integration text %q", required)
		}
	}
	for _, required := range []string{
		"go test ./internal/providers -count=1",
		"go test ./test -run TestProviderParserDispatchHasOneOwner -count=1",
	} {
		if !strings.Contains(testingDoc, required) {
			t.Fatalf("TESTING missing provider integration command %q", required)
		}
	}
}

// TEST-508
func TestDocsClaudeSupportContract(t *testing.T) {
	t.Parallel()

	readme := readRepoDoc(t, "README.md")
	testingDoc := readRepoDoc(t, "TESTING.md")
	agents := readRepoDoc(t, "AGENTS.md")
	for _, required := range []string{
		"Claude Code support",
		"--provider claude --path",
		"--claude-hook",
		"claude.turn.transcript",
		"claude.agent",
		"claude.transcript",
		"claude.terminal",
		"claude.tool.command",
		"claude.tool.file_change",
		"claude.tool.mcp",
		"claude.tool.generic",
		"Claude Code subscription billing is separate from Anthropic API token pricing",
	} {
		if !strings.Contains(readme, required) {
			t.Fatalf("README missing %q", required)
		}
	}
	for _, required := range []string{
		"go test ./internal/claudetrace -count=1",
		"go test ./internal/claudehook ./internal/exportstate ./internal/watch -run 'TestClaudeHookEnqueuesStopOnly|TestExportStateQueueDedupe|TestWatchDrainsClaudeQueue|TestWatchReloadsClaudeQueueFromHookState' -count=1",
		"go test ./cmd/codex-langfuse-exporter -run 'TestCLIProviderSelection|TestManualProviderExportCLIIntegration' -count=1",
		"CHECK-001",
	} {
		if !strings.Contains(testingDoc, required) {
			t.Fatalf("TESTING missing %q", required)
		}
	}
	for _, required := range []string{
		"Do not add Claude polling",
		"Keep Claude pricing definitions source-backed",
		"do not add local cost math",
		"Do not mutate Claude settings automatically",
	} {
		if !strings.Contains(agents, required) {
			t.Fatalf("AGENTS missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"native Claude OTEL forwarding",
		"Claude transcript polling",
		"automatic Claude settings mutation",
		"Claude cost calculation",
	} {
		if strings.Contains(readme, forbidden) {
			t.Fatalf("README contains unsupported Claude claim %q", forbidden)
		}
	}
}

// TEST-530
func TestDocsCanonicalSemanticToolFamilies(t *testing.T) {
	t.Parallel()

	readme := readRepoDoc(t, "README.md")
	for _, required := range []string{
		"codex.tool.command",
		"codex.tool.file_change",
		"codex.tool.mcp",
		"codex.tool.web_search",
		"codex.tool.tool_search",
		"claude.tool.command",
		"claude.tool.file_change",
		"claude.tool.mcp",
		"claude.tool.generic",
		"tool:command",
		"tool:file_change",
		"<provider>.tool.command",
		"<provider>.tool.file_change",
		"<provider>.tool.mcp",
	} {
		if !strings.Contains(readme, required) {
			t.Fatalf("README missing canonical semantic family text %q", required)
		}
	}
	for _, forbidden := range []string{
		strings.Join([]string{"codex", "tool", "exec_command"}, "."),
		strings.Join([]string{"codex", "tool", "apply_patch"}, "."),
		strings.Join([]string{"claude", "tool", "bash"}, "."),
		strings.Join([]string{"tool", "bash"}, ":"),
		"patch" + "_count",
		strings.Join([]string{"Claude pricing", "deferred"}, " is "),
	} {
		if strings.Contains(readme, forbidden) {
			t.Fatalf("README contains legacy semantic family text %q", forbidden)
		}
	}
}

// EVAL-008
func TestEvalDocsClaudeContractCompleteness(t *testing.T) {
	t.Parallel()

	readme := readRepoDoc(t, "README.md")
	for _, required := range []string{
		"Rerun `CHECK-001` after Claude Code upgrades that change transcript shape",
		"Stop hook",
		"hook queues work only",
		"watch service drains the queued transcript",
		"thinking blocks are omitted",
		"Langfuse calculates cost",
	} {
		if !strings.Contains(readme, required) {
			t.Fatalf("README missing Claude completeness phrase %q", required)
		}
	}
}

// TEST-620
func TestDocsPerformanceGateSeparation(t *testing.T) {
	t.Parallel()

	testingDoc := readRepoDoc(t, "TESTING.md")
	handoff := readRepoDoc(t, filepath.Join("plans", "multi-machine-tracing-gateway-handoff.md"))
	for _, required := range []string{
		"Benchmark(InsightRollup|ClaudeParserCorpus)",
		"TestEvalWatchExportLatency|TestEvalHookQueueDrainLatency",
		"performance-test-stability.md",
	} {
		if !strings.Contains(testingDoc, required) {
			t.Fatalf("TESTING missing performance-gate contract %q", required)
		}
	}
	for _, retired := range []string{
		"TestEvalInsightRollupLatency",
		"TestEvalClaudeParserDeterminismAndLatency",
	} {
		if strings.Contains(testingDoc, retired) {
			t.Fatalf("TESTING still references retired scheduler-sensitive test %q", retired)
		}
	}
	for _, required := range []string{
		"benchmarks are non-binding engineering evidence",
		"binding watcher and Claude queue latency thresholds remain release-blocking",
	} {
		if !strings.Contains(handoff, required) {
			t.Fatalf("gateway handoff missing performance closeout %q", required)
		}
	}
	for _, stale := range []string{
		"result remains failed",
		"on a quiet host",
		"Performance threshold failures are release-blocking",
	} {
		if strings.Contains(handoff, stale) {
			t.Fatalf("gateway handoff contains superseded performance guidance %q", stale)
		}
	}
}

// TEST-409
func TestNoLocalCostDetailsOrDirectIngestionShortcut(t *testing.T) {
	t.Parallel()

	for _, pattern := range []string{
		filepath.Join("..", "internal", "langfuse", "*.go"),
		filepath.Join("..", "cmd", "codex-langfuse-exporter", "*.go"),
	} {
		paths, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range paths {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			text := string(raw)
			if strings.Contains(text, "cost_details") {
				t.Fatalf("%s emits local cost_details", path)
			}
			if strings.Contains(text, "/api/public/ingestion") && filepath.Base(path) != "scores.go" {
				t.Fatalf("%s uses direct ingestion export path", path)
			}
		}
	}
	if !strings.Contains(readRepoDoc(t, "README.md"), "/api/public/otel/v1/traces") {
		t.Fatal("README must keep OTLP as the trace export path")
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
