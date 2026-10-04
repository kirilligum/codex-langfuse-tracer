# Testing

Run the focused checks for the layer changed, then the full suite before publishing. Use a private `TMPDIR` under `/home/kirill/.cache` when `/tmp` lacks space.

## Local checks

```sh
go test ./... -count=1
go test -race ./internal/exportstate ./internal/claudehook ./internal/watch ./cmd/codex-langfuse-exporter -count=1
go test ./internal/laminar ./internal/config -count=1
go test ./internal/agenttrace -run 'TestInsightTagFacets|TestDeterministicScores|TestInsightRollup' -count=1
go test ./internal/laminar -run 'TestLaminarSpanProjectionPreservesTraceContextAndMetadata|TestLaminarToolProjectionPreservesInputsOutputsAndErrors' -count=1
go test ./test -run '^TestFullAcceptanceLaminarTagsAndMCP$' -count=1
go test ./test -run 'TestInstallUninstallScripts|TestInstallOrderingAndFailures|TestInstallReportsPostStopFailureState' -count=1
git diff --check
```

The parser, redaction, and normalized cross-provider contract are exercised by the full suite. `testdata/manifest.json` is the only fixture inventory; keep raw OTLP fields out of golden fixtures. `TestGoldenTraceContract` exercises the same Laminar span contract for Codex and Claude.

The Laminar tests verify the local authenticated `/v1/traces` contract, empty-request installer/doctor preflight, span identity and hierarchy, typed metadata, redaction, and failure status. The installer integration runs the real compiler and installer against a local HTTP receiver while stubbing only systemd. It verifies that receiver failure leaves the old binary/unit/state/lock intact and that successful promotion preserves version 3 state. Lock-recovery coverage includes `TestStateLockRecoversAfterKilledOwner`, `TestStateInterruptedWritePreservesCommittedJSON`, `TestStateWriteErrorsPreserveCommittedFile`, `TestWatchRetriesQueueRemovalAfterCheckpoint`, `TestClaudeHookLockTimeoutIsNotAcknowledged`, and `TestCLISignalCancelsStateWait`.

Watcher retry tests establish at-least-once behavior around acknowledgement loss, process termination before a durable checkpoint, and state-lock contention. `TestWatchRetriesPendingSpanCheckpointOnly`, `TestLegacyPendingScoreCheckpointReexportsTraceWithStableEnvironment`, and `TestLegacyPendingCheckpointBypassesSourceCache` prove that version 3 Langfuse-era score checkpoints re-send the full trace to Laminar, preserve the saved environment, clear the old checkpoint only on success, and avoid skipping the source cache. They do not prove Collector durability or exactly-once delivery.

## Performance and memory

Run latency tests serially and five times when watcher or Claude queue behavior changes:

```sh
go test -p=1 ./internal/watch -run '^(TestEvalWatchExportLatency|TestEvalHookQueueDrainLatency)$' -parallel=1 -count=5 -v
```

Insight rollup and Claude parser benchmarks are non-binding engineering evidence. Record five samples with allocation counts when either path changes; do not convert one sample into a release threshold. See [the performance test rationale](plans/performance-test-stability.md).

```sh
go test -p=1 ./internal/agenttrace ./internal/claudetrace -run '^$' -bench 'Benchmark(InsightRollup|ClaudeParserCorpus)$' -benchmem -count=5
```

Run the generated memory matrix only when the host meets the frozen reserve/PSI protocol in [the watcher acceptance plan](plans/watcher-scan-recovery-and-memory-validation-plan.md). It creates fixtures under disk-backed `/var/tmp` and runs candidate and installed-baseline workers serially in fresh systemd user scopes capped at 768 MiB with swap disabled. Each workload has five samples; failures or skipped workers are not passes.

Build the legacy comparison worker from the exact installed baseline. The helper files are copied into a detached baseline worktree only for this measurement:

```sh
baseline_worktree=$(mktemp -d /var/tmp/codex-langfuse-baseline.XXXXXX)
git worktree add --detach "$baseline_worktree" 03139fa58eb57c9a66b24c76903847d9c1330ea3
cp internal/watch/memory_gate_spec_test.go "$baseline_worktree/internal/watch/"
cp internal/watch/memory_gate_legacy_worker_test.go "$baseline_worktree/internal/watch/"
(
  cd "$baseline_worktree"
  go test -p=1 -tags=legacywatchmemory -c \
    -ldflags "-X github.com/kirilligum/codex-langfuse-tracer/internal/watch.memoryGateLegacySourceRevision=03139fa58eb57c9a66b24c76903847d9c1330ea3" \
    -o /var/tmp/codex-langfuse-baseline.test ./internal/watch
)
```

Run the comparison and remove only the temporary worktree and binary afterward:

```sh
CODEX_LANGFUSE_MEMORY_GATE_BASELINE_BINARY=/var/tmp/codex-langfuse-baseline.test \
CODEX_LANGFUSE_RUN_MEMORY_GATE=1 go test -p=1 ./internal/watch \
  -run '^TestWatchMemoryEnvelope$' -count=1 -timeout=90m -v
git worktree remove --force "$baseline_worktree"
rm -f /var/tmp/codex-langfuse-baseline.test
```

