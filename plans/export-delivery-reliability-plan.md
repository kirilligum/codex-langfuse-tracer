# Export delivery diagnostics and operational guidance implementation plan

- Project: `codex-langfuse-tracer`
- Date: 2026-09-22
- Source baseline inspected: `c6a086e001e9a42a73e047fae8cc9fbbf17ce2ba`
- Status: P0-P4 complete. Runtime revision `78bfafeeb27f619e4df6f9af01cb768c9dacc55c` is published and installed, and the canary shape check passed. P6 runtime code `0912de5c89d93f0edc8f0b43217c2bc4fea57882` is published and installed; the incident-sized rollout probe passed at 41,408 KiB maximum RSS. P5/P6 availability acceptance remains pending the post-install observation window and final test/docs publication.
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

The execution request includes deployment, so complete this phase after P4. Local tests do not imply live verification.

1. Run the complete current Production Gate in `TESTING.md`. Record candidate source and prior installed/running revisions without printing secrets.
2. Confirm version 3 state and preserve its data. Install through `install.sh`; do not remove or restore state or the persistent lock sidecar.
3. Verify the managed user service is loaded, enabled, active, and running. Record main PID/restart count, installed build revision, and matching installed/running `/proc/<main-pid>/exe` digests. Recheck PID if it changes.
4. Use one fresh benign canary through exactly one automatic path. Prefer corrected CHECK-001 when the Claude CLI is installed. If it is unavailable, use the documented Codex automatic canary and record Claude CHECK-001 as unperformed. Do not manually replay the canary or run a probe that exports the same trace again.
5. Confirm new success log -> existing checkpoint-success log -> scored log, durable processed state, and expected remote shape through the provider-matched read-only smoke test (`TestLiveClaudeSmokeTrace` or `TestLiveCodexSmokeTrace`). Record identities/counts at inspection time without claiming exactly once.
6. Keep deliberate kills, broken acknowledgements, and checkpoint faults confined to P3 tests; do not inject them into production.
7. Update the canonical handoff with exact tested/deployed revision, service evidence, canary outcome, and limitations. Publish/merge only within the execution request's authorization.

**Rollback:** preserve current version 3 state and sidecar. Reinstall a known-good revision containing the `827a66c` advisory-lock correction; the incident's `2b8b915` build is such a baseline. Never roll back to the exclusive-create lock protocol, delete progress, or restore older state that could cause replay. Reverting these additive diagnostics needs no migration. A rollback restart still has the documented acceptance/checkpoint ambiguity.

### P6. Bound watcher memory for large, recently modified rollouts

**Why this phase was added (2026-09-22):** `ScanOnce` selects rollout files whose mtime is newer than the saved scan watermark, then calls `codextrace.ParseTurns`. A 113,657,501-byte file had mtime `1790113323.335820036` seconds and persisted watermark `1790113320577025434` ns (about 2.76 seconds earlier), so it qualified. `ParseTurns` currently uses `os.ReadFile`, makes a full string copy, splits the complete file into lines, and retains parsed observations for every turn before the watcher can discard already-processed traces. At 14:41 PDT, an active watcher process (81 seconds old) reached 824,424 KiB RSS, 1,096,988 KiB high-water RSS, and 1,892,860 KiB VmData. OOM-killed runs and full swap were independently observed. A nearby process snapshot showed `workerd` at about 3.0 GiB and `codex` at about 1.8 GiB in user tmux scopes, plus multiple large Node/web-server and ClickHouse containers. This strongly implicates the whole-file/all-turn working set as a restart amplifier under concurrent host pressure; exact per-allocation causality is not measured. If killed before the scan watermark transaction, the next process sees the same file as eligible and parses it again. Do not stop these active scopes speculatively.

