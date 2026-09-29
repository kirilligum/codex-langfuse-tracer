# Codex and Claude Code Tracer for Laminar

This Go service exports completed Codex CLI and Claude Code turns as OpenTelemetry spans through the authenticated, host-local Laminar receiver. It watches Codex rollout files, drains Claude Stop-hook requests, and sends one OTLP batch per completed turn.

The repository, executable, systemd unit, and state filenames retain their historical `codex-langfuse-*` names so existing installs and hooks keep working. The exporter now targets Laminar only.

## Status

- Supports Codex CLI rollout JSONL and explicit Claude Code transcript JSONL exports.
- Supports one Linux `systemd --user` watcher for Codex polling and queued Claude exports.
- Uses the transcript-specific local Laminar OTLP/HTTP receiver at
  `127.0.0.1:14320` by default.
- Sends only completed turns with non-empty canonical input and output.
- Requires the official host-local Laminar receiver and persistent Collector to be installed and running separately.

Codex and Claude transcript formats are not stable public APIs. Run the focused parser and local receiver tests after upgrading either client.

## Quick start

Clone and build with Go 1.26 or a compatible release:

```sh
git clone https://github.com/kirilligum/codex-langfuse-tracer.git
cd codex-langfuse-tracer
```

Install the official Laminar Codex/CLI forwarder first. It owns both local
authenticated OTLP receivers and the persistent queues. `make provision`
creates separate projects and receiver credentials for the official plugin and
this transcript exporter. The plugin keeps
`~/.config/lmnr/codex-plugin.json` on port 14318; this exporter reads
`~/.config/lmnr/codex-tracer.json` on port 14320. It does not read either
forwarder's backend API key or send traces directly to the Laminar service.

The transcript config must contain `projectApiKey` and `baseUrl`, where
`projectApiKey` is the local receiver token and `baseUrl` is a loopback HTTP
URL. The default is `http://127.0.0.1:14320`. Keep the file private (mode
`0600`) and its `lmnr` directory private (mode `0700`). If `XDG_CONFIG_HOME` is
set, it is read from `$XDG_CONFIG_HOME/lmnr/codex-tracer.json`.

Install the watcher:

```sh
./install.sh
```

For a new forwarder, run `make provision` and `make up` in
`cli-llm-laminar-trace` first. When adding the transcript route to an existing
forwarder, verify the original queue is empty before `make up`; the managed
update recreates the Collector once and preserves its mounted queue directory.

The executable and user service keep their legacy `codex-langfuse-*` names so
existing installations can upgrade in place; the watcher now sends traces only
through the dedicated Laminar project.

The installer builds a staged executable and checks the authenticated local receiver with an empty OTLP request before it changes the installed binary or stops an existing watcher. It preserves the version 3 export-state file and its `.lock` sidecar, then installs/enables/restarts `codex-langfuse-watch.service`.

Check local health:

```sh
~/.codex/bin/codex-langfuse-exporter --doctor
systemctl --user status codex-langfuse-watch.service
```

A successful receiver check proves that the local endpoint accepted the authenticated request. It does not prove that the Collector has delivered a span to the Laminar backend. Confirm an actual trace in the Laminar project after a new completed turn.

## How exports work

`codex-langfuse-watch.service` runs `~/.codex/bin/codex-langfuse-exporter --watch`. The watcher scans `~/.codex/sessions/` for eligible Codex rollout files, retains only bounded parsed-file metadata in memory, and processes completed turns. Claude Code's Stop hook queues the transcript path and does not export it directly; the watcher performs the export.

Each turn becomes one parent `PIPELINE` span, one `LLM` transcript span, and child observation spans. The spans share a stable trace ID and deterministic span IDs. The exporter sends canonical redacted input/output, tool activity, model and token usage, workspace identity, tags, and provider insight metadata through OTLP/HTTP. Laminar attributes use `lmnr.span.type`, `lmnr.span.input`, `lmnr.span.output`, `lmnr.association.properties.*`, and OpenTelemetry `gen_ai.*` conventions, including `gen_ai.usage.*` token fields.

Codex span names include `codex.agent`, `codex.transcript`, `codex.tool.command`, `codex.tool.file_change`, `codex.tool.mcp`, `codex.tool.web_search`, and `codex.tool.tool_search` when those events are present. The transcript span carries model and token usage. Tool spans preserve inputs, outputs, command status, and file-change metadata after shared redaction and truncation.

