# Ask-Me Record: Trace Insight Rollup Clarifying Questions

## Scope
- Project: Codex Langfuse Tracer.
- Repository: `/home/kirill/p/codex-langfuse-tracer`.
- Source plan: `plans/trace-insight-rollup-plan.md`.
- Purpose: record the clarifying questions raised during review of the trace insight rollup plan before implementation.

## Terminology
- **Trace insight rollup**: compact deterministic metadata derived from a parsed Codex turn. It is planned for `internal/codextrace/insight.go` and anchored by `codextrace.Turn`, `codextrace.Observation`, `ParseTurns`, `addObservation`, and `TerminalObservation`.
- **Turn**: one completed Codex interaction represented by `codextrace.Turn` in `internal/codextrace/model.go`. It contains `UserMessages`, `AssistantTexts`, `TokenUsage`, `TerminalEntries`, and `Observations`.
- **Command observation**: a `codex.tool.exec_command` observation emitted from `parseEventMessage` in `internal/codextrace/parser.go` for `exec_command_end` events.
- **Patch observation**: a `codex.tool.apply_patch` observation emitted from `parseEventMessage` in `internal/codextrace/parser.go` for `patch_apply_end` events. File metadata is currently produced by `FileChangeMetadata`.
- **Verification command**: a command observation classified as validation work, currently planned from command kinds `test`, `build`, `lint`, and `format`.
- **Langfuse projection**: the OpenTelemetry export path in `internal/langfuse/export.go`, especially `ExportTurn`, `EmitTurn`, `turnAttributes`, `observationAttributes`, and `metadataAttributes`.
- **Normalized contract**: deterministic JSON representation produced by `internal/tracecontract/contract.go`, especially `Trace`, `Observation`, `FromTurn`, and `normalizeObservation`.
- **Root metadata**: metadata attached at trace or `codex.agent` level for filtering and table scanning.
- **Observation metadata**: metadata attached to a single observation, currently serialized through `langfuse.observation.metadata`.
- **Lean / one way**: project constraint to avoid legacy paths, fallback implementations, duplicated classifiers, unnecessary adapters, and speculative fields.

### 1) Question 1

Should verification be modeled as a single explicit status enum instead of the current boolean-oriented plan?

### 2) Context & clarification

The plan currently says `verification_passed=false` when no verification command ran. That is locally simple, but it conflates at least three states:

- Codex made no edits and verification is not relevant.
- Codex made edits but did not run verification.
- Codex ran verification and it failed.

The current source has no verification model yet. Parsed commands enter the model through `parseEventMessage` and become `codextrace.Observation` values:

```go
case "exec_command_end":
    output := commandOutput(payload)
    addObservation(turn, "codex.tool.exec_command", timestamp, FormatCommand(payload["command"]), output, metadataWithoutLargeFields(payload, map[string]bool{
        "command": true, "stdout": true, "stderr": true, "aggregated_output": true, "formatted_output": true, "parsed_cmd": true,
    }), "tool", payload["duration"])
```

Project impact: this field will be used in Langfuse filtering. A boolean risks making chat-only turns look like failed validation.

### 3) Options

- `Option A`: Explicit verification state enum
  - **Rubrics**: `Conf:85% | Invest:a | Commit:a | Fit:a | Lib:a | Obs:a | Surface:b | Perf:na`
  - **Approach**: Replace `verification_passed` with `verification_status` values such as `not_applicable`, `not_run`, `passed`, and `failed`; keep `verification_command_count`, `last_verification_command`, and `last_verification_status`.
  - **Architecture**: Fits the planned `internal/codextrace/insight.go` functional core. `internal/tracecontract` and `internal/langfuse` consume the same derived value.
  - **SSoT**: `BuildInsightRollup` is the only place that decides verification state from observations and patch counts.
  - **System limits**: API rate limits are not relevant; no concurrency limits; time complexity is O(number of observations) and space complexity is O(1) besides metadata fields.
  - **Trade-offs**: Most correct and auditable; slightly larger contract surface than a boolean.

- `Option B`: Keep boolean plus command count
  - **Rubrics**: `Conf:70% | Invest:b | Commit:b | Fit:b | Lib:b | Obs:b | Surface:a | Perf:na`
  - **Approach**: Keep `verification_passed` and require callers to interpret it with `verification_command_count`.
  - **Architecture**: Minimal extension of the current plan, but semantics are spread across two fields.
  - **SSoT**: `BuildInsightRollup` still computes both fields, but downstream readers must combine them correctly.
  - **System limits**: API rate limits are not relevant; no concurrency limits; time complexity is O(number of observations) and space complexity is O(1).
  - **Trade-offs**: Smaller surface, but easier to misread in Langfuse tables and filters.

