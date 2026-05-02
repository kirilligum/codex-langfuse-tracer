# Ask-Me Record: Langfuse Tags, Saved Views, MCP Usage, and Install Behavior

## Scope

- Project: Codex Langfuse Tracer.
- Repository: `/home/kirill/p/codex-langfuse-tracer`.
- Related plan: `plans/langfuse-tags-and-mcp-usage-plan.md`.
- Date: 2026-05-01.
- Purpose: Record the clarifying decisions needed before implementing trace tags, MCP usage metadata, saved filter workflows, backfill behavior, command tags, file-change labels, live Langfuse verification, and install behavior.

## Terminology and code anchors

- **Trace**: A Langfuse trace corresponding to one exportable Codex turn. In this repository the local model is `codextrace.Turn` in `internal/codextrace/model.go`, exported through `internal/langfuse/export.go`.
- **Observation**: A child unit inside a trace, represented by `codextrace.Observation`. Tool observations include names such as `codex.tool.exec_command`, `codex.tool.apply_patch`, `codex.tool.mcp`, and web-search observations.
- **`codex_insight.navigation`**: The current single trace-table filter index. It is produced by `InsightRollup.Metadata()` and `InsightRollup.navigationValues()` in `internal/codextrace/insight.go`, then exported as `langfuse.trace.metadata.codex_insight.navigation` by `insightMetadataAttributes()` in `internal/langfuse/export.go`.
- **Command kind**: The deterministic classification attached to `codex.tool.exec_command` observations as `command_kind`. It is produced by `CommandInsightMetadata()` and `ClassifyCommand()` in `internal/codextrace/insight.go`. The current documented enum is `test`, `build`, `lint`, `format`, `git`, `read`, `search`, `install`, `systemd`, `network`, or `other`.
- **Trace tag**: A Langfuse tag intended for fast human filtering at the trace level. The planned exporter key is `langfuse.trace.tags` on the root `codex.agent` span in `internal/langfuse/export.go`. External contract evidence: Langfuse documents tags as strings up to 200 characters and documents OpenTelemetry mapping through `langfuse.trace.tags` at https://langfuse.com/docs/observability/features/tags and https://langfuse.com/integrations/native/opentelemetry.
- **MCP observation**: A `codex.tool.mcp` observation created from `mcp_tool_call_end` in `internal/codextrace/parser.go`. Current parser output excludes raw `invocation`, `result`, and `duration` from metadata through `metadataWithoutLargeFields(...)`.
- **MCP server tag**: A planned tag of the form `mcp:<server>`, derived only from observed MCP calls. The source value should come from `payload.invocation.server`, stored as `mcp_server` on the `codex.tool.mcp` observation, not from configured-but-unused MCP servers in config.
- **Saved view**: A persisted Langfuse UI filter preset. The repository currently documents filter recipes in `README.md` under "Filtering And Saved Views"; it does not contain code that creates Langfuse saved views through an API.
- **Backfill**: Re-exporting old rollout files so previously ingested Langfuse traces get newly added metadata or tags. Existing source modes are parsed in `cmd/codex-langfuse-exporter/main.go` and tested in `cmd/codex-langfuse-exporter/cli_test.go`: `--path`, `--session-id`, `--latest`, and `--watch`.
- **Watcher install path**: `install.sh` builds `~/.codex/bin/codex-langfuse-exporter`, installs `systemd/codex-langfuse-watch.service`, and restarts `codex-langfuse-watch.service`, whose `ExecStart` runs `~/.codex/bin/codex-langfuse-exporter --watch`.

### 1.1 Question

Should saved views be created or seeded by this repository, or should tags plus documented filter recipes remain the reusable navigation mechanism?

### 1.2 Context & clarification

- This question exists because Langfuse UI filters are useful only if users can reuse them without retyping metadata filters.
- The repository already has a documentation-only pattern in `README.md` under "Filtering And Saved Views"; examples include `Metadata codex_insight.navigation contains files:changed` and `Metadata command_kind equals search`.
- The current source tree has no saved-view client, no Langfuse project-settings API wrapper, and no test fixture for saved-view creation.
- External contract evidence is weaker than local evidence: Langfuse docs mention saved views in UI-oriented documentation, but no stable saved-view write API is verified in this repository.
- The decision affects whether implementation remains a pure exporter feature or grows an admin/configuration boundary.

### 1.3 Options