1. Add a regression using a generated rollout with many already-processed turns and one pending/new turn. Prove that filtering happens before retaining their observations, and that the pending score-only turn is still included. Preserve `ParseTurns` behavior for callers that need every turn.
2. Read JSONL incrementally instead of holding the raw file, full string copy, and split-line slice. In the watcher path, derive the processed-ID set once and omit already-processed turns before accumulating their observations. Keep the existing deterministic trace IDs and repeated-context merge behavior. Do not impose a file-size cap, silently skip an eligible rollout, or change state version 3.
3. Treat parser errors as an incomplete scan: retain the previous watermark, log the error, and retry the eligible source on a later scan. Preserve per-turn checkpoints and at-least-once delivery. Add a regression proving corrupt input cannot advance the watermark or cause a partial parse to be reported as complete.
4. Run focused parser/watcher tests, `go test ./... -count=1`, targeted race checks, and `git diff --check`. Run resource-heavy verification only after host pressure permits it; never stop active unrelated workloads just to make the test pass. Record any remaining single-turn memory limit honestly.
5. Deploy the new revision without resetting v3 state or manually replaying a trace. Verify installed/running digests, doctor/state, and the watcher RSS/restart count while scanning the same eligible source. A fresh automatic canary and read-only shape check follow only after the watcher stays active without OOM restarts for a 60-minute observation window.

**Exit:** tests show no processed-turn observations are retained by the watcher path; all unprocessed/pending-score turns retain the same projection and checkpoints; parse failure does not advance the watermark; the previously eligible large rollout scans without repeated OOM; and production recovery checks pass. If the watcher still grows toward OOM for a single unprocessed turn, P6 is not complete and a separate bounded projection/memory design is required.

**Execution status (2026-09-22 15:09 PDT):** P6 code and regression steps 1-3 are complete. The full test suite, parser/watcher race checks, parser fuzz run, and `git diff --check` pass. The fix is not yet committed or installed. The old service revision was active with `NRestarts=85`; that counter was unchanged from 15:03 to 15:09, available RAM had recovered to 4.6 GiB, and swap remained effectively full. This is a short improvement window, not the required post-install 60-minute acceptance.

**Large-rollout live check (2026-09-22, after install):** `TestLiveCodexLargeRolloutFilteredScan` ran the actual 117,007,638-byte rollout through `ScanOnce` via a temporary root. It loaded production version 3 state into memory and moved only the temporary watermark back to make this source eligible. The scan completed in 1.39 seconds with maximum RSS 41,408 KiB; 1,020 existing processed traces were recognized, with zero span or score callbacks. The check made no remote calls and did not write production state. The new watcher process was healthy at the time of the check, but the 60-minute no-OOM/no-restart acceptance is still pending.

### Post-deployment checks and remaining risks

- Keep monitoring for `span_checkpoint_unconfirmed`; it means the remote send callback succeeded but local checkpointing failed, so retry can create duplicate observations.
- **Availability remains unaccepted (2026-09-22 14:36 PDT).** Systemd recorded repeated OOM kills of processes in `codex-langfuse-watch.service`; `NRestarts` rose from 2 during the initial check to 32 at 14:26 and 49 at 14:36. It was active/running at the 14:36 snapshot (PID 411806), but that point-in-time status does not establish availability. Unit logs through restart counter 45 show `status=9/KILL`, `Result=oom-kill`, and recent per-run peaks of 169.5, 548.2, and 230.4 MiB; earlier OOM snapshots showed watcher RSS around 0.77-0.79 GiB. At 14:33, the host had 30 GiB RAM, 29 GiB used, 221 MiB free, 1.1 GiB available, 8 GiB swap effectively full (116 KiB free), and memory PSI `some` avg10/60/300 of 93.21/76.33/71.12 and `full` of 66.78/47.74/42.50. Nearby process snapshots included large `workerd`, Codex, Node/web-server, and ClickHouse processes. These observations establish severe host pressure and watcher OOM victimization, not which workload caused the pressure. No unrelated process was stopped and no OOM priority, cgroup limit, or service restart policy was changed.
- **Separate input-size risk, not a confirmed OOM cause:** `internal/codextrace/parser.go` reads each source file with `os.ReadFile`, converts it to a string, and splits the whole file into lines. Large active rollout files can therefore add substantial transient and retained memory. The recent low-memory OOM victims and concurrent host pressure do not prove that parser allocation caused the restart loop. Do not add a file-size cap or silently skip traces. If parser hardening is proposed, first profile representative large files in an isolated environment and preserve full turn/observation semantics in any streaming design.
- **Recovery/acceptance checks:** the host owner should identify the lifecycle and budget of the largest active memory consumers and choose any workload-specific remediation only after checking that work is safe to interrupt. After host pressure is relieved, record two host/service snapshots at least 60 minutes apart. Acceptance requires no increase in `NRestarts` from the new baseline, no watcher OOM events, sustained active/running status, materially recovered memory PSI and swap headroom, clean `--doctor`, intact version 3 state with no unexpected pending/queued work, and matching installed/running executable digests. Then send one fresh benign turn through one automatic path and run the provider-matched read-only smoke check; do not manually replay it. A shorter healthy interval is not evidence that sustained availability is restored.
- `span_export_succeeded`, `exported`, and `scored` describe local callback/checkpoint steps. They do not prove lasting remote visibility or exactly-once delivery.
- Live smoke validation observes the current paginated API snapshot and five seconds of stability. It cannot establish that no historical duplicate existed or that the backend never merged rows.
- Run Claude CHECK-001 on a host with Claude Code and its existing Stop hook before claiming live Claude hook acceptance. Keep any manual transcript check on a distinct, unqueued session.
- Version 3 state, the persistent `.lock` sidecar, and at-least-once delivery remain part of the contract. Never reset state to clear an ambiguous delivery.
- Reconciliation remains independent and unimplemented; refresh its stale read/visibility/error assumptions before adding a resend path.