- `Option C`: Remove verification summary and rely on command observations
  - **Rubrics**: `Conf:65% | Invest:c | Commit:c | Fit:c | Lib:c | Obs:c | Surface:c | Perf:na`
  - **Approach**: Do not add turn-level verification metadata; users inspect `codex.tool.exec_command` observations directly.
  - **Architecture**: Uses existing observations without new rollup semantics.
  - **SSoT**: Command observation metadata remains the only source.
  - **System limits**: API rate limits are not relevant; no concurrency limits; time complexity is unchanged and space complexity adds no root fields.
  - **Trade-offs**: Smallest implementation, but fails the product need for table-level filtering of unverified or failed turns.

### 4) Recommendation

Recommend Option A. Correctness and local semantics matter more than the tiny additional field surface. A single explicit state prevents false interpretation of `false` and fits the planned one-source rollup design.

### 1) Question 2

Should the command classification enum remain as planned or be reduced before implementation?

### 2) Context & clarification

The plan currently defines `command_kind` values `test`, `build`, `lint`, `format`, `git`, `read`, `search`, `install`, `systemd`, `network`, and `other`. The parser already records command text using `FormatCommand`, but no classification exists yet.

```go
func FormatCommand(command any) string {
    parts := sliceValue(command)
    if len(parts) > 0 {
        if len(parts) >= 3 && stringValue(parts[len(parts)-2]) == "-lc" {
            return stringValue(parts[len(parts)-1])
        }
        rendered := make([]string, 0, len(parts))
        for _, part := range parts {
            rendered = append(rendered, stringValue(part))
        }
        return strings.Join(rendered, " ")
    }
    return StableJSON(command)
}
```

Project impact: enum size affects Langfuse filter usefulness and implementation complexity. `network` and `install` may be useful, but they are more likely to be noisy than validation-related classes.

### 3) Options

- `Option A`: Keep the full planned enum and freeze it
  - **Rubrics**: `Conf:80% | Invest:a | Commit:a | Fit:a | Lib:a | Obs:a | Surface:c | Perf:na`
  - **Approach**: Implement the planned enum exactly and require ADR updates for later enum expansion.
  - **Architecture**: One table-driven classifier in `internal/codextrace/insight.go`; tests in `internal/codextrace/insight_test.go`.
  - **SSoT**: The enum list and classifier live in the insight package; tests assert every value.
  - **System limits**: API rate limits are not relevant; no concurrency limits; time complexity is O(command length) and space complexity is O(1).
  - **Trade-offs**: Best immediate observability; larger metadata vocabulary to maintain.

- `Option B`: Keep only validation and repo-navigation categories
  - **Rubrics**: `Conf:75% | Invest:b | Commit:b | Fit:b | Lib:b | Obs:b | Surface:b | Perf:na`
  - **Approach**: Use `test`, `build`, `lint`, `format`, `git`, `read`, `search`, and `other`; omit `install`, `systemd`, and `network`.
  - **Architecture**: Same implementation shape with fewer cases.
  - **SSoT**: `internal/codextrace/insight.go` remains the only classifier source.
  - **System limits**: API rate limits are not relevant; no concurrency limits; time complexity is O(command length) and space complexity is O(1).
  - **Trade-offs**: Leaner and focused on common coding-agent actions; loses visibility into setup and service operations.

- `Option C`: Verification-only classification
  - **Rubrics**: `Conf:65% | Invest:c | Commit:c | Fit:c | Lib:c | Obs:c | Surface:a | Perf:na`
  - **Approach**: Classify only `test`, `build`, `lint`, `format`, and `other`.
  - **Architecture**: Minimal implementation in `internal/codextrace/insight.go`.
  - **SSoT**: The classifier only exists to support verification status.
  - **System limits**: API rate limits are not relevant; no concurrency limits; time complexity is O(command length) and space complexity is O(1).
  - **Trade-offs**: Smallest enum; weakens the broader goal of understanding Codex actions.

### 4) Recommendation

Recommend Option A with a fixed enum and no expansion during implementation. The planned categories are still bounded and useful for trace insight. The constraint should be no more categories without evidence, not premature shrinking.

