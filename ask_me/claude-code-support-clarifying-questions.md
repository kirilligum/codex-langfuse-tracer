# Claude Code Support Clarifying Questions

## Terminology and code anchors

- **Claude Code support**: Adding support for Claude Code transcript JSONL alongside the existing Codex rollout JSONL path. Current Codex parsing is anchored in `internal/codextrace.ParseTurns`, `internal/codextrace.Turn`, `internal/codextrace.Observation`, and `internal/codextrace.ExportableTurns`.
- **Transcript schema coverage**: The set of raw JSONL record shapes the parser can correctly interpret. Codex schema coverage is currently fixture-backed by `testdata/manifest.json`, `testdata/rollouts/*.jsonl`, and `testdata/golden/*.normalized.json`.
- **Synthetic fixture**: A committed JSONL file written from documented or observed shape, with no real user content. Current fixture inventory is anchored by `testdata/manifest.json`.
- **Sanitized real-derived fixture**: A committed JSONL fixture derived from a real local transcript after removing private content and preserving only structural fields needed for parser behavior.
- **Golden contract**: A normalized expected trace file under `testdata/golden` compared by `test/contract_test.go` through `tracecontract.FromTurn`.
- **Provider-neutral model**: A planned `internal/agenttrace` package that would own shared `Turn`, `Observation`, `TokenUsage`, redaction, stable IDs, terminal assembly, command classification, insight rollup, and exportability. Today those responsibilities are mostly in `internal/codextrace`.
- **Hook-to-queue-to-watch**: The planned Claude automatic path. Claude Code sends hook JSON, `--claude-hook` writes a queue record, and `internal/watch.WatchSessions` or its provider-neutral successor drains the queue and exports through `internal/langfuse.ExportTurn`.
- **Stop hook**: The Claude Code hook event intended to run when the main agent finishes a response. The plan currently accepts only `hook_event_name=Stop` for automatic export.
- **Pricing catalog**: The Langfuse model definition sync in `internal/langfuse/models.go`, anchored by `SyncModelPricing`, `codexModelPricingCatalog`, source URLs, and source dates.
- **Thinking blocks**: Claude transcript content blocks that may contain hidden or internal reasoning. The plan currently omits them completely from exported `input`, `output`, observations, metadata, and golden files.
- **Runtime naming surfaces**: User-visible names such as `buildinfo.InstalledBinaryName`, `buildinfo.InstalledServiceName`, `buildinfo.DefaultServiceName`, and `buildinfo.TraceName`.
- **MVP**: The smallest production-quality support slice that proves parser, fixture, CLI, hook, queue, watch, and Langfuse projection behavior without adding legacy paths or duplicate logic.

### 1.1 Question

Should Claude parser tests use sanitized real-derived fixtures before implementation, or should the MVP rely on synthetic fixtures only?

### 1.2 Context & clarification

- I am asking because Claude transcript schema drift is the lowest-confidence part of the plan.
- Local Codex behavior is well grounded by `internal/codextrace.ParseTurns`, `testdata/manifest.json`, and golden fixtures. Claude support currently has only researched external docs and local shape observations, not committed Claude fixtures.
- The decision affects correctness, privacy, and future parser maintenance. Real-derived fixtures improve schema confidence but require careful removal of private transcript content.
- Implementation evidence: existing parser tests read fixture JSONL files and compare normalized outputs through `tracecontract.FromTurn`.
- External contract evidence: Claude Code owns the transcript and hook JSON format; official docs are needed for external field semantics, while local fixtures prove only this repository's parser behavior.

### 1.3 Options

