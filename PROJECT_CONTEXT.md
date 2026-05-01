# Project Context For Future Codex Sessions

This file summarizes the final shape of this repository and the local Langfuse setup. It intentionally omits real API keys, Basic auth values, passwords, and other secrets.

## Goal

The project is a machine-level Codex companion that exports useful visible Codex CLI activity to Langfuse:

- user prompts and final assistant answers
- visible assistant commentary
- shell/tool calls and command output
- `apply_patch` diffs and file-change metadata
- MCP, web search, and deferred tool-search calls when Codex records them locally

The repository is standalone and not tied to any one working repo.

## Final Design

The setup has two layers:

1. Native Codex OTEL sends spans, timing, and metadata to Langfuse.
2. `bin/export_codex_session_to_langfuse.py` reads Codex rollout JSONL under `~/.codex/sessions/` and sends supplemental OTLP spans with visible content.

The supplemental exporter exists because native Codex OTEL can leave Langfuse Input/Output fields empty.

The implementation is intentionally small:

- one Python exporter
- one bash/zsh sourceable wrapper at `shell/functions/codex.sh`
- one installer, `install.sh`
- one uninstaller, `uninstall.sh`
- no fish support
- no dry-run mode
- no per-file observation fanout
- no include/exclude config surface

## Important Files

```text
LICENSE
README.md
bin/export_codex_session_to_langfuse.py
examples/codex-config.toml
install.sh
shell/functions/codex.sh
uninstall.sh
PROJECT_CONTEXT.md
```

## Important Behavior

`install.sh` installs:

```text
~/.codex/bin/export_codex_session_to_langfuse.py
~/.codex/shell/codex-langfuse-tracer.sh
```

Users load the wrapper with:

```sh
source ~/.codex/shell/codex-langfuse-tracer.sh
```

The wrapper starts a background watcher before launching the real `codex` binary, then runs one final verified export for the newest rollout file after Codex exits.

The exporter reads Langfuse credentials from `~/.codex/config.toml` under `[mcp_servers.langfuse.env]`. This is the only supported credential path.

## Langfuse Metadata

The transcript span and supplemental observation spans carry trace metadata for:

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

## Limitations To Preserve

Do not weaken these caveats in future docs:

- Codex rollout JSONL is not a stable public tracing API.
- This is best-effort, not authoritative tracing.
- It does not export hidden reasoning or encrypted reasoning content.
- It is not a byte-for-byte terminal recording.
- It cannot guarantee a canonical list of files added to model context.
- File reads may be visible as recorded tool calls, but that is not the same as structured model-context tracking.
- File writes outside `apply_patch` may be missed unless they appear in command output or another recorded event.
- Re-export may duplicate observations; OTLP ingestion is not treated as an upsert contract here.
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

`~/.codex/config.toml` was updated to point native Codex OTEL and the Langfuse MCP env at `http://localhost:3000`. A backup was created before editing:

```text
~/.codex/config.toml.backup.20260430173229
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
python3 -m py_compile bin/export_codex_session_to_langfuse.py
bash -n install.sh uninstall.sh shell/functions/codex.sh
git diff --check
```

The bash/zsh installer and uninstaller were smoke-tested against a temporary `HOME`.

`zsh` is not installed on this machine, so `zsh -n` was not run.

## Future Work Guidance

- Keep the code and docs secret-free.
- Keep one supported install path unless there is a strong reason to broaden support.
- Prefer direct changes over compatibility layers.
- Do not add per-file observations, include/exclude rules, SDK migration, or extra config unless real usage shows the need.
- If improving file visibility, distinguish between files modified, files read by commands, and files actually added to model context.
