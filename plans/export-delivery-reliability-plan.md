# Export delivery diagnostics and operational guidance implementation plan

- Project: `codex-langfuse-tracer`
- Date: 2026-09-22
- Source baseline inspected: `c6a086e001e9a42a73e047fae8cc9fbbf17ce2ba`
- Status: P0 through P4 implementation and local gates complete; authorized P5 publication and deployment in progress
- Intended executor: a coding model such as GPT-5.6 Luna, working one phase at a time
- Incident record: [duplicate observations RCA](duplicate-observations-rca-20260922.md)
- Operational owner: [multi-machine tracing handoff](multi-machine-tracing-gateway-handoff.md)

## 1. Objective and fixed decisions

Correct instructions that can cause repeated submissions, expose the distinction between a successful exporter return and a saved watcher checkpoint, and demonstrate the remaining retry behavior with isolated failure tests.

The stale-lock incident remains closed. Commit `827a66c` supplied the lock correction, included in the installed `2b8b915` build inspected during the RCA. That historical deployment evidence does not establish deployment of this follow-up.

Implement these decisions without reopening architecture selection:

1. Retain **at-least-once** delivery. An acknowledgement lost after acceptance, or process death before a checkpoint, can cause repeated submissions. Never mark a turn complete before sending to suppress duplicates.
2. Preserve state version 3, its fields, the persistent advisory-lock sidecar, and checkpoint-only retry on lock contention. No migration is needed.
3. Keep the watcher as the normal automatic path. Manual export remains an explicit operation that neither reads nor advances watcher completion checkpoints. Explain its limits accurately.
4. Add two bounded watcher diagnostics at the existing send/checkpoint boundary. Preserve existing `exported` and `scored` lines and manual CLI JSON output.
5. Extend existing regressions and add isolated acknowledgement-loss and subprocess-death tests. Use temporary files and loopback mock servers.
6. Reconciliation is independent work. It must correct its outdated API assumptions before implementation; it does not depend on completing this plan or achieving exactly-once delivery. Read-only inventory can be designed independently; do not implement an inventory command in this change.

### Scope limits

Do not implement remote preflight, a canonical observation classifier, manual/watcher coordination, a durable delivery ledger, new flags, alternate state files, a gateway, score timestamp changes, reconciliation, historical replay, or cleanup. Do not change span IDs, projection, scores, retry timing, or the HTTP exporter. If tests reveal a transport defect, preserve the concrete reproduction and report a separate bounded fix instead of silently expanding this patch.

Gateway admission alone would not make forwarding to Langfuse atomic with the gateway's checkpoint. This plan makes no exactly-once claim and assigns no unmeasured probabilities such as “low duplicate risk.”

## 2. Owners to read before editing

Use symbols and headings to locate code; line numbers can move.

| File / symbol | Ownership and relevance |
| --- | --- |
| `AGENTS.md` | Repository boundaries, fixture rules, verification commands. |
| `README.md`: Manual Export, delivery, Claude Code support | User-facing export scope, retries, hooks, caveats. |
| `TESTING.md`: Manual Checks, Production Gate | CHECK-001 currently sends one transcript through two paths. |
| `cmd/codex-langfuse-exporter/main.go`: `run`, `parseArgs` | Filters by `TurnID` only when supplied; otherwise exports every exportable turn in the selected session. Manual code calls export/scores directly. Read only. |
| `internal/watch/watch.go`: `processTurn` | Calls `ExportSpans`, saves pending scores, logs `exported`, sends scores, saves processed state, logs `scored`. Primary production edit. |
| Same file: `ScanOnce`, `drainQueue`, `mutateState`, `retryStateOperation` | Both providers share the turn path. Failed sends remain eligible; busy checkpoints retry without re-entering the send. |
| `internal/langfuse/export.go`: `ExportSpans`, `emitSpans`, `statusRecorder` | Actual OTLP transport and SDK batching. Successful return is a callback contract, not independent proof of remote visibility or complete storage. Read only. |
| `internal/exportstate/state.go`: `Load`, `Update`, `SetPendingScore`, `AddProcessed` | Durable version 3 state. Use the APIs in tests; leave implementation unchanged. |
| `internal/watch/watch_test.go` | `watchFixture`, `setMTime`, `completeTraceID`, `testWorkspace`, `TestWatchLogs`, score-retry and failed-send tests. |
| `internal/watch/watch_lock_unix_test.go` | Existing startup/checkpoint contention tests and synchronized log-writer patterns. |
| `internal/exportstate/state_lock_test.go` | Reference child-test-executable pattern, barriers, termination, cleanup. Helpers are package-private; do not export them. |
| `internal/langfuse/otlp_http_test.go` | OTLP mock/decoding pattern; existing success and HTTP-401 failure tests. |
| `internal/langfuse/api.go`: `ObservationClient.List`, `VerifyTrace` | Current v2 observation reader. The old reconciliation plan's `FetchTrace`/HTTP-404 design is stale. Read only. |
| `internal/langfuse/live_claude_parity_test.go` | `TestLiveClaudeParityTrace` requires command, file-change, MCP observations/tags, usage, and pricing. A simple no-tool prompt cannot satisfy it. Add a separate opt-in basic smoke validator in this file. The current parity check uses a name-keyed map, so validate duplicate IDs/counts on the uncollapsed list first. |
| `test/docs_static_test.go` | Existing text checks; they do not establish operational safety. |
| `testdata/manifest.json` | Only fixture inventory; reuse registered sources. No new corpus fixture is expected. |