- `Option A`: Build sanitized real-derived fixtures plus synthetic controls
  - **Rubrics**: `Conf:80% | Invest:i | Blast:i | Reversal:i | Fit:i | Lib:i | Obs:i | Surface:iii | Perf:na`
  - **Approach**: Derive a small fixture corpus from two or three local Claude transcripts, strip all private text, preserve structural JSON fields, and add synthetic edge fixtures for incomplete, corrupt, and thinking records.
  - **Architecture**: Fits the manifest-driven contract pattern already used by `test/contract_test.go` and `testdata/golden`.
  - **SSoT**: `testdata/manifest.json` remains the only fixture registry; parser behavior is anchored in `internal/claudetrace`.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: Highest schema confidence and best drift protection, but requires a careful sanitization pass and broader fixture review.

- `Option B`: Use synthetic fixtures and non-committed local shape validation
  - **Rubrics**: `Conf:70% | Invest:ii | Blast:ii | Reversal:ii | Fit:ii | Lib:ii | Obs:ii | Surface:ii | Perf:na`
  - **Approach**: Commit only synthetic fixtures, then run local shape inspection against real transcripts without committing derived content.
  - **Architecture**: Keeps committed testdata simple while still using local evidence to tune parser assumptions.
  - **SSoT**: `testdata/manifest.json` remains the committed source of parser behavior; local inspection is evidence, not a second registry.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: Better privacy posture and lower committed-data risk, but weaker reproducibility for future maintainers.

- `Option C`: Use synthetic fixtures only
  - **Rubrics**: `Conf:60% | Invest:iii | Blast:iii | Reversal:iii | Fit:iii | Lib:iii | Obs:iii | Surface:i | Perf:na`
  - **Approach**: Commit synthetic fixtures based on official docs and observed shapes, then rely on parser tolerance for unknown non-conversation records.
  - **Architecture**: Smallest fixture change and easiest to review.
  - **SSoT**: `testdata/manifest.json` remains the only source of fixture truth.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: Fastest MVP path, but schema confidence is lower and real-world failures are more likely after release.

### 1.4 Recommendation

Recommend Option A. Correctness and verified local context come first, and parser support is only as good as the raw shapes it is tested against. The fixture set should be aggressively sanitized and small, but it should include real-derived structure before parser implementation.

### 2.1 Question

Should `Stop` be the only Claude hook event accepted for automatic export in the MVP?

### 2.2 Context & clarification

- I am asking because the plan currently rejects `SessionEnd`, `StopFailure`, polling, and direct hook export to keep one automatic path.
- The current Codex automatic path is centralized in `internal/watch.WatchSessions` and `internal/watch.ScanOnce`.
- The planned Claude path should preserve that shape: hook code should only enqueue, while the background service owns export, state, and dedupe.
- `Stop` means the hook event used when the Claude agent finishes a response. `SessionEnd` and `StopFailure` are separate lifecycle events that could add extra semantics and duplicate turn-completion logic.

### 2.3 Options

- `Option A`: Use an explicit hook event state machine and accept only `Stop`
  - **Rubrics**: `Conf:80% | Invest:i | Blast:i | Reversal:ii | Fit:i | Lib:i | Obs:i | Surface:ii | Perf:na`
  - **Approach**: Model hook events as an explicit accepted/rejected state transition. Only `Stop` creates an export queue record; other events exit cleanly without enqueueing.
  - **Architecture**: Fits the one-way watcher design by keeping export in the background service and hook behavior side-effect-minimal.
  - **SSoT**: `internal/claudehook` owns hook parsing; `internal/exportstate` owns queue state; `internal/watch` owns export drain.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: Strong fail-fast behavior and a small lifecycle surface, but failed-turn telemetry is not automatic in MVP.

- `Option B`: Keep automatic Claude export out of MVP
  - **Rubrics**: `Conf:75% | Invest:ii | Blast:ii | Reversal:i | Fit:ii | Lib:ii | Obs:ii | Surface:i | Perf:na`
  - **Approach**: Implement only `--provider claude --path` first, then add hook queueing after manual export is stable.
  - **Architecture**: Preserves CLI-only semantics while avoiding hook lifecycle uncertainty.
  - **SSoT**: CLI dispatch owns Claude ingestion for MVP; no hook queue state exists yet.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: Lowest lifecycle risk, but does not solve automatic Claude export and reduces parity with Codex watcher behavior.

