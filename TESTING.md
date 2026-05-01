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
```

Watcher state, retry, dedupe, and cancellation:

```sh
go test ./internal/watch -count=1
```

Langfuse OTLP projection and trace verification:

```sh
go test ./internal/langfuse -count=1
```

## Fuzz Smoke

```sh
go test ./internal/codextrace -run '^$' -fuzz=FuzzParseTurnsDoesNotPanic -fuzztime=10s
go test ./internal/codextrace -run '^$' -fuzz=FuzzExportTextRedactsSentinels -fuzztime=10s
```

## Fixture Contract

`testdata/manifest.json` is the single fixture inventory. Add rollout fixtures under `testdata/rollouts` and normalized expectations under `testdata/golden`; do not add another registry.

Every new fixture should cover a clear behavior category, avoid real secrets, and keep raw OTLP transport fields out of golden files.