## 3. Exact diagnostic contract

Add diagnostics only in `processTurn`. Do not introduce a logger abstraction or wrap the export path.

| Event | Exact line form | Placement |
| --- | --- | --- |
| Export callback succeeded | `span_export_succeeded trace=<trace-id> status=<status> checkpoint=pending` | `Stdout`, after `ExportSpans` returns `err == nil`, before invoking the mutation that sets pending scores. Suppress with `Quiet`. |
| Span checkpoint operation returned an error | `ERROR: span_checkpoint_unconfirmed trace=<trace-id> export_result=success replay_possible=true` | `Stderr`, before returning the error from that same mutation. Emit even with `Quiet`; preserve the original returned error. |

Keep `exported trace=... status=... path=...` after the successful pending-score checkpoint and `scored` after the processed checkpoint. Retain their exact formats and positions.

Interpretation rules:

- `span_export_succeeded` means the callback returned success. It does not certify all observations or SDK batches were stored, scores succeeded, or the turn is processed.
- `checkpoint=pending` means the operation has not completed; it does not mean a pending-score entry already exists on disk.
- `span_checkpoint_unconfirmed` means the operation returned an error. Do not claim no write reached disk: final cleanup can fail after a write.
- A busy lock that eventually recovers produces one new success line, existing throttled lock messages, and then `exported`. It must not produce another send or success line per lock retry.
- A killed process may emit the success line and nothing later. Logs are diagnostics, not a durable delivery ledger; missing logs cannot prove missing remote effects.
- On span export error, use the existing failure path and emit neither new line. The existing return type cannot classify every error as rejected or accepted.
- Score-only retry emits no new span success line because it does not send spans.
- New lines contain only event name, deterministic trace ID, numeric status, and fixed tokens. Add no source path, prompt, answer, tool output, URL, response body, credentials, or raw error text.

Intended control flow:

```text
status, err = ExportSpans(...)
if err != nil: existing send-error handling
if !Quiet: log span_export_succeeded
state, err = mutateState(... SetPendingScore ...)
if err != nil:
    log span_checkpoint_unconfirmed
    return using the existing error/count/failed values
if !Quiet: existing exported log
continue existing scores and processed-checkpoint handling
```

Never move network delivery inside the state lock or retry a whole turn to retry a checkpoint.

## 4. Implementation phases

Complete each phase before proceeding. The only planned production code changes are the two diagnostic sites. Planned test names below are not executed gates until the tests exist and have run.

### P0. Establish the baseline

1. Read section 2 owners; inspect `git status --short` and `git log -1 --oneline`. Preserve unrelated changes.
2. If relevant source has changed beyond the inspected baseline, map current symbols before editing.
3. Run existing incident regressions:

   ```sh
   go test ./internal/exportstate ./internal/watch -run '^(TestStateLockRecoversAfterKilledOwner|TestWatchWaitsForStateWithoutRestarting|TestWatchRetriesPendingCheckpointOnly|TestCompletedTurnScoreRetryUsesStableEnvironment)$' -count=1 -v
   ```

