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

1. The executable `codex` wrapper runs the real Codex CLI.
2. After Codex exits, the exporter sends one supplemental Langfuse OTLP trace for each completed turn in the recorded rollout.

Native Codex OTEL is intentionally not part of this setup. It emits many low-level runtime spans such as streaming, socket, dispatch, and server internals that are not useful for understanding Codex prompts, answers, terminal output, tool calls, token usage, or file changes.

The exporter fills Langfuse trace and observation fields such as:

```text
langfuse.trace.input
langfuse.trace.output
langfuse.observation.input
langfuse.observation.output
langfuse.observation.metadata
```

With the wrapper installed first on `PATH`, running `codex` launches the real Codex CLI and exports the recorded rollout after Codex exits.

## What You Can See In Langfuse

The exporter sends these observations when Codex records the data locally:

- `codex.agent`: root agent observation with the trace-table prompt and final answer.
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

## Trace Contract

The trace surface is intentionally small. The goal is to understand Codex's input, output, visible terminal activity, tool calls, timing, and token usage without storing runtime internals.

Keep and emit:

- One Langfuse trace per completed Codex turn, named `codex.turn.transcript`.
- Stable trace IDs derived from the Codex session id and turn id when Codex does not record a trace id.
- `codex.agent` as the root `agent` observation so Langfuse renders Codex turns as agent workflows.
- `codex.transcript` as the main child `generation` observation, with user input, final assistant output, and token usage.
- Trace-level input/output on `codex.agent` so Langfuse trace tables show the prompt and final answer without terminal timestamps.
- Token usage fields: `input`, `output`, `total`, `cached_input`, and `reasoning_output` when Codex records them.
- `codex.terminal` as one ordered visible CLI stream for the turn.
- `codex.tool.exec_command` for shell command input, terminal output, status, exit code, and duration.
- `codex.tool.apply_patch` for patch input, patch output, changed files, change types, and unified diffs.
- `codex.tool.mcp`, `codex.tool.web_search`, and `codex.tool.tool_search` when Codex records those calls.
- `codex.message.commentary` for visible assistant progress messages.
- `codex.reasoning.summary` only when Codex records a non-empty visible reasoning summary.

Do not emit:

- Native Codex OTEL runtime spans. This includes names such as `socket reader`, `perform`, `serve_inner`, `transport_worker`, `get_model_info`, `account/read`, `initialize`, `skills/list`, `list_all_tools`, `account/rateLimits/read`, `model_client.stream_responses_websocket`, `thread/list`, `thread/start`, `thread/read`, `thread/resume`, `thread/unsubscribe`, `model/list`, `list_models`, `session_loop`, `turn/steer`, `codex.exec`, `run_turn`, `run_sampling_request`, `handle_responses`, `receiving`, `receiving_stream`, `build_tool_call`, and `dispatch_tool_call_with_code_mode_result`.
- `codex.timeline`; `codex.terminal` replaces it.
- Per-file observation fanout. File changes stay as metadata on `codex.tool.apply_patch`.
- Inferred "model context" observations. File reads may appear as command output, but they are not labeled as model context.
- Hidden chain-of-thought or encrypted reasoning content.
- Duplicate `langfuse.trace.input` / `langfuse.trace.output` on child observations.
- Multiple export paths for the same turn, such as native OTEL plus supplemental export or watcher plus final export.

Existing old traces in Langfuse can still contain deleted names. New sessions should not emit them once `[otel]` is removed from `~/.codex/config.toml` and this wrapper/exporter is installed.

## Important Limits

The `codex.terminal` observation is an ordered stream of the terminal-relevant events Codex records locally. It is not a byte-for-byte TUI recording and it is not a hidden reasoning export.

Langfuse Trace Log View and Agent Graphs are UI features built from the same observations. The exporter emits typed observations (`agent`, `generation`, `tool`, and `span`) with parent-child nesting so those views can render without adding a second trace format.

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

Install the exporter and shell-agnostic `codex` wrapper:

```sh
./install.sh
```

This installs:

```text
~/.codex/bin/export_codex_session_to_langfuse.py
~/.codex/bin/codex
```

Put `~/.codex/bin` before the real Codex binary on `PATH`.

For bash/zsh:

```sh
export PATH="$HOME/.codex/bin:$PATH"
```

For fish:

```fish
fish_add_path --path --prepend "$HOME/.codex/bin"
```

To make bash/zsh permanent, add the `export PATH=...` line to `~/.bashrc` or `~/.zshrc`. To make fish permanent, add the `fish_add_path ...` line to `~/.config/fish/config.fish`.

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
8. Open Log View and confirm `codex.agent` is the root, `codex.transcript` is a `Generation`, and tool calls are `Tool` rows.

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

Check the wrapper resolves before the real Codex binary:

```sh
command -v codex
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
- Shell aliases or functions that bypass `PATH` and call the real Codex binary by absolute path. They should call `codex` through `PATH` so `~/.codex/bin/codex` runs first.
- Langfuse ingestion delay. Wait a few seconds and refresh the UI.
- Empty Input/Output on unrelated observations. Select `codex.transcript`.

## Remove

Remove the transcript exporter and `codex` wrapper:

```sh
cd ~/p/codex-langfuse-tracer
./uninstall.sh
```

Open a new shell and confirm `codex` resolves to the real binary:

```sh
type codex
```

If Langfuse MCP was added only for this setup, remove the `[mcp_servers.langfuse]` block too.
