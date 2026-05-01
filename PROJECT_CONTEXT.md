# Project Context For Future Codex Sessions

This file captures the intended shape of the repository. Keep it generic and secret-free so the repo remains suitable for public sharing.

## Goal

Codex Langfuse Tracer is a machine-level Codex companion that exports useful visible Codex CLI activity to Langfuse:

- user prompts and final assistant answers
- ordered visible terminal events recorded by Codex
- assistant commentary
- shell/tool calls and command output
- `apply_patch` diffs and file-change metadata
- token usage and command timing when available
- MCP, web search, and deferred tool-search calls when Codex records them locally
- compact verification and file-impact metadata for trace-table scanning

The repository is standalone and not tied to any one working repo.

## Design

There is one supported automatic tracing path:

1. A `systemd --user` service runs `~/.codex/bin/codex-langfuse-exporter --watch`.
2. The exporter polls Codex rollout JSONL files under `~/.codex/sessions/`.
3. Each newly completed turn with visible input/output is exported to Langfuse over OTLP HTTP.
4. Processed trace IDs are persisted in `~/.codex/langfuse-export-state.json`.

Native Codex OTEL is intentionally not part of this setup. It produces low-level runtime spans that do not match this repository's review/audit goals.

Keep the implementation small:

- one Go exporter binary
- one user service at `systemd/codex-langfuse-watch.service`
- one installer, `install.sh`
- one uninstaller, `uninstall.sh`
- one manifest-driven fixture corpus under `testdata`
- no wrapper-based export path
- no per-file observation fanout
- no include/exclude config surface
- no parallel fixture registry

## Important Files

```text
README.md
TESTING.md
LICENSE
cmd/codex-langfuse-exporter/
internal/
testdata/manifest.json
testdata/rollouts/
testdata/golden/
systemd/codex-langfuse-watch.service
examples/codex-config.toml
install.sh
uninstall.sh
PROJECT_CONTEXT.md
```

## Install Behavior

`install.sh` installs:

```text
~/.codex/bin/codex-langfuse-exporter
~/.config/systemd/user/codex-langfuse-watch.service
```

It reloads user systemd, enables the service, and restarts `codex-langfuse-watch.service`.

The exporter reads Langfuse credentials from `~/.codex/config.toml` under `[mcp_servers.langfuse.env]`.

Docs may mention `loginctl enable-linger "$USER"` as an optional production workstation step when users want the watcher to keep running after logout.

## Langfuse Contract

The exporter emits:

- `codex.agent` as the Langfuse `agent` root observation.
- `codex.transcript` as the main `generation` child with prompt, final answer, and token usage.
- trace-level input/output on `codex.agent`.
- stable trace IDs derived from Codex session ID and turn ID when Codex does not record a trace ID.
- `codex.terminal` as one ordered stream of visible CLI events Codex recorded locally.
- `codex.tool.*` observations as Langfuse `tool` observations.
- `codex.reasoning.summary` only when Codex records a non-empty visible reasoning summary.

The root trace/`codex.agent` includes compact `codex_insight` metadata:

- `tool_count`
- `command_count`
- `failed_command_count`
- `patch_count`
- `changed_file_count`
- `changed_extensions`
- `touched_test_files`
- `verification_command_count`
- `verification_status`
- `last_verification_command`
- `last_verification_status`

`verification_status` is one of `not_applicable`, `not_run`, `passed`, or `failed`.

`codex.tool.exec_command` metadata includes:

- `command_kind`
- `status`
- `exit_code`
- `duration_ms`
- `failure_type`

`command_kind` is fixed to `test`, `build`, `lint`, `format`, `git`, `read`, `search`, `install`, `systemd`, `network`, or `other`.

## Limitations To Preserve

Do not weaken these caveats in future docs:

- Codex rollout JSONL is not a stable public tracing API.
- This is best-effort, not authoritative tracing.
- It does not export hidden chain-of-thought or encrypted reasoning content.
- `codex.terminal` is not a byte-for-byte terminal recording.
- It cannot guarantee a canonical list of files added to model context.
- File reads may be visible as recorded tool calls, but that is not structured model-context tracking.
- File writes outside `apply_patch` may be missed unless they appear in command output or another recorded event.
- Re-export may duplicate observations after the watch state is deleted or manual backfill is repeated.
- Redaction is a last line of defense, not a security boundary.

## Testing Contract

The test suite should remain useful for LLM maintainers:

- direct Go commands only
- manifest-driven rollout fixtures
- normalized golden traces
- focused package tests for parser/exporter/watcher/config behavior
- native Go fuzz tests for parser panic resistance and redaction leakage

Primary command:

```sh
go test ./... -count=1
```

See `TESTING.md` for focused commands.

## Future Work Guidance

- Keep docs public-safe and secret-free.
- Keep one supported install path unless real usage shows another is needed.
- Prefer direct changes over compatibility layers.
- Do not add per-file observations, include/exclude rules, SDK migrations, or extra config without clear usage evidence.
- If improving file visibility, distinguish files modified, files read by commands, and files actually added to model context.
