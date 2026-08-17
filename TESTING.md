# Testing

Use these commands before and after code changes. They are intentionally direct Go commands so Codex/LLM maintainers do not need a separate harness.

Multi-machine gateway promotion and missing-trace reconciliation are not implemented in the current release. Do not treat the machine-local `switch-langfuse-target` prototype as a production test command. The implementation sequence and future acceptance controls are in [the canonical handoff](plans/multi-machine-tracing-gateway-handoff.md); add the focused executable tests to this file when the repository-owned reconciliation mode exists.

## Fast Checks

```sh
go test ./... -count=1
```

## Performance Checks

Correctness and determinism stay in the normal test suite. Binding latency gates cover the user-visible watcher scan and Claude hook queue drain; run them serially and repeat them five times:

```sh
go test -p=1 ./internal/watch -run '^(TestEvalWatchExportLatency|TestEvalHookQueueDrainLatency)$' -parallel=1 -count=5 -v
```

Insight rollup and Claude parser micro-performance are non-binding Go benchmarks. Record five samples with allocation counts for comparison when either path changes:

```sh
go test -p=1 ./internal/agenttrace ./internal/claudetrace -run '^$' -bench 'Benchmark(InsightRollup|ClaudeParserCorpus)$' -benchmem -count=5
```

Do not turn a single benchmark sample into a release threshold. The rationale and superseded scheduler-sensitive assertions are recorded in [ADR-PERF-001](plans/performance-test-stability.md).

Run the normalized rollout contract only:

```sh
go test ./test -run TestGoldenTraceContract -count=1
```

Parser, redaction, reasoning, and tool mapping:

```sh
go test ./internal/codextrace -count=1
go test ./internal/claudetrace -count=1
```

Watcher state, queue, retry, dedupe, hook, and cancellation:

```sh
go test ./internal/claudehook ./internal/exportstate ./internal/watch -run 'TestClaudeHookEnqueuesStopOnly|TestExportStateQueueDedupe|TestWatchDrainsClaudeQueue|TestWatchReloadsClaudeQueueFromHookState' -count=1
go test ./internal/watch -count=1
```

Progressive unfinished-turn contracts:

```sh
go test ./internal/codextrace ./internal/watch -run 'TestIncompleteObservationPrefixStability|TestProgressiveSuffixPlan' -count=1
go test ./internal/exportstate -run 'TestVersion2State|TestStateUpdatePreservesQueue' -count=1
go test ./internal/langfuse -run 'TestOTLPProgressiveThenFinal|TestProgressiveSpanAttributes' -count=1
go test ./internal/watch -run 'TestWatchProgressiveLifecycle|TestWatchProgressiveFailureRetry|TestWatchLogs' -count=1
go test ./internal/watch -run TestEvalWatchExportLatency -count=1 -v
go test ./test -run TestDocsProgressiveCodexVisibility -count=1
```

Live child-before-parent verification against the configured loopback Langfuse project:

```sh
LIVE_LANGFUSE_PROGRESSIVE_PROBE=1 go test ./internal/langfuse -run TestLiveProgressiveChildBeforeParent -count=1 -v
```

Provider CLI checks:

```sh
go test ./cmd/codex-langfuse-exporter -run 'TestCLIProviderSelection|TestManualProviderExportCLIIntegration' -count=1
go test ./internal/providers -count=1
go test ./test -run TestProviderParserDispatchHasOneOwner -count=1
```

Doctor, trace URL, JSON output, and deterministic score checks:

```sh
go test ./cmd/codex-langfuse-exporter -run 'TestDoctorMode|TestManualExportCLIJSONOutput' -count=1
go test ./internal/agenttrace -run 'TestDeterministicScores|TestInsightRollup' -count=1
go test ./internal/langfuse -run 'TestCreateDeterministicScores|TestOTLPHTTPExport' -count=1
go test ./test -run TestDocsWorkspaceIdentity -count=1
```

Langfuse MCP launcher compatibility check:

```sh
go test ./test -run TestDocsLangfuseMCPVersionConstraint -count=1
```

Langfuse OTLP projection and trace verification:

```sh
go test ./internal/langfuse -count=1
```

Count metadata and Langfuse projection checks:

```sh
go test ./internal/agenttrace -run TestInsightCountMetadataSingleRepresentation -count=1
go test ./test -run TestGoldenLangfuseSingleRepresentation -count=1
go test ./internal/langfuse -run TestCountMetadataExportedOnAgent -count=1
go test ./test -run TestDocsNavigationFacetsAndFilters -count=1
```

Tags and MCP usage checks:

```sh
go test ./internal/agenttrace -run TestInsightTagFacets -count=1
go test ./test -run TestGoldenLangfuseTagsContract -count=1
go test ./internal/langfuse -run TestLangfuseTraceTagsExportedOnSpans -count=1
go test ./test -run TestDocsTagsAndMCPUsage -count=1
```

Model pricing sync checks:

```sh
go test ./internal/langfuse -run 'TestModelPricingCatalogCoversOpenAIAndAnthropicModels|TestModelDefinitionSyncCreatesMissingModels' -count=1
```

Workspace identity checks:

```sh
go test ./internal/langfuse -run '^(TestWorkspaceIdentity|TestWorkspaceIdentityProjection|TestOTLPProgressiveThenFinal)$' -count=1
go test ./cmd/codex-langfuse-exporter -run '^TestManualWorkspaceIdentity$' -count=1
go test ./internal/watch -run '^(TestWatchEnvironmentSnapshot|TestWatchEnvironmentRetry)$' -count=1
go test ./test -run '^TestDocsWorkspaceIdentity$' -count=1
```

After an explicitly authorized deployment and the destructive version 1 reset documented in `README.md`, verify that startup created only version 2 state:

```sh
jq -e '.version == 2' ~/.codex/langfuse-export-state.json
```

To compare one authorized live trace with locally observed identity values:

```sh
LIVE_LANGFUSE_IDENTITY_TRACE_ID="<trace-id>" LIVE_LANGFUSE_HOSTNAME="$(hostname)" LIVE_LANGFUSE_ENVIRONMENT="<derived-environment>" LIVE_LANGFUSE_CWD="$(pwd -P)" LIVE_LANGFUSE_BRANCH="$(git branch --show-current)" go test ./internal/langfuse -run TestLiveWorkspaceIdentityTrace -count=1
```

This live gate checks the trace User and Environment, every observation's Environment and CWD/branch metadata, and every deterministic score's Environment without printing those private values on success.

Live Claude pricing check for a trace produced by the same validation session:

```sh
LIVE_LANGFUSE_CLAUDE_COST_TRACE_ID="<trace-id>" go test ./internal/langfuse -run TestLiveClaudeCostTrace -count=1
```

## Fuzz Smoke

```sh
go test ./internal/codextrace -run '^$' -fuzz=FuzzParseTurnsDoesNotPanic -fuzztime=10s
go test ./internal/codextrace -run '^$' -fuzz=FuzzExportTextRedactsSentinels -fuzztime=10s
```

## Fixture Contract

`testdata/manifest.json` is the single fixture inventory. Add source JSONL fixtures under `testdata/sources/<provider>` and normalized expectations under `testdata/golden`; do not add another registry.

Every new fixture should cover a clear behavior category, avoid real secrets, and keep raw OTLP transport fields out of golden files.

## Manual Checks

CHECK-001 is the live Claude Code smoke check. Use the cheapest Claude model available in the installed CLI, for example `haiku`.

1. Run a small Claude Code print-mode prompt that persists a transcript and triggers the configured Stop hook, for example `claude --model haiku -p "Reply exactly: clt-live-fixture"`.
2. Run `~/.codex/bin/codex-langfuse-exporter --provider claude --path <transcript.jsonl>` against the created transcript.
3. Run `LIVE_LANGFUSE_CLAUDE_TRACE_ID="<trace-id>" go test ./internal/langfuse -run TestLiveClaudeParityTrace -count=1` for the trace produced by the same validation session.
4. Let `codex-langfuse-watch.service` drain the queued hook request.
5. In Langfuse, confirm `claude.turn.transcript`, `claude.agent`, `claude.transcript`, `claude.terminal`, and any expected canonical tool observations such as `claude.tool.command`, `claude.tool.file_change`, `claude.tool.mcp`, or `claude.tool.generic` appear.
6. Record the Claude Code version, model alias, trace IDs, and whether manual export and hook-triggered export both verified in Langfuse.

## Production Gate

Before publishing a release or public demo, run:

```sh
go test ./... -count=1
go test ./... -coverpkg=./... -coverprofile=/tmp/codex-langfuse-tracer.all.cover
go test ./internal/codextrace -run '^$' -fuzz=FuzzParseTurnsDoesNotPanic -fuzztime=10s
go test ./internal/codextrace -run '^$' -fuzz=FuzzExportTextRedactsSentinels -fuzztime=10s
go test ./internal/claudetrace ./internal/claudehook ./internal/exportstate -count=1
go test -p=1 ./internal/watch -run '^(TestEvalWatchExportLatency|TestEvalHookQueueDrainLatency)$' -parallel=1 -count=5 -v
git diff --check
```
