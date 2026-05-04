# Testing

Use these commands before and after code changes. They are intentionally direct Go commands so Codex/LLM maintainers do not need a separate harness.

## Fast Checks

```sh
go test ./... -count=1
```

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

Provider CLI checks:

```sh
go test ./cmd/codex-langfuse-exporter -run 'TestCLIProviderSelection|TestManualProviderExportCLIIntegration' -count=1
go test ./internal/providers -count=1
go test ./test -run TestProviderParserDispatchHasOneOwner -count=1
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
git diff --check
```
