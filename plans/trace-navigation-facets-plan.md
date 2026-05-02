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

Required operator views:

- `Traces: read only`
- `Traces: changed files`
- `Traces: search commands`
- `Traces: read commands`
- `Traces: network commands`
- `Traces: install commands`
- `Traces: tests run`
- `Traces: failed verification`
- `Traces: web search used`
- `Traces: patch tool used`
- `Observations: exec commands`
- `Observations: command search`
- `Observations: command read`
- `Observations: command network`
- `Observations: command install`
- `Observations: command test`
- `Observations: failed commands`
- `Observations: apply patches`
- `Observations: web search`

Optional full-coverage views:

- `Traces: git commands`
- `Traces: build commands`
- `Traces: lint commands`
- `Traces: format commands`
- `Traces: systemd commands`
- `Traces: other commands`
- `Observations: command git`
- `Observations: command build`
- `Observations: command lint`
- `Observations: command format`
- `Observations: command systemd`
- `Observations: command other`

In the local Langfuse UI, saved views are created through:

```text
Views -> Create Custom View
```

The dialog saves the current column arrangement, active filters, and sort order. This is the preferred solution for avoiding repeated manual entry of custom metadata filters.

## 8. Detailed Implementation Plan

### Phase 0: Preflight and Baseline
Objective: confirm current behavior and avoid mixing unrelated changes into the implementation.

Actions:

1. Check working tree.
   ```bash
   git status --short
   ```

2. Run current focused tests before editing.
   ```bash
   go test ./internal/codextrace ./internal/langfuse ./internal/tracecontract ./test
   ```

3. Inspect current insight and export paths.
   - `internal/codextrace/insight.go`
   - `internal/langfuse/export.go`
   - `internal/tracecontract/contract.go`
   - `testdata/golden/*.normalized.json`

Exit criteria:

- Baseline focused tests pass or failures are documented before implementation.
- Unrelated dirty files are identified and left untouched.

### Phase 1: Add Rollup Data Model
Objective: extend `InsightRollup` with reusable facet state while keeping one implementation path.

Files:

- `internal/codextrace/insight.go`

Implementation:

1. Add fields:
   ```go
   CommandKindCounts map[string]int
   ToolNameCounts    map[string]int
   CommandKinds      []string
   ToolNames         []string
   HasFileChanges    bool
   IsReadOnly        bool
   ```

2. Keep existing fields unchanged:
   ```go
   ToolCount
   CommandCount
   FailedCommandCount
   PatchCount
   ChangedFileCount
   VerificationCommandCount
   VerificationStatus
   LastVerificationCommand
   LastVerificationStatus
   ChangedExtensions
   TouchedTestFiles
   ```

3. Add fixed helper lists for command kinds:
   ```go
   []string{
       CommandKindTest,
       CommandKindBuild,
       CommandKindLint,
       CommandKindFormat,
       CommandKindGit,
       CommandKindRead,
       CommandKindSearch,
       CommandKindInstall,
       CommandKindSystemd,
       CommandKindNetwork,
       CommandKindOther,
   }
   ```

4. Add tool-family normalization:
   ```text
   codex.tool.exec_command -> exec_command
   codex.tool.apply_patch -> apply_patch
   codex.tool.web_search -> web_search
   codex.tool.tool_search -> tool_search
   codex.tool.mcp -> mcp
   other codex.tool.<name> -> <name>
   ```

5. Add small helpers:
   ```go
   incrementCounter(map[string]int, string)
   sortedCounterKeys(map[string]int) []string
   observationToolName(name string) string
   ```

Exit criteria:

- New fields are populated inside `BuildInsightRollup`.
- Existing metadata output remains stable until Phase 2 explicitly emits new fields.

### Phase 2: Emit Trace Facets
Objective: add human-readable metadata keys through `InsightRollup.Metadata()`.

Files:

- `internal/codextrace/insight.go`

Implementation:

1. Add direct file state fields:
   ```json
   {
     "has_file_changes": true,
     "is_read_only": false
   }
   ```

2. Add command arrays and counts:
   ```json
   {
     "command_kinds": ["read", "search"],
     "ran_read_command": true,
     "read_command_count": 2,
     "ran_search_command": true,
     "search_command_count": 1
   }
   ```

3. Add fields for every fixed command kind, even when the count is zero:
   ```text
   ran_test_command
   test_command_count
   ran_build_command
   build_command_count
   ran_lint_command
   lint_command_count
   ran_format_command
   format_command_count
   ran_git_command
   git_command_count
   ran_read_command
   read_command_count
   ran_search_command
   search_command_count
   ran_install_command
   install_command_count
   ran_systemd_command
   systemd_command_count
   ran_network_command
   network_command_count
   ran_other_command
   other_command_count
   ```

