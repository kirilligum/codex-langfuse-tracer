# Trace Navigation Facets Plan

## 1. Purpose
- Project: Codex Langfuse Tracer
- Date: 2026-05-01
- Scope: Add deterministic, human-readable trace metadata facets that make common Codex CLI runs easy to find in Langfuse trace filters, while preserving detailed per-event metadata on observations.
- Non-goal: Do not add new observation types, large trace metadata, raw command output rollups, file path fanout, or UI-specific config.

## 2. Problem
Langfuse observation filters can find detailed events such as:

- `codex.tool.exec_command` with `command_kind=search`
- `codex.tool.exec_command` with `command_kind=read`
- `codex.tool.apply_patch` with changed file metadata

Trace filters are better for navigation, but trace-level metadata currently lacks direct facets for common operator questions:

- Which turns changed files?
- Which turns were read-only?
- Which turns ran search/read/git/test/build/install/network commands?
- Which turns used Codex web search or apply patch?

The solution is a compact trace-level navigation layer derived from existing observations.

## 3. Design Principles
- Use names that are clear to someone who knows Codex CLI but has not read this repo.
- Prefer observed facts over risk labels.
- Keep detailed facts on observations.
- Promote only bounded booleans, counts, and small enums/arrays to trace metadata.
- Keep metadata always-on and deterministic.
- Avoid vague names such as `may_modify_workspace`, `safe`, `unsafe`, or `potentially_mutating`.

## 4. Naming Decisions
Use `codex_insight.<field>` as the Langfuse trace metadata namespace, consistent with the existing insight metadata.

Recommended trace fields:

| Field | Type | Meaning |
|---|---:|---|
| `has_file_changes` | bool | The exported turn includes observed local file changes. |
| `is_read_only` | bool | The exported turn has no observed local file changes. |
| `used_apply_patch` | bool | The turn used the Codex patch tool. |
| `used_web_search` | bool | The turn used the Codex web search tool. |
| `used_mcp_tool` | bool | The turn used an MCP tool observation. |
| `used_tool_search` | bool | The turn used deferred tool discovery. |
| `ran_read_command` | bool | At least one shell command classified as `read` ran. |
| `read_command_count` | int | Number of shell commands classified as `read`. |
| `ran_search_command` | bool | At least one shell command classified as `search` ran. |
| `search_command_count` | int | Number of shell commands classified as `search`. |
| `ran_git_command` | bool | At least one shell command classified as `git` ran. |
| `git_command_count` | int | Number of shell commands classified as `git`. |
| `ran_test_command` | bool | At least one shell command classified as `test` ran. |
| `test_command_count` | int | Number of shell commands classified as `test`. |
| `ran_build_command` | bool | At least one shell command classified as `build` ran. |
| `build_command_count` | int | Number of shell commands classified as `build`. |
| `ran_lint_command` | bool | At least one shell command classified as `lint` ran. |
| `lint_command_count` | int | Number of shell commands classified as `lint`. |
| `ran_format_command` | bool | At least one shell command classified as `format` ran. |
| `format_command_count` | int | Number of shell commands classified as `format`. |
| `ran_install_command` | bool | At least one shell command classified as `install` ran. |
| `install_command_count` | int | Number of shell commands classified as `install`. |
| `ran_systemd_command` | bool | At least one shell command classified as `systemd` ran. |
| `systemd_command_count` | int | Number of shell commands classified as `systemd`. |
| `ran_network_command` | bool | At least one shell command classified as `network` ran. |
| `network_command_count` | int | Number of shell commands classified as `network`. |
| `ran_other_command` | bool | At least one shell command classified as `other` ran. |
| `other_command_count` | int | Number of shell commands classified as `other`. |
| `command_kinds` | string array | Sorted command kinds present in the turn. |
| `tool_names` | string array | Sorted tool observation families present in the turn. |

Keep existing fields:

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

## 5. Definitions
`has_file_changes`:

```text
changed_file_count > 0 OR patch_count > 0
```

`is_read_only`:

```text
has_file_changes == false
```

This intentionally means "no observed local file changes in the exported turn." It does not mean no network calls, no installs, or no commands that could have changed files outside the structured patch path.