### 1) Question 3

Should root insight metadata be attached only at the trace or `codex.agent` level instead of repeated on every observation?

### 2) Context & clarification

The current exporter appends `metadataAttributes(turn)` to both turn-level and observation-level attributes:

```go
func turnAttributes(turn codextrace.Turn, environment, observationType string, includeTraceIO bool) []attribute.KeyValue {
    attrs := baseObservationAttributes(turn, environment, observationType, turn.InputText(), turn.OutputText())
    attrs = append(attrs, metadataAttributes(turn)...)
    return attrs
}

func observationAttributes(turn codextrace.Turn, observation codextrace.Observation, environment string) []attribute.KeyValue {
    attrs := baseObservationAttributes(turn, environment, observation.Type, observation.Input, observation.Output)
    attrs = append(attrs, metadataAttributes(turn)...)
    if len(observation.Metadata) > 0 {
        attrs = append(attrs, attribute.String("langfuse.observation.metadata", jsonString(observation.Metadata)))
    }
    return attrs
}
```

If root rollup fields are added to `metadataAttributes(turn)`, each observation could receive duplicated trace-level rollup data. That conflicts with the lean/DRY goal and can clutter Langfuse observation views.

### 3) Options

- `Option A`: Split root metadata from observation metadata
  - **Rubrics**: `Conf:90% | Invest:a | Commit:a | Fit:a | Lib:a | Obs:a | Surface:b | Perf:na`
  - **Approach**: Keep session and turn identifiers where needed, but add insight rollup only to trace or `codex.agent` metadata; attach command-specific fields only to command observations.
  - **Architecture**: Small refactor in `internal/langfuse/export.go`; `codextrace.InsightRollup` remains the source.
  - **SSoT**: Rollup logic lives in `internal/codextrace/insight.go`; projection functions only decide placement.
  - **System limits**: API rate limits are not relevant; no concurrency limits; time complexity is O(number of attributes) and space complexity reduces duplicated attributes.
  - **Trade-offs**: Clearest semantics and least metadata duplication; requires touching exporter helper structure.

- `Option B`: Keep current shared helper and append rollup everywhere
  - **Rubrics**: `Conf:80% | Invest:b | Commit:b | Fit:b | Lib:b | Obs:b | Surface:a | Perf:na`
  - **Approach**: Add rollup fields to `metadataAttributes(turn)` as the plan currently implies.
  - **Architecture**: Minimal change to the existing shared metadata helper.
  - **SSoT**: Rollup values are still computed once, but projection duplicates them across spans.
  - **System limits**: API rate limits are not relevant; no concurrency limits; time complexity and space usage grow with number of observations.
  - **Trade-offs**: Fastest implementation; creates noisy repeated metadata.

- `Option C`: Put all insight metadata only in normalized contracts, not Langfuse
  - **Rubrics**: `Conf:60% | Invest:c | Commit:c | Fit:c | Lib:c | Obs:c | Surface:c | Perf:na`
  - **Approach**: Avoid Langfuse metadata changes and keep insight values in `internal/tracecontract` output only.
  - **Architecture**: Uses the existing golden contract path but bypasses the production observability goal.
  - **SSoT**: `internal/tracecontract` consumes rollup output; Langfuse does not.
  - **System limits**: API rate limits are not relevant; no concurrency limits; time complexity is O(number of observations) for contract generation only.
  - **Trade-offs**: Smallest production exporter change; does not satisfy trace table/filter use cases.

### 4) Recommendation

Recommend Option A. It best follows correctness, fit, DRY, and minimal surface. Root metadata belongs on the root trace/agent; observation metadata should describe the observation.

### 1) Question 4

Should Langfuse root metadata include full changed file paths or only compact file-impact summaries?

### 2) Context & clarification

`FileChangeMetadata` already extracts full file paths and change categories from `apply_patch` payloads:

```go
func FileChangeMetadata(changes map[string]any) map[string]any {
    changedFiles := []string{}
    fileChangeTypes := map[string]string{}
    for path, raw := range changes {
        change := mapValue(raw)
        changeType := firstString(change["type"], "change")
        changedFiles = append(changedFiles, path)
        fileChangeTypes[path] = changeType
    }
    return map[string]any{
        "changed_files":      changedFiles,
        "file_change_types":  fileChangeTypes,
        "changed_file_count": len(changedFiles),
    }
}
```