4. Add tool arrays and booleans:
   ```json
   {
     "tool_names": ["exec_command", "web_search"],
     "used_exec_command": true,
     "used_apply_patch": false,
     "used_web_search": true,
     "used_mcp_tool": false,
     "used_tool_search": false
   }
   ```

5. Emit tool counts for common tool families:
   ```text
   exec_command_tool_count
   apply_patch_tool_count
   web_search_tool_count
   mcp_tool_count
   tool_search_tool_count
   ```

Naming decision:

- Use `used_mcp_tool`, not `used_mcp`, because "MCP tool" is clearer in Langfuse filters.
- Use `used_exec_command`, not `used_command`, because the observation name is `exec_command`.

Exit criteria:

- `Metadata()` is deterministic.
- Arrays are sorted.
- Zero-count command fields are present so Langfuse filters can rely on stable keys.

### Phase 3: Unit Tests for Rollup Semantics
Objective: test the data contract before touching Langfuse projection or golden fixtures.

Files:

- `internal/codextrace/insight_test.go`

Add tests:

1. `TestInsightNavigationFacetsReadOnly`
   - Fixture turn contains `sed -n`, `rg -n`, and no patch.
   - Expected:
     ```text
     is_read_only=true
     has_file_changes=false
     ran_read_command=true
     read_command_count=1
     ran_search_command=true
     search_command_count=1
     used_exec_command=true
     used_apply_patch=false
     command_kinds=["read","search"]
     ```

2. `TestInsightNavigationFacetsFileChanges`
   - Fixture turn contains `codex.tool.apply_patch`.
   - Expected:
     ```text
     is_read_only=false
     has_file_changes=true
     used_apply_patch=true
     apply_patch_tool_count=1
     ```

3. `TestInsightNavigationFacetsNetworkAndInstallAreOrthogonal`
   - Fixture turn contains `curl` and `npm install`, no patch.
   - Expected:
     ```text
     is_read_only=true
     ran_network_command=true
     ran_install_command=true
     ```
   - This documents that read-only means no observed file changes, not no external or install activity.

4. `TestInsightNavigationFacetsToolNames`
   - Fixture includes `codex.tool.web_search`, `codex.tool.mcp`, and `codex.tool.tool_search`.
   - Expected sorted `tool_names` and corresponding `used_*` booleans.

Exit criteria:

```bash
go test ./internal/codextrace -run 'TestInsightNavigationFacets|TestInsightRollup|TestInsightCommandClassification' -count=1
```

passes.

### Phase 4: Contract and Golden Fixtures
Objective: make normalized trace contracts include the new trace metadata fields.

Files:

- `internal/tracecontract/contract.go`
- `test/contract_fixture_test.go`
- `test/full_acceptance_test.go`
- `testdata/golden/*.normalized.json`

Implementation:

1. Confirm `internal/tracecontract` already uses `BuildInsightRollup(turn).Metadata()`.
2. Update fixtures so root `metadata` includes the new fields.
3. Add test assertions for a representative subset:
   ```text
   is_read_only
   has_file_changes
   ran_search_command
   search_command_count
   ran_read_command
   read_command_count
   used_apply_patch
   used_web_search
   command_kinds
   tool_names
   ```

Exit criteria:

```bash
go test ./internal/tracecontract ./test -count=1
```

passes.

### Phase 5: Langfuse Projection
Objective: ensure trace-level facets are searchable on traces and not duplicated onto child observations.

Files:

- `internal/langfuse/export.go`
- `internal/langfuse/spans_test.go`

Implementation:

1. Reuse the existing `insightMetadataAttributes(turn)` path.
2. Do not add a second projection path.
3. Ensure array metadata is serialized consistently with existing metadata arrays.
4. Keep observation-level `command_kind` unchanged.

Tests:

1. Extend existing metadata projection test to assert:
   ```text
   langfuse.trace.metadata.codex_insight.is_read_only
   langfuse.trace.metadata.codex_insight.has_file_changes
   langfuse.trace.metadata.codex_insight.ran_search_command
   langfuse.trace.metadata.codex_insight.search_command_count
   ```

2. Preserve existing assertion that child observations do not repeat root insight attributes.

Exit criteria:

```bash
go test ./internal/langfuse -run 'TestInsightMetadata|TestEvalInsightMetadata' -count=1
```

passes.

### Phase 6: Documentation
Objective: document how operators should use trace filters, observation filters, and saved views.

Files:

