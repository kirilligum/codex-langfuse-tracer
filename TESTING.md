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

Codex discovery, incomplete-scan health, watermark recovery, and cache lifecycle regressions:

```sh
go test ./internal/codextrace -run '^(TestSessionPathsIncludesMixedDepths|TestSessionPathsReturnsPartialResultsAndError|TestSessionPathsMissingRootIsAnError|TestFindSessionByIDPropagatesMissingRoot|TestLatestSessionPropagatesMissingRoot|TestParseTurnsFilteredOmitsProcessedTurnObservations)$' -count=1
go test ./internal/watch -run '^(TestWatchStatFailureRetainsWatermarkAndRecovers|TestWatchDiscoveryFailureAllowsHealthyProgress|TestWatchCancellationAfterParseDoesNotExportOrAdvance|TestWatchCorruptSourceDoesNotBlockHealthyTurn|TestWatchParseRetryDeadlineAndRepair|TestWatchCachedIncompleteTurnCompletesAfterAppend|TestWatchPendingScoreBypassesSuccessCache|TestWatchChangedDuringParseDoesNotCacheOrAdvance|TestWatchScanCacheEvictsOldestMetadataEntry|TestWatchCacheEvictionRereadsWithoutRepeatingCompletedWork|TestWatchRestartDoesNotDuplicateDurablyProcessedTrace|TestWatchMissingSessionsRootIsIncompleteAndDrainsQueue|TestWatchFiltersProcessedTurnsBeforeRetainingObservations|TestLargeRolloutProbeRejectsSkippedTarget|TestLargeRolloutTargetConsumptionOracle|TestLargeRolloutProbeRejectsMalformedTail)$' -count=1
go test ./cmd/codex-langfuse-exporter -run '^TestDoctorMode$' -count=1
```

The stat and discovery regressions prove successful turns can checkpoint while an uncertain source pins the global watermark. Quiet-mode tests require a bounded `ERROR: watch_scan_incomplete` line, and the doctor fixture fails on that marker, pending scores, and unavailable journal data. Cache tests cover parse retry deadlines, appends, a source changing during parse, pending-score bypass, process restart, and an over-capacity source set. Eviction may cause rereading; durable processed IDs must still prevent duplicate callbacks.

Export state lock recovery and upgrade transaction checks:

```sh
go test ./internal/exportstate ./internal/claudehook ./internal/watch ./cmd/codex-langfuse-exporter -count=1
go test -race ./internal/exportstate ./internal/claudehook ./internal/watch ./cmd/codex-langfuse-exporter -count=1
go test ./test -run 'TestInstallUninstallScripts|TestInstallOrderingAndFailures|TestInstallReportsPostStopFailureState|TestDocsWorkspaceIdentity|TestDocsExportStateLockUpgrade' -count=1
```

These cover killed lock owners, partial writes, serialized subprocess writers, startup and checkpoint retries, hook non-acknowledgement, SIGTERM cancellation, staged installer promotion, and version 3 state preservation. They establish process-crash recovery on the tested local filesystem; they do not certify power-loss durability or mixed legacy/new writer operation.

`TestCLIHookSignalsTerminatePendingInputAndEnqueue` exercises SIGINT and SIGTERM in subprocesses after incomplete hook input and before a contended enqueue can commit. Hooks retain normal signal termination; the watcher alone intercepts signals for a graceful exit. `TestInstallUninstallScripts` runs the real installer, compiler, and pricing preflight against a TLS mock with explicit certificate trust and strict method, path, authentication, and model payload checks. Only systemd is stubbed. It covers fresh install, upgrade, pricing failure, promotion ordering, state preservation, and uninstall. `TestInstallOrderingAndFailures` retains fast stubbed checks of individual failure boundaries.

