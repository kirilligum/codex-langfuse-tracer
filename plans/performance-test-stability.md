# Performance Test Stability Decision

- Decision ID: ADR-PERF-001
- Status: Accepted
- Date: 2026-08-15
- Owners: codex-langfuse-tracer maintainers

## Context

Two default-suite tests measured small units of work with wall-clock deadlines on the developer host. `TestEvalInsightRollupLatency` required 100 rollups within 10 ms. `TestEvalClaudeParserDeterminismAndLatency` combined 200 fixture reads, parsing, JSON marshaling, and determinism comparison under a 200 ms deadline while also using `t.Parallel()`. Both failed under host contention even though repeated focused system-level watcher checks remained far below their budgets. These assertions measured scheduler and filesystem availability together with product code, so a failure did not identify a product regression.

This repository needs one direct verification model: deterministic functional tests in the default suite, Go benchmarks for micro-performance observations, and binding latency thresholds only at the user-visible watcher boundaries.

## Decision

- Keep rollup and Claude parser correctness, redaction, and determinism in ordinary tests without wall-clock assertions.
- Measure rollup and parser micro-performance with `BenchmarkInsightRollup` and `BenchmarkClaudeParserCorpus`, using `b.Loop()` and `b.ReportAllocs()`.
- Treat benchmark results as comparative engineering evidence, not release pass/fail thresholds.
- Keep `TestEvalWatchExportLatency` and `TestEvalHookQueueDrainLatency` as the binding performance gates because they exercise the actual scan/export and queued-hook paths.
- Run binding gates with one package process and no parallel test execution so they measure the intended system path consistently.
- Do not add retries, load detection, environment-based skips, CPU affinity, alternate test paths, or production optimizations solely to satisfy developer-host micro-timers.

## Verification

Default correctness and determinism:

```sh
go test ./internal/agenttrace ./internal/claudetrace -count=1
```

Non-binding benchmark evidence:

```sh
go test -p=1 ./internal/agenttrace ./internal/claudetrace -run '^$' -bench 'Benchmark(InsightRollup|ClaudeParserCorpus)$' -benchmem -count=5
```

Binding release evidence:

```sh
go test -p=1 ./internal/watch -run '^(TestEvalWatchExportLatency|TestEvalHookQueueDrainLatency)$' -parallel=1 -count=5 -v
```

## Consequences

- The default suite no longer fails because a sub-millisecond operation was descheduled by an unrelated host workload.
- Determinism failures remain hard failures and retain their fixed fixture corpus and repeated comparisons.
- Parser and rollup changes still produce comparable time and allocation samples.
- Release latency remains protected at the boundaries users experience.
- The former 10 ms rollup and 200 ms parser corpus thresholds are superseded wherever older implementation plans record them.