- `Option A`: Code-owned saved view seeding after verifying a stable Langfuse API
  - **Rubrics**: `Conf:50% | Invest:i | Blast:i | Reversal:i | Fit:iii | Lib:i | Obs:i | Surface:iii | Perf:na`
  - **Approach**: Add one explicit saved-view seeding path only after verifying an official Langfuse API or documented import mechanism. Store view definitions in one repo-owned table generated from the same tag/navigation constants used by tests.
  - **Architecture**: This introduces a new external boundary next to `internal/langfuse`. It should not be part of parser or rollup code. It would require tests that mock the Langfuse saved-view API and docs that distinguish trace tags from UI view persistence.
  - **SSoT**: Saved-view definitions must be derived from the same canonical tag/navigation constants as `InsightRollup.navigationValues()` and the planned tag helper. No separate string list in docs.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: Best long-term reuse if Langfuse exposes a stable API, but highest uncertainty and blast radius because the repository currently only exports traces.
- `Option B`: Documentation-owned saved view recipes, backed by tags and metadata
  - **Rubrics**: `Conf:90% | Invest:ii | Blast:ii | Reversal:ii | Fit:i | Lib:ii | Obs:ii | Surface:i | Perf:na`
  - **Approach**: Keep saved views as human-created Langfuse UI presets. Update `README.md` and static docs tests so the recipes use trace tags where tags are intended and `codex_insight.navigation` where full metadata coverage is needed.
  - **Architecture**: This follows the current architecture exactly: exporter emits queryable data, docs describe how to filter it, and no admin API client is added.
  - **SSoT**: The definitive runtime facts live in `BuildInsightRollup()` and exported OTel attributes. Docs are verified by `test/docs_static_test.go` so they cannot silently diverge.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: Lowest implementation risk and strongest fit. Users still create views manually in Langfuse once.
- `Option C`: Tags only, no saved-view recipe work
  - **Rubrics**: `Conf:80% | Invest:iii | Blast:iii | Reversal:iii | Fit:ii | Lib:iii | Obs:iii | Surface:ii | Perf:na`
  - **Approach**: Emit trace tags and rely on Langfuse's tag UI without documenting saved-view recipes.
  - **Architecture**: Keeps code small, but removes a current user-facing documentation pattern that already exists in `README.md`.
  - **SSoT**: Runtime SSoT remains clean, but user workflow knowledge lives outside the repo.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: Simple, but weaker operational usability because the repo no longer explains the intended filters.

### 1.4 Recommendation

Recommend Option B. It follows verified local architecture, avoids inventing a Langfuse saved-view API, keeps one exporter path, and still gives users reusable navigation by documenting exactly which Langfuse views to create. Move to Option A only if an official saved-view API is verified and saved-view seeding becomes a hard product requirement.

### 2.1 Question

Should old Langfuse traces be backfilled automatically, explicitly re-exported by an operator, or left unchanged?

### 2.2 Context & clarification

- This question exists because new tag and MCP metadata fields apply only when a rollout is exported after the code change.
- The existing CLI already supports explicit source modes through `cmd/codex-langfuse-exporter/main.go`: `--path`, `--session-id`, `--latest`, and `--watch`.
- `install.sh` restarts the watcher, so future completed turns will use the new exporter after installation.
- Automatic backfill would require deciding which historical rollout files to replay, how to avoid duplicates or stale trace IDs, and how much old data to rewrite in Langfuse.
- The current local architecture has no migration subsystem; replay is already possible through explicit CLI export.

### 2.3 Options

- `Option A`: Explicit operator re-export workflow using existing CLI modes
  - **Rubrics**: `Conf:85% | Invest:i | Blast:ii | Reversal:ii | Fit:i | Lib:i | Obs:i | Surface:i | Perf:ii`
  - **Approach**: Document exact re-export commands using the existing CLI, such as `~/.codex/bin/codex-langfuse-exporter --path <rollout.jsonl> --no-verify` or `--session-id <SESSION_ID>`. Add static docs tests for the wording.
  - **Architecture**: Uses existing source-mode parsing and avoids a new migration command. Keeps imperative side effects at the CLI boundary.
  - **SSoT**: Rollout files remain the source of replay data. Export behavior remains in `internal/langfuse/export.go` and source selection remains in `cmd/codex-langfuse-exporter/main.go`.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: Clear, controlled, and reversible at the operator level. It does not silently update every old trace.