### 2.4 Recommendation

Recommend Option A. It is the simplest automatic path that fits the existing watcher architecture. Accepting only `Stop` keeps lifecycle semantics explicit and avoids a second completion heuristic.

### 3.1 Question

Should Claude model pricing be included in the Claude support MVP, or split into a separate follow-up?

### 3.2 Context & clarification

- I am asking because pricing is useful but not required to prove parser, trace, CLI, hook, queue, or Langfuse export support.
- Existing pricing sync lives in `internal/langfuse/models.go` through `SyncModelPricing` and `codexModelPricingCatalog`.
- Pricing correctness depends on official external pricing docs, not only local code. Cache token semantics can affect double counting.
- The current trace path can send model and token usage without adding pricing entries; Langfuse remains the cost calculator.

### 3.3 Options

- `Option A`: Split Claude pricing into a separate source-checked follow-up
  - **Rubrics**: `Conf:85% | Invest:i | Blast:ii | Reversal:ii | Fit:i | Lib:i | Obs:i | Surface:i | Perf:na`
  - **Approach**: Keep the MVP focused on parser and export behavior. Add Claude pricing later with its own tests, source URLs, source dates, and model IDs.
  - **Architecture**: Preserves `internal/langfuse/models.go` as the pricing SSoT without coupling pricing changes to parser rollout.
  - **SSoT**: Pricing remains centralized in `internal/langfuse/models.go`; trace usage mapping remains in `agenttrace.TokenUsage`.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: Reduces MVP scope and risk, but initial Claude traces may show usage without calculated cost until pricing is added.

- `Option B`: Keep Claude pricing in the MVP after parser/export work
  - **Rubrics**: `Conf:70% | Invest:ii | Blast:i | Reversal:i | Fit:ii | Lib:ii | Obs:ii | Surface:ii | Perf:na`
  - **Approach**: Add pricing in the same plan after official Anthropic pricing is source-checked.
  - **Architecture**: Extends the existing model sync contract directly.
  - **SSoT**: `internal/langfuse/models.go` remains the only pricing catalog.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: More complete product outcome, but increases MVP scope and external-doc dependency.

- `Option C`: Omit Claude pricing indefinitely
  - **Rubrics**: `Conf:60% | Invest:iii | Blast:iii | Reversal:iii | Fit:iii | Lib:iii | Obs:iii | Surface:iii | Perf:na`
  - **Approach**: Send usage details and never add Claude pricing definitions.
  - **Architecture**: Keeps trace export simple but leaves model cost behavior incomplete.
  - **SSoT**: Usage remains in trace export; pricing has no Claude owner.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: Smallest code change, but weakens cost reporting and leaves a durable product gap.

### 3.4 Recommendation

Recommend Option A. Parser/export correctness should land first. Pricing should be a separate source-checked change because it depends on an external billing contract and is not necessary to validate Claude trace support.

### 4.1 Question

Should the provider-neutral `internal/agenttrace` refactor be a hard prerequisite before adding the Claude parser?

### 4.2 Context & clarification

- I am asking because this is the riskiest code movement in the plan.
- Current generic logic lives in `internal/codextrace`: `Turn`, `Observation`, `TokenUsage.LangfuseUsageDetails`, `StableTraceID`, `StableSpanID`, `ExportableTurns`, redaction, terminal assembly, and insight rollup.
- Adding Claude parser logic before centralizing these helpers risks duplicated behavior or a provider parser importing a package named `codextrace` for generic logic.
- The project preference is one direct path with no logic, style, or code duplication.

### 4.3 Options

- `Option A`: Full `internal/agenttrace` extraction before Claude parser work
  - **Rubrics**: `Conf:80% | Invest:i | Blast:i | Reversal:i | Fit:i | Lib:i | Obs:i | Surface:ii | Perf:na`
  - **Approach**: Move all provider-neutral logic into `internal/agenttrace`, update Codex parser to emit that model, and only then add `internal/claudetrace`.
  - **Architecture**: Creates a clean frontend-parser to canonical-model to Langfuse-projection architecture.
  - **SSoT**: `internal/agenttrace` becomes the single owner of shared trace logic.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: Highest refactor blast radius, but best long-term maintainability and lowest duplication risk.