## 5. Failure handling

| Finding | Required action |
| --- | --- |
| Lost-ack test unexpectedly returns success or commits progress | Preserve minimal reproduction and inspect exporter/SDK error propagation. Report a separate transport blocker. Do not weaken the assertion or introduce a ledger/transport rewrite silently. |
| Child cannot reach crash boundary | Fix readiness/cleanup. Do not substitute an ordinary error or sleep for actual process death. |
| Diagnostics alter send/score counts | Correct implementation; logging must not affect control flow. |
| A new requirement demands strict duplicate prevention | Report scope change. Neither preflight nor gateway admission alone establishes that contract. |
| Manual Claude live smoke cannot be isolated from hooks | Leave optional check unperformed with reason; automatic validation remains independent. |
| `TestObservationClientHTTPFailures` intermittently reports `http: CloseIdleConnections called` | Keep HTTP-status assertions unchanged. `httptest.Server.Close` closes idle connections on the shared default transport, so run these tiny status subtests serially; verify with repeated package runs. |
| The configured ChatGPT account rejects a pinned `gpt-5.4-mini` canary | Use Codex's configured default model in the smoke command with low reasoning effort. Do not reuse the incomplete rejected session or manually export it. |
| Claude CLI is unavailable on the deployment host | Mark Claude CHECK-001 unperformed. A Codex automatic canary validates the shared watcher/OTLP delivery path but does not certify Claude parsing or hooks. |
| Watcher is repeatedly OOM-killed while host RAM/swap/PSI show severe pressure | Treat production availability as blocked. Preserve version 3 state; do not change OOM priority, cgroup limits, restart policy, or unrelated active workloads speculatively. Have the host owner identify and remediate memory pressure, then satisfy the 60-minute no-restart recovery checks before a fresh canary. |
| Older state or legacy writer found during rollout | Follow existing lock-upgrade instructions; do not improvise deletion or reclamation. |

## 6. Completion checklist

- [x] Manual session scope, `--turn-id`, and watcher independence documented accurately.
- [x] CHECK-001 documents the automatic export path only; optional manual validation uses another unqueued session.
- [x] Both diagnostics follow section 3, including quiet behavior and bounded content.
- [x] Contention retries persistence only; score retries send no spans.
- [x] Real lost-ack HTTP and subprocess-death tests expose the retained replay window.
- [x] New test names exist and execute; repeated/race runs pass on Linux.
- [x] Version 3 state and advisory locking unchanged.
- [x] Reconciliation has no artificial dependency and remains accurately marked unimplemented/outdated.
- [x] Full local checks pass; diff has no unintended production changes.
- [x] Final report separates local implementation, publication, deployment, live evidence, and limitations.

## 7. Evidence record and implementation prompt

This plan was first written as an unexecuted handoff. The table below records checks actually run during implementation; planned tests are not passed gates.