Incomplete turns remain local until the client records completion. The watcher does not stream tokens or partial assistant text and does not export partial tool observations.

Deterministic turn summaries (`verification_run`, `verification_passed`, `had_failed_command`, `had_file_changes`, `changed_tests`, `docs_only`, `changed_file_count`, and `outcome`) are carried as typed span metadata with a data type and explanation. They are not submitted as native Laminar evaluator score records. They do not make additional model calls or calculate cost locally.

The repository maps the turn's export-time Git worktree and branch into Laminar environment metadata. The Linux runtime hostname is sent as the Laminar user association. The branch, repository, and hostname are identity metadata; they are not configurable through command-line flags.

Workspace environments use the repository folder and export-time branch, normalized to lowercase safe characters and capped at 40 characters with the first six lowercase hexadecimal SHA-256 characters as a suffix. A detached Git checkout uses `detached`; a missing, non-Git, unreadable, or timed-out workspace uses `default`. The user association is the non-empty Linux runtime hostname. Hidden or encrypted reasoning blocks are omitted.

Root metadata includes deterministic turn summaries and compact provider insight fields such as `verification_status`, `verification_command_count`, `changed_file_count`, `changed_extensions`, `touched_test_files`, `command_kind`, `duration_ms`, and `failure_type`. Navigation is emitted as `codex_insight.navigation` or `claude_insight.navigation`; reusable low-cardinality tags such as `files:changed`, `verification:not_run`, `tool:command`, `tool:file_change`, `tool:web_search`, and `mcp:<server>` use `lmnr.association.properties.tags`. The exact MCP tool names, prompt text, output text, paths, and session IDs are not used as tags.

## Delivery and state

The version 3 state file is `~/.codex/langfuse-export-state.json`; its advisory lock sidecar is `~/.codex/langfuse-export-state.json.lock`. Keep both in place during upgrades. The state file stores `processed_trace_ids`, queued Claude hook requests in `queue`, `scan_watermark_ns`, and a legacy `pending_scores[trace_id]` map. During an upgrade from the older `O_EXCL` lock protocol, pause new Claude Stop-hook invocations and any other independent state writers until the installer has stopped the watcher and promoted the new binary. Never delete or rename the `.lock` sidecar during install, restart, or uninstall.

Older version 3 installations may have `pending_scores` entries from the previous Langfuse exporter. On upgrade, the watcher re-exports that turn as one full Laminar trace using its persisted environment. It clears the legacy entry only after the local Collector accepts the batch and the processed checkpoint is saved. Do not edit or reset the state file to force a retry.

Delivery is at-least-once. A timeout after Collector acceptance, or process termination between acceptance and local checkpoint persistence, can cause a repeated OTLP submission with the same span identities. The exporter does not claim exactly-once delivery or prove backend visibility from a local receiver acknowledgement. The Laminar Collector's persistent queue and backend metrics govern downstream retry and delivery.

An incomplete discovery, unreadable source, source change during a scan, or failed export keeps the scan watermark in place. Healthy turns in the same scan can still be checkpointed. The watcher prints an `ERROR: watch_scan_incomplete ... watermark_advanced=false` diagnostic even in quiet mode. The doctor reports a queued hook request, a pending legacy checkpoint, an unavailable receiver/Collector endpoint, an inactive watcher, or recent watcher errors as unhealthy.

`span_export_succeeded ... checkpoint=pending` means the local receiver accepted a batch and the processed checkpoint is not yet durable. `span_checkpoint_unconfirmed` means checkpoint persistence failed after receiver acceptance, so replay is possible. These local diagnostics do not prove the backend has indexed the trace.

Discovery uses visible paths and modification time. A source file restored after its mtime falls behind an advanced watermark is outside automatic recovery. A rewrite that preserves file identity, size, and mtime is also not detectable. Keep source transcripts intact and use the normal watcher recovery path; do not delete state to clear a warning.

## Manual export

Manual exports are explicit sends. A manual export does not read or update watcher checkpoints, so it can duplicate a turn already queued or processed by the watcher. Only export a transcript when you have established that no automatic path will send it.

