# Codex Langfuse Tracer

Export useful Codex CLI activity to Langfuse, including visible prompts, final answers, terminal output, tool calls, command output, patch diffs, token usage, timing, and file-change metadata.

This is a machine-level Codex setup, not a project-level dependency. Install it once on a workstation and it can trace Codex sessions from any repository.

## Status

- Tested with Codex CLI `0.128.0`.
- Uses Codex's local rollout JSONL files under `~/.codex/sessions/`.
- Unofficial best-effort companion exporter. Codex's rollout file format may change.
- Apache-2.0 licensed.

## What It Does

The setup has one tracing path:

1. The shell wrapper runs the real Codex CLI.
2. After Codex exits, the exporter reads the newest local Codex rollout JSONL file and sends one supplemental Langfuse OTLP trace for each completed turn.

Native Codex OTEL is intentionally not part of this setup. It emits many low-level runtime spans such as streaming, socket, dispatch, and server internals that are not useful for understanding Codex prompts, answers, terminal output, tool calls, token usage, or file changes.

The exporter fills Langfuse trace and observation fields such as:

```text
langfuse.trace.input
langfuse.trace.output
langfuse.observation.input
langfuse.observation.output
langfuse.observation.metadata
```

With a shell wrapper installed, running `codex` launches the real Codex CLI and exports the newest recorded rollout after Codex exits.

## What You Can See In Langfuse

The exporter sends these observations when Codex records the data locally:

- `codex.transcript`: generation observation with the user prompt, final assistant answer, and token usage.
- `codex.terminal`: ordered visible CLI event stream built from Codex's recorded user messages, assistant messages, tool calls, command output, and patch output.
- `codex.message.commentary`: assistant progress updates shown in the CLI.
- `codex.reasoning.summary`: visible reasoning summaries when Codex records a non-empty summary.
- `codex.tool.exec_command`: shell command input and terminal output.
- `codex.tool.apply_patch`: patch input, changed files, and unified diffs.
- `codex.tool.mcp`: MCP invocation and result.
- `codex.tool.web_search`: web search query/action.
- `codex.tool.tool_search`: deferred-tool discovery calls and results.

Tool observations use Langfuse's `tool` observation type. The transcript uses `generation`.

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

The `codex.terminal` observation is an ordered stream of the terminal-relevant events Codex records locally. It is not a byte-for-byte TUI recording and it is not a hidden reasoning export.

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

Protect `~/.codex/config.toml` if it contains API keys:

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

Create a Langfuse API key pair in the target project.

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
7. Select `codex.terminal` to inspect the ordered visible CLI stream for the turn.

The visible prompt/answer text is expected on `codex.transcript`. The ordered CLI stream is expected on `codex.terminal`, and filterable tool details are expected on `codex.tool.*` observations.

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

- Wrong Langfuse host. Use the same host where the target Langfuse project lives.
- Native Codex OTEL still enabled. Delete the `[otel]` section from `~/.codex/config.toml` if Langfuse shows low-level spans such as `handle_responses`, `receiving`, `socket reader`, or `serve_inner`.
- Langfuse ingestion delay. Wait a few seconds and refresh the UI.
- Empty Input/Output on unrelated observations. Select `codex.transcript`.

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

If Langfuse MCP was added only for this setup, remove the `[mcp_servers.langfuse]` block too.