- `Option B`: Extract only the minimal shared model first, then consolidate remaining helpers before final acceptance
  - **Rubrics**: `Conf:65% | Invest:ii | Blast:ii | Reversal:ii | Fit:ii | Lib:ii | Obs:ii | Surface:i | Perf:na`
  - **Approach**: Move `Turn`, `Observation`, `TokenUsage`, and provider identity first; defer redaction, insight, and terminal helper movement until tests expose exact coupling points.
  - **Architecture**: Moves toward provider-neutral design gradually.
  - **SSoT**: Shared structs have one owner early, but helper ownership remains split during intermediate phases.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: Lower early blast radius, but creates a temporary split-brain design that must be cleaned before release.

### 4.4 Recommendation

Recommend Option A. Fit with the desired architecture and DRY constraints matters more than minimizing early refactor discomfort. The phase should be micro-stepped and golden-tested, but the shared model should be a hard prerequisite.

### 5.1 Question

Should fixture paths move directly from `testdata/rollouts` to provider-neutral `testdata/sources/codex` with no compatibility shim?

### 5.2 Context & clarification

- I am asking because the direct move is cleaner but touches many tests and docs.
- Today the repo source of truth says fixtures live under `testdata/rollouts` and `testdata/golden`, with inventory in `testdata/manifest.json`.
- If Claude fixtures are placed under `testdata/rollouts`, the term "rollout" stops meaning Codex rollout JSONL and becomes misleading.
- A compatibility shim would preserve old paths but violate the one-way/no-legacy preference.

### 5.3 Options

- `Option A`: Direct provider-neutral fixture path migration
  - **Rubrics**: `Conf:85% | Invest:i | Blast:i | Reversal:i | Fit:i | Lib:i | Obs:i | Surface:ii | Perf:na`
  - **Approach**: Move Codex fixture sources to `testdata/sources/codex`, add Claude fixtures under `testdata/sources/claude`, update `testdata/manifest.json`, tests, README, TESTING, and AGENTS in one change.
  - **Architecture**: Keeps one fixture registry and makes source format explicit.
  - **SSoT**: `testdata/manifest.json` remains the only fixture inventory.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: Broad test/doc diff, but clean semantics and no compatibility code.

- `Option B`: Keep Codex fixtures under `testdata/rollouts` and add Claude under `testdata/sources/claude`
  - **Rubrics**: `Conf:70% | Invest:ii | Blast:ii | Reversal:ii | Fit:ii | Lib:ii | Obs:ii | Surface:i | Perf:na`
  - **Approach**: Preserve existing Codex fixture paths and add provider metadata to the manifest, while using provider-neutral paths only for new Claude fixtures.
  - **Architecture**: Keeps existing tests more stable but leaves asymmetric naming.
  - **SSoT**: `testdata/manifest.json` remains the registry, but fixture path semantics are split.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: Lower migration blast, but preserves confusing naming and increases future cleanup pressure.

### 5.4 Recommendation

Recommend Option A. It aligns with correctness, fit, and one-way design. The migration should be direct and test-backed rather than preserving a legacy path for convenience.

### 6.1 Question

Should runtime naming remain Codex-named for MVP after adding Claude support?

### 6.2 Context & clarification

- I am asking because `codex-langfuse-exporter` and `codex-langfuse-watch.service` become semantically narrower than the supported behavior once Claude support exists.
- Current anchors are `buildinfo.InstalledBinaryName`, `buildinfo.InstalledServiceName`, `buildinfo.DefaultServiceName`, and `buildinfo.TraceName`.
- A clean rename is more accurate long term but changes install docs, service names, binary paths, tests, and user muscle memory.
- Adding aliases would broaden the surface and create two ways to do the same thing.

