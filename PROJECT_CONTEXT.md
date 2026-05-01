# Project Context For Future Codex Sessions

This file summarizes the final shape of this repository and the local Langfuse setup. It intentionally omits real API keys, passwords, and other secrets.

## Goal

The project is a machine-level Codex companion that exports useful visible Codex CLI activity to Langfuse:

- user prompts and final assistant answers
- ordered visible terminal output recorded by Codex
- visible assistant commentary
- shell/tool calls and command output
- `apply_patch` diffs and file-change metadata
- token usage and command timing when available
- MCP, web search, and deferred tool-search calls when Codex records them locally

The repository is standalone and not tied to any one working repo.

## Final Design

The setup has one tracing path:

1. A `systemd --user` service runs `~/.codex/bin/codex-langfuse-exporter --watch`.
2. The exporter polls Codex rollout JSONL under `~/.codex/sessions/` and sends Langfuse OTLP spans for newly completed turns.

Native Codex OTEL is intentionally disabled. It emits low-level runtime spans such as streaming, socket, dispatch, and server internals that are not useful for this repository's trace goals.

The implementation is intentionally small:

- one Go exporter binary
- one user service at `systemd/codex-langfuse-watch.service`
- one installer, `install.sh`
- one uninstaller, `uninstall.sh`
- no dry-run mode
- no wrapper-based export
- no per-file observation fanout
- no include/exclude config surface

## Important Files

```text
LICENSE
README.md
cmd/codex-langfuse-exporter
internal/
testdata/
systemd/codex-langfuse-watch.service
examples/codex-config.toml
install.sh
uninstall.sh
PROJECT_CONTEXT.md
```

## Important Behavior

`install.sh` installs:

```text
~/.codex/bin/codex-langfuse-exporter
~/.config/systemd/user/codex-langfuse-watch.service
```

The installer starts `codex-langfuse-watch.service` and removes the older `~/.codex/bin/codex` wrapper if present. `codex` should resolve to the real Codex CLI.

The watcher stores processed trace ids in:

```text
~/.codex/langfuse-export-state.json
```

On first start it sets an initial time watermark so local history is not flooded into Langfuse. Recent/current rollout files remain eligible so active Codex sessions can still be exported.

The exporter reads Langfuse credentials from `~/.codex/config.toml` under `[mcp_servers.langfuse.env]`. This is the only supported credential path.

## Langfuse Metadata

The exporter emits:

- `codex.agent` as a Langfuse `agent` root observation for the Codex turn.
- `codex.transcript` as a Langfuse `generation` child observation with prompt, final answer, and token usage.
- trace-level input/output on `codex.agent` so Langfuse trace tables show prompt and final answer previews without terminal timestamps.
- stable trace IDs derived from Codex session id and turn id when Codex does not record a trace id.
- `codex.terminal` as one ordered stream of the visible CLI events Codex recorded locally.
- `codex.tool.*` observations as Langfuse `tool` observations.
- `codex.reasoning.summary` only when Codex records a non-empty visible reasoning summary.

The transcript and supplemental observations carry trace metadata for:

- `codex_session_id`
- `codex_turn_id`
- `codex_transcript_exported`

`apply_patch` observations include file-change metadata when Codex records structured changes:

- `changed_files`
- `added_files`
- `modified_files`
- `deleted_files`
- `moved_files`
- `file_change_types`
- `changed_file_count`

The root trace/`codex.agent` includes compact `codex_insight` metadata for filtering and table scanning:

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

`verification_status` is one of `not_applicable`, `not_run`, `passed`, or `failed`. Full `changed_files` stays on `codex.tool.apply_patch`; root metadata only carries compact file-impact summaries.

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
- File reads may be visible as recorded tool calls, but that is not the same as structured model-context tracking.
- File writes outside `apply_patch` may be missed unless they appear in command output or another recorded event.
- Re-export may duplicate observations; OTLP ingestion is not treated as an upsert contract here. The watcher state avoids normal duplicates, but deleting the state file or manual re-export can duplicate traces.
- Redaction is a last line of defense, not a security boundary.

## Local Self-Hosted Langfuse

Langfuse was installed separately from this repo via the official Docker Compose self-hosting flow:

```text
/home/kirill/p/langfuse
```

The local UI runs at:

```text
http://localhost:3000
```

Local Langfuse credentials and generated API keys live in:

```text
/home/kirill/p/langfuse/.env
```

Do not copy those values into this repository.

`~/.codex/config.toml` was updated to point the Langfuse MCP env at `http://localhost:3000`. Native Codex OTEL was removed to avoid noisy internal spans. Backups were created before config edits:

```text
~/.codex/config.toml.backup.20260430173229
~/.codex/config.toml.backup.20260430181234
```

The local stack was verified by sending a small OTLP smoke trace to `http://localhost:3000/api/public/otel/v1/traces` and fetching it back from `/api/public/traces/<trace_id>`.

Useful local Langfuse commands:

```sh
cd ~/p/langfuse
docker compose ps
docker compose logs -f langfuse-web langfuse-worker
docker compose up -d
docker compose down
```

## Validation

After the final simplification, these checks passed:

```sh
go test ./...
bash -n install.sh uninstall.sh
git diff --check
systemctl --user status codex-langfuse-watch.service
```

The generated payload was checked against a local rollout: it includes `codex.terminal`, does not include `codex.timeline`, and child observations no longer set `langfuse.trace.input` / `langfuse.trace.output`.

The installer and uninstaller were smoke-tested against a temporary `HOME`.

The local fish `co` workflow is covered by the watcher because it writes the same Codex rollout files as `codex` and `codex exec`.

The installed watcher was verified with a real `codex exec` prompt. `codex` resolved to the real Codex binary, the service exported the completed turn without wrapper/manual export, and Langfuse returned clean trace input/output for trace `d65bc7460042d68c6955aa4c0228f087`.

## Future Work Guidance

- Keep the code and docs secret-free.
- Keep one supported install path unless there is a strong reason to broaden support.
- Prefer direct changes over compatibility layers.
- Do not add per-file observations, include/exclude rules, SDK migration, or extra config unless real usage shows the need.
- If improving file visibility, distinguish between files modified, files read by commands, and files actually added to model context.