The matrix uses the same version 3 source/state for processed history followed by a selected completed EOF turn, unprocessed backlog, a large selected turn, a 16 MiB JSON record, a legacy pending-checkpoint migration, and healthy parsing behind corruption. The legacy baseline uses its historical score-only path; the candidate must deliver the pending turn as a full Laminar trace. Never weaken the host reserve, PSI, memory cap, or sample count to turn a resource failure into a pass.

## Local live acceptance

These checks use the installed local receiver and the real Laminar backend. Do not print credentials, transcript contents, prompt text, state JSON, or raw service logs in issue comments.

1. Run `~/.codex/bin/codex-langfuse-exporter --doctor`. Require authenticated local receiver acceptance, Collector health/metrics endpoints, an active watcher, an empty queue, no pending legacy checkpoints, and no recent watcher errors.
2. Run one benign Codex turn that produces a completed rollout. Let the automatic watcher send it. Record only the trace ID from the `processed trace=` watcher event and the time of the request.
3. Confirm that the local Collector accepted the batch and that sent-span counters increased with no new queue backlog. Confirm the same trace and expected Codex span shape in the Laminar project UI. The local HTTP status alone is not backend-delivery evidence.
4. For Claude acceptance, use a separate benign Claude Code turn with the user's already-installed Stop hook. Confirm the watcher drains the queued request and verify its trace in Laminar. Do not manually export that transcript.
5. Record source revision, binary digest, service invocation, state version/counts, Collector image digest/config/queue state, trace IDs, and the distinct source, local-receiver, backend, and user-visible acceptance results. Keep provider/API inference out of the acceptance probe.

A restart or retry may produce the same trace IDs again if the Collector accepted the batch but the watcher checkpoint did not commit. Verify span identity and delivery evidence; do not claim exactly-once behavior or erase history to hide duplicates.

## Large local rollout probe

The opt-in probe copies a selected rollout and version 3 state to private temporary storage, appends a unique completed marker only to the copy, and checks that the parser visits the target IDs without retaining already-processed observations. It does not modify the supplied source or production state:

```sh
go test -c -o /var/tmp/codex-langfuse-watch-live.test ./internal/watch
CODEX_LANGFUSE_LARGE_ROLLOUT_PATH="/path/to/large-rollout.jsonl" \
CODEX_LANGFUSE_WATCH_STATE_PATH="$HOME/.codex/langfuse-export-state.json" \
/usr/bin/time -v /var/tmp/codex-langfuse-watch-live.test \
  -test.run '^TestLiveCodexLargeRolloutFilteredScan$' -test.count=1 -test.v
```

The source must be at least 100 MiB and the state must be version 3. The probe reports only aggregate source/state counts, parser visits, Collector callback count, and RSS; do not publish trace IDs or rollout contents.

## Fuzz smoke

```sh
go test ./internal/codextrace -run '^$' -fuzz=FuzzParseTurnsDoesNotPanic -fuzztime=10s
go test ./internal/codextrace -run '^$' -fuzz=FuzzExportTextRedactsSentinels -fuzztime=10s
```

## Release gate

```sh
go test ./... -count=1
go test -race ./internal/exportstate ./internal/claudehook ./internal/watch ./cmd/codex-langfuse-exporter -count=1
go test -p=1 ./internal/watch -run '^(TestEvalWatchExportLatency|TestEvalHookQueueDrainLatency)$' -parallel=1 -count=5 -v
git diff --check
```

A green source suite, successful GitHub checks, published release, installed binary, running service, Collector delivery, and a trace visible in Laminar are separate gates. Report each separately. The earlier Langfuse API probes and model-pricing tests do not apply to this Laminar-only exporter; the repository does not query a tracing backend API or calculate token cost locally.

## Completed steps during a running turn

`TestProgressiveStepsResolveUnderFinalRootWithoutDuplicateSpans` verifies stable
child identities, native pending-parent path metadata, and one final root.
`TestProgressiveCheckpointSurvivesRetryRestartAndCompletion` verifies that failed
exports do not advance progress, accepted prefixes survive restart, and completion
sends only the remaining spans. `TestClaudeToolHookExportsStepsAndStopFinalizesWithoutPolling`
uses the existing Claude fixture and queue to cover completed-tool events and
Stop finalization without directory polling. Per-call token usage tests reject
reuse of cumulative turn counters, and span projection checks that transcript
usage does not duplicate model-call usage.

For live acceptance, run a benign Codex turn with a short completed tool followed
by a longer tool. While it is running, verify completed model/tool spans in both
projects and inspect the pending turn tree in the native dashboard. Let the same
turn finish; verify one root per project, unchanged span identities, full output,
and no duplicated token accounting. Enable the Traces table's Realtime toggle
when checking newly arriving table rows. Do not manually export the test turn.

Claude requires an installed client and explicitly configured completed-tool
hooks in addition to Stop. Unit coverage is not native live Claude acceptance.