Project impact: full paths are useful for audits, but root metadata arrays can clutter Langfuse and expose repository structure. The plan already excludes per-file observation fanout.

### 3) Options

- `Option A`: Store bounded full path list plus compact summaries
  - **Rubrics**: `Conf:80% | Invest:a | Commit:a | Fit:a | Lib:a | Obs:a | Surface:c | Perf:na`
  - **Approach**: Root metadata includes `changed_file_count`, sorted bounded `changed_files`, `changed_extensions`, and `touched_test_files`.
  - **Architecture**: Reuses `apply_patch` metadata and centralizes rollup in `internal/codextrace/insight.go`.
  - **SSoT**: `FileChangeMetadata` remains the structured source for patch file paths; rollup only sorts, dedupes, and bounds.
  - **System limits**: API rate limits are not relevant; no concurrency limits; time complexity is O(number of changed files log number of changed files) for sorting; space complexity is O(number of changed files).
  - **Trade-offs**: Most auditable; higher metadata surface and potential path exposure.

- `Option B`: Store compact summaries only
  - **Rubrics**: `Conf:85% | Invest:b | Commit:b | Fit:b | Lib:b | Obs:b | Surface:a | Perf:na`
  - **Approach**: Root metadata includes `changed_file_count`, `changed_extensions`, and `touched_test_files`; full `changed_files` remains only on `codex.tool.apply_patch`.
  - **Architecture**: Keeps detailed file paths on the existing patch observation and root metadata compact.
  - **SSoT**: `FileChangeMetadata` remains the detailed source; `BuildInsightRollup` emits only compact root summaries.
  - **System limits**: API rate limits are not relevant; no concurrency limits; time complexity is O(number of changed files log number of changed files) for sorting summaries; space complexity is O(number of extensions and test paths).
  - **Trade-offs**: Better privacy and table cleanliness; less direct filtering by exact file path at trace root.

- `Option C`: Store only counts
  - **Rubrics**: `Conf:70% | Invest:c | Commit:c | Fit:c | Lib:c | Obs:c | Surface:b | Perf:na`
  - **Approach**: Root metadata includes only `changed_file_count` and maybe `patch_count`.
  - **Architecture**: Minimal rollup over existing patch observations.
  - **SSoT**: Detailed file metadata remains exclusively inside `codex.tool.apply_patch`.
  - **System limits**: API rate limits are not relevant; no concurrency limits; time complexity is O(number of observations) and space complexity is O(1).
  - **Trade-offs**: Cleanest metadata; too little insight into what Codex changed.

### 4) Recommendation

Recommend Option B. It keeps root metadata lean and useful while preserving full path details in the existing patch observation. This follows security/data integrity before observability breadth.

### 1) Question 5

Should exec command duration be normalized to only `duration_ms` instead of exporting both raw nested `duration` and normalized duration?

### 2) Context & clarification

Exec command observations currently receive span timing via `ObservationBounds`, while raw payload metadata can include nested `duration` unless excluded:

```go
func addObservation(turn *Turn, name, timestamp, input, output string, metadata map[string]any, observationType string, duration any) {
    startNS, endNS := ObservationBounds(firstString(timestamp, firstString(turn.EndTS, turn.StartTS)), duration)
    turn.Observations = append(turn.Observations, Observation{
        StartTimeUnixNS: startNS,
        EndTimeUnixNS:   endNS,
        Metadata:        metadata,
    })
}
```

Project impact: keeping raw nested duration and normalized `duration_ms` duplicates timing in different formats. A single normalized metadata field is easier to filter and test.

### 3) Options

- `Option A`: Normalize command metadata to `duration_ms` only
  - **Rubrics**: `Conf:85% | Invest:a | Commit:a | Fit:a | Lib:a | Obs:a | Surface:a | Perf:na`
  - **Approach**: Exclude raw `duration` from exec command metadata and add `duration_ms` through the insight helper. Span start/end timing remains unchanged.
  - **Architecture**: Fits `internal/codextrace/insight.go` as the only command insight source and keeps `ObservationBounds` responsible for span timing.
  - **SSoT**: `ObservationBounds` owns span time; insight metadata owns `duration_ms`.
  - **System limits**: API rate limits are not relevant; no concurrency limits; time complexity is O(1) per command; space complexity is O(1).
  - **Trade-offs**: Cleanest contract and easiest filtering; removes raw nested duration from command metadata.