The focused regression names are `TestStateLockRecoversAfterKilledOwner`, `TestStateLockDoesNotStealLiveOwner`, `TestStateUpdatesSerializeAcrossProcesses`, `TestStateInterruptedWritePreservesCommittedJSON`, `TestStateCommitSurvivesKillBeforeUnlock`, `TestStateLoadOrCreatePreservesEnqueueInEitherOrder`, `TestStateInvalidJSONIsNeverReset`, `TestStateWriteErrorsPreserveCommittedFile`, `TestStateLockCancellationAndCallbackFailureRelease`, `TestWatchWaitsForStateWithoutRestarting`, `TestWatchRetriesPendingCheckpointOnly`, `TestWatchRetriesQueueRemovalAfterCheckpoint`, `TestWatchLockBackoffLogThrottleAndShutdown`, `TestClaudeHookLockTimeoutIsNotAcknowledged`, and `TestCLISignalCancelsStateWait`.

Delivery ambiguity and checkpoint diagnostics use local mock receivers and temporary version 3 state:

```sh
go test ./internal/watch -run '^(TestWatchSpanCheckpointFailureLogs|TestWatchRetriesAfterLostAcknowledgement|TestWatchRestartAfterSpanSuccessBeforeCheckpoint)$' -count=1 -timeout=120s -v
go test ./internal/watch -run '^(TestWatchRetriesAfterLostAcknowledgement|TestWatchRestartAfterSpanSuccessBeforeCheckpoint)$' -count=5 -timeout=120s -v
go test -race ./internal/watch -run '^(TestWatchRetriesAfterLostAcknowledgement|TestWatchRestartAfterSpanSuccessBeforeCheckpoint)$' -count=1 -timeout=120s
go test ./internal/langfuse -run '^(TestValidateClaudeObservationRows|TestClaudeSmokeTraceRejectsDuplicatePaginatedIDs)$' -count=1
```

The callback-success line occurs before the watcher checkpoint; the checkpoint-unconfirmed test injects cancellation before the state write. The lost-ack test records an OTLP request before canceling its response. The Unix subprocess test kills a child after its export-success log and before the checkpoint. These tests establish retry behavior at their controlled boundaries; they do not certify Langfuse durability, all SDK batches, exactly-once delivery, or absence of historical duplicates.

Completed-turn and canonical observation contracts:

```sh
go test ./internal/codextrace ./internal/watch -run 'TestIncompleteTurnWaitsForCompletion|TestCompletedTurnScoreRetryUsesStableEnvironment' -count=1
go test ./internal/exportstate -run 'TestVersion3State|TestStateUpdatePreservesQueue' -count=1
go test ./internal/langfuse -run 'TestOTLPCompletedTurnSingleBatch|TestCanonicalObservationIO' -count=1
go test ./internal/watch -run 'TestIncompleteTurnWaitsForCompletion|TestCompletedTurnScoreRetryUsesStableEnvironment|TestWatchLogs' -count=1
go test ./internal/watch -run '^TestWatchSpanCheckpointFailureLogs$' -count=1 -v
go test ./internal/watch -run TestEvalWatchExportLatency -count=1 -v
go test ./test -run TestDocsCompletedCodexVisibility -count=1
```

Live completed-trace verification against the configured loopback Langfuse project:

```sh
LIVE_LANGFUSE_COMPLETED_TRACE_PROBE=1 go test ./internal/langfuse -run TestLiveCompletedTraceShape -count=1 -v
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
LIVE_LANGFUSE_CODEX_SMOKE_TRACE_ID="<trace-id>" go test ./internal/langfuse -run '^TestLiveCodexSmokeTrace$' -count=1 -v
```

`TestTraceVerificationClient` models the v2 API's raw serialized I/O strings, including delayed output visibility. `TestObservationTextMatchesSerializedStringOnly` verifies exactly one JSON string decoding step and rejects missing, null, structured, malformed, mismatched, and unencoded values. Literal user quotes and equivalent JSON escapes retain their meaning. The live completed-trace check uses the same comparison contract.