| Phase | Revision | Checks actually run | Result / limitations |
| --- | --- | --- | --- |
| P0 baseline | `c6a086e001e9a42a73e047fae8cc9fbbf17ce2ba` | Targeted export-state, watcher startup/checkpoint, and score-retry regressions; documentation/CLI regression sets | PASS. The full suite later exposed that a docs assertion used wording different from the still-valid README statement; the assertion now protects the actual sentence and its focused test passes. |
| P1 instructions and live validator | Worktree based on `c6a086e` | Manual Codex CLI/export checks; focused docs checks; Claude and Codex observation validators; duplicate-pagination test; env-gated tests | PASS. Claude CHECK-001 is unavailable because `claude` is not installed. The README smoke command now uses the configured Codex model rather than pinning an unsupported model. |
| P2 diagnostics | Worktree based on `c6a086e` | Watcher log, quiet-mode, canceled-checkpoint, contention, and score-only retry regressions; focused set with `-race` | PASS. State version and retry flow unchanged. |
| P3 failure tests | Worktree based on `c6a086e` | Real OTLP lost-acknowledgement and Unix subprocess-kill tests `-count=5`; same failure tests with `-race`; Langfuse validator/pagination regressions | PASS. Tests use loopback mocks and temporary state only. The OTLP SDK prints its expected canceled-loopback request diagnostic. |
| P4 local handoff | Worktree based on `c6a086e` | `go test ./... -count=1`; full coverage run; both 10-second codextrace fuzz targets; race checks for exportstate, Claude hook, watcher, CLI, and Langfuse; Claude parser/hook/state checks; five serial watcher latency repetitions; `git diff --check`; `TestObservationClientHTTPFailures -count=50`; `go test ./internal/langfuse -count=10` | PASS. A first full run exposed a parallel `httptest.Server.Close`/shared-transport race in the HTTP status test. Its assertions were retained and the four mock status cases serialized; stress, full-suite, and race runs passed. Coverage is reported per package, with no aggregate threshold. |
| P5 publication, install, and deployment | Runtime revision `0912de5c89d93f0edc8f0b43217c2bc4fea57882` on `origin/main` | `./install.sh`; doctor; version 3 state; installed/running executable SHA-256 `79e15cd4765885489f808516f13a37f47555d7802808460ea487e423556f8429`; build VCS revision matches; Codex canary and read-only `TestLiveCodexSmokeTrace` | **PARTIAL / availability observation pending.** The original canary shape was one root, one transcript, two observations with unique IDs stable for 5 seconds. Version 3 state is preserved (1,020 processed, zero queued/pending); doctor reports config, health, auth, project, watcher, and recent errors OK. The prior OOM loop reached 85 restarts before the fix. P6's incident-sized scan now passes; no post-install OOM/restart has been seen in the initial observation. Claude CHECK-001 remains unavailable because the CLI is absent. The required 60-minute post-install observation is not complete. |
| P6 bounded-memory Codex scan | Runtime code commit `0912de5c89d93f0edc8f0b43217c2bc4fea57882` | 2,000-turn filtered parser regression; watcher-level processed-turn regression; parse-error watermark regression; `GOMAXPROCS=1 go test -p=1 ./... -count=1`; `GOMAXPROCS=1 go test -race -p=1 ./internal/codextrace ./internal/watch -count=1`; 10-second `FuzzParseTurnsDoesNotPanic`; env-gated live scan of a 117,007,638-byte rollout with production state; `/usr/bin/time -v`; `git diff --check` | **PASS for local code, tests, publication, installation, and incident-sized input.** JSONL reads line-by-line; watcher filters processed IDs before retaining observations; parse errors preserve the prior watermark. The live scan completed in 1.39 seconds at 41,408 KiB maximum RSS, with zero span/score callbacks and no remote or persistent-state writes. The 60-minute post-install availability observation remains pending. |

Suggested prompt:

> Implement `plans/export-delivery-reliability-plan.md` through P6, one phase at a time. Follow `AGENTS.md`, preserve unrelated changes, and record actual evidence. Architecture decisions are fixed: retain at-least-once delivery, version 3 state, existing locks, and manual CLI behavior. Correct the operational instructions, add the two specified watcher diagnostics, extend failure regressions, and reduce Codex rollout scan memory by filtering processed turns before accumulating observations. Do not add a file-size cap, silently skip an eligible rollout, or change the delivery contract. Do not add preflight, a ledger, reconciliation, or gateway code. Finish with local tests, race checks, publication, installation, and recorded post-install availability evidence; P5 and P6 production acceptance both require the requested deployment evidence.