### 6.3 Options

- `Option A`: Rename runtime surfaces in a dedicated breaking release
  - **Rubrics**: `Conf:65% | Invest:i | Blast:i | Reversal:i | Fit:ii | Lib:i | Obs:ii | Surface:iii | Perf:na`
  - **Approach**: Rename binary, service, default service.name, docs, and install contract to a provider-neutral name in a separate planned migration.
  - **Architecture**: Produces the cleanest long-term naming model.
  - **SSoT**: `internal/buildinfo` remains the naming source of truth.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: Accurate long-term semantics, but too broad for Claude parser MVP and harder to reverse.

- `Option B`: Keep Codex-named runtime surfaces for MVP and document Claude support explicitly
  - **Rubrics**: `Conf:85% | Invest:ii | Blast:ii | Reversal:ii | Fit:i | Lib:ii | Obs:i | Surface:i | Perf:na`
  - **Approach**: Keep binary and service names unchanged; add concise docs saying the service watches Codex rollouts and drains Claude hook queue requests.
  - **Architecture**: Fits the existing install and service contract without adding aliases.
  - **SSoT**: `internal/buildinfo` remains the naming source; docs explain support scope.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: Lowest operational disruption, but name is less general than behavior.

### 6.4 Recommendation

Recommend Option B for MVP. It preserves the existing production contract and avoids alias sprawl. A rename can be planned later as one direct migration if the project scope becomes agent-neutral.

### 7.1 Question

How broad should Claude tool support be in MVP?

### 7.2 Context & clarification

- I am asking because the plan lists Bash, Read, Edit, Write, MultiEdit, WebSearch, WebFetch, MCP-like tools, Agent-like tools, and unknown tools.
- Current Codex tool observations are represented through `codextrace.Observation` and exported by `internal/langfuse.emitObservation`.
- Broad tool-specific mapping improves trace quality but can overfit unproven Claude shapes.
- A generic observation family keeps the MVP smaller while preserving visibility.

### 7.3 Options

- `Option A`: Fixture-backed canonical tool family model
  - **Rubrics**: `Conf:70% | Invest:i | Blast:i | Reversal:i | Fit:i | Lib:i | Obs:i | Surface:iii | Perf:na`
  - **Approach**: Add canonical families for every fixture-backed Claude tool type and map each to bounded metadata.
  - **Architecture**: Best long-term fit with provider-neutral rollups and provider profiles.
  - **SSoT**: `internal/agenttrace` owns tool families; `internal/claudetrace` only maps raw Claude fields.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: Richest trace semantics, but higher parser and fixture scope.

- `Option B`: Bash-specific support plus generic tool observations
  - **Rubrics**: `Conf:80% | Invest:ii | Blast:ii | Reversal:ii | Fit:ii | Lib:ii | Obs:ii | Surface:ii | Perf:na`
  - **Approach**: Give Bash first-class command/output/status metadata and map other known tools into generic observations with normalized names and safe metadata.
  - **Architecture**: Uses one observation pipeline while limiting tool-specific logic.
  - **SSoT**: `internal/agenttrace` owns generic observation families; `internal/claudetrace` owns raw pairing.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: Good MVP visibility with lower scope, but file/search/agent tools get less specialized metadata at first.

- `Option C`: Generic tool observations only
  - **Rubrics**: `Conf:75% | Invest:iii | Blast:iii | Reversal:iii | Fit:iii | Lib:iii | Obs:iii | Surface:i | Perf:na`
  - **Approach**: Normalize every Claude `tool_use` and `tool_result` into a generic observation without tool-specific semantics.
  - **Architecture**: Smallest parser footprint and simplest output.
  - **SSoT**: `internal/claudetrace` maps raw tools into the shared observation type; no specialized tool family ownership exists yet.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: Very small MVP, but weak command and verification insight for the most important tool type.

### 7.4 Recommendation