4. Record the revision and result. Investigate baseline failures; do not weaken assertions to proceed.

**Exit:** baseline recorded; scope matches section 1. No service, live state, or backend changes.

### P1. Correct operational instructions

Files: `README.md`, `TESTING.md`; adjust existing `test/docs_static_test.go` assertions only if affected.

1. State that `--latest`, `--session-id`, and `--path` select a session/source and send all its completed exportable turns by default. They do not select only missing/unprocessed turns.
2. Add the existing single-turn example:

   ```sh
   ~/.codex/bin/codex-langfuse-exporter --session-id <SESSION_ID> --turn-id <TURN_ID>
   ```

3. Explain that `--turn-id` restricts scope only. It does not deduplicate, query remote existence, or mark watcher state. Even a missing turn can later be sent automatically. An intentional manual send requires establishing that no automatic path is scheduled to send that turn. Do not recommend editing state or stopping the watcher as a permanent deduplication technique.
4. Put the warning before whole-session examples and beside the Claude manual command. Keep `--no-verify` described only as skipping post-export verification. Do not recommend ordinary replay to repair tags, scores, or pricing.
5. Rewrite CHECK-001 as the automatic Claude path only, with explicit basic-smoke and full-parity procedures:
   - Basic smoke: generate one new small Claude session with the already user-configured Stop hook; a “reply exactly” prompt is sufficient for this procedure.
   - Let the watcher drain the request. Do not manually export that transcript.
   - Obtain the trace ID from the successful `scored` line in the watcher log for that session. This line follows span and score callbacks. Run the opt-in read-only smoke validator below. Do not run the manual exporter to obtain the ID or verify it.
   - Full parity: use a separate new automatically exported session that deliberately exercises a benign command, a file change confined to a temporary directory, and an already configured read-only MCP tool. Only then run `LIVE_LANGFUSE_CLAUDE_TRACE_ID="<trace-id>" go test ./internal/langfuse -run '^TestLiveClaudeParityTrace$' -count=1 -v`. Confirm the verifier's configured project matches the watcher's target without printing keys.
   - The existing parity test requires all three tool families, tags, usage, and pricing. Do not run it against a no-tool prompt or weaken it to pass that prompt. If a safe MCP tool or another prerequisite is unavailable, record full parity as unperformed while reporting basic smoke independently. Do not install infrastructure or change Claude settings for this check.
   - If the queue does not drain, diagnose/report that failure. Manual export cannot substitute for passing the automatic-path check.
   - Record the session/trace identity and automatic-path result without publishing private content.
6. Document optional manual live validation separately, using a distinct new session whose tracing hook is not configured and whose transcript has no queued export. Use a user-managed test configuration; do not edit Claude settings or invent a hook-disable CLI flag. If isolation cannot be established, record the optional check as unperformed. Stopping the watcher alone is insufficient because hooks can still enqueue work.
7. State explicitly that the same session must not be used for automatic and manual validation. Preserve existing provider, pricing, canonical observation, and upgrade guidance; report basic smoke and full tool parity as different evidence.
8. Review examples as an ordered procedure. Substring tests did not catch the double-send problem; do not replace this review with a new paragraph snapshot or brittle prose assertions.

Add opt-in, read-only `TestLiveClaudeSmokeTrace` in `internal/langfuse/live_claude_parity_test.go`:

- Require `LIVE_LANGFUSE_CLAUDE_SMOKE_TRACE_ID`; skip when unset so `go test ./...` stays hermetic.
- Load the same default Langfuse config as the watcher/live parity tests. Query all pages with `NewObservationClient.List`, requesting `core,basic,io,trace_context`.
- Allow delayed visibility with a bounded 30-second deadline and 250-ms interval. After the root and transcript first appear, require their exact same ID/name/count snapshot to remain stable for at least 5 seconds before passing. Fail on a duplicate ID or extra root/transcript immediately. On timeout print only trace ID, last API status/error, and observed counts; never print observation I/O.
- Inspect the uncollapsed list. Reject missing/empty IDs, repeated IDs, root count other than one, transcript count other than one, or names other than `claude.agent` / `claude.transcript`. Require non-empty canonical root input/output under the existing serialized-text contract. Keep assertions structural; this live check does not reproduce Claude content.
- Add a synthetic `httptest.Server` regression in `internal/langfuse/verify_test.go` for repeated observation IDs, plus unit cases for duplicate root/transcript names and missing IDs. Keep it read-only and deterministic.
- Enhance `TestLiveClaudeParityTrace` to validate unique IDs and exactly one root/transcript on its raw paginated slice before its existing name-keyed field assertions. Do not remove or relax command/file/MCP/usage/pricing requirements.
- In CHECK-001, use the `scored` event for the basic session, then run the smoke validator with `LIVE_LANGFUSE_CLAUDE_SMOKE_TRACE_ID="<trace-id>"`. Run the parity test only for the separate full-parity session.