- `Option B`: Future-only behavior after install
  - **Rubrics**: `Conf:90% | Invest:ii | Blast:iii | Reversal:iii | Fit:ii | Lib:ii | Obs:ii | Surface:ii | Perf:i`
  - **Approach**: State that `install.sh` affects only future watcher exports. Old rows stay as they are unless manually re-exported outside the implementation scope.
  - **Architecture**: Matches the current watcher lifecycle exactly and adds no replay behavior.
  - **SSoT**: No new state is introduced. The export state remains owned by the watcher code in `internal/watch`.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: Smallest behavior change, but old traces will not show new tags and can confuse UI validation.
- `Option C`: Automatic historical replay during install
  - **Rubrics**: `Conf:35% | Invest:iii | Blast:i | Reversal:i | Fit:iii | Lib:iii | Obs:iii | Surface:iii | Perf:iii`
  - **Approach**: Make install or watcher startup scan historical rollout files and re-export them automatically.
  - **Architecture**: This would turn install into a data migration path and make watcher startup mutate historical Langfuse state. That is a poor fit with the current simple install script.
  - **SSoT**: Replay state would need a new durable migration marker or it risks duplicating work. That introduces another source of lifecycle state.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: Convenience is high, but correctness and reversibility are weak. This conflicts with the no-fallback, no-surprise-automation preference.

### 2.4 Recommendation

Recommend Option A. It uses verified existing CLI modes, keeps data changes explicit, and avoids automatic historical mutation. Option B is acceptable only if old rows not showing tags is acceptable. Avoid Option C.

### 3.1 Question

Should observed MCP server names become trace tags as `mcp:<server>`, or should exact MCP server names stay only in observation metadata?

### 3.2 Context & clarification

- This question exists because MCP server names are useful for navigation but may reveal private tool names or internal integration names.
- The parser currently handles `mcp_tool_call_end` in `internal/codextrace/parser.go` and emits `codex.tool.mcp` while excluding raw `invocation`, `result`, and `duration`.
- The planned minimal metadata is `mcp_server` and `mcp_tool` on the `codex.tool.mcp` observation.
- The planned tag rule is dynamic: only observed non-empty MCP servers produce `mcp:<server>`. Configured-but-unused MCPs must not be tagged.
- Exact MCP tools, such as `issues/list`, are high-cardinality and should stay metadata-only.

### 3.3 Options

- `Option A`: Dynamic observed-server tags plus observation metadata
  - **Rubrics**: `Conf:75% | Invest:i | Blast:i | Reversal:i | Fit:i | Lib:i | Obs:i | Surface:ii | Perf:ii`
  - **Approach**: Add `mcp_server` and `mcp_tool` to MCP observation metadata. Add `mcp:<server>` tags only for observed non-empty server names after normalization and privacy validation.
  - **Architecture**: Fits the parser -> rollup -> exporter pipeline. `parser.go` extracts scalar metadata, `BuildInsightRollup()` counts observed MCP servers, and the tag helper projects low-cardinality values.
  - **SSoT**: Observed MCP facts originate in `codex.tool.mcp` observations. Tags are derived from rollup state, not config files or a static current-MCP list.
  - **System limits**: Langfuse tag length is documented as 200 characters per tag. Other MCP server cardinality limits are unknown - not available in local context.
  - **Trade-offs**: Best navigation value and most general implementation. Requires treating MCP server names as non-secret operator labels.
- `Option B`: `tool:mcp` tag only, exact server and tool metadata on observations
  - **Rubrics**: `Conf:85% | Invest:ii | Blast:ii | Reversal:ii | Fit:ii | Lib:ii | Obs:ii | Surface:i | Perf:i`
  - **Approach**: Emit a trace-level `tool:mcp` tag when any MCP call occurs. Keep `mcp_server` and `mcp_tool` on `codex.tool.mcp` observations for drill-down filtering.
  - **Architecture**: Uses existing tool-family counts and adds only observation metadata. It avoids a new dynamic tag family.
  - **SSoT**: MCP detail remains on the observation where the event happened. Trace tags only say that MCP was used.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: Strongest privacy posture and smallest tag surface. Weaker trace-table navigation when comparing GitHub, Gmail, Google Calendar, or private MCP usage.