`ran_*_command`:

```text
<kind>_command_count > 0
```

`used_*` tool facets:

```text
true when a matching tool observation family appears in the turn.
```

## 6. Observation Metadata
Keep detailed metadata where it already belongs:

- `codex.tool.exec_command.metadata.command_kind`
- `codex.tool.exec_command.metadata.status`
- `codex.tool.exec_command.metadata.exit_code`
- `codex.tool.exec_command.metadata.duration_ms`
- `codex.tool.exec_command.metadata.failure_type`
- `codex.tool.apply_patch.metadata.changed_files`
- `codex.tool.apply_patch.metadata.changed_file_count`
- `codex.tool.apply_patch.metadata.file_change_types`

Do not duplicate raw command text, command output, changed file lists, or diffs in trace metadata.

## 7. Langfuse Usage
Trace filters should cover navigation:

```text
Metadata codex_insight.is_read_only equals true
Metadata codex_insight.has_file_changes equals true
Metadata codex_insight.ran_search_command equals true
Metadata codex_insight.ran_read_command equals true
Metadata codex_insight.ran_test_command equals true
Metadata codex_insight.used_web_search equals true
```

Observation filters should cover drill-down:

```text
Name equals codex.tool.exec_command
Metadata command_kind equals search
```

Recommended saved views:

- `Traces: read only`
- `Traces: changed files`
- `Traces: search commands`
- `Traces: failed verification`
- `Observations: exec commands`
- `Observations: command search`
- `Observations: command read`
- `Observations: failed commands`
- `Observations: apply patches`

## 8. Implementation Plan
1. Extend `InsightRollup` in `internal/codextrace/insight.go`.
   - Add command kind counters.
   - Add tool family counters or booleans.
   - Add `HasFileChanges` and `IsReadOnly`.

2. Centralize facet naming.
   - Add helper methods so `Metadata()` emits stable names.
   - Avoid repeated string construction in tests or export code.

3. Update rollup computation.
   - Count command kinds while processing `codex.tool.exec_command`.
   - Count tool families while processing observations with `Type == "tool"`.
   - Derive `has_file_changes` after `patch_count` and `changed_file_count` are known.

4. Update contract and golden fixtures.
   - Add trace metadata fields to normalized golden files.
   - Keep observation metadata unchanged except where tests need coverage.

5. Update Langfuse projection tests.
   - Assert trace/agent metadata contains selected new fields.
   - Assert child observations do not repeat trace-level facets.

6. Update docs.
   - Document trace filters vs observation filters.
   - Document `is_read_only` as "no observed local file changes."
   - Document saved views as the UI solution for repeated metadata filters.

7. Validate locally.
   - Run focused `go test ./internal/codextrace ./internal/langfuse ./test`.
   - Run full `go test ./...`.
   - Run `git diff --check`.

8. Validate in Langfuse.
   - Export or wait for a turn containing `rg`, `sed`, and no patch.
   - Confirm trace metadata includes `is_read_only=true`, `ran_search_command=true`, and `ran_read_command=true`.
   - Export or use a turn with `apply_patch`.
   - Confirm trace metadata includes `has_file_changes=true` and `used_apply_patch=true`.

## 9. Acceptance Criteria
- Trace metadata supports filtering for changed-file turns without opening observations.
- Trace metadata supports filtering for read-only turns without opening observations.
- Trace metadata supports filtering for command families such as search, read, git, test, build, install, network, and format.
- Observation metadata still supports detailed filtering by `command_kind`.
- No raw command output, diffs, hidden reasoning, or full changed file lists are added to trace metadata.
- Existing trace and observation names remain unchanged.
- Existing verification metadata behavior remains unchanged.
- All Go tests pass.

## 10. Open Questions
- Should `tool_names` use full observation names like `codex.tool.exec_command` or short names like `exec_command`? Recommendation: short names for trace metadata, full names stay visible in observations.
- Should `used_mcp_tool` be split by server or tool name? Recommendation: no for v1 because it can become high-cardinality.
- Should `ran_other_command` be exposed in saved views? Recommendation: expose metadata but do not create a default saved view for it.
