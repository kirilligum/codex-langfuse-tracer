# Codex Langfuse Tracer

Export useful Codex CLI activity to Langfuse, including visible prompts, final answers, tool calls, command output, patch diffs, and file-change metadata.

This is a machine-level Codex setup, not a project-level dependency. Install it once on a workstation and it can trace Codex sessions from any repository.

## Status

- Tested with Codex CLI `0.125.0`.
- Uses Codex's local rollout JSONL files under `~/.codex/sessions/`.
- Unofficial best-effort companion exporter. Codex's rollout file format and OTEL config schema may change.
- Apache-2.0 licensed.

## What It Does

The setup has two layers:

1. Native Codex OpenTelemetry export sends Codex spans, timing, and metadata to Langfuse.
2. This tracer reads local Codex rollout JSONL files and sends extra Langfuse OTLP spans with visible content.

The second layer exists because native Codex OTEL can show useful metadata while leaving Langfuse Input/Output fields empty. This tracer fills Langfuse trace and observation fields such as:

```text
langfuse.trace.input
langfuse.trace.output
langfuse.observation.input
langfuse.observation.output
langfuse.observation.metadata
```

With the fish wrapper installed, running `codex` starts a background watcher before launching the real Codex CLI. The watcher exports tool/content observations while the interactive session is open, and a final export runs after Codex exits.

## What You Can See In Langfuse

The exporter sends these observations when Codex records the data locally:

- `codex.transcript`: user prompt and final assistant answer.
- `codex.message.commentary`: assistant progress updates shown in the CLI.
- `codex.tool.exec_command`: shell command input and terminal output.
- `codex.tool.apply_patch`: patch input, changed files, and unified diffs.
- `codex.tool.mcp`: MCP invocation and result.
- `codex.tool.web_search`: web search query/action.
- `codex.tool.tool_search`: deferred-tool discovery calls and results.
- `codex.timeline`: one combined view of commentary and tool activity for the turn.

For `codex.tool.apply_patch`, metadata includes:

- `changed_files`
- `added_files`
- `modified_files`
- `deleted_files`
- `moved_files`
- `file_change_types`
- `changed_file_count`

This makes file changes filterable and inspectable in Langfuse.

## Important Limits

This is not a byte-for-byte recording of the terminal and it is not a hidden reasoning export.

It does not include:

- Hidden chain-of-thought or encrypted reasoning content.
- Text that Codex does not write to local rollout JSONL files.
- A guaranteed canonical list of every file added to model context.
- File writes performed outside `apply_patch` as structured file-change metadata, unless they appear in command output or diffs recorded by Codex.
- Arbitrarily large outputs beyond the exporter's per-field cap.
- Perfect secret handling.

For context files specifically: Codex may record startup instructions, user-provided text, shell commands that read files, and patch diffs. It does not always emit a distinct structured event saying "this file was added to model context." Treat file-context visibility as best-effort.

## Security

This can export prompt text, assistant text, tool inputs, command output, and diffs to Langfuse. Do not enable it where Codex sessions may contain secrets, customer data, tax data, banking data, card data, private legal data, or other sensitive material unless that export is intentionally approved.

The exporter redacts several common token/key patterns, but redaction is a last line of defense, not a security boundary.

Protect `~/.codex/config.toml` if it contains API keys or a Basic auth header:

```fish
chmod 600 ~/.codex/config.toml
```

## Install

Clone the repo:

```fish
git clone https://github.com/kirilligum/codex-langfuse-tracer.git ~/p/codex-langfuse-tracer
cd ~/p/codex-langfuse-tracer
```

Install the exporter and fish wrapper:

```fish
./install.fish
```

This installs:

```text
~/.codex/bin/export_codex_session_to_langfuse.py
~/.config/fish/functions/codex.fish
```

Reload the wrapper in the current fish shell:

```fish
functions -e codex
source ~/.config/fish/functions/codex.fish
```

## Configure Langfuse

Create a Langfuse API key pair in the target Langfuse project.

Set fish universal variables for the transcript exporter:

```fish
set -Ux LANGFUSE_HOST "https://us.cloud.langfuse.com"
set -Ux LANGFUSE_PUBLIC_KEY "<LANGFUSE_PUBLIC_KEY>"
set -Ux LANGFUSE_SECRET_KEY "<LANGFUSE_SECRET_KEY>"
```

Use the host for your Langfuse region. For EU/cloud default projects this may be:

```fish
set -Ux LANGFUSE_HOST "https://cloud.langfuse.com"
```

Build the Basic auth token for Codex native OTEL:

```fish
set -l LANGFUSE_OTEL_AUTH (printf "%s:%s" $LANGFUSE_PUBLIC_KEY $LANGFUSE_SECRET_KEY | base64 | tr -d '\n')
echo $LANGFUSE_OTEL_AUTH
```

Edit `~/.codex/config.toml` and add the Codex OTEL section:

```toml
[otel]
environment = "default"
exporter = "none"
log_user_prompt = false
trace_exporter = { otlp-http = { endpoint = "https://us.cloud.langfuse.com/api/public/otel/v1/traces", protocol = "binary", headers = { "Authorization" = "Basic <BASE64_PUBLIC_KEY_COLON_SECRET_KEY>", "x-langfuse-ingestion-version" = "4" } } }
```

Do not use the older `[telemetry]` block for Codex CLI `0.125.0`; that version uses `[otel]`.

In the tested setup, Codex did not expand environment variables inside OTEL headers, so the Basic header had to be pasted directly into `config.toml`. Re-check this behavior for your Codex version before relying on env-var interpolation.

Optional Langfuse MCP config can live in the same file:

```toml
[mcp_servers.langfuse]
command = "uvx"
args = ["--python", "3.11", "langfuse-mcp"]

[mcp_servers.langfuse.env]
LANGFUSE_HOST = "https://us.cloud.langfuse.com"
LANGFUSE_PUBLIC_KEY = "<LANGFUSE_PUBLIC_KEY>"
LANGFUSE_SECRET_KEY = "<LANGFUSE_SECRET_KEY>"
```

See [examples/codex-config.toml](examples/codex-config.toml).

## Verify

Syntax-check the installed files:

```fish
python3 -m py_compile ~/.codex/bin/export_codex_session_to_langfuse.py
fish -n ~/.config/fish/functions/codex.fish
```

Run a small Codex prompt:

```fish
codex exec --model gpt-5.4-mini --config model_reasoning_effort='"low"' --sandbox read-only --skip-git-repo-check "Reply exactly: langfuse-smoke-test"
```

Then open Langfuse:

1. Go to the target project.
2. Open Tracing.
3. Search for `codex.turn.transcript` or `langfuse-smoke-test`.
4. Open the trace.
5. Select the `codex.transcript` observation.
6. Confirm Input and Output show the prompt and answer text.

Native Codex observations can still have empty Input/Output. The visible prompt/answer text is expected on the supplemental `codex.transcript` observation. Tool details are expected on `codex.tool.*` observations and `codex.timeline`.

## Manual Backfill

Export the latest local Codex session:

```fish
~/.codex/bin/export_codex_session_to_langfuse.py --latest
```

Export a known Codex session:

```fish
~/.codex/bin/export_codex_session_to_langfuse.py --session-id <SESSION_ID>
```

Export a specific rollout file:

```fish
~/.codex/bin/export_codex_session_to_langfuse.py --path ~/.codex/sessions/YYYY/MM/DD/rollout-....jsonl
```

Parse without sending:

```fish
~/.codex/bin/export_codex_session_to_langfuse.py --path ~/.codex/sessions/YYYY/MM/DD/rollout-....jsonl --dry-run
```

## Troubleshooting

Check the wrapper is loaded:

```fish
functions codex
```

Check the real Codex binary still resolves:

```fish
command -v codex
```

Check exporter logs:

```fish
tail -n 100 ~/.codex/langfuse-transcript-export.log
```

Find the local session file for a prompt:

```fish
rg -l "some prompt text" ~/.codex/sessions
```

Common failure modes:

- Wrong Langfuse region. Use the same host as the project, for example `https://us.cloud.langfuse.com`.
- Old Codex config schema. Codex CLI `0.125.0` uses `[otel]`, not `[telemetry]`.
- Basic header is split across lines. The token must be one continuous string.
- Fish wrapper was installed after an existing Codex session started. Exit Codex and start a new `codex`.
- Langfuse ingestion delay. Wait a few seconds and refresh the UI.
- Empty native observations. Select the supplemental `codex.transcript` observation.

## Remove

Remove the transcript exporter and fish wrapper:

```fish
cd ~/p/codex-langfuse-tracer
./uninstall.fish
```

Open a new shell and confirm `codex` resolves to the real binary:

```fish
type codex
```

To remove native Codex OTEL export too, edit `~/.codex/config.toml` and delete the `[otel]` section:

```toml
[otel]
environment = "default"
exporter = "none"
log_user_prompt = false
trace_exporter = { otlp-http = { endpoint = "...", protocol = "binary", headers = { "Authorization" = "...", "x-langfuse-ingestion-version" = "4" } } }
```

If Langfuse MCP was added only for this setup, remove the `[mcp_servers.langfuse]` block too.

## Better Alternatives To Consider

This project is intentionally small and works with the data Codex already writes locally. For more robust production use, consider:

- Native Codex OTEL only, if metadata and timings are enough.
- A future official Codex hook/plugin API, if one exposes transcript and file events directly.
- A dedicated Langfuse SDK exporter that models sessions, generations, tool calls, and files as first-class Langfuse objects.
- An OpenTelemetry collector pipeline if you need central redaction, filtering, routing, or retention control before data reaches Langfuse.