```sh
~/.codex/bin/codex-langfuse-exporter --latest
~/.codex/bin/codex-langfuse-exporter --session-id <SESSION_ID>
~/.codex/bin/codex-langfuse-exporter --path ~/.codex/sessions/YYYY/MM/DD/rollout-....jsonl
~/.codex/bin/codex-langfuse-exporter --provider claude --path <transcript.jsonl>
```

Use `--turn-id <TURN_ID>` to select one turn from a manually selected source. The CLI prints `collector_accepted` after the local receiver accepts an OTLP batch; that status does not prove the backend has indexed the trace. `--json` emits the provider, source path, turn ID, trace ID, and local Collector status.

## Claude Code hook

Configure the hook in Claude Code yourself; the installer does not edit Claude settings. The hook queues work and the systemd watcher sends it:

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

Manual Claude export is separate from the hook queue. Do not manually export a transcript that the hook has queued.

Claude spans use `claude.agent`, `claude.transcript`, `claude.tool.command`, `claude.tool.file_change`, `claude.tool.mcp`, and `claude.tool.generic` names when those observations exist. The exporter does not run Claude or modify its settings.

A separately configured Langfuse MCP connection is not used by this exporter. If another workflow still uses it, keep its configuration independent. The example pins `langfuse-mcp==0.10.0` with `mcp>=1.28,<2`; an unconstrained install can resolve the incompatible MCP SDK v2 and close during `initialize` rather than producing the tested MCP SDK v1 startup.

## Privacy

The exporter can send prompt text, assistant text, tool inputs, command output, diffs, paths, and metadata to the configured Laminar project. Shared redaction and truncation run before export, but that does not guarantee every sensitive value is removed. Do not enable it for sessions containing secrets or regulated data unless sending those records is approved. Never paste receiver or backend credentials into chat, issue comments, logs, or source control.

## Troubleshooting

- `--doctor` reports receiver authentication failure: confirm the transcript
  forwarder route is running and `~/.config/lmnr/codex-tracer.json` contains its
  current receiver `projectApiKey` and loopback `baseUrl`; do not use either
  backend API key.
- The installer fails before service promotion: fix the local receiver/configuration problem and rerun `./install.sh`. The old binary and unit remain in place on a preflight failure.
- The receiver accepts a manual trace but no trace appears in Laminar: inspect the forwarder service, persistent Collector queue, Collector delivery metrics, and backend availability. A local HTTP success is only the queue acceptance boundary.
- `watch_scan_incomplete` appears: inspect the bounded error counts and watcher journal, then let the eligible source retry. Do not remove the source, state file, or lock sidecar.
- The doctor reports `state_legacy_pending_traces`: leave the watcher active so it can re-export and checkpoint the affected traces. A source missing from disk or outside the scan watermark requires separate recovery.
- The unit is missing or inactive: check the installer output and rerun it after the receiver preflight succeeds.

## Development

Run the local suite and focused integration checks:

```sh
go test ./... -count=1
go test -race ./internal/exportstate ./internal/claudehook ./internal/watch ./cmd/codex-langfuse-exporter -count=1
```

See [TESTING.md](TESTING.md) for the watcher resource gate, delivery replay tests, local receiver integration tests, and production acceptance boundaries.

The normalized provider contract uses one fixture inventory:

```text
testdata/manifest.json
testdata/sources/<provider>/*.jsonl
testdata/golden/*.normalized.json
```

To add a provider, implement its transcript parser under `internal/<provider>trace`, return `agenttrace.Turn`, register it in `internal/providers/providers.go`, and extend the existing fixtures and Laminar span contract. Keep provider-specific parsing separate from shared redaction, trace identity, tags, insight summaries, and Laminar projection. Hooks should enqueue state only; the watcher remains the single automatic export path.

Codex and Claude tool families use stable semantic names such as `codex.tool.command`, `codex.tool.file_change`, `codex.tool.mcp`, `codex.tool.web_search`, `codex.tool.tool_search`, and the corresponding `claude.tool.*` names. Tool metadata includes observed command kind, status, exit code, duration, and failure type where available.

## Remove

```sh
./uninstall.sh
```

Uninstall removes the state JSON but leaves its empty `.lock` sidecar. This preserves the lock inode for processes that may still be using the state path.