Checks:

```sh
go test ./test -run '^(TestDocsCompletedCodexVisibility|TestDocsClaudeSupportContract|TestEvalDocsClaudeContractCompleteness|TestDocsTagsAndMCPUsage|TestDocsLangfuseCostPricing)$' -count=1
go test ./cmd/codex-langfuse-exporter -run '^(TestManualProviderExportCLIIntegration|TestManualExportCLIJSONOutput)$' -count=1
go test ./internal/langfuse -run '^TestLiveClaudeSmokeTrace$' -count=1 -v
git diff --check
```

The smoke command skips unless its trace-ID variable is intentionally supplied. Unit duplicate-shape regressions run with the normal Langfuse package suite.

**Exit:** smoke instructions use one export path per session; manual scope and watcher independence are explicit. Runtime behavior is unchanged.

### P2. Add diagnostics and extend regressions

Files: `internal/watch/watch.go`, `internal/watch/watch_test.go`, `internal/watch/watch_lock_unix_test.go`, README delivery/troubleshooting text.

1. Implement section 3's two log sites. Preserve return values, error propagation, state calls, span/score order, existing logs, and retry policy.
2. Extend `TestWatchLogs`: assert new success -> existing `exported` -> existing `scored` ordering; neither new line on export error; no success lines with quiet mode. Retain privacy assertions.
3. Extend `TestWatchRetriesPendingCheckpointOnly` with a synchronized log collector. While checkpointing is blocked: one span call, zero scores, one new success line, no `exported`/`scored`. After release: one total span call, one score call, one success line, final processed state. Never read a `bytes.Buffer` concurrently with writes.
4. Extend `TestCompletedTurnScoreRetryUsesStableEnvironment`: the score-only retry adds no span success line. Retain environment and callback-count checks.
5. Add `TestWatchSpanCheckpointFailureLogs` in `watch_test.go`, table cases normal/quiet:
   - Save valid initial state. Cancel the scan context inside the span callback immediately before returning success, so `exportstate.Update` fails before this checkpoint commits.
   - Assert one span callback, zero scores, error matching `context.Canceled`, and unchanged durable state.
   - Assert one unconfirmed-checkpoint line in both modes; one success line only outside quiet mode; no `exported`/`scored` line.
   - Assert no payload/credential sentinels in new diagnostics.
6. Document section 3's log meanings. This cancellation case is a deterministic checkpoint-error test, not a claim about all filesystem failures or the incident's original cause.

Checks:

```sh
go test ./internal/watch -run '^(TestWatchLogs|TestWatchSpanCheckpointFailureLogs|TestWatchRetriesPendingCheckpointOnly|TestCompletedTurnScoreRetryUsesStableEnvironment)$' -count=1 -v
go test -race ./internal/watch -run '^(TestWatchLogs|TestWatchSpanCheckpointFailureLogs|TestWatchRetriesPendingCheckpointOnly|TestCompletedTurnScoreRetryUsesStableEnvironment)$' -count=1
```

**Exit:** diagnostics distinguish callback success from checkpoint completion; contention still retries persistence without resending in the same live process.

### P3. Demonstrate acknowledgement loss and process death

New files: `internal/watch/watch_delivery_test.go` and `internal/watch/watch_delivery_unix_test.go` (Unix build tag for the kill test). All supporting code stays test-only. Add no production injection hook, exported helper, generic fault framework, or fixture registry.

Shared setup:

- Reuse `watchFixture` and its registered `complete-tools.jsonl`. Set fixed scan time and source mtime with `old watermark < source mtime <= scan Now`.
- Save non-empty version 3 state at a real temporary `StatePath`. Reload disk state between attempts; reusing in-memory state is not restart evidence.
- Use `testWorkspace`, a loopback `httptest.Server`, dummy keys, and real `langfuse.ExportSpans` in the scan callback. Counted score stubs are sufficient; these tests do not validate score transport.
- Adapt the existing OTLP decode pattern to record requests and `(trace ID, span ID)` sets. Validate before recording acceptance. Keep the small fixture below one SDK batch; do not claim multi-batch certification.
- Synchronize counters and switches. Use channels/pipes for the boundary, timeouts only as failure guards, and cleanup on every exit. Do not guess readiness with sleeps. Report handler errors to the test thread rather than using `t.Fatal` inside handlers.
- Receipts in test memory/files model a receiver recording a request. They do not establish actual Langfuse persistence.

#### P3a. `TestWatchRetriesAfterLostAcknowledgement`

1. First scan: mock decodes and records OTLP, then withholds all response headers/body. Notify the test once the request has been recorded.
2. Inside the export callback, derive a child context for the real HTTP call and cancel it only after that notification. Keep the outer scan context alive so normal failed-send handling completes. Add an overall test deadline and release handlers/clients on cleanup.
3. Assert an export error, at least one recorded request, zero scores, no new diagnostic, no pending/processed target checkpoint, and no watermark advancement past the source. `ScanOnce` currently treats a send error as retryable and can return nil; do not require a fatal scan error here.
4. Reload state, switch the server to acknowledge normally, and scan with a fresh context.
5. Assert another recorded request with the same trace/span identity set, one score callback, and durable processed state. A third scan from disk must send nothing.
6. Count logical callback invocations separately from HTTP requests. The first two scans make two logical attempts; SDK/transport behavior may make more than two HTTP requests. Assert repeated identities across failure and recovery, not an unjustified exact wire-attempt count.

**Evidence limit:** demonstrates retry after a mock has recorded data but the client lacks acknowledgement. It does not establish real storage, every SDK retry behavior, or a complete batch-delivery guarantee.

#### P3b. `TestWatchRestartAfterSpanSuccessBeforeCheckpoint`

1. Add guarded `TestWatchDeliveryProcessHelper`, following the existing child-test-executable pattern. Return immediately without a test-specific environment flag. Pass only temporary paths, mock URL, fixed times, and a mode.
2. Parent owns initial state and the mock server. Child loads that state, runs one scan with real `langfuse.ExportSpans`, and receives an HTTP success. Its score stub emits a test-only marker if called.
3. First child's test-only `Stdout` writer forwards the new `span_export_succeeded` line to the parent, then blocks inside `Write` before returning. This is the exact barrier after export success and before `mutateState`; no production hook is needed.
4. After the full readiness line and a recorded request, kill only that child and `Wait` to reap it. Register cleanup first. Never signal the production watcher or match processes by name.
5. Assert intentional child death, zero score markers, no `exported`/`scored`, and durable state bytes identical to initial state.
6. Start a second helper with the same state/source/mock and a normal writer. Assert repeated trace/span identities, one score callback, successful exit, and processed state with no pending score entry.
7. A third scan/helper from disk must issue no additional send. Keep bounded child diagnostics and exit status on failure; leave no process behind.

**Evidence limit:** demonstrates real process death after an acknowledged export and before its checkpoint, followed by a retry from durable state. Repetition is expected under the retained policy. Do not “fix” the test by recording completion before sending.

#### Reuse existing coverage

| Behavior | Existing coverage / action |
| --- | --- |
| HTTP success / 401 rejection | `TestOTLPCompletedTurnSingleBatch`, `TestOTLPHTTPExportFailure`. |
| Failed send leaves state eligible | `TestWatchEnvironmentPersistsOnlyAfterSuccessfulSpanExport`, `TestWatchScanSemantics`; P3a adds real lost-ack transport behavior. |
| Killed lock owner / blocked startup | `TestStateLockRecoversAfterKilledOwner`, `TestWatchWaitsForStateWithoutRestarting`. |
| Checkpoint contention / score-only retry | Extend the P2 tests; do not duplicate them. |
| Remote visibility, pagination, multiple SDK batches | Existing reader tests remain; no new classifier or multi-batch redesign. Record separately any discovered defect. |