- `Option C`: Core curated MCP server tags only
  - **Rubrics**: `Conf:55% | Invest:iii | Blast:iii | Reversal:iii | Fit:iii | Lib:iii | Obs:iii | Surface:iii | Perf:iii`
  - **Approach**: Tag only a hand-maintained list such as `mcp:github`, `mcp:gmail`, and `mcp:google-drive`.
  - **Architecture**: This creates a second taxonomy that must be updated whenever available MCPs change.
  - **SSoT**: The tag list would duplicate runtime observed server names and configuration knowledge.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: Limits accidental private tags, but creates maintenance churn and misses user-defined MCPs.

### 3.4 Recommendation

Recommend Option A if MCP server names are acceptable as non-secret labels in Langfuse. It is the cleanest one-rule implementation and avoids a static MCP list. Choose Option B instead if private MCP server names may contain customer, project, or secret-like identifiers.

### 4.1 Question

Should trace tags include all `command_kind` values, only selected high-signal command kinds, or no command tags?

### 4.2 Context & clarification

- This question exists because the current plan tags only `command:search`, `command:network`, and `command:install`, while the current `codex_insight.navigation` already includes all observed command kinds.
- `CommandInsightMetadata()` attaches `command_kind` to `codex.tool.exec_command` observations.
- `BuildInsightRollup()` counts command kinds in `CommandKindCounts`.
- `navigationValues()` currently appends `command:<kind>` for every command kind with count greater than zero.
- Duplicating one curated tag subset would add another taxonomy unless it is intentionally narrower than navigation.

### 4.3 Options

- `Option A`: Tags for every observed `command_kind`
  - **Rubrics**: `Conf:90% | Invest:i | Blast:i | Reversal:i | Fit:i | Lib:i | Obs:i | Surface:ii | Perf:ii`
  - **Approach**: Emit `command:<kind>` tags for every non-zero command kind already counted by `BuildInsightRollup()`: `test`, `build`, `lint`, `format`, `git`, `read`, `search`, `install`, `systemd`, `network`, and `other`.
  - **Architecture**: This mirrors `navigationValues()` and avoids a second curated tag list.
  - **SSoT**: The `commandKinds` enum and `CommandKindCounts` remain the source. The tag helper simply projects existing rollup facts.
  - **System limits**: Time complexity is linear in the number of observations, matching current rollup behavior. API rate limits are unknown - not available in local context.
  - **Trade-offs**: Most general and least duplicative. The tag UI may contain more command tags, including lower-signal values such as `command:other`.
- `Option B`: Tags only for selected high-signal command kinds
  - **Rubrics**: `Conf:80% | Invest:ii | Blast:ii | Reversal:ii | Fit:ii | Lib:ii | Obs:ii | Surface:i | Perf:i`
  - **Approach**: Emit only selected tags such as `command:search`, `command:network`, and `command:install`, while all command kinds remain available through `codex_insight.navigation` and observation metadata.
  - **Architecture**: Keeps the tag surface smaller but introduces a second projection policy.
  - **SSoT**: The rollup remains the source of truth, but the tag subset is an additional rule that docs and tests must preserve.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: Cleaner tag list, but less general and easier to drift from the command-kind enum.
- `Option C`: No command tags; command filters stay metadata-only
  - **Rubrics**: `Conf:85% | Invest:iii | Blast:iii | Reversal:iii | Fit:iii | Lib:iii | Obs:iii | Surface:iii | Perf:iii`
  - **Approach**: Use trace tags only for file state, verification, tool families, and MCP servers. Use `codex_insight.navigation` or observation metadata for commands.
  - **Architecture**: Minimizes tag semantics, but ignores an already-implemented, deterministic command classifier.
  - **SSoT**: Command SSoT remains `command_kind`; tags do not duplicate it.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: Smallest tag surface, but worse for filtering common command-heavy traces.

### 4.4 Recommendation

Recommend Option A. It best matches the one-way principle: one command enum, one rollup count map, one projection rule. The extra tags are bounded by the existing enum and avoid duplicating a curated subset.

### 5.1 Question

Should file-change tags keep the existing `files:changed` and `files:read_only` names, or should the vocabulary be renamed before it becomes a more visible trace-tag contract?

### 5.2 Context & clarification

- This question exists because `files:read_only` can be read as "this trace read files" or "this trace did not modify files." The intended meaning is "no observed local workspace file changes in the exported turn."
- Existing local evidence: `navigationValues()` emits `files:changed` when `PatchCount > 0` or `ChangedFileCount > 0`; otherwise it emits `files:read_only`.
- `README.md` already clarifies that read-only does not mean no network activity, no install command, or no external API call.
- Renaming this vocabulary would affect tests, docs, golden fixtures, and any existing Langfuse filters using `codex_insight.navigation`.