- `README.md`
- `PROJECT_CONTEXT.md`
- `TESTING.md`

Documentation updates:

1. Add a short "Trace navigation facets" section.
2. Explain trace vs observation filters:
   ```text
   Use Traces for turn-level navigation.
   Use Observations for specific tool calls and command details.
   ```

3. Document common trace filters:
   ```text
   Metadata codex_insight.is_read_only equals true
   Metadata codex_insight.has_file_changes equals true
   Metadata codex_insight.ran_search_command equals true
   Metadata codex_insight.ran_read_command equals true
   Metadata codex_insight.used_web_search equals true
   ```

4. Document common observation filters:
   ```text
   Name equals codex.tool.exec_command
   Metadata command_kind equals search
   ```

5. Document saved views:
   ```text
   Views -> Create Custom View saves current columns, filters, and sort order.
   ```

Exit criteria:

- Docs explain why `command_kind` works in Observations and `ran_search_command` works in Traces.
- Docs define `is_read_only` precisely.
- Docs list the default saved views to create in Langfuse.

### Phase 7: Saved Langfuse Views
Objective: create reusable Langfuse views for the trace facets and observation drill-down filters introduced by this plan.

Prerequisites:

- Local Langfuse is running at `http://localhost:3000`.
- The project is `codex-local`.
- At least one trace exists for each common scenario, or the view can still be created with zero matching rows.

Rules:

- Create views after the metadata fields are deployed and at least one new trace has been exported.
- Use short, predictable view names.
- Keep trace views on the `Tracing -> Traces` tab.
- Keep drill-down views on the `Tracing -> Observations` tab.
- Do not save views with a temporary `Session ID` filter unless the view is intentionally session-specific.
- Keep sort order as newest first unless a view has a stronger reason to sort differently.
- Include enough columns to inspect the result without opening every row.

Required trace views to create:

| View name | Tab | Filters | Suggested columns |
|---|---|---|---|
| `Traces: read only` | Traces | `Metadata codex_insight.is_read_only equals true` | Timestamp, Name, Input, Output, Metadata, Latency, Tokens |
| `Traces: changed files` | Traces | `Metadata codex_insight.has_file_changes equals true` | Timestamp, Name, Input, Output, Metadata, Latency |
| `Traces: search commands` | Traces | `Metadata codex_insight.ran_search_command equals true` | Timestamp, Name, Input, Output, Metadata |
| `Traces: read commands` | Traces | `Metadata codex_insight.ran_read_command equals true` | Timestamp, Name, Input, Output, Metadata |
| `Traces: network commands` | Traces | `Metadata codex_insight.ran_network_command equals true` | Timestamp, Name, Input, Output, Metadata |
| `Traces: install commands` | Traces | `Metadata codex_insight.ran_install_command equals true` | Timestamp, Name, Input, Output, Metadata |
| `Traces: tests run` | Traces | `Metadata codex_insight.ran_test_command equals true` | Timestamp, Name, Input, Output, Metadata |
| `Traces: failed verification` | Traces | `Metadata codex_insight.verification_status equals failed` | Timestamp, Name, Input, Output, Metadata |
| `Traces: web search used` | Traces | `Metadata codex_insight.used_web_search equals true` | Timestamp, Name, Input, Output, Metadata |
| `Traces: patch tool used` | Traces | `Metadata codex_insight.used_apply_patch equals true` | Timestamp, Name, Input, Output, Metadata |

Optional trace views to create if the team wants one-click access to every command family:

| View name | Tab | Filters | Suggested columns |
|---|---|---|---|
| `Traces: git commands` | Traces | `Metadata codex_insight.ran_git_command equals true` | Timestamp, Name, Input, Output, Metadata |
| `Traces: build commands` | Traces | `Metadata codex_insight.ran_build_command equals true` | Timestamp, Name, Input, Output, Metadata |
| `Traces: lint commands` | Traces | `Metadata codex_insight.ran_lint_command equals true` | Timestamp, Name, Input, Output, Metadata |
| `Traces: format commands` | Traces | `Metadata codex_insight.ran_format_command equals true` | Timestamp, Name, Input, Output, Metadata |
| `Traces: systemd commands` | Traces | `Metadata codex_insight.ran_systemd_command equals true` | Timestamp, Name, Input, Output, Metadata |
| `Traces: other commands` | Traces | `Metadata codex_insight.ran_other_command equals true` | Timestamp, Name, Input, Output, Metadata |

Required observation views to create:

| View name | Tab | Filters | Suggested columns |
|---|---|---|---|
| `Observations: exec commands` | Observations | `Name equals codex.tool.exec_command` | Start Time, Type, Name, Input, Output, Metadata, Latency |
| `Observations: command search` | Observations | `Name equals codex.tool.exec_command`; `Metadata command_kind equals search` | Start Time, Name, Input, Output, Metadata |
| `Observations: command read` | Observations | `Name equals codex.tool.exec_command`; `Metadata command_kind equals read` | Start Time, Name, Input, Output, Metadata |
| `Observations: command network` | Observations | `Name equals codex.tool.exec_command`; `Metadata command_kind equals network` | Start Time, Name, Input, Output, Metadata |
| `Observations: command install` | Observations | `Name equals codex.tool.exec_command`; `Metadata command_kind equals install` | Start Time, Name, Input, Output, Metadata |
| `Observations: command test` | Observations | `Name equals codex.tool.exec_command`; `Metadata command_kind equals test` | Start Time, Name, Input, Output, Metadata |
| `Observations: failed commands` | Observations | `Name equals codex.tool.exec_command`; `Metadata failure_type not equals none` | Start Time, Name, Input, Output, Metadata |
| `Observations: apply patches` | Observations | `Name equals codex.tool.apply_patch` | Start Time, Name, Input, Output, Metadata |
| `Observations: web search` | Observations | `Name equals codex.tool.web_search` | Start Time, Name, Input, Output, Metadata |

Optional observation views to create if the team wants one-click access to every command family:

| View name | Tab | Filters | Suggested columns |
|---|---|---|---|
| `Observations: command git` | Observations | `Name equals codex.tool.exec_command`; `Metadata command_kind equals git` | Start Time, Name, Input, Output, Metadata |
| `Observations: command build` | Observations | `Name equals codex.tool.exec_command`; `Metadata command_kind equals build` | Start Time, Name, Input, Output, Metadata |
| `Observations: command lint` | Observations | `Name equals codex.tool.exec_command`; `Metadata command_kind equals lint` | Start Time, Name, Input, Output, Metadata |
| `Observations: command format` | Observations | `Name equals codex.tool.exec_command`; `Metadata command_kind equals format` | Start Time, Name, Input, Output, Metadata |
| `Observations: command systemd` | Observations | `Name equals codex.tool.exec_command`; `Metadata command_kind equals systemd` | Start Time, Name, Input, Output, Metadata |
| `Observations: command other` | Observations | `Name equals codex.tool.exec_command`; `Metadata command_kind equals other` | Start Time, Name, Input, Output, Metadata |

If the local Langfuse metadata operator list does not support `not equals`, replace `Observations: failed commands` with two views:

| View name | Tab | Filters | Suggested columns |
|---|---|---|---|
| `Observations: failed commands: exit` | Observations | `Name equals codex.tool.exec_command`; `Metadata failure_type equals nonzero_exit` | Start Time, Name, Input, Output, Metadata |
| `Observations: failed commands: timeout` | Observations | `Name equals codex.tool.exec_command`; `Metadata failure_type equals timeout` | Start Time, Name, Input, Output, Metadata |

Manual UI procedure for each view:

1. Open the correct tab, either `Tracing -> Traces` or `Tracing -> Observations`.
2. Apply the listed filters in the left filter panel.
3. Adjust columns if needed through `Columns`.
4. Confirm sort order is newest first.
5. Click `Views`.
6. Click `Create Custom View`.
7. Enter the exact view name from the table.
8. Click `Save View`.
9. Reopen `Views` and verify the saved view appears.
10. Click the saved view and confirm the filters are restored.

Validation:

```text
Views count increases after each saved view.
Each saved view restores its filters after navigation away and back.
Trace views do not contain observation-only filters such as command_kind.
Observation views do not rely on trace-level codex_insight fields unless intentionally needed.
```

Exit criteria:

- All required trace views exist.
- All required observation views exist.
- Optional full-coverage views are either created or explicitly deferred.
- At least one saved trace view and one saved observation view are reopened successfully and restore the expected filters.

### Phase 8: Local Validation
Objective: complete repo-local validation before live smoke.

Commands:

```bash
go test ./internal/codextrace ./internal/langfuse ./internal/tracecontract ./test -count=1
go test ./... -count=1
git diff --check
```

Optional stability pass:

```bash
go test ./... -run 'TestEval' -count=3 -parallel 8
```

Exit criteria:

- All required commands pass.
- Any optional command failure is explained with exact output.

### Phase 9: Local Langfuse Smoke
Objective: confirm the metadata is actually filterable in the running local Langfuse instance.

Procedure:

1. Produce or identify a read-only turn that runs both `rg` and `sed`.
2. Export it with the watcher or explicit exporter.
3. Fetch the trace from Langfuse API:
   ```bash
   curl -s -u "$LANGFUSE_PUBLIC_KEY:$LANGFUSE_SECRET_KEY" \
     "$LANGFUSE_HOST/api/public/traces/$TRACE_ID" |
   jq '.metadata'
   ```

4. Confirm:
   ```text
   codex_insight.is_read_only = true
   codex_insight.has_file_changes = false
   codex_insight.ran_search_command = true
   codex_insight.search_command_count > 0
   codex_insight.ran_read_command = true
   codex_insight.read_command_count > 0
   ```

5. Produce or identify a patch turn.
6. Confirm:
   ```text
   codex_insight.is_read_only = false
   codex_insight.has_file_changes = true
   codex_insight.used_apply_patch = true
   ```

7. In the Langfuse UI, create or refresh the saved views listed in Phase 7.

Exit criteria:

- The trace table can filter on `codex_insight.is_read_only`.
- The trace table can filter on `codex_insight.has_file_changes`.
- The trace table can filter on `codex_insight.ran_search_command`.
- The observation table can still filter on `command_kind=search`.
- Saved views restore the trace and observation filters without retyping metadata keys.

## 9. Test Matrix

| Scenario | Input observations | Expected trace facets |
|---|---|---|
| Chat only | no tools | `is_read_only=true`, `has_file_changes=false`, no command kinds |
| Read-only inspection | `sed`, `rg` exec commands | `ran_read_command=true`, `ran_search_command=true`, `is_read_only=true` |
| Patch edit | `apply_patch` | `has_file_changes=true`, `is_read_only=false`, `used_apply_patch=true` |
| Test run | `go test ./...` | `ran_test_command=true`, `verification_status` unchanged semantics |
| Network research | `curl` exec command | `ran_network_command=true`, `is_read_only=true` if no patch |
| Install command | `npm install` exec command | `ran_install_command=true`, `is_read_only=true` if no observed file changes |
| Web search | `codex.tool.web_search` | `used_web_search=true`, `web_search_tool_count=1` |
| Failed command | nonzero exec command | existing `failed_command_count` and `failure_type` remain correct |
| Unknown command | `printf` or unclassified command | `ran_other_command=true`, `other_command_count>0` |

## 10. Data Contract Snapshot

Trace metadata fields added by this plan:

```yaml
codex_insight:
  has_file_changes: boolean
  is_read_only: boolean
  command_kinds: [string]
  tool_names: [string]
  ran_test_command: boolean
  test_command_count: integer
  ran_build_command: boolean
  build_command_count: integer
  ran_lint_command: boolean
  lint_command_count: integer
  ran_format_command: boolean
  format_command_count: integer
  ran_git_command: boolean
  git_command_count: integer
  ran_read_command: boolean
  read_command_count: integer
  ran_search_command: boolean
  search_command_count: integer
  ran_install_command: boolean
  install_command_count: integer
  ran_systemd_command: boolean
  systemd_command_count: integer
  ran_network_command: boolean
  network_command_count: integer
  ran_other_command: boolean
  other_command_count: integer
  used_exec_command: boolean
  exec_command_tool_count: integer
  used_apply_patch: boolean
  apply_patch_tool_count: integer
  used_web_search: boolean
  web_search_tool_count: integer
  used_mcp_tool: boolean
  mcp_tool_count: integer
  used_tool_search: boolean
  tool_search_tool_count: integer
```

Observation metadata remains unchanged:

```yaml
codex.tool.exec_command:
  command_kind: enum
  status: string
  exit_code: integer
  duration_ms: integer
  failure_type: enum
```

## 11. Acceptance Criteria
- Trace metadata supports filtering for changed-file turns without opening observations.
- Trace metadata supports filtering for read-only turns without opening observations.
- Trace metadata supports filtering for command families such as search, read, git, test, build, install, network, and format.
- Observation metadata still supports detailed filtering by `command_kind`.
- No raw command output, diffs, hidden reasoning, or full changed file lists are added to trace metadata.
- Existing trace and observation names remain unchanged.
- Existing verification metadata behavior remains unchanged.
- All Go tests pass.

## 12. Open Questions
- Should `tool_names` use full observation names like `codex.tool.exec_command` or short names like `exec_command`? Recommendation: short names for trace metadata, full names stay visible in observations.
- Should `used_mcp_tool` be split by server or tool name? Recommendation: no for v1 because it can become high-cardinality.
- Should `ran_other_command` be exposed in saved views? Recommendation: expose metadata but do not create a default saved view for it.