- `Option B`: Keep raw `duration` and add `duration_ms`
  - **Rubrics**: `Conf:75% | Invest:b | Commit:b | Fit:b | Lib:b | Obs:b | Surface:b | Perf:na`
  - **Approach**: Preserve current metadata shape and add normalized duration as a convenience field.
  - **Architecture**: Minimal change to parser behavior.
  - **SSoT**: Duration source remains the rollout payload, but two metadata representations exist.
  - **System limits**: API rate limits are not relevant; no concurrency limits; time complexity is O(1) per command; space complexity is slightly higher per command.
  - **Trade-offs**: Lower implementation risk; violates the no-duplication preference.

- `Option C`: Rely only on span timing and omit duration metadata
  - **Rubrics**: `Conf:70% | Invest:c | Commit:c | Fit:c | Lib:c | Obs:c | Surface:c | Perf:na`
  - **Approach**: Do not export `duration_ms`; users inspect span latency.
  - **Architecture**: Uses existing OTel span timing only.
  - **SSoT**: `ObservationBounds` is the only timing representation.
  - **System limits**: API rate limits are not relevant; no concurrency limits; time complexity and space complexity are unchanged.
  - **Trade-offs**: Least metadata; weaker table/filter ergonomics for command duration.

### 4) Recommendation

Recommend Option A. It is the cleanest single representation: span timing for traces and `duration_ms` for command metadata filters.

### 1) Question 6

Should Phase P00 remain as a separate metadata contract phase or be folded into implementation?

### 2) Context & clarification

P00 currently creates failing golden contract tests before production code changes. This follows verification-first planning, but it is a separate contract-bookkeeping phase in a small repo.

Relevant local test anchors:

- `test/contract_fixture_test.go`
- `test/contract_test.go`
- `testdata/golden/complete-tools.normalized.json`
- `internal/tracecontract/contract.go`

Project impact: separate P00 improves traceability and protects Go contract drift, but it adds phase overhead.

### 3) Options

- `Option A`: Keep P00 separate and small
  - **Rubrics**: `Conf:85% | Invest:a | Commit:a | Fit:a | Lib:a | Obs:a | Surface:b | Perf:na`
  - **Approach**: Keep P00 as the contract-first phase, but limit it to failing schema/golden tests and expected metadata keys.
  - **Architecture**: Fits the existing golden fixture architecture and formal plan structure.
  - **SSoT**: Golden expectations live in `testdata/golden`; schema checks live in `test/contract_fixture_test.go`.
  - **System limits**: API rate limits are not relevant; tests run locally with no concurrency beyond Go test parallelism; time complexity is O(size of golden fixture) and space complexity is O(size of golden fixture).
  - **Trade-offs**: Strongest verification discipline; some phase overhead.

- `Option B`: Merge P00 into P02/P03 implementation phases
  - **Rubrics**: `Conf:75% | Invest:b | Commit:b | Fit:b | Lib:b | Obs:b | Surface:a | Perf:na`
  - **Approach**: Create failing fixture and projection tests immediately before implementing the relevant code in P02/P03.
  - **Architecture**: Still uses existing test files, but reduces formal phase count.
  - **SSoT**: Golden expectations remain in `testdata/golden`; implementation remains in `internal/codextrace` and projection packages.
  - **System limits**: API rate limits are not relevant; tests run locally with no concurrency beyond Go test parallelism; time complexity is O(size of fixtures) and space complexity is O(size of fixtures).
  - **Trade-offs**: Leaner execution plan; weaker standalone contract-freeze step.

- `Option C`: Drop golden contract-first testing
  - **Rubrics**: `Conf:55% | Invest:c | Commit:c | Fit:c | Lib:c | Obs:c | Surface:c | Perf:na`
  - **Approach**: Implement code first and update golden fixtures afterward.
  - **Architecture**: Uses existing tests only after implementation.
  - **SSoT**: Contract expectations are updated after code exists.
  - **System limits**: API rate limits are not relevant; tests run locally with no concurrency beyond Go test parallelism; time complexity is O(size of fixtures) and space complexity is O(size of fixtures).
  - **Trade-offs**: Lowest ceremony; conflicts with verification-first requirements and risks rubber-stamping drift.

### 4) Recommendation

Recommend Option A. The repo is small, but this change is primarily about a trace contract. Keeping P00 small preserves verification-first behavior without adding runtime complexity.