`TestLiveCodexSmokeTrace` is a read-only live check for one already-exported Codex trace. It reads all observation pages, rejects repeated observation IDs, requires exactly one `codex.agent` root and `codex.transcript` generation, verifies non-empty serialized root input/output, and requires the row IDs/names/counts to remain stable for five seconds. A current snapshot does not establish historical uniqueness or a backend-wide exactly-once guarantee.

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
go test ./internal/langfuse -run '^(TestWorkspaceIdentity|TestWorkspaceIdentityProjection|TestOTLPCompletedTurnSingleBatch)$' -count=1
go test ./cmd/codex-langfuse-exporter -run '^TestManualWorkspaceIdentity$' -count=1
go test ./internal/watch -run '^TestWatchEnvironmentPersistsOnlyAfterSuccessfulSpanExport$' -count=1
go test ./test -run '^TestDocsWorkspaceIdentity$' -count=1
```

After deployment, confirm version 3 state is present and preserved through the lock-only update. Do not perform the older-schema destructive reset for a version 3 lock-protocol upgrade:

```sh
jq -e '.version == 3' ~/.codex/langfuse-export-state.json
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

For a fresh Codex turn that the automatic watcher has already scored, run the read-only Langfuse check against its trace ID:

Use the `LIVE_LANGFUSE_CODEX_SMOKE_TRACE_ID` command above with the trace ID from the watcher's `scored` log line. Do not manually export the turn. The test uses the configured project's read-only observation API and does not create or modify a trace.

CHECK-001 validates the automatic Claude hook-to-watcher path. Use the cheapest Claude model available in the installed CLI, for example `haiku`. Do not manually export this transcript.

1. Record a start time. Run a small Claude Code print-mode prompt that persists a transcript and triggers the already user-configured Stop hook, for example `claude --model haiku -p "Reply exactly: clt-live-fixture"`.
2. Let `codex-langfuse-watch.service` drain the queued hook request. From the local watcher log, record the `trace` value on the successful `scored` line for this run; this line follows span export and score callbacks. Do not publish raw journal output or transcript contents. If there is no success line, diagnose the hook, queue, and watcher failure; manual export does not make CHECK-001 pass.
3. Check the basic trace shape in the same configured Langfuse project:

   ```sh
   LIVE_LANGFUSE_CLAUDE_SMOKE_TRACE_ID="<trace-id>" go test ./internal/langfuse -run '^TestLiveClaudeSmokeTrace$' -count=1 -v
   ```

   This read-only check waits for the root and transcript, reads all observation pages, checks unique IDs and exactly one `claude.agent` root and `claude.transcript` generation, and requires non-empty root input/output. Current returned rows do not prove that historical duplicates never existed or that the backend never merged them.
4. Record Claude Code version, model alias, run time, trace ID, and the smoke result without private transcript or journal contents.

Full tool parity is a separate optional live check. Use another automatically exported session with a benign command observation, a file change contained in a temporary directory, and an already configured read-only MCP tool. Do not install tools or alter Claude settings just for this check. Only when all three tool families are present, run:

```sh
LIVE_LANGFUSE_CLAUDE_TRACE_ID="<full-parity-trace-id>" go test ./internal/langfuse -run '^TestLiveClaudeParityTrace$' -count=1 -v
```

The full parity check also requires tags, usage, and model pricing. A reply-only smoke trace cannot pass it. If a safe MCP tool or another requirement is unavailable, record full parity as unperformed and keep the basic smoke result separate.

Manual CLI validation is a separate optional check using a different transcript/session that was not queued by the Stop hook. Do not use the CHECK-001 session for manual export or manual parity. If hook-free input cannot be established with the user's configuration, mark manual validation unperformed. The tracer does not edit Claude settings.

## Production Gate

Before publishing a release or public demo, run:

The opt-in large-rollout probe copies the target to a private disk-backed snapshot, appends a unique completed marker to that same file, inventories actual source trace IDs, and verifies the marker and known source IDs were visited. It uses a private state copy and local span/score callbacks; it never changes the supplied source or production state. To run it against an actual source and state:

```sh
go test -c -o /tmp/codex-langfuse-watch-live.test ./internal/watch
CODEX_LANGFUSE_LARGE_ROLLOUT_PATH="/path/to/large-rollout.jsonl" \
CODEX_LANGFUSE_WATCH_STATE_PATH="$HOME/.codex/langfuse-export-state.json" \
/usr/bin/time -v /tmp/codex-langfuse-watch-live.test -test.run '^TestLiveCodexLargeRolloutFilteredScan$' -test.count=1 -test.v
```

The live probe requires a source of at least 100 MiB and an existing version 3 state. It records byte and trace counts, source/state overlap, callbacks and RSS. Do not include rollout content or trace IDs in reports. This probe is separate from the generated multi-case memory gate.

Run the resource matrix only on a host that meets the frozen reserve/PSI protocol. It writes generated JSONL under disk-backed `/var/tmp`, runs candidate and installed-baseline workers serially in fresh systemd user scopes hard-capped at 768 MiB memory with no swap, takes five RSS/time measurements per case for each revision, and fails if either worker cannot complete or candidate process RSS exceeds the fixed 768 MiB allowance.

Build the comparison worker from the exact installed baseline recorded in the acceptance plan. The helper files are copied into a detached worktree only for this measurement:

```sh
baseline_worktree=$(mktemp -d /var/tmp/codex-langfuse-baseline.XXXXXX)
git worktree add --detach "$baseline_worktree" 03139fa58eb57c9a66b24c76903847d9c1330ea3
cp internal/watch/memory_gate_spec_test.go "$baseline_worktree/internal/watch/"
cp internal/watch/memory_gate_legacy_worker_test.go "$baseline_worktree/internal/watch/"
(
  cd "$baseline_worktree"
  go test -p=1 -c -ldflags "-X github.com/kirilligum/codex-langfuse-tracer/internal/watch.memoryGateLegacySourceRevision=03139fa58eb57c9a66b24c76903847d9c1330ea3" -o /var/tmp/codex-langfuse-baseline.test ./internal/watch
)
```

Run the measured gate and then remove the temporary worktree and binary:

```sh
CODEX_LANGFUSE_MEMORY_GATE_BASELINE_BINARY=/var/tmp/codex-langfuse-baseline.test \
CODEX_LANGFUSE_RUN_MEMORY_GATE=1 go test -p=1 ./internal/watch -run '^TestWatchMemoryEnvelope$' -count=1 -timeout=90m -v
git worktree remove --force "$baseline_worktree"
rm -f /var/tmp/codex-langfuse-baseline.test
```

The input preparation and each worker are separate processes. Both revisions use the same generated source and version 3 state for 100/400 MiB processed history followed by one selected completed EOF turn, a 100 MiB unprocessed backlog, a 100 MiB selected turn, a 16 MiB JSON record, pending-score retry, and healthy parsing behind corruption. The normal watcher suite also has a small fixture-oracle test for the processed-history plus selected-EOF case. A killed or skipped case is not a pass; preserve the reported resource failure and update [the acceptance record](plans/watcher-scan-recovery-and-memory-validation-plan.md).

```sh
go test ./... -count=1
go test ./... -coverpkg=./... -coverprofile=/tmp/codex-langfuse-tracer.all.cover
go test ./internal/codextrace -run '^$' -fuzz=FuzzParseTurnsDoesNotPanic -fuzztime=10s
go test ./internal/codextrace -run '^$' -fuzz=FuzzExportTextRedactsSentinels -fuzztime=10s
go test ./internal/claudetrace ./internal/claudehook ./internal/exportstate -count=1
go test -p=1 ./internal/watch -run '^(TestEvalWatchExportLatency|TestEvalHookQueueDrainLatency)$' -parallel=1 -count=5 -v
git diff --check
```
