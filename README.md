# Codex Langfuse Tracer

Export completed Codex CLI turns to Langfuse.

This is a small machine-level companion for people using Codex heavily across many repositories. Install it once on a Linux workstation and it watches Codex's local rollout files, then sends clean Langfuse traces with prompts, final answers, visible terminal activity, tool calls, command output, patch diffs, token usage, timing, and file-change metadata.

It is intentionally not a wrapper around `codex`. Codex runs normally; a `systemd --user` service exports completed turns in the background.

## Status

- Tested with Codex CLI `0.128.0`.
- Built with Go `1.26.0`.
- Uses Codex rollout JSONL files under `~/.codex/sessions/`.
- Uses Langfuse OTLP ingestion at `/api/public/otel/v1/traces`.
- Supports Linux user services through `systemd --user`.
- Licensed under Apache-2.0.

Codex rollout JSONL is not a stable public API. This exporter is production-ready for workstation use, but it is still best-effort around Codex file-format drift.

## Why Use It

Codex already records useful local session data. This exporter turns that data into Langfuse traces that are easier to review, search, and audit.

You can inspect:

- what prompt was given
- what final answer came back
- which shell commands ran
- command status, exit code, duration, and output
- patches and changed files
- MCP, web search, and deferred tool-search calls when Codex records them
- whether verification commands ran and whether any failed
- token usage when Codex records it

This is useful for reviewing LLM coding sessions, debugging failed agent work, sharing traces with a team, and keeping an operational record of important Codex turns.

## Quick Start

### 1. Clone

```sh
git clone https://github.com/kirilligum/codex-langfuse-tracer.git
cd codex-langfuse-tracer
```

### 2. Configure Langfuse

Create a Langfuse API key pair in your target project, then add the credentials to `~/.codex/config.toml`:

```toml
[mcp_servers.langfuse.env]
LANGFUSE_HOST = "https://cloud.langfuse.com"
LANGFUSE_PUBLIC_KEY = "<LANGFUSE_PUBLIC_KEY>"
LANGFUSE_SECRET_KEY = "<LANGFUSE_SECRET_KEY>"
```

Host examples:

- Langfuse Cloud US: `https://us.cloud.langfuse.com`
- Langfuse Cloud EU/default: `https://cloud.langfuse.com`
- Local self-hosted Langfuse: `http://localhost:3000`

If your config already has a `[mcp_servers.langfuse]` block for Langfuse MCP, keep it. The exporter only reads the `env` values.

Protect the config file if it contains API keys:

```sh
chmod 600 ~/.codex/config.toml
```

### 3. Install

```sh
./install.sh
```

The installer builds the Go binary, installs the user service, reloads systemd, enables the service, and restarts it.

Installed files:

```text
~/.codex/bin/codex-langfuse-exporter
~/.config/systemd/user/codex-langfuse-watch.service
~/.codex/langfuse-export-state.json
```

The state file records processed trace IDs so normal watcher runs do not duplicate exports.

If you want the user service to run even when you are logged out, enable lingering for your Linux user:

```sh
loginctl enable-linger "$USER"
```

### 4. Verify

Check the service:

```sh
systemctl --user status codex-langfuse-watch.service
```

Run a tiny Codex turn:

```sh
codex exec --model gpt-5.4-mini -c model_reasoning_effort=low --sandbox read-only --skip-git-repo-check "Reply exactly: langfuse-smoke-test"
```

Open Langfuse, go to Tracing, and search for `langfuse-smoke-test` or `codex.turn.transcript`.

Expected trace shape:

- root observation: `codex.agent`
- main generation: `codex.transcript`
- visible terminal stream: `codex.terminal`
- tool calls: `codex.tool.*`

## Requirements

- Linux with `systemd --user`
- Go `1.26.0` or compatible
- Codex CLI installed and writing rollout files under `~/.codex/sessions/`
- A Langfuse project with public and secret API keys
- Network access from your workstation to the Langfuse host

This repo does not install Codex or Langfuse.

## How It Works

There is one automatic export path:

1. `codex-langfuse-watch.service` runs `~/.codex/bin/codex-langfuse-exporter --watch`.
2. The exporter polls `~/.codex/sessions/` for `rollout-*.jsonl`.
3. Completed turns with visible input and output are parsed.
4. Each completed turn becomes one Langfuse trace named `codex.turn.transcript`.
5. Successfully exported trace IDs are saved in `~/.codex/langfuse-export-state.json`.

The service is independent of the shell and Codex launch path. It covers `codex`, `co`, `codex exec`, and `codex resume` as long as Codex writes rollout files under `~/.codex/sessions/`.