Recommend Option B. Bash is the highest-value tool for trace review and verification metadata, while generic observations avoid overbuilding unsupported tool-specific logic.

### 8.1 Question

Should live Claude/Langfuse validation be mandatory before public docs claim Claude support?

### 8.2 Context & clarification

- I am asking because the plan currently treats live validation as optional and outside the RTM.
- Existing deterministic coverage can use fake Langfuse servers and synthetic fixtures. Live validation depends on local Claude Code, user settings, and Langfuse credentials.
- Public docs can safely claim fixture-tested experimental support, but non-experimental support should be backed by at least one real hook-triggered export.

### 8.3 Options

- `Option A`: Require live validation before non-experimental Claude support claims
  - **Rubrics**: `Conf:75% | Invest:i | Blast:i | Reversal:i | Fit:i | Lib:ii | Obs:i | Surface:ii | Perf:na`
  - **Approach**: Keep docs experimental until a live `claude.turn.transcript` trace is observed from manual export and hook queue export.
  - **Architecture**: Preserves deterministic RTM while adding a release-quality live gate.
  - **SSoT**: Tests remain the release contract; CHECK-001 is a live promotion gate, not an RTM test.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: Best public-claim integrity, but depends on local external services.

- `Option B`: Claim fixture-tested experimental support without mandatory live validation
  - **Rubrics**: `Conf:85% | Invest:ii | Blast:ii | Reversal:ii | Fit:ii | Lib:i | Obs:ii | Surface:i | Perf:na`
  - **Approach**: Use fake-server and fixture tests as the binding acceptance gate, and label Claude support experimental until live smoke is performed.
  - **Architecture**: Keeps automated acceptance deterministic and secret-free.
  - **SSoT**: `go test` suites and golden fixtures own the implemented contract; docs own support wording.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: Good MVP discipline, but less real-world confidence before first use.

### 8.4 Recommendation

Recommend Option B for MVP docs and Option A before removing experimental wording. This keeps deterministic tests as the core gate while preserving public accuracy.

### 9.1 Question

Should Claude thinking content be absolutely omitted in MVP?

### 9.2 Context & clarification

- I am asking because thinking blocks are sensitive and may contain internal reasoning.
- Existing Codex privacy behavior is anchored by `internal/codextrace.ExportText`, redaction tests, and visible reasoning tests.
- Claude support should not expand the exported data surface beyond visible user prompts, assistant text, and tool results.
- This is a security and data integrity decision before it is an observability decision.

### 9.3 Options

- `Option A`: Absolute omission of all Claude thinking content
  - **Rubrics**: `Conf:90% | Invest:i | Blast:i | Reversal:ii | Fit:i | Lib:i | Obs:i | Surface:i | Perf:na`
  - **Approach**: Parser drops thinking blocks before canonical `agenttrace.Turn` construction; tests assert thinking text does not appear in trace fields, observations, metadata, tags, or golden JSON.
  - **Architecture**: Matches the current hidden-reasoning exclusion posture and keeps privacy at the parser boundary.
  - **SSoT**: `internal/claudetrace` owns raw thinking-block omission; `internal/agenttrace.ExportText` still owns redaction for exported visible fields.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: Strongest security posture, but no reasoning observability for Claude thinking.

- `Option B`: Future explicit visible-summary support only after official field verification
  - **Rubrics**: `Conf:60% | Invest:ii | Blast:ii | Reversal:i | Fit:ii | Lib:ii | Obs:ii | Surface:ii | Perf:na`
  - **Approach**: Omit thinking in MVP, but leave an ADR path for a future officially documented, user-visible summary field if such a field is verified.
  - **Architecture**: Keeps MVP secure while acknowledging a possible future public field.
  - **SSoT**: `internal/claudetrace` remains the raw-content boundary; any future summary mapping must be fixture-backed and explicit.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: More flexible long term, but introduces a future decision surface.

### 9.4 Recommendation

Recommend Option A. Security and data integrity outrank observability here. MVP should export no Claude thinking content in any form.