Checks after tests exist:

```sh
go test ./internal/watch -list '^TestWatch(SpanCheckpointFailureLogs|RetriesAfterLostAcknowledgement|RestartAfterSpanSuccessBeforeCheckpoint)$'
go test ./internal/watch -run '^(TestWatchRetriesAfterLostAcknowledgement|TestWatchRestartAfterSpanSuccessBeforeCheckpoint)$' -count=5 -timeout=120s -v
go test -race ./internal/watch -run '^(TestWatchRetriesAfterLostAcknowledgement|TestWatchRestartAfterSpanSuccessBeforeCheckpoint)$' -count=1 -timeout=120s
go test ./internal/langfuse -run '^(TestOTLPCompletedTurnSingleBatch|TestOTLPHTTPExportFailure)$' -count=1
```

On supported Linux, all three new assertion names must be listed and the kill test must execute, not skip. “No tests to run” is not success.

**Exit:** failure tests pass repeatedly and under the race detector, contact no production endpoint, and leave no child/mock-request leaks.

### P4. Align docs and record implementation evidence

1. Add focused commands and their meanings to `TESTING.md`; retain the current full production gate and fixture inventory.
2. Check planning corrections made with this handoff:
   - Reconciliation no longer depends on a new delivery-policy decision or treats deterministic IDs as preventing duplicate side effects.
   - Its obsolete `FetchTrace`/HTTP-404 assumptions still require an independent design refresh. Do not mechanically replace 404 with an empty list: specify visibility delay, complete pagination, errors, and partial traces before implementing writes.
   - The canonical handoff separates the closed incident, this follow-up, and unimplemented reconciliation/gateway work.
3. Record actual phase results in section 7. Planned names and mock results are not live acceptance; list skipped live work.
4. Run:

   ```sh
   go test ./... -count=1
   go test -race ./internal/exportstate ./internal/watch -count=1
   git diff --check
   ```

5. Review the diff. Production edits should be the two watcher log sites only. No dependency, state, installer, systemd, manual CLI, transport, or projection change is expected. Explain unexpected changes before handoff.

**Exit:** docs and implementation agree; local checks pass; delivery guarantees are unchanged. This is the local implementation completion point.

### P5. Deployment, when included in the execution request

Creating this plan does not deploy it. If the later execution request includes deployment, continue here; otherwise report “implemented locally; not deployed” after P4. Local tests do not imply live verification.

1. Run the complete current Production Gate in `TESTING.md`. Record candidate source and prior installed/running revisions without printing secrets.
2. Confirm version 3 state and preserve its data. Install through `install.sh`; do not remove or restore state or the persistent lock sidecar.
3. Verify the managed user service is loaded, enabled, active, and running. Record main PID/restart count, installed build revision, and matching installed/running `/proc/<main-pid>/exe` digests. Recheck PID if it changes.
4. Use one fresh benign canary through exactly one automatic path. Prefer corrected CHECK-001 when the Claude CLI is installed. If it is unavailable, use the documented Codex automatic canary and record Claude CHECK-001 as unperformed. Do not manually replay the canary or run a probe that exports the same trace again.
5. Confirm new success log -> existing checkpoint-success log -> scored log, durable processed state, and expected remote shape through existing read-only verification. Record identities/counts at inspection time without claiming exactly once.
6. Keep deliberate kills, broken acknowledgements, and checkpoint faults confined to P3 tests; do not inject them into production.
7. Update the canonical handoff with exact tested/deployed revision, service evidence, canary outcome, and limitations. Publish/merge only within the execution request's authorization.

**Rollback:** preserve current version 3 state and sidecar. Reinstall a known-good revision containing the `827a66c` advisory-lock correction; the incident's `2b8b915` build is such a baseline. Never roll back to the exclusive-create lock protocol, delete progress, or restore older state that could cause replay. Reverting these additive diagnostics needs no migration. A rollback restart still has the documented acceptance/checkpoint ambiguity.

## 5. Failure handling

