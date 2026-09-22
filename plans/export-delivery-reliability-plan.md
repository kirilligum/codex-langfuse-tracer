# Export delivery reliability follow-up

- Project: `codex-langfuse-tracer`
- Date: 2026-09-22
- Status: scoped assessment and decision plan; no runtime changes authorized
- Related incident: [September 21 duplicate observations RCA](duplicate-observations-rca-20260922.md)
- Owner: this repository for local source discovery, watcher, state, and export; Langfuse owns remote ingestion semantics

## Purpose

The September 21 repeat-export incident is resolved by the deployed state-lock recovery in `827a66c`. This plan covers only remaining cases where the remote may accept spans while the local process fails before it records progress, and where two local export paths try the same trace.

This is a separate reliability problem. It must not reopen the completed stale-lock incident or imply that manual export caused the recorded duplicates.

## Current facts

- The watcher sends spans before saving `pending_scores` and `processed_trace_ids`.
- `pending_scores` records a trace ID and environment after span export succeeds; it does not record an in-flight request or an unknown remote result.
- The watcher retries source turns until local state advances.
- Manual export sends directly and does not consult watcher state.
- State mutations use a local file lock. The network send and local checkpoint do not form one atomic operation.
- Stable span IDs identify retries, but the configured Langfuse v4 ingestion path does not guarantee that re-ingesting an observation replaces or deduplicates it.
- The current incident trace is back to five unique observations. Do not replay it for testing or cleanup.

## Delivery guarantee and unavoidable trade-off

An HTTP request can be accepted while its acknowledgement or the following state write is lost. The local process then cannot prove acceptance from its checkpoint alone.

| Policy after an uncertain result | Duplicate risk | Missing trace risk |
| --- | --- | --- |
| Retry automatically (at least once) | Possible | Low, assuming the source remains available |
| Mark complete before sending (at most once) | Lower | Possible if the process dies before acceptance |
| Receiver idempotency for a stable export key | Low if the receiver enforces it | Low, subject to receiver contract |
| Query remote observations and decide | Lower after accepted data becomes visible | Still possible during delayed or partial visibility |

The repository cannot promise exactly-once delivery with a local checkpoint and a receiver that does not enforce an idempotency key. A preflight read narrows some duplicate paths but cannot make the read plus write atomic.

## Recommended scope

Keep automatic recovery at least once until a remote idempotency contract exists. Make the uncertainty visible, make local send ownership explicit, and test that known local checkpoint contention never triggers a second send in the same live process. Assess a bounded remote observation check before replay only as a mitigation; it must handle visibility delay, pagination, and partial batches and must not treat a lookup error as absence.

Do not add a second exporter, state file, distributed lock, destructive cleanup, score migration, or reconciliation implementation as part of this work.

## Work sequence

### D0. Preserve the incident closeout

- Keep the RCA and its evidence summary as the record for this incident.
- Mark this plan as a separate follow-up; retain the deployed `827a66c` fix as the incident remedy.
- Defer reconciliation until its delivery decision uses the same tested send ownership and retry contract.

**Exit:** the RCA is linked from the operational handoff; the older reconciliation plan is explicitly deferred pending this delivery decision.

### D1. Add fault-injection coverage before choosing an implementation

Use a local `httptest.Server`, temporary state, and a synthetic completed turn. Never replay a production trace.

Exercise these cut points:

1. OTLP request rejected before acceptance.
2. OTLP request accepted, then return an acknowledgement the client cannot observe.
3. OTLP request accepted, then fail the next local state mutation.
4. Process exit after remote acceptance and before a durable checkpoint, followed by watcher restart.
5. Two local paths attempt the same deterministic trace while one is in flight.
6. Observation visibility is delayed, a page fails, or only part of a multi-request projection is visible.
7. Score ingestion fails after span progress is persisted.

For every case, assert span-request count, score-request count, state contents, exit status, and whether the final outcome is accepted, rejected, or unknown. Do not assert exactly-once success where the mock receiver cannot provide it.

**Exit:** tests reproduce the remaining replay window and report which current paths already serialize or retry safely.

### D2. Record the delivery decision

After D1, choose and document:

- whether missing-trace risk or duplicate risk wins when acceptance is unknown;
- how long a remote visibility check waits and what evidence counts as present;
- how a partial observation set is handled without claiming a resend is duplicate-free;
- whether manual export joins watcher-owned state or remains a separate explicit operation;
- whether one local send owner is sufficient for supported workstation usage;
- whether a receiver-side idempotency key is available from Langfuse or requires a separate gateway service.

The repository recommendation is at-least-once with an explicit `outcome_unknown` report and retry, unless the tests or product requirements justify accepting possible trace loss. A remote lookup alone must not be described as exactly once.

**Exit:** one written decision defines duplicate versus loss behavior, retry rules, and supported writer topology.

### D3. Implement only the selected local policy

Potential local changes, selected only after D2:

- serialize same-trace sends among watcher and manual paths through the existing local state owner;
- represent pending, accepted, and unknown outcomes durably if restart recovery requires that distinction;
- preserve source-path and score retry information across restart;
- emit a bounded diagnostic when acceptance cannot be established;
- keep prompt, answer, tool output, credentials, and response bodies out of logs.

Do not move network delivery into state locking until contention duration, crash recovery, and cancellation behavior are specified. Do not persist raw trace payloads in state.

**Exit:** no normal checkpoint-lock failure can cause a tight resend loop; crash/timeout tests match the D2 policy; one trace is not concurrently sent twice by supported local writers.

### D4. Roll out and verify

- Run `go test ./... -count=1`, the focused fault-injection tests, `go test -race` for state and watcher code, and `git diff --check`.
- Install through `install.sh`, preserving version 3 state.
- Verify the running executable revision and watcher restart count.
- Use one new controlled canary to exercise normal send and restart recovery. Do not replay the historical incident trace.
- Record observed send counts and final Langfuse observation counts. State any remaining at-least-once limitation directly.

**Exit:** the installed revision matches the tested revision, the watcher is healthy, and the canary matches the documented delivery policy.

## Non-goals

- Reconstructing or deleting the historical 3,138 accepted copies. ClickHouse has already merged those rows in the configured instance, and no cleanup is needed for the inspected trace.
- Proving duplicates are absent across every project, environment, or backend.
- Treating separate trace observations (`codex.agent`, `codex.transcript`, tools) as duplicate rows.
- Treating deterministic IDs, a local mutex, or a preflight read as remote idempotency.
- Implementing broad manual-export guards, score timestamp changes, or `--reconcile` before the delivery decision is made.
