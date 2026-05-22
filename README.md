# Codex Langfuse Tracer

[![Codex Langfuse Tracer showcase](https://img.youtube.com/vi/Y6XycKR0Z0I/hqdefault.jpg)](https://youtu.be/Y6XycKR0Z0I)

Export completed Codex CLI and Claude Code turns to Langfuse.

This is a small machine-level companion for people using Codex heavily across many repositories. Install it once on a Linux workstation and it watches Codex's local rollout files, then sends clean Langfuse traces with prompts, final answers, visible terminal activity, tool calls, command output, patch diffs, token usage, timing, and file-change metadata.

It is intentionally not a wrapper around `codex`. Codex runs normally; a `systemd --user` service exports completed turns in the background.

## Status

- Tested with Codex CLI `0.128.0`.
- Claude Code support is implemented for explicit transcript export and Stop hook queueing.
- Built with Go `1.26.0`.
- Uses Codex rollout JSONL files under `~/.codex/sessions/`.
- Uses explicit Claude Code transcript JSONL paths supplied by `--provider claude --path` or Claude Code hook input.
- Uses Langfuse OTLP ingestion at `/api/public/otel/v1/traces`.
- Supports Linux user services through `systemd --user`.
- Licensed under Apache-2.0.

Codex rollout JSONL and Claude Code transcript JSONL are not stable public APIs. Codex and Claude export are production-ready for workstation use after the local live checks in `TESTING.md` pass. Rerun `CHECK-001` after Claude Code upgrades that change transcript shape.

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

The exporter reads one Langfuse project key pair from `~/.codex/config.toml`. Use one of these two setups.

#### Option A: Local-only Langfuse

Use this when Langfuse only listens on your workstation, for example `http://localhost:3000`. Langfuse still expects a project public key and secret key for ingestion, but they do not need to protect a hosted service. For a fresh local self-hosted instance, add fixed local-only keys to the Langfuse service environment before first startup:

```dotenv
LANGFUSE_INIT_ORG_ID=local
LANGFUSE_INIT_ORG_NAME=Local
LANGFUSE_INIT_PROJECT_ID=codex-local
LANGFUSE_INIT_PROJECT_NAME=Codex-Local
LANGFUSE_INIT_PROJECT_PUBLIC_KEY=pk-lf-local-codex-tracer
LANGFUSE_INIT_PROJECT_SECRET_KEY=sk-lf-local-codex-tracer
```

Set the same local-only values in `~/.codex/config.toml`:

```toml
[mcp_servers.langfuse.env]
LANGFUSE_HOST = "http://localhost:3000"
LANGFUSE_PUBLIC_KEY = "pk-lf-local-codex-tracer"
LANGFUSE_SECRET_KEY = "sk-lf-local-codex-tracer"
```

Do not use these fixed values for a shared, public, or remotely reachable Langfuse instance. If your local Langfuse project already exists, create or copy a project API key in the local Langfuse UI instead of relying on first-start initialization.

If the local Langfuse UI should be reachable from another device on a private network such as Tailscale, generate unique project keys instead of using the fixed local-only defaults above. Set Langfuse's `NEXTAUTH_URL` to the URL users open in the browser, for example `http://100.x.y.z:3000`. Leaving `NEXTAUTH_URL` at `http://localhost:3000` can make sign-in redirect to the wrong machine.

For a private single-user local instance, create the initial UI user through Langfuse's init environment and disable future signups:

```dotenv
NEXTAUTH_URL=http://100.x.y.z:3000
LANGFUSE_INIT_USER_EMAIL=admin@local.langfuse
LANGFUSE_INIT_USER_NAME=Local Admin
LANGFUSE_INIT_USER_PASSWORD=<strong-local-password>
AUTH_DISABLE_SIGNUP=true
```

If your Langfuse Docker Compose file does not pass `AUTH_DISABLE_SIGNUP` through to the `langfuse-web` container, add it to that service's environment or to a local Compose override. `AUTH_DISABLE_SIGNUP=true` prevents new accounts; it does not remove users that already exist.

#### Option B: Hosted or shared Langfuse

Use this for Langfuse Cloud or any self-hosted Langfuse instance reachable by other machines. Create a real project API key pair in the target Langfuse project: open the project, go to **Project Settings -> API Keys**, create a new API key, and copy both the public key and secret key. Langfuse documents project API keys in [project settings](https://langfuse.com/faq/all/where-are-langfuse-api-keys) and self-hosted first-start key seeding through [`LANGFUSE_INIT_PROJECT_PUBLIC_KEY` and `LANGFUSE_INIT_PROJECT_SECRET_KEY`](https://langfuse.com/self-hosting/administration/headless-initialization).

For hosted self-hosted deployments that need deterministic first-start provisioning, generate unique keys instead of using the local-only defaults:

```sh
printf 'LANGFUSE_INIT_PROJECT_PUBLIC_KEY=pk-lf-%s\n' "$(openssl rand -hex 16)"
printf 'LANGFUSE_INIT_PROJECT_SECRET_KEY=sk-lf-%s\n' "$(openssl rand -hex 32)"
```

Then add the hosted credentials to `~/.codex/config.toml`:

```toml
[mcp_servers.langfuse.env]
LANGFUSE_HOST = "https://cloud.langfuse.com"
LANGFUSE_PUBLIC_KEY = "pk-lf-..."
LANGFUSE_SECRET_KEY = "sk-lf-..."
```

Host examples:

- Langfuse Cloud US: `https://us.cloud.langfuse.com`
- Langfuse Cloud EU/default: `https://cloud.langfuse.com`
- Local self-hosted Langfuse: `http://localhost:3000`

If your config already has a `[mcp_servers.langfuse]` block for Langfuse MCP, keep it. The exporter only reads the `env` values.

Optional: if you want Langfuse's built-in User column to show the Codex working directory instead of a real user id, add this setting to `[mcp_servers.langfuse.env]`:

```toml
LANGFUSE_USER_ID_MODE = "workspace"
```

In workspace mode, `/home/<linux-user>/...` under the current user's home directory is shown as `~/...`, and the current Git branch is appended when available. For example, `/home/alice/app` on branch `main` becomes `~/app (main)`. Leave `LANGFUSE_USER_ID_MODE` unset for normal Langfuse user behavior.

Protect the config file if it contains hosted or shared-instance API keys:

```sh
chmod 600 ~/.codex/config.toml
```

### 3. Install

```sh
./install.sh
```

The installer builds the Go binary, syncs Langfuse model pricing from the configured project, installs the user service, reloads systemd, enables the service, and restarts it. Langfuse must already be reachable at `LANGFUSE_HOST`, and the project key pair in `~/.codex/config.toml` must already authenticate to that Langfuse instance. If model pricing sync fails, the installer stops before installing the `codex-langfuse-watch.service` unit.

Useful preflight checks after setting equivalent shell variables:

```sh
curl -fsS "$LANGFUSE_HOST/api/public/health"
curl -fsS -u "$LANGFUSE_PUBLIC_KEY:$LANGFUSE_SECRET_KEY" "$LANGFUSE_HOST/api/public/models?page=1&limit=1" >/dev/null
```

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

Claude Code support can be checked with an explicit sanitized transcript path:

```sh
~/.codex/bin/codex-langfuse-exporter --provider claude --path <transcript.jsonl>
```

Claude Code hooks can call the same binary in hook mode. The hook queues work only; the existing watch service drains the queued transcript and performs the Langfuse export.

```json
{
  "hooks": {
    "Stop": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "~/.codex/bin/codex-langfuse-exporter --claude-hook --quiet"
          }
        ]
      }
    ]
  }
}
```

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

Claude Code support uses one automatic trigger path:

1. Claude Code sends a Stop hook payload containing `session_id`, `transcript_path`, `cwd`, and `hook_event_name`.
2. `codex-langfuse-exporter --claude-hook` validates the hook JSON and appends one queue request to `~/.codex/langfuse-export-state.json`.
3. `codex-langfuse-watch.service` drains the queue, parses the explicit transcript path, exports completed Claude turns, and records processed trace IDs in the same state file.

There is no Claude directory discovery in this release. The exporter does not edit Claude settings; add the hook command yourself when you want automatic Claude exports.

Native Codex OTEL is intentionally not part of this setup. It emits low-level runtime spans such as streaming, socket, dispatch, and server internals that are not useful for reviewing prompts, answers, terminal output, tool calls, token usage, or file changes.

If your `~/.codex/config.toml` has a native `[otel]` section and Langfuse shows noisy spans such as `handle_responses`, `receiving`, `socket reader`, or `serve_inner`, remove that section so this exporter is the single tracing path.

## What Appears In Langfuse

The exporter sends these observations when Codex records the data locally:

- `codex.agent`: root agent observation with trace-table input and output.
- `codex.transcript`: generation observation with the user prompt, final assistant answer, model name, and token usage.
- `codex.terminal`: ordered visible CLI event stream for the turn.
- `codex.message.commentary`: assistant progress updates shown in the CLI.
- `codex.reasoning.summary`: visible reasoning summaries when Codex records a non-empty summary.
- `codex.tool.command`: shell command input and terminal output.
- `codex.tool.file_change`: patch input, changed files, and unified diffs.
- `codex.tool.mcp`: MCP invocation and result.
- `codex.tool.web_search`: web search query/action metadata.
- `codex.tool.tool_search`: deferred-tool discovery calls and results.

Tool observations use Langfuse's `tool` observation type. The transcript uses `generation`.

Claude Code support emits:

- trace name: `claude.turn.transcript`
- `claude.agent`: root agent observation with trace-table input and output.
- `claude.transcript`: generation observation with the prompt, final answer, model name, and token usage when Claude records it.
- `claude.terminal`: ordered visible transcript stream for the turn.
- `claude.tool.command`: Bash tool input, output, status, and command metadata.
- `claude.tool.file_change`: file-writing tool metadata when Claude records structured path fields.
- `claude.tool.mcp`: MCP invocation and result when Claude records structured MCP tool names.
- `claude.tool.generic`: bounded metadata and redacted input/output for other Claude tools.

Claude thinking blocks are omitted, including redacted or encrypted thinking-like blocks. Visible assistant text, final answers, tool input, tool output, terminal stream, and metadata strings use the shared redaction and truncation path.

`<provider>.tool.file_change` metadata includes:

- `changed_files`
- `added_files`
- `modified_files`
- `deleted_files`
- `moved_files`
- `file_change_types`
- `changed_file_count`

The root trace carries compact provider insight metadata for table scanning. Codex writes `codex_insight`; Claude writes `claude_insight`.

- `tool_count`
- `command_count`
- `failed_command_count`
- `file_change_tool_count`
- `changed_file_count`
- `changed_extensions`
- `touched_test_files`
- `verification_command_count`
- `verification_status`
- `last_verification_command`
- `last_verification_status`
- `navigation`
- `<kind>_command_count` for each command kind
- `<tool_family>_tool_count` for each tool family

`verification_status` is one of `not_applicable`, `not_run`, `passed`, or `failed`. Full `changed_files` stays on `<provider>.tool.file_change`; root metadata only carries compact file-impact summaries.

Workspace metadata includes `cwd` and, when `cwd` is inside an attached Git worktree, `git_branch`. The branch is resolved from the working directory at export time and is omitted for non-Git directories or detached HEAD checkouts. If `LANGFUSE_USER_ID_MODE = "workspace"` is set, the exporter also sets `langfuse.user.id` to the normalized cwd plus branch, such as `~/app (main)`, so the Langfuse User column can be used as a workspace column.

Navigation metadata is always-on. A read-only trace means `navigation contains files:read_only`, which only means no observed local file changes in the exported turn. It does not mean no network activity, no install command, or no external API call. Counts remain the metric representation. `<provider>_insight.navigation`, for example `codex_insight.navigation` or `claude_insight.navigation`, is the canonical low-cardinality navigation field that trace tags project into Langfuse's tag UI.

Cost tracking uses Langfuse's model and usage handling. The exporter sends `langfuse.observation.model.name` and `langfuse.observation.usage_details` on provider transcript observations; it does not multiply tokens locally or emit `cost_details`. Configure pricing in Langfuse model definitions so Langfuse calculates input, output, total, and cost columns from the canonical model and usage values.

Langfuse calculates cost. The built-in pricing sync creates source-backed model definitions for supported Codex/OpenAI models and current Claude models: Opus 4.7, Sonnet 4.6, and Haiku 4.5. Claude Code subscription billing is separate from Anthropic API token pricing; these definitions are for Langfuse trace cost columns when Claude records compatible model and usage details.

`install.sh` runs `~/.codex/bin/codex-langfuse-exporter --sync-model-pricing --quiet` before restarting `codex-langfuse-watch.service`. The same setup can be run directly:

```sh
~/.codex/bin/codex-langfuse-exporter --sync-model-pricing
```

The built-in pricing catalog is source-dated from https://openai.com/api/pricing/ on 2026-05-02 and covers `gpt-5.5`, `gpt-5.4`, and `gpt-5.4-mini`. It also covers `gpt-5.3-codex-spark` using the official `gpt-5.3-codex` pricing from https://developers.openai.com/api/docs/models/gpt-5.3-codex on 2026-05-02. It covers `claude-opus-4-7`, `claude-sonnet-4-6`, and `claude-haiku-4-5-20251001` using Claude Opus 4.7, Claude Sonnet 4.6, and Claude Haiku 4.5 pricing from https://platform.claude.com/docs/en/about-claude/pricing on 2026-05-05. OpenAI/Codex usage keys are `input`, `input_cached_tokens`, `output`, `output_reasoning_tokens`, and `total`; Claude usage keys are `input`, `cache_creation_input_tokens`, `cache_read_input_tokens`, `output`, and `total`. Child token buckets are subtracted from parent input/output buckets to avoid double counting. For Claude Code traces, a small `input` count can be correct when most prompt context is reported in `cache_creation_input_tokens` and `cache_read_input_tokens`.

When provider pricing changes or a supported coding agent emits a new model name, update `internal/langfuse/models.go` and its catalog tests in the same change. Do not add fallback local cost multiplication.

Langfuse calculates cost during ingestion. Existing rows are not backfilled automatically; use an explicit re-export for old sessions after model pricing is synced:

```sh
~/.codex/bin/codex-langfuse-exporter --session-id <session-id> --no-verify
```

`<provider>.tool.command` metadata includes:

- `command_kind`
- `status`
- `exit_code`
- `duration_ms`
- `failure_type`

`command_kind` uses a fixed enum: `test`, `build`, `lint`, `format`, `git`, `read`, `search`, `install`, `systemd`, `network`, or `other`.

`<provider>.tool.mcp` metadata includes:

- `mcp_server`
- `mcp_tool`

MCP metadata is derived only from observed structured MCP events. Configured but unused MCP servers are not exported as usage. Exact MCP tools such as `issues/list` stay in observation metadata and are not trace tags.

Trace tags are emitted through `langfuse.trace.tags`. The built-in tag contract is the active provider's insight navigation values plus observed `mcp:<server>` values. That means every navigation value, including `files:changed`, `command:other`, `tool:command`, `tool:file_change`, `tool:mcp`, and `verification:not_run`, is available as a trace tag, and an observed MCP server such as GitHub also adds `mcp:github`. Tags are sorted, unique, lowercase, and never contain exact MCP tool names, prompts, outputs, cwd, file paths, session IDs, or trace IDs.

Source-level custom tags can be added as compiled Go rules in `internal/agenttrace/tagrules.go`; see `internal/agenttrace/TAG_RULES.md` for examples such as emitting a fixed tag when the user's request contains a known string. Custom tags are code changes, not runtime configuration.

## Filtering

Use trace tags for turn-level navigation and observation filters for individual tool calls.

Trace tags are the primary reusable trace filters:

- `files:changed`
- `files:read_only`
- `command:<kind>` for each observed `command_kind`
- `tool:<family>` for each observed tool family
- `verification:<status>`
- `mcp:<server>` for each observed MCP server

Common tag filters include `command:search`, `command:read`, `command:network`, `command:install`, `tool:web_search`, and `verification:failed`.

Use `mcp_server` and `mcp_tool` on `<provider>.tool.mcp` observations when you need exact MCP call details.

Observation filters use observation metadata:

- `Observations: command search`: `Name equals codex.tool.command` and `Metadata command_kind equals search`
- `Observations: command read`: `Name equals codex.tool.command` and `Metadata command_kind equals read`
- `Observations: command network`: `Name equals codex.tool.command` and `Metadata command_kind equals network`
- `Observations: command install`: `Name equals codex.tool.command` and `Metadata command_kind equals install`
- `Observations: failed commands`: `Name equals codex.tool.command` and `Metadata failure_type equals nonzero_exit`
- `Observations: file changes`: `Name equals codex.tool.file_change`
- `Observations: web search`: `Name equals codex.tool.web_search`

After `install.sh` restarts `codex-langfuse-watch.service`, future watcher exports include these tags and MCP metadata automatically. Existing Langfuse rows are not automatically backfilled; use an explicit re-export command when old rows need the new fields.

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

Explicit re-export for backfill uses the same command shape:

```sh
~/.codex/bin/codex-langfuse-exporter --path <rollout.jsonl> --no-verify
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
- local cost calculations or `cost_details`

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
- `./install.sh` fails with `connect: connection refused`: Langfuse is not running at `LANGFUSE_HOST`, or the host URL points at the wrong machine or port.
- `./install.sh` fails with `Langfuse model list /api/public/models failed with HTTP 401`: the configured public/secret key pair is not valid for the Langfuse instance at `LANGFUSE_HOST`. Seed the same `LANGFUSE_INIT_PROJECT_PUBLIC_KEY` and `LANGFUSE_INIT_PROJECT_SECRET_KEY` before first startup, or create/copy a project key pair from the Langfuse UI and update `~/.codex/config.toml`.
- `systemctl --user status codex-langfuse-watch.service` says the unit is not found after `./install.sh`: the installer likely failed before the service install step. Fix the Langfuse reachability or authentication error and rerun `./install.sh`.
- Browser sign-in redirects to `localhost`: the Langfuse server's `NEXTAUTH_URL` is still set to `http://localhost:3000`. Set it to the actual browser URL, such as a Tailscale URL, and recreate the `langfuse-web` container.
- A browser on Windows cannot reach a Tailscale IP that works from WSL: Tailscale may be running only inside WSL. Run Tailscale on the Windows host too, or open the browser inside the same WSL network environment.
- Native Codex OTEL still enabled, causing noisy duplicate traces.
- Claude Code transcript exists but no trace appears because the `Stop` hook is not installed in Claude settings. Use `~/.codex/bin/codex-langfuse-exporter --provider claude --path <transcript.jsonl>` for explicit backfill, or add the documented `--claude-hook --quiet` command to Claude's `Stop` hook for future automatic exports.
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
testdata/sources/<provider>/*.jsonl
testdata/golden/*.normalized.json
```

Keep that as the single behavioral contract. Do not add a second fixture registry.

### Adding A Coding Agent

This exporter is provider-neutral after parsing. Codex, Claude Code, and future coding agents such as Gemini CLI, OpenCode, Goose, or another local agent must converge on the same internal contract:

```text
source transcript/log -> internal/<provider>trace -> agenttrace.Turn -> tracecontract.Trace -> langfuse.EmitTurn
```

Add a new coding agent by extending the existing path:

1. Add a parser package named `internal/<provider>trace` that reads that agent's local transcript or log format and returns `[]agenttrace.Turn`.
2. Keep provider-specific logic in that parser only. Shared redaction, terminal assembly, token usage, trace IDs, insight rollups, trace tags, and Langfuse projection stay in `internal/agenttrace`, `internal/tracecontract`, and `internal/langfuse`.
3. Add the provider constant in `internal/agenttrace/model.go` and profile names in `internal/agenttrace/profile.go`, including trace name, observation names, metadata prefix, and insight metadata key.
4. Register the parser once in `internal/providers/providers.go`. `cmd/codex-langfuse-exporter`, `internal/watch`, and contract tests must call the provider registry instead of importing the provider parser directly.
5. Add sanitized fixtures under `testdata/sources/<provider>/*.jsonl`, add entries to `testdata/manifest.json`, and add normalized expectations under `testdata/golden`.
6. Run `go test ./test -run TestGoldenTraceContract -count=1` so the new provider is held to the same normalized Langfuse contract as Codex and Claude.
7. If the provider has a stable completion hook, add a small hook package that validates the hook payload and enqueues `exportstate.QueueRequest`. The hook process must not load Langfuse config or export directly. `--watch` remains the only automatic exporter.
8. If the provider does not have a stable hook or session discovery contract, support explicit path export first: `codex-langfuse-exporter --provider <provider> --path <transcript>`.

Do not add provider wrapper execution, a second fixture manifest, a second Langfuse projection, direct hook export, transcript directory polling, compatibility shims, or placeholder providers without real fixtures. New agents should make the shared tests broader, not create parallel Codex-shaped and provider-shaped test suites.

## Remove

```sh
./uninstall.sh
```

If Langfuse MCP was added only for this setup, remove the optional `[mcp_servers.langfuse]` block from `~/.codex/config.toml`.

## Social Summary

Codex Langfuse Tracer is a small Go exporter that watches local Codex CLI rollout files and turns completed coding-agent turns into clean Langfuse traces: prompts, final answers, terminal activity, shell commands, patch diffs, token usage, and verification metadata. Install once per workstation; Codex keeps running normally.