### 5.3 Options

- `Option A`: Rename globally to `workspace:changed` and `workspace:unchanged`
  - **Rubrics**: `Conf:70% | Invest:i | Blast:i | Reversal:i | Fit:ii | Lib:i | Obs:i | Surface:ii | Perf:na`
  - **Approach**: Replace `files:changed` and `files:read_only` everywhere with `workspace:changed` and `workspace:unchanged`, including `navigationValues()`, tests, README, and golden fixtures. Do not keep old aliases.
  - **Architecture**: A direct contract change with one vocabulary. It requires broad but mechanical updates.
  - **SSoT**: The definitive names live in the central navigation/tag projection helper. Docs and tests consume the same names.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: Clearer semantics before tags become more visible. Breaks existing saved filters and old trace rows that contain the previous metadata string.
- `Option B`: Keep `files:changed` and `files:read_only`, but strengthen docs and tests
  - **Rubrics**: `Conf:90% | Invest:ii | Blast:iii | Reversal:iii | Fit:i | Lib:ii | Obs:ii | Surface:i | Perf:na`
  - **Approach**: Preserve current names and keep the README clarification. Add docs tests that require the precise meaning of `files:read_only`.
  - **Architecture**: Fits existing contract and avoids metadata churn.
  - **SSoT**: `navigationValues()` remains the source of truth. Docs describe the existing semantics without introducing aliases.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: Lowest disruption. The label remains slightly ambiguous without reading docs.
- `Option C`: Use `files:changed` and `files:unchanged`
  - **Rubrics**: `Conf:75% | Invest:iii | Blast:ii | Reversal:ii | Fit:iii | Lib:iii | Obs:iii | Surface:iii | Perf:na`
  - **Approach**: Rename only the negative state from `files:read_only` to `files:unchanged`, keeping the `files:` namespace.
  - **Architecture**: Smaller vocabulary change than Option A but still changes existing filters and golden fixtures.
  - **SSoT**: One central helper owns the names; no aliases.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: More precise than `read_only`, less semantically broad than `workspace`. Still potentially ambiguous because unchanged files may have been read.

### 5.4 Recommendation

Recommend Option B if existing filters matter today. Recommend Option A if this feature is still pre-production enough to make a breaking cleanup now. My preference is Option A before implementation because it gives the clearest user-facing label and still keeps one vocabulary with no legacy aliases.

### 6.1 Question

Should live local Langfuse verification be a required release gate or an optional manual check after automated tests?

### 6.2 Context & clarification

- This question exists because unit tests can prove this exporter emits `langfuse.trace.tags`, but only a live Langfuse instance proves the UI displays and filters those tags as expected.
- Local source evidence: `insightMetadataAttributes()` currently emits trace metadata attributes in `internal/langfuse/export.go`. The planned tag export would add `langfuse.trace.tags` near that logic.
- External contract evidence: Langfuse's OpenTelemetry docs document `langfuse.trace.tags` as a `string[]` trace attribute, and Langfuse tags docs document tag filtering and the 200-character limit.
- The repository already has in-memory span tests in `internal/langfuse/spans_test.go` and full fixture tests under `test/`.
- Live verification depends on a running local Langfuse at `http://localhost:3000` and configured credentials, so it is not as deterministic as pure Go tests.

### 6.3 Options

- `Option A`: Required manual release gate against local Langfuse
  - **Rubrics**: `Conf:75% | Invest:i | Blast:i | Reversal:ii | Fit:ii | Lib:ii | Obs:i | Surface:ii | Perf:na`
  - **Approach**: Keep automated tests as binding code gates, then require a documented `CHECK-###` live smoke before declaring production readiness. The smoke exports a fixture with the installed binary and confirms tags in the Langfuse UI.
  - **Architecture**: Keeps nondeterministic external behavior outside Go tests while still making it a release gate.
  - **SSoT**: Automated tests own the code contract. The manual check validates the external Langfuse UI contract without duplicating code logic.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: Best external-contract confidence. Requires a running Langfuse instance and human confirmation.
