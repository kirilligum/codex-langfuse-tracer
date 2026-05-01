# Codex Langfuse Tracer

Export useful Codex CLI activity to Langfuse, including visible prompts, final answers, tool calls, command output, patch diffs, and file-change metadata.

This is a machine-level Codex setup, not a project-level dependency. Install it once on a workstation and it can trace Codex sessions from any repository.

## Status

- Tested with Codex CLI `0.128.0`.
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

With a shell wrapper installed, running `codex` starts a background watcher before launching the real Codex CLI. The watcher exports tool/content observations while the interactive session is open, and a final export runs after Codex exits.

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

Every supplemental observation also carries trace metadata for:

- `codex_session_id`
- `codex_turn_id`
- `codex_transcript_exported`

## Important Limits

This is not a byte-for-byte recording of the terminal and it is not a hidden reasoning export.

It does not include:

- Hidden chain-of-thought or encrypted reasoning content.
- Text that Codex does not write to local rollout JSONL files.
- A guaranteed canonical list of every file added to model context.
- File writes performed outside `apply_patch` as structured file-change metadata, unless they appear in command output or diffs recorded by Codex.
- Arbitrarily large outputs beyond the exporter's per-field cap.
- Perfect secret handling.
- Guaranteed idempotent re-export behavior.

For context files specifically: Codex may record startup instructions, user-provided text, shell commands that read files, and patch diffs. It does not always emit a distinct structured event saying "this file was added to model context." Treat file-context visibility as best-effort.

## Security

This can export prompt text, assistant text, tool inputs, command output, and diffs to Langfuse. Do not enable it where Codex sessions may contain secrets, customer data, tax data, banking data, card data, private legal data, or other sensitive material unless that export is intentionally approved.

The exporter redacts several common token/key patterns, but redaction is a last line of defense, not a security boundary.

Protect `~/.codex/config.toml` if it contains API keys or a Basic auth header:

```sh
chmod 600 ~/.codex/config.toml
```

## Install

Clone the repo:

```sh
git clone https://github.com/kirilligum/codex-langfuse-tracer.git ~/p/codex-langfuse-tracer
cd ~/p/codex-langfuse-tracer
```

Install the exporter and bash/zsh wrapper:

```sh
./install.sh
```

This installs:

```text
~/.codex/bin/export_codex_session_to_langfuse.py
~/.codex/shell/codex-langfuse-tracer.sh
```

Load the wrapper in the current shell:

```sh
source ~/.codex/shell/codex-langfuse-tracer.sh
```

To load it automatically, add that `source` line to `~/.bashrc` or `~/.zshrc`.

## Configure Langfuse

Create a Langfuse API key pair in the target project. Use the same host and keys for both the supplemental exporter and native Codex OTEL.

Host examples:

- Local self-hosted Langfuse: `http://localhost:3000`
- Langfuse Cloud US: `https://us.cloud.langfuse.com`
- Langfuse Cloud EU/default: `https://cloud.langfuse.com`

Store the exporter credentials in `~/.codex/config.toml`:

```toml
[mcp_servers.langfuse]
command = "uvx"
args = ["--python", "3.11", "langfuse-mcp"]

[mcp_servers.langfuse.env]
LANGFUSE_HOST = "http://localhost:3000"
LANGFUSE_PUBLIC_KEY = "<LANGFUSE_PUBLIC_KEY>"
LANGFUSE_SECRET_KEY = "<LANGFUSE_SECRET_KEY>"
```

Build the Basic auth token for native Codex OTEL:

```sh
LANGFUSE_PUBLIC_KEY="<LANGFUSE_PUBLIC_KEY>"
LANGFUSE_SECRET_KEY="<LANGFUSE_SECRET_KEY>"
LANGFUSE_OTEL_AUTH="$(printf "%s:%s" "$LANGFUSE_PUBLIC_KEY" "$LANGFUSE_SECRET_KEY" | base64 | tr -d '\n')"
echo "$LANGFUSE_OTEL_AUTH"
```

Add the Codex OTEL section to the same `~/.codex/config.toml` file:

```toml
[otel]
environment = "default"
exporter = "none"
log_user_prompt = false
trace_exporter = { otlp-http = { endpoint = "http://localhost:3000/api/public/otel/v1/traces", protocol = "binary", headers = { "Authorization" = "Basic <BASE64_PUBLIC_KEY_COLON_SECRET_KEY>", "x-langfuse-ingestion-version" = "4" } } }
```

Use the matching host in `LANGFUSE_HOST` and in the OTEL endpoint. Do not use the older `[telemetry]` block for tested Codex CLI versions; use `[otel]`.

In the tested setup, Codex did not expand environment variables inside OTEL headers, so the Basic header had to be pasted directly into `config.toml`. Re-check this behavior for your Codex version before relying on env-var interpolation.

See [examples/codex-config.toml](examples/codex-config.toml).

## Verify

Syntax-check the installed files:

```sh
python3 -m py_compile ~/.codex/bin/export_codex_session_to_langfuse.py
```

Run a small Codex prompt:

```sh
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

```sh
~/.codex/bin/export_codex_session_to_langfuse.py --latest
```

Export a known Codex session:

```sh
~/.codex/bin/export_codex_session_to_langfuse.py --session-id <SESSION_ID>
```

Export a specific rollout file:

```sh
~/.codex/bin/export_codex_session_to_langfuse.py --path ~/.codex/sessions/YYYY/MM/DD/rollout-....jsonl
```

## Troubleshooting

Check the wrapper is loaded:

```sh
type codex
```

Check the real Codex binary still resolves:

```sh
command -v codex
```

Check exporter logs:

```sh
tail -n 100 ~/.codex/langfuse-transcript-export.log
```

Find the local session file for a prompt:

```sh
rg -l "some prompt text" ~/.codex/sessions
```

Common failure modes:

- Wrong Langfuse host. Use the same host in `LANGFUSE_HOST` and the OTEL endpoint.
- Old Codex config schema. Tested Codex CLI versions use `[otel]`, not `[telemetry]`.
- Basic header is split across lines. The token must be one continuous string.
- The wrapper was installed after an existing Codex session started. Exit Codex and start a new `codex`.
- Langfuse ingestion delay. Wait a few seconds and refresh the UI.
- Empty native observations. Select the supplemental `codex.transcript` observation.

## Remove

Remove the transcript exporter and bash/zsh wrapper:

```sh
cd ~/p/codex-langfuse-tracer
./uninstall.sh
```

Open a new shell and confirm `codex` resolves to the real binary:

```sh
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