| Finding | Required action |
| --- | --- |
| Lost-ack test unexpectedly returns success or commits progress | Preserve minimal reproduction and inspect exporter/SDK error propagation. Report a separate transport blocker. Do not weaken the assertion or introduce a ledger/transport rewrite silently. |
| Child cannot reach crash boundary | Fix readiness/cleanup. Do not substitute an ordinary error or sleep for actual process death. |
| Diagnostics alter send/score counts | Correct implementation; logging must not affect control flow. |
| A new requirement demands strict duplicate prevention | Report scope change. Neither preflight nor gateway admission alone establishes that contract. |
| Manual Claude live smoke cannot be isolated from hooks | Leave optional check unperformed with reason; automatic validation remains independent. |
| Older state or legacy writer found during rollout | Follow existing lock-upgrade instructions; do not improvise deletion or reclamation. |

## 6. Completion checklist

- [ ] Manual session scope, `--turn-id`, and watcher independence documented accurately.
- [ ] CHECK-001 uses automatic export only; optional manual validation uses another unqueued session.
- [ ] Both diagnostics follow section 3, including quiet behavior and bounded content.
- [ ] Contention retries persistence only; score retries send no spans.
- [ ] Real lost-ack HTTP and subprocess-death tests expose the retained replay window.
- [ ] New test names exist and execute; repeated/race runs pass on Linux.
- [ ] Version 3 state and advisory locking unchanged.
- [ ] Reconciliation has no artificial dependency and remains accurately marked unimplemented/outdated.
- [ ] Full local checks pass; diff has no unintended production changes.
- [ ] Final report separates local implementation, publication, deployment, live evidence, and limitations.

## 7. Evidence record and implementation prompt

This plan was first written as an unexecuted handoff. The table below records checks actually run during implementation; planned tests are not passed gates.

| Phase | Revision | Checks actually run | Result / limitations |
| --- | --- | --- | --- |
| P0 baseline | `c6a086e001e9a42a73e047fae8cc9fbbf17ce2ba` | Targeted export-state, watcher startup/checkpoint, and score-retry regressions; documentation/CLI regression sets | PASS. The full suite later exposed that a docs assertion used wording different from the still-valid README statement; the assertion now protects the actual sentence and its focused test passes. |
| P1 instructions and live validator | Worktree based on `c6a086e` | Manual Codex CLI/export checks; focused docs checks; `TestValidateClaudeObservationRows`, duplicate-pagination test, and env-gated live-test compilation | PASS local. Live Claude CHECK-001 is unavailable because `claude` is not installed; env-gated live checks were skipped. |
| P2 diagnostics | Worktree based on `c6a086e` | Watcher log, quiet-mode, canceled-checkpoint, contention, and score-only retry regressions; focused set with `-race` | PASS. State version and retry flow unchanged. |
| P3 failure tests | Worktree based on `c6a086e` | Real OTLP lost-acknowledgement and Unix subprocess-kill tests `-count=5`; same failure tests with `-race`; Langfuse validator/pagination regressions | PASS. Tests use loopback mocks and temporary state only. The OTLP SDK prints its expected canceled-loopback request diagnostic. |
| P4 local handoff | Worktree based on `c6a086e` | `go test ./... -count=1`; full coverage run; both 10-second codextrace fuzz targets; race checks for exportstate, Claude hook, watcher, and CLI; Claude parser/hook/state checks; five serial watcher latency repetitions; `git diff --check` | PASS. The coverage command reported per-package results (cmd 61.8%, watcher 50.4%, Langfuse 51.2%); the repository defines no aggregate coverage threshold. |
| P5 publication and deployment | Authorized; underway | Pre-deploy doctor: health/auth OK, target project `codex-local`, watcher active, queue 0, processed 1017, recent errors 0; version 3 state preserved | Deployment, post-install digest, canary, and remote-shape checks remain pending. |

Suggested prompt:

> Implement `plans/export-delivery-reliability-plan.md` through P4, one phase at a time. Follow `AGENTS.md`, preserve unrelated changes, and record actual evidence. Architecture decisions are fixed: retain at-least-once delivery, version 3 state, existing locks, and manual CLI behavior. Correct the operational instructions, add only the two specified watcher diagnostics, extend regressions, and add the isolated acknowledgement-loss and subprocess-death tests. Do not add preflight, a ledger, coordination, reconciliation, or gateway code. If a separate transport defect appears, report the concrete reproduction without weakening the test or expanding scope silently. Finish with changed files, test results, and deployment status. P5 applies when the execution request includes deployment.