- `Option B`: Optional manual smoke after automated tests
  - **Rubrics**: `Conf:70% | Invest:ii | Blast:ii | Reversal:iii | Fit:i | Lib:i | Obs:ii | Surface:i | Perf:na`
  - **Approach**: Keep live Langfuse smoke as an optional checklist. Production readiness is based on `go test ./... -count=1`, focused span tests, golden fixtures, eval tests, and `git diff --check`.
  - **Architecture**: Fits the current deterministic test-heavy style and avoids environment-dependent release blocks.
  - **SSoT**: Same as Option A, but the external UI check is advisory rather than required.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: Easier to run consistently, but weaker proof for UI filter behavior.
- `Option C`: Automated tests only
  - **Rubrics**: `Conf:65% | Invest:iii | Blast:iii | Reversal:i | Fit:iii | Lib:iii | Obs:iii | Surface:iii | Perf:na`
  - **Approach**: Do not require or document any live Langfuse UI check. Trust OTel span assertions and fixture contracts.
  - **Architecture**: Cleanest local-only test boundary.
  - **SSoT**: Local contract tests are the only acceptance source.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: Fastest, but it cannot prove the user-visible UI behavior that motivated the feature.

### 6.4 Recommendation

Recommend Option A for production readiness. Correctness against the external Langfuse UI is central to this feature, and a manual release gate is the cleanest way to verify it without making normal Go tests depend on local services.

### 7.1 Question

Should `install.sh` remain the only install path that restarts the existing watcher, or should installation add any new tag/MCP-specific behavior?

### 7.2 Context & clarification

- This question exists because it is tempting to add an MCP watcher, a tag migration step, or a saved-view setup step during install.
- Current install behavior is simple and verified by local source: `install.sh` builds the Go exporter, installs `systemd/codex-langfuse-watch.service`, runs `systemctl --user enable`, and restarts `codex-langfuse-watch.service`.
- The service file has one `ExecStart`: `%h/.codex/bin/codex-langfuse-exporter --watch`.
- Existing tests in `test/install_test.go` assert the service path and restart behavior.
- Adding install-time MCP or view behavior would broaden operational responsibility beyond "install the exporter and run the watcher."

### 7.3 Options

- `Option A`: Keep install as the single watcher deployment path
  - **Rubrics**: `Conf:95% | Invest:i | Blast:ii | Reversal:ii | Fit:i | Lib:i | Obs:i | Surface:i | Perf:na`
  - **Approach**: Keep `install.sh` behavior unchanged except documentation if needed. The installed watcher automatically applies new parser, rollup, and exporter behavior to future exports after restart.
  - **Architecture**: Uses the existing deployment boundary exactly. No new service, no MCP watcher, no config file, no saved-view setup command.
  - **SSoT**: Runtime behavior lives in the exporter binary. Service lifecycle lives in `install.sh` and `systemd/codex-langfuse-watch.service`.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: Simple, testable, and consistent. Does not create saved views or backfill old traces.
- `Option B`: Keep install behavior, but add explicit post-install operator guidance
  - **Rubrics**: `Conf:90% | Invest:ii | Blast:iii | Reversal:iii | Fit:ii | Lib:ii | Obs:ii | Surface:ii | Perf:na`
  - **Approach**: Leave service behavior unchanged, but update README/TESTING and possibly installer output to state that future traces get tags and old traces require explicit re-export.
  - **Architecture**: Behavior stays in the existing install path; user guidance is clearer.
  - **SSoT**: Installer state remains the systemd service. Documentation points to the same CLI commands already supported by `main.go`.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: Better operator clarity with minimal code change. Installer output can become noisy if too much guidance is printed.
- `Option C`: Add install-time tag/MCP setup behavior
  - **Rubrics**: `Conf:45% | Invest:iii | Blast:i | Reversal:i | Fit:iii | Lib:iii | Obs:iii | Surface:iii | Perf:na`
  - **Approach**: Extend `install.sh` to seed views, scan MCP config, or trigger migration/backfill behavior.
  - **Architecture**: Expands install from deployment into state mutation and admin setup. This conflicts with the current narrow shell script.
  - **SSoT**: Install would start duplicating knowledge from exporter code, Langfuse UI/admin state, or config.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: More automated for first run, but highest risk of hacks, duplicated logic, and surprising side effects.

### 7.4 Recommendation

Recommend Option A, with Option B's documentation wording if operator confusion remains. Keep install as one direct deployment path and put all trace behavior in the exporter code, not the shell installer.