Native Codex OTEL is intentionally not part of this setup. It emits low-level runtime spans such as streaming, socket, dispatch, and server internals that are not useful for reviewing prompts, answers, terminal output, tool calls, token usage, or file changes.

If your `~/.codex/config.toml` has a native `[otel]` section and Langfuse shows noisy spans such as `handle_responses`, `receiving`, `socket reader`, or `serve_inner`, remove that section so this exporter is the single tracing path.

## What Appears In Langfuse

The exporter sends these observations when Codex records the data locally:

- `codex.agent`: root agent observation with trace-table input and output.
- `codex.transcript`: generation observation with the user prompt, final assistant answer, and token usage.
- `codex.terminal`: ordered visible CLI event stream for the turn.
- `codex.message.commentary`: assistant progress updates shown in the CLI.
- `codex.reasoning.summary`: visible reasoning summaries when Codex records a non-empty summary.
- `codex.tool.exec_command`: shell command input and terminal output.
- `codex.tool.apply_patch`: patch input, changed files, and unified diffs.
- `codex.tool.mcp`: MCP invocation and result.
- `codex.tool.web_search`: web search query/action metadata.
- `codex.tool.tool_search`: deferred-tool discovery calls and results.

Tool observations use Langfuse's `tool` observation type. The transcript uses `generation`.

`codex.tool.apply_patch` metadata includes:

- `changed_files`
- `added_files`
- `modified_files`
- `deleted_files`
- `moved_files`
- `file_change_types`
- `changed_file_count`

The root trace/`codex.agent` carries compact `codex_insight` metadata for table scanning:

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

`command_kind` uses a fixed enum: `test`, `build`, `lint`, `format`, `git`, `read`, `search`, `install`, `systemd`, `network`, or `other`.

## Manual Export

The watcher is the normal production path. Manual export is for explicit backfill or debugging.

Export the latest local Codex session:

```sh
~/.codex/bin/codex-langfuse-exporter --latest
```

Export a known Codex session:

```sh
~/.codex/bin/codex-langfuse-exporter --session-id <SESSION_ID>
```

Export a specific rollout file:

```sh
~/.codex/bin/codex-langfuse-exporter --path ~/.codex/sessions/YYYY/MM/DD/rollout-....jsonl
```

Skip post-export verification:

```sh
~/.codex/bin/codex-langfuse-exporter --latest --no-verify
```

## Safety And Privacy

This exporter can send prompt text, assistant text, tool inputs, command output, and diffs to Langfuse. Do not enable it where Codex sessions may contain secrets, customer data, tax data, banking data, card data, private legal data, or other sensitive material unless exporting that data is intentionally approved.

The exporter redacts several common token/key patterns, but redaction is a last line of defense, not a security boundary.

The exporter does not emit:

- hidden chain-of-thought or encrypted reasoning content
- native Codex runtime spans
- byte-for-byte TUI recordings
- per-file observation fanout
- inferred "model context" observations

`codex.terminal` is an ordered stream of terminal-relevant events Codex records locally. It is not a full terminal recording.

## Troubleshooting

Check the service:

```sh
systemctl --user status codex-langfuse-watch.service
```

Check service logs:

```sh
journalctl --user -u codex-langfuse-watch.service -n 100 --no-pager
```

Confirm the exporter binary exists:

```sh
test -x ~/.codex/bin/codex-langfuse-exporter
```

Find a local session file for a prompt:

```sh
rg -l "some prompt text" ~/.codex/sessions
```

Common failure modes:

- Missing Langfuse credentials in `~/.codex/config.toml`.
- Wrong Langfuse host for the project keys.
- Native Codex OTEL still enabled, causing noisy duplicate traces.
- Watch state already marked a historical turn as processed.
- Langfuse ingestion delay. Wait a few seconds and refresh the UI.
- Empty Input/Output on unrelated observations. Select `codex.transcript`.

## Development

Run the local suite:

```sh
go test ./... -count=1
```

Run focused parser, contract, watcher, exporter, and fuzz checks from [TESTING.md](TESTING.md).

The repo uses a manifest-driven contract corpus:

```text
testdata/manifest.json
testdata/rollouts/*.jsonl
testdata/golden/*.normalized.json
```

Keep that as the single behavioral contract. Do not add a second fixture registry.

## Remove

```sh
./uninstall.sh
```

If Langfuse MCP was added only for this setup, remove the optional `[mcp_servers.langfuse]` block from `~/.codex/config.toml`.

## Social Summary

Codex Langfuse Tracer is a small Go exporter that watches local Codex CLI rollout files and turns completed coding-agent turns into clean Langfuse traces: prompts, final answers, terminal activity, shell commands, patch diffs, token usage, and verification metadata. Install once per workstation; Codex keeps running normally.
