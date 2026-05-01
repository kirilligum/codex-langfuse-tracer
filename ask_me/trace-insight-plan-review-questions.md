# Ask-Me Record: Trace Insight Plan Review

## Scope
- Project: Codex Langfuse Tracer.
- Repository: `/home/kirill/p/codex-langfuse-tracer`.
- Source plan: `plans/trace-insight-rollup-plan.md`.
- Status: no clarifying questions were asked during the plan review.

## Terminology
- **Plan**: `plans/trace-insight-rollup-plan.md`, the standalone SRS/test/implementation plan for trace insight rollups.
- **Lean / one way**: the project constraint to avoid legacy branches, fallback paths, adapters, duplicate logic, and speculative safety for unlikely situations.
- **Trace insight rollup**: planned deterministic metadata derived from parsed Codex turn observations. It is anchored in the current codebase by `codextrace.Turn`, `codextrace.Observation`, `ParseTurns`, `addObservation`, and `TerminalObservation` in `internal/codextrace/`.
- **Command observation**: a `codex.tool.exec_command` observation emitted from `parseEventMessage` in `internal/codextrace/parser.go`.
- **Patch observation**: a `codex.tool.apply_patch` observation emitted from `parseEventMessage` in `internal/codextrace/parser.go`; planned file impact metadata is derived from this existing observation metadata.
- **Langfuse projection**: the OTel/Langfuse export path in `internal/langfuse/export.go`, anchored by `ExportTurn`, `EmitTurn`, `emitObservation`, `turnAttributes`, `observationAttributes`, and `metadataAttributes`.
- **Normalized contract**: the language-agnostic fixture contract in `internal/tracecontract/contract.go`, anchored by `Trace`, `Observation`, `FromTurn`, and `normalizeObservation`.
- **Planned single implementation path**: `internal/codextrace/insight.go`, the planned single file for command classification, failure metadata, and turn rollup logic.

## Clarifying Questions
No clarifying questions were asked in the preceding plan-review exchange. The plan was updated directly from verified repository context and explicit constraints:

- Prefer one direct implementation path.
- Avoid legacy behavior, fallback paths, adapters, and duplicate logic.
- Prefer simple general code over speculative optimization.
- Keep trace additions metadata-only and deterministic.
- Avoid hidden reasoning export, new observation families, and new configuration.

Because no individual clarifying question was asked, there are no ranked option sets, rubrics, or recommendations to record here. Creating fictional questions would make the ask-me record less accurate.

## Auditable Decision Summary
- Decision: remove `primary_language` from the plan.
  - Reason: language guessing adds mapping logic without enough value; `changed_extensions` is simpler and directly derived from known file paths.
- Decision: restrict `failure_type` to `none`, `nonzero_exit`, `timeout`, and `unknown`.
  - Reason: detailed categories such as network or missing-file causes would require brittle command-output parsing.
- Decision: keep one implementation file for the new insight logic.
  - Reason: `internal/codextrace/insight.go` centralizes classification, failure metadata, and rollup derivation; `internal/tracecontract` and `internal/langfuse` should consume that output directly.
- Decision: keep live Langfuse smoke as a production check after automated acceptance.
  - Reason: automated tests remain the binding acceptance path; live smoke validates the installed watcher and real Langfuse export path.
