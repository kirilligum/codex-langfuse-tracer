# Export State Lock Recovery Plan

- Issue: [#13 — Recover stale export-state locks after abrupt process termination](https://github.com/kirilligum/codex-langfuse-tracer/issues/13)
- Status: implementation and local verification complete; deployment preflight and operational evidence pending
- Date: 2026-09-21
- Source reviewed: local `main` at `63af12dac64e01d6894b6d43cefd726f9cf0cdec`
- Owner: this repository, principally `internal/exportstate`
- Scope: crash recovery, state transaction serialization, watcher contention handling, and safe installation of the new locking protocol

## Findings that change the initial recommendation

The kernel lock recommendation still holds, but replacing `O_EXCL` alone would leave several correctness gaps.

| Finding | Evidence in the reviewed source | Consequence |
| --- | --- | --- |
| An empty legacy lock without an open file descriptor can belong to a live writer. | `internal/exportstate/state.go:193` creates the sentinel, immediately closes it, and removes it only after the transaction. | An empty `lsof` result, file age, or successful `flock` attempt cannot establish that a legacy writer has finished. |
| Legacy and new writers cannot safely coexist. | Legacy writers ignore advisory locks and unlink the sidecar on completion. | An old writer can overlap a new transaction and then remove the inode on which the new writer holds its lock. Upgrading requires all old writers to stop. |
| Initialization bypasses the state transaction. | `internal/watch/watch.go:35` calls `Save`; `WatchSessions` previously checked existence outside a lock. | A hook can create/enqueue state between the existence check and initialization, and initialization can overwrite that queue. |
| Missing parent directories fail before the current creation path runs. | `Update` acquires the sidecar before `Save` calls `MkdirAll`. | First use of a state path in a new directory needs directory creation before acquisition. |
| State contention currently terminates the watcher. | Mutation failures propagate through `ScanOnce` and `WatchSessions`; the unit has `Restart=on-failure` and `RestartSec=5`. | The two-second lock timeout plus the restart delay can repeat indefinitely. Five seconds of delay does not provide recovery. |
| The state lock can be encountered after expensive work. | Scanning, parsing, and remote export can precede the next state mutation. | Restarting or retrying the entire scan can repeat parsing and remote calls. Retry the pending state transaction. |
| The installer exposes a new binary before stopping the old watcher. | `install.sh:24` builds to the installed path, then syncs pricing and eventually restarts the service. | New hook invocations could use a different protocol while the old watcher is still running. |
| Production starts with a background context. | `main()` calls `run(context.Background(), ...)`. | A cancellable lock loop also needs actual signal cancellation wired into the CLI. |

A scratch Linux process probe confirmed both protocol hazards: a new `flock` acquisition succeeded while a legacy sentinel owner was alive; after that owner unlinked the path, two separate inodes at that path could be locked concurrently. This is protocol evidence, not a test of the future Go implementation.

The issue's restart count and RSS measurements are incident reports. This review did not reproduce the production workload or establish why the original process exited. Preventing repeated startup work does not establish a memory leak fix or a new memory limit.

## Decision and alternatives

Use `golang.org/x/sys/unix.Flock` with a persistent `<state-path>.lock` sidecar on the supported Linux workstation filesystem. `golang.org/x/sys` is already in `go.mod`; promote it to a direct dependency if imported. The lock protects each local read/modify/write transaction. Hold its descriptor until that transaction ends, and close it on every exit. Descriptor inheritance must not extend lock ownership into unrelated child processes. Linux associates `flock` ownership with an open file description and releases it when the last associated descriptor closes. [Linux flock documentation](https://man7.org/linux/man-pages/man2/flock.2.html)

| Alternative | Assessment |
| --- | --- |
| Delete an old sentinel based on age, PID, or absence from `lsof` | Unsafe for the current protocol, which records no owner and closes its descriptor immediately. Adding owner identity and safe reclamation would create a more complex protocol. |
| Lock `state.json` itself | Its inode changes during atomic replacement. Different writers could lock different versions of the file. Use the stable sidecar. |
| Remove the sidecar after releasing a kernel lock | A waiting process may still reference the removed inode. Leave the sidecar in place during normal operation. |
| Use an indefinitely blocking `flock` call | Avoids stale sentinels, but does not provide the chosen acquisition deadline or straightforward context cancellation. Use nonblocking attempts and cancellable timers. |
| Hold the shared state lock for the watcher's whole lifetime | Prevents hooks from enqueuing. Keep the lock around local transactions only. |
| Fix only systemd restart limits or backoff | Reduces restart frequency but does not repair lock ownership. A start limit can also leave tracing stopped after contention ends. |
| Move the queue to a database or add another daemon/state file | Requires migration and new operational ownership without evidence that the existing state store is insufficient for this fix. |

Keep the current systemd restart policy for process failures. Handle a busy state lock inside the watcher. `RestartSec` controls the delay between restarts; start limits are a separate mechanism, and reaching one can stop automatic restart attempts. [systemd service documentation](https://man7.org/linux/man-pages/man5/systemd.service.5.html), [systemd unit documentation](https://man7.org/linux/man-pages/man5/systemd.unit.5.html)

## Required behavior

1. An unlocked sidecar may already exist, including an empty file left by a terminated legacy writer. Once the upgrade's quiescence condition is met, it must not prevent a new transaction.
2. A live owner retains its lock until it closes the descriptor or exits. A waiter never deletes, renames, truncates, or steals that owner's sidecar.
3. All production state mutations, including first creation, use the same transaction lock. Each mutation reads the latest state after acquiring it.
4. Hold the lock through state loading, mutation, and atomic replacement. Release it before parsing transcripts, making network calls, or sleeping between exports.
5. Maintain one version 3 JSON state and the existing sidecar path. This change requires no state reset or schema migration.
6. Preserve processed IDs, pending score environments, queued requests, and the scan watermark unless the successful requested mutation changes them.
7. Existing valid state wins over initialization defaults. Invalid JSON and unsupported versions remain errors; they must never trigger an automatic reset.
8. A lock acquisition timeout occurs before the callback runs. Only that error is eligible for automatic transaction retry. Callback, write, rename, permission, and malformed-state errors retain their own diagnostics.
9. A watcher encountering contention stays in the same process, pauses further work at that state operation, and resumes automatically when acquisition succeeds.
10. A hook reports enqueue success only after the queue mutation commits. A hook that cannot acquire the lock within its budget exits with a clear error and cannot claim the request was retained.
11. Cancellation interrupts acquisition polling and watcher backoff. Successful acquisition must be followed by a cancellation check before invoking the mutation.
12. Read-only state snapshots may keep using `Load`, because committed JSON is replaced atomically. A snapshot must not later be written back as a full replacement outside the transaction.

The supported deployment remains one watcher per workstation state, with concurrent hook enqueue operations. Transaction serialization does not make complete scan/export cycles exclusive. The documented at-least-once delivery contract still permits a duplicate after remote acceptance but before a local checkpoint commits. See [README delivery semantics](../README.md#how-it-works).

## State transaction implementation

Keep the implementation within `internal/exportstate`; do not add a general lock manager or a second persistence abstraction.

1. Make acquisition context aware and check cancellation before starting. Create the parent directory using the existing directory policy before opening the sidecar. Open/create the sidecar with mode `0600`, without `O_EXCL` or truncation.
2. Retain the open descriptor. Attempt `LOCK_EX | LOCK_NB`; retry only contention and interrupted acquisition within the deadline. Return filesystem and unsupported-lock errors explicitly. Use the ordinary Go close-on-exec behavior and never pass this descriptor through `ExtraFiles`.
3. Expose a recognizable acquisition error, such as `ErrLockBusy` wrapped with the sidecar path and wait duration. Preserve `errors.Is` classification for cancellation. Do not claim to know the holder PID from an empty file.
4. Add context to the existing `Update` and `Enqueue` call chains. Adapt `claudehook.Handle`, watcher mutation calls, and the CLI to pass the existing operation context.
5. Make the raw atomic save helper private. `Update` must acquire exactly once and call the private writer. Avoid acquiring a second independent lock from inside an already locked transaction. Adapt test state setup to use the transaction API where it currently calls exported `Save`.
6. Add a small `LoadOrCreate(ctx, path, initialState)` operation sharing that same acquisition and writer. Under the lock, return valid existing state without rewriting it; create only when the file is absent. Return whether creation occurred so the initialization message remains accurate.
7. Route `watch.InitializeState` through `LoadOrCreate`. Run this operation at startup even when state already exists, so contention is handled before scanning. Preserve the existing initial-lookback policy and a hook-created watermark.
8. Retain same-directory temporary write plus atomic rename. All writers now own the transaction lock before using the temporary path. An interrupted temporary write must not replace or be promoted over the committed JSON. A later successful write may replace the abandoned temporary file under the lock.
9. Release the descriptor on success and every error path, including callback errors. Leave the sidecar file in place. Keep state permissions at `0600`.

This plan promises recovery from process termination on a functioning local filesystem. The current atomic rename prevents a partially written temporary JSON file from replacing committed state; it does not establish power-loss durability. Adding file and parent-directory `fsync` is a separate durability extension with latency implications and would need its own failure and performance evidence. [Linux fsync documentation](https://man7.org/linux/man-pages/man2/fsync.2.html)

All callers must use the same configured state path. Network filesystems, concurrent legacy writers, external deletion/replacement of the sidecar, and alternate hard-link/symlink aliases are outside this locking contract. No destructive fallback is allowed when locking is unavailable.

## Exact contention and shutdown policy

These are proposed internal constants, not new CLI or configuration options.

| Surface | Proposed policy |
| --- | --- |
| One acquisition attempt | At most two seconds of contention polling, or the caller's earlier cancellation/deadline. Poll at 25 ms while waiting; attempt immediately when uncontended. |
| Watcher after acquisition timeout | Retry the same pending state operation after delays of 1, 2, 4, 8, 16, then at most 30 seconds. Reset the delay after success. |
| Persistent watcher contention | Wait until release or shutdown with bounded attempt duration, retry frequency, and log volume. No process exit, new scan, or repeated remote export solely because the lock is busy. |
| Hook enqueue | One bounded acquisition budget, then a nonzero exit with the path and wait duration if still busy. |
| Logging | Emit an actionable `ERROR:` on the first timeout, then at most once per 60 seconds for that unresolved operation. Include elapsed wait and the next retry delay. Emit one recovery message unless `--quiet` is set. Contention errors remain visible under `--quiet`. |
| Other errors | Return promptly with the operation/path and underlying error. Do not classify all I/O failures as lock contention. |
| Shutdown | Wire SIGINT/SIGTERM to `signal.NotifyContext` in `main`, pass it through, and release signal resources before `os.Exit`. Treat watcher context cancellation as a clean stop with exit zero and no `ERROR:` log. Keep hook cancellation unsuccessful when enqueue did not commit. |

Implement retry at the watcher state-operation boundary. If spans have already been accepted and `SetPendingScore` is waiting for the lock, retain that pending mutation and complete it before proceeding to scores. Do not restart `processTurn` or `ScanOnce`. Likewise, wait on the existing `AddProcessed`, queue-removal, or watermark mutation without repeating their preceding side effects. Retain the previous in-memory state until the transaction succeeds, then use the newly loaded and updated state returned by it.

The two-second budget bounds lock contention waits; it cannot bound a kernel filesystem operation stuck on a failing device. Eventual acquisition requires a free-lock opportunity when the waiter is scheduled; nonblocking polling does not promise FIFO fairness under continuous competing writes. A cap of 30 seconds on backoff gives a corresponding retry opportunity after release, subject to scheduling and I/O time.

Keep context errors recognizable inside `exportstate` and `watch`; handle normal watcher cancellation at the CLI boundary. Update the existing `TestRunWatchCanceled`, which currently expects exit one. Returning exit one after catching SIGTERM would turn an ordinary signal stop into a failure under `Restart=on-failure` and would create a misleading recent-error entry. Deadlines, hook enqueue failures, and actual operational failures remain errors.

Document the issue's ambiguous phrase “bounded live contention” as: **each acquisition attempt, retry frequency, and log rate are bounded; the daemon continues waiting without restarting until the lock is released or the daemon is stopped.** A finite total wait followed by permanent service failure would sacrifice automatic recovery.

The existing doctor reads recent `ERROR:` journal entries. Preserve that visibility instead of treating systemd's `active` status as proof of progress. Recent-error counts can remain nonzero for the existing 15-minute window after recovery; fresh logs and new trace delivery establish recovery.

## Implementation sequence

### Phase 1: State ownership and process recovery

- Change `internal/exportstate/state.go` and add a focused lock implementation file only if it makes the OS boundary clearer.
- Implement persistent advisory acquisition, context/error classification, and descriptor cleanup.
- Make raw writes private, add atomic load-or-create, and update state API callers so the repository builds.
- Add meaningful subprocess tests for death while holding the lock, live contention, and competing updates. Test startup state preservation with explicit process synchronization.
- Exit evidence: the same state can be updated after a killed owner; live ownership is respected; concurrent acknowledged changes are retained; invalid state remains untouched.

### Phase 2: Watcher and hook behavior

- Change `internal/watch/watch.go`, `internal/claudehook/hook.go`, and `cmd/codex-langfuse-exporter/main.go`.
- Apply startup load-or-create and retry the individual state operation using the policy above.
- Wire cancellation through the complete state call chain and from OS signals.
- Keep hook calls bounded and ensure failed enqueue never prints success.
- Add watcher tests demonstrating recovery in the same invocation and no repeated export caused by checkpoint contention.
- Exit evidence: a watcher remains alive while another process holds the lock, resumes after release, and terminates promptly when canceled during acquisition or backoff.

### Phase 3: Installation and user documentation

- Change `install.sh` to build to a temporary executable in the installation directory and run pricing preflight with that staged executable before altering the installed binary or stopping the service.
- Once preflight passes, stop an existing watcher and wait for completion before atomically promoting the staged executable. Handle a genuinely absent service on a fresh install; do not mask an actual stop failure.
- Install the unit, reload, enable, and restart through the existing installer flow. Clean up the staged executable on failure. A failure after stopping must report the service's actual state and the required recovery step.
- Document the mandatory first-upgrade quiescence procedure below. The installer stops its service; it cannot prove that independently launched legacy hooks or exporters have stopped. Do not automatically edit Claude settings.
- Extend `test/install_test.go` to cover successful ordering, an existing installation, preflight failure with the old executable preserved, and stop failure before promotion.
- Resolve the baseline installer-test failure recorded below before relying on this gate. Prefer test isolation or explicit handling of unrelated probe traffic after attribution; retain assertions that detect unexpected requests from the exporter itself.
- Update `README.md` for persistent sidecar semantics, contention diagnostics, hook enqueue failures, first-upgrade requirements, and rollback. State explicitly that the existing older-schema destructive-reset instructions do not apply to a version 3 lock-only upgrade.
- Update `TESTING.md` with implemented test names and commands after the tests exist. Keep `testdata/manifest.json` as the sole trace fixture inventory.
- Exit evidence: source and fake-service installation tests prove the intended ordering. Actual service behavior remains a separate deployment check.

### Phase 4: Verification and rollout

- Run focused correctness tests, the relevant race checks, and repository-required checks once the implementation is complete.
- Run the watcher and hook latency controls because transaction acquisition and startup have changed.
- Before a release, complete the existing [TESTING.md production gate](../TESTING.md#production-gate).
- Deploy through the stopped-writer procedure and collect the operational evidence below. Close the issue only after implementation tests and the declared deployment scope are supported by evidence.

## Regression matrix

These tests use temporary state and existing fixture helpers. Runtime lock tests do not require another fixture inventory or production transcript data.

| Test | Behavior to prove |
| --- | --- |
| `TestStateLockRecoversAfterKilledOwner` | A child acknowledges acquisition over a pipe, then is killed before cleanup. A fresh process updates the prepopulated state while retaining queued requests, pending scores, processed IDs, and watermark. Run with an existing sidecar too. |
| `TestStateLockDoesNotStealLiveOwner` | A child holds the lock beyond the acquisition budget. A contender receives `ErrLockBusy`, its callback never runs, state bytes are unchanged, and the owner still has the lock. Acquisition succeeds after explicit release. |
| `TestStateUpdatesSerializeAcrossProcesses` | Several independent writers perform read/modify/write operations; all acknowledged IDs, score entries, and queue requests survive. Check exact expected contents and valid JSON. |
| `TestStateLockKeepsSidecarInode` | Across successive transactions the persistent sidecar path retains the same inode. |
| `TestStateLoadOrCreatePreservesEnqueueInEitherOrder` | Hook enqueue followed by initialization preserves the hook watermark and queue; initialization followed by enqueue preserves initialized state and adds the queue. Nested path creation and private file permissions are checked. |
| `TestStateInvalidJSONIsNeverReset` | Invalid JSON and unsupported versions remain errors; their bytes are not replaced by initialization or mutation. |
| `TestStateWriteErrorsPreserveCommittedFile` | Inject temporary-write and rename failures; confirm the committed JSON and error classification survive, then confirm a later transaction acquires the lock and succeeds. |
| `TestStateLockCancellationAndCallbackFailureRelease` | Callback failure and cancellation return through their own error paths, release ownership, and leave another writer able to proceed. |
| `TestStateInterruptedWritePreservesCommittedJSON` | Terminate a child after a partial temporary write but before rename. The old JSON remains readable and a fresh transaction succeeds. |
| `TestStateCommitSurvivesKillBeforeUnlock` | Terminate a child after rename but before lock cleanup; readers see the complete new state and the next writer proceeds. |
| `TestWatchWaitsForStateWithoutRestarting` | Startup contention lasts beyond one attempt; the same watcher invocation stays alive, logs within policy, does no source/export work while startup is blocked, and progresses after release. |
| `TestWatchRetriesPendingCheckpointOnly` | Introduce contention after a successful span/score callback. The corresponding mutation completes after release without another call to the already completed callback; queue and watermark updates obey the same retry rule. |
| `TestWatchLockBackoffLogThrottleAndShutdown` | Exercise delay growth/cap, log throttling, and cancellation during backoff using a small private clock/wait seam. Acquisition cancellation is covered by `TestStateLockCancellationAndCallbackFailureRelease`. |
| `TestClaudeHookLockTimeoutIsNotAcknowledged` | A busy lock produces a bounded nonzero hook result, no enqueue-success output, and no false queue entry. A later successful retry deduplicates normally. |
| `TestCLISignalCancelsStateWait` | A real exporter subprocess receives SIGTERM while waiting for state access and exits cleanly without waiting through the retry cap or logging a false operational error. Update `TestRunWatchCanceled` for the same CLI contract. |
| `TestWatchRetriesQueueRemovalAfterCheckpoint` | A queued Claude request's state is committed, then queue-removal contends. The watcher retries removal without replaying span export or scoring and advances the watermark after recovery. |
| Existing installer and documentation tests, extended | Staging/preflight/stop/promotion ordering, failure handling, state preservation, and clear version 3 upgrade instructions. |

Use ready/release pipes or equivalent barriers; do not guess when a child has acquired a lock by sleeping. Give subprocesses generous failure timeouts and always terminate/reap them in cleanup. Scope any commit-stage fault injection to private test helpers; do not add production environment switches or CLI modes. Kill/recovery tests establish process-crash behavior, not power-loss behavior.

Focused and release-gate commands:

```sh
go test ./internal/exportstate ./internal/claudehook ./internal/watch ./cmd/codex-langfuse-exporter -count=1
go test -race ./internal/exportstate ./internal/claudehook ./internal/watch ./cmd/codex-langfuse-exporter -count=1
go test ./test -run 'TestInstallUninstallScripts|TestInstallReportsPostStopFailureState|TestEvalInstallRuntimeSurface|TestDocs' -count=1
go test ./... -count=1
go test -p=1 ./internal/watch -run '^(TestEvalWatchExportLatency|TestEvalHookQueueDrainLatency)$' -parallel=1 -count=5 -v
git diff --check
```

The race detector complements subprocess assertions; it cannot establish cross-process file locking. Record actual test execution separately from this planned matrix. Avoid adding a synthetic RSS threshold based on the incident's observed peak.

## First upgrade and operational acceptance

This changes a locking protocol without changing the state schema. There is no safe automatic proof of legacy owner death from the old empty sentinel. Deployment must establish that legacy writers are stopped before new state writers run.

1. Build and verify the intended clean source revision. Record binary build metadata and a digest. Inspect the effective service executable/configuration/state path privately; retain existing private overrides and the configured Langfuse project.
2. Pause new hook invocations and other state-writing commands at their source for the cutover window. Let in-flight hooks finish or stop them explicitly. Stop the watcher and any independently launched legacy watchers. Stopping only the systemd unit is insufficient if other writers exist.
3. Confirm those known processes have exited without dumping full command lines or environment values. Absence of an open lock-file descriptor is not confirmation. If writers cannot be quiesced, defer the protocol change.
4. While writers are stopped, validate the existing JSON/version and record a private checksum plus queue/checkpoint counts. Preserve the version 3 state. The existing sidecar may remain; the new implementation can open and lock it after the legacy writers have stopped.
5. Run the verified installer. Confirm the installed executable's build identity/digest and the service's effective executable. Ensure hook commands will resolve to that same updated binary before resuming them.
6. Resume producers. Confirm there are no fresh acquisition errors, the watcher makes progress, existing queued work drains on success, and pending-score and processed-ID behavior is retained. A live queue may shrink normally; compare its disposition rather than requiring a byte-identical state after export resumes.
7. Produce one bounded trace through the normal watcher and verify it in the configured Langfuse project. Check fresh logs and `NRestarts` over an observation window of at least two maximum retry intervals plus a normal poll interval. Stable `active` status alone is insufficient.
8. On a disposable local state/fixture setup with the built executable, hold a real advisory lock long enough to trigger retry, then release it and verify progress in the same process. Perform forced-kill tests against disposable state, not the live workstation queue.
9. Record source/build identity, local gates, deployment identity, state preservation, trace visibility, and restart/progress evidence separately. The historical incident figures are not substitutes for any of these checks.

A failed hook enqueue has not been retained. Document retrying the existing hook invocation or using the existing explicit transcript export path after contention clears. Do not add a second queue, direct hook export, automatic hook installation, or a background hook retry daemon.

## Initial planning-stage validation recorded on 2026-09-21

These observations were collected before implementation began. Later entries and final gates must report the implemented tree separately.

| Check | Observed result |
| --- | --- |
| Source review and scratch legacy/new-protocol probe | Confirmed the ownership and mixed-version hazards described above. |
| `go test ./... -count=1` | All command/internal packages passed. The `test` package failed in `TestInstallUninstallScripts` at `test/install_test.go:64`: `unexpected Langfuse request GET /`. The full gate is not green. |
| `go test ./test -run '^TestInstallUninstallScripts$' -count=1 -v` | Reproduced the same unexpected root request and test failure. |
| Independent disposable loopback HTTP listener | Received an unsolicited `GET /` from a loopback peer with a Go HTTP client User-Agent and no Authorization header while the probe itself sent zero HTTP requests. The sender process was not identified. |
| Secondary-loopback installer mock | The first 30-second listener observation saw no probe, but a later 36-second installer test received the same unauthenticated loopback `GET /`. The test now classifies only that exact method/path, Go HTTP User-Agent, missing-Authorization, loopback-peer signature and logs the count. It continues to reject all other unexpected requests and checks BasicAuth on both expected model routes. This is an explicit accommodation for the independently observed probe, not an exporter endpoint allowlist. |
| Model-sync source review | `listModels` and `createModel` construct `/api/public/models` requests. No root request was found in that reviewed call path. |
| Document checks | Existing diff whitespace check and the new-file whitespace check found no errors; all three relative document links resolve. |

The initial installer failure was later resolved by classifying only the independently observed, unauthenticated loopback `GET /` probe signature. The exporter source review and installer tests continue to require BasicAuth on the model endpoints and reject all other unexpected routes. Implementation evidence and final gates are recorded below; this historical planning section is not itself a passing implementation test.

## Implementation validation

Local code, documentation, install ordering, and process-crash recovery are implemented. These results were collected on 2026-09-21:

| Check | Result |
| --- | --- |
| `go test ./... -count=1` | Passed all packages. |
| `go test ./... -coverpkg=./... -coverprofile=/tmp/codex-langfuse-tracer.all.cover` | Passed all packages; `./...` statement coverage was 49.1%. |
| `go test -race ./internal/exportstate ./internal/claudehook ./internal/watch ./cmd/codex-langfuse-exporter -count=1` | Passed all four packages, including killed-owner, live-owner, and contention recovery cases. |
| Five serial watcher/hook latency samples | `TestEvalWatchExportLatency` and `TestEvalHookQueueDrainLatency` passed all five repetitions; maximum observed watch scan time was 6.51 ms against the 5 s limit. |
| Two 10-second `internal/codextrace` fuzz gates | `FuzzParseTurnsDoesNotPanic` and `FuzzExportTextRedactsSentinels` passed. |
| Installer and documentation tests | Passed fresh install, existing-install ordering, pricing and stop failure preservation, post-stop restart failure reporting, sidecar retention, and lock-upgrade documentation assertions. The test logged one independently observed unauthenticated loopback `GET /` probe and rejected all other unexpected requests. |
| `git diff --check`, `bash -n install.sh uninstall.sh` | Passed. |

No production service rollout, live Langfuse trace, or restart-window observation is claimed here. Those require the documented first-upgrade writer quiescence procedure and the target workstation's operational evidence; tests establish process-crash recovery on the tested local filesystem, not power-loss durability.

## Rollback and completion

Prefer a rollback build that retains the new lock protocol. A rollback to the legacy binary requires the same quiescence procedure for every new writer. Only after all writers are stopped may the persistent sidecar be removed so that the legacy `O_EXCL` implementation can start. Preserve the JSON state. Returning to the legacy implementation also restores the original crash vulnerability.

Do not add routine sidecar deletion to startup, restart, installation, or uninstall cleanup. Its continued existence is normal. Any manual removal for a deliberate return to the old protocol requires stopped writers; a successful advisory-lock probe alone does not prevent another writer from opening the path afterward.

The work is complete when the meaningful subprocess and watcher regressions pass, installation cannot unintentionally overlap service versions, the first-upgrade instructions account for hooks and independent writers, required repository checks pass, and the deployment evidence matches the scope claimed. Reconciliation, gateway promotion, pricing, trace projection, and historical backfill retain their existing owners and contracts in the [canonical multi-machine handoff](multi-machine-tracing-gateway-handoff.md).
