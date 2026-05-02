# Langfuse Cost Pricing Clarifying Questions

## Purpose

This ask-me record captures open decisions for implementing robust Langfuse cost tracking in the Codex Langfuse Tracer repository. The target behavior is one direct cost path: Codex rollout token usage is exported as Langfuse model and usage facts, Langfuse model definitions provide pricing, and Langfuse calculates the UI/API `Total Cost` fields. The implementation should avoid legacy paths, fallbacks, duplicated logic, local cost multiplication, and unnecessary abstraction.

## Terminology and code anchors

- `Total Cost`: The Langfuse UI/API cost value expected to become nonzero after ingestion. The local plan names observation-level `calculatedTotalCost` and trace-level `totalCost` as the fields to verify.
- Native Langfuse cost path: The exporter emits `langfuse.observation.model.name` and `langfuse.observation.usage_details`; Langfuse joins those usage facts with model pricing. Code anchors: `internal/langfuse.ExportTurn`, `internal/langfuse.transcriptAttributes`, `internal/langfuse.AuthHeader`, and README cost-tracking text.
- Local cost engine: Any exporter-side multiplication of token counts by prices or emission of `cost_details`. This is intentionally out of scope because it would duplicate Langfuse's cost engine.
- Model setup: The planned direct setup path that lists Langfuse models and creates missing required model definitions through `/api/public/models`. Planned anchors: `internal/langfuse/models.go`, `cmd/codex-langfuse-exporter --sync-model-pricing`, and `install.sh`.
- Source mode: The mutually exclusive CLI mode selected by `cmd/codex-langfuse-exporter.options.Mode()`. Current modes are `--session-id`, `--path`, `--latest`, and `--watch`.
- Strict model list: A source-dated set of Codex model names that this repository knows how to price, currently planned as `gpt-5.5`, `gpt-5.4`, and `gpt-5.4-mini`.
- Existing model conflict: A Langfuse model definition already exists for a managed Codex model name but has a different match pattern or price keys than the repository's required definition.
- Source-dated pricing: Pricing values stored with an explicit external source URL and date so future maintainers know when the prices were captured.
- Token buckets: `internal/codextrace.TokenUsage` fields: `InputTokens`, `OutputTokens`, `TotalTokens`, `CachedInputTokens`, and `ReasoningOutputTokens`.
- Canonical usage projection: The planned `TokenUsage.LangfuseUsageDetails()` function that centralizes conversion from Codex token buckets to Langfuse `usage_details` keys.
- Live cost proof: The env-gated test in `internal/langfuse/live_cost_test.go`, currently planned around session `019de6d8-fb40-74a0-af20-46b088397c53`.

Relevant current source anchors:

```go
// internal/codextrace/model.go
type TokenUsage struct {
    InputTokens           int
    OutputTokens          int
    TotalTokens           int
    CachedInputTokens     int
    ReasoningOutputTokens int
}
```

```go
// cmd/codex-langfuse-exporter/main.go
func (o options) Mode() string {
    switch {
    case o.SessionID != "":
        return "session-id"
    case o.Path != "":
        return "path"
    case o.Latest:
        return "latest"
    case o.Watch:
        return "watch"
    default:
        return ""
    }
}
```

```go
// internal/langfuse/export.go
attrs = append(attrs, attribute.String("langfuse.observation.model.name", turn.Model))
attrs = append(attrs, attribute.String("langfuse.observation.usage_details", jsonString(usage)))
```

```bash
# install.sh
(cd "$repo_dir" && go build -o "$exporter_dst" ./cmd/codex-langfuse-exporter)
install -m 644 "$service_src" "$service_dst"
systemctl --user restart codex-langfuse-watch.service
```

## Evidence boundaries

- Local implementation evidence: This repository currently has Go code under `cmd/`, `internal/`, and `test/`, no package.json, no Makefile, and no CI workflow. `install.sh` builds the exporter and restarts the systemd user service. `internal/langfuse/export.go` exports OTLP traces to `/api/public/otel/v1/traces`.
- External contract evidence: Langfuse model API shape, Langfuse cost-calculation semantics, and OpenAI pricing are vendor-managed contracts. Before implementation changes that depend on those contracts, verify them against local Langfuse source/API schemas or official vendor documentation.
- Assumption: The plan's model API endpoints and pricing-tier shape are valid for the local Langfuse instance, but implementation should re-check the contract before coding the HTTP payloads.

### 1.1 Question

Should model coverage stay as a strict source-dated model list, or should setup derive required model names from recent Codex rollout data and fail when pricing is missing?

### 1.2 Context & clarification

- This question controls the source of truth for which Codex models get Langfuse pricing definitions.
- The current plan uses a strict model list: `gpt-5.5`, `gpt-5.4`, and `gpt-5.4-mini`.
- The current parser records the model on `codextrace.Turn.Model`; `transcriptAttributes` exports it as `langfuse.observation.model.name`.
- A strict model list keeps pricing explicit and reviewable. Deriving model names from rollout data can catch drift earlier, but it adds rollout scanning and introduces a second concern into setup.
- The implementation should not guess prices for unknown model names. If a model is not in the pricing catalog, correctness requires a failure or a deliberate no-cost export until pricing is added.

### 1.3 Options

- `Option A`: Explicit pricing catalog plus observed-model validation
  - **Rubrics**: `Conf:70% | Invest:i | Blast:i | Reversal:ii | Fit:ii | Lib:i | Obs:i | Surface:iii | Perf:ii`
  - **Approach**: Keep a source-dated pricing catalog and add a validation step that scans selected local rollout fixtures or recent rollout files for model names. Setup fails if an observed model lacks a catalog entry.
  - **Architecture**: Uses `codextrace.ParseTurns` and `Turn.Model` for validation while keeping model creation inside `internal/langfuse`.
  - **SSoT**: Pricing values live only in the catalog. Rollout data is used only to validate coverage, not to invent pricing.
  - **System limits**: API rate limits are not involved in local scanning. File scan complexity is O(number of selected rollout files). External model API rate limits are `"Unknown - not available in local context."`
  - **Trade-offs**: Strongest drift detection, but broader than the direct setup path and risks coupling install behavior to local rollout history.

- `Option B`: Explicit pricing catalog plus fixture coverage
  - **Rubrics**: `Conf:85% | Invest:ii | Blast:ii | Reversal:iii | Fit:i | Lib:ii | Obs:ii | Surface:i | Perf:iii`
  - **Approach**: Keep the strict supported model list in code and test that repository fixtures and planned model definitions are covered by that catalog.
  - **Architecture**: Fits the current Go test structure and keeps setup focused on Langfuse model definitions.
  - **SSoT**: The pricing catalog is the only model-pricing source. Tests guard repository-known model coverage.
  - **System limits**: Test runtime is bounded by fixture size. Langfuse API rate limits are `"Unknown - not available in local context."`
  - **Trade-offs**: Simple, direct, and maintainable. It will not catch a brand-new local Codex model until fixtures or explicit pricing records are updated.

- `Option C`: Minimal strict seed list only
  - **Rubrics**: `Conf:80% | Invest:iii | Blast:iii | Reversal:i | Fit:iii | Lib:iii | Obs:iii | Surface:ii | Perf:i`
  - **Approach**: Seed only the planned model names and document that new model names require a pricing catalog update.
  - **Architecture**: Smallest code change and closest to the current plan.
  - **SSoT**: The pricing catalog remains authoritative, but there is less automated evidence that the catalog covers repository data.
  - **System limits**: Runtime is O(number of configured model definitions). Langfuse API rate limits are `"Unknown - not available in local context."`
  - **Trade-offs**: Lowest implementation cost, but lower drift visibility.

### 1.4 Recommendation

Recommend `Option B`. It preserves one explicit pricing source, avoids deriving behavior from incidental local history, and gives enough verification through repository fixtures. It best follows correctness, fit with existing architecture, simplicity, and observability without widening install behavior.

### 2.1 Question

When a Langfuse project already has a model definition for a managed Codex model name with different prices or match pattern, should setup fail or update the existing definition?

### 2.2 Context & clarification

- A conflict can make `Total Cost` wrong even when token usage is exported correctly.
- The planned setup path uses `/api/public/models` to list and create model definitions. Update and delete semantics are not verified in this repository.
- Updating an existing Langfuse model definition could overwrite user-owned pricing policy. Failing is stricter but preserves data integrity.
- The code anchor for authentication is `internal/langfuse.AuthHeader`; planned model setup should reuse the same credential source from `internal/config.Load`.

### 2.3 Options

- `Option A`: Explicit owned-update mode after API ownership is verified
  - **Rubrics**: `Conf:50% | Invest:i | Blast:i | Reversal:i | Fit:ii | Lib:i | Obs:i | Surface:iii | Perf:na`
  - **Approach**: Add a separate explicit update command only if the Langfuse API supports stable update semantics and the implementation can prove the definition is owned by this tracer.
  - **Architecture**: Would require extending the planned `internal/langfuse/models.go` boundary and adding ownership metadata or a separate registry if the external API supports it.
  - **SSoT**: Pricing still lives in the source-dated catalog. Ownership state must have one verified source, otherwise this option should not proceed.
  - **System limits**: Update/delete endpoint behavior and rate limits are `"Unknown - not available in local context."`
  - **Trade-offs**: Most complete long-term path for managed definitions, but currently assumption-heavy and likely overbuilt for this repository.

- `Option B`: Fail on conflict with structured diagnostics
  - **Rubrics**: `Conf:90% | Invest:ii | Blast:ii | Reversal:ii | Fit:i | Lib:ii | Obs:ii | Surface:i | Perf:na`
  - **Approach**: If an existing model differs from the catalog, return a non-secret error naming the model and mismatched fields. Do not issue PATCH, PUT, or DELETE.
  - **Architecture**: Keeps model setup to list/create only and matches the current direct setup plan.
  - **SSoT**: The catalog defines required values; Langfuse remains the store of applied model definitions.
  - **System limits**: Uses one list request and zero create requests for conflicting definitions. API rate limits are `"Unknown - not available in local context."`
  - **Trade-offs**: Best data-integrity posture and easiest to reason about. Requires a human to resolve conflicts in Langfuse or approve a future explicit update design.

- `Option C`: Treat existing Langfuse definitions as authoritative
  - **Rubrics**: `Conf:60% | Invest:iii | Blast:iii | Reversal:iii | Fit:iii | Lib:iii | Obs:iii | Surface:ii | Perf:na`
  - **Approach**: If a model exists, skip it even when its prices differ.
  - **Architecture**: Small implementation, but it weakens the repo's ability to prove cost correctness.
  - **SSoT**: Langfuse becomes the practical source of truth for conflicting prices, while the repo catalog becomes advisory.
  - **System limits**: Uses one list request. API rate limits are `"Unknown - not available in local context."`
  - **Trade-offs**: Lowest operational friction, but it can silently preserve wrong costs. This is acceptable only if local Langfuse is intentionally managed outside the repo.

### 2.4 Recommendation

Recommend `Option B`. Correctness and data integrity outrank convenience here. The codebase should fail fast on conflicting cost policy and avoid update/delete behavior until the external API and ownership model are verified.

### 3.1 Question

Should `install.sh` fail before restarting the watcher if model pricing setup fails, or should install continue and warn?

### 3.2 Context & clarification

- `install.sh` currently builds `codex-langfuse-exporter`, installs the service file, reloads systemd, enables the service, and restarts `codex-langfuse-watch.service`.
- The planned setup adds `~/.codex/bin/codex-langfuse-exporter --sync-model-pricing --quiet` after the binary build and before service restart.
- If install restarts the watcher without pricing setup, new traces can still export but `Total Cost` can remain zero.
- If install blocks on pricing setup, it surfaces misconfiguration immediately but makes installation stricter.

### 3.3 Options

- `Option A`: Block restart on pricing setup failure
  - **Rubrics**: `Conf:90% | Invest:i | Blast:i | Reversal:ii | Fit:i | Lib:ii | Obs:i | Surface:i | Perf:na`
  - **Approach**: Keep `set -euo pipefail`; run pricing setup before `systemctl --user restart`; any setup error exits install.
  - **Architecture**: Fits the existing fail-fast shell style and keeps setup in the installed binary, not shell HTTP code.
  - **SSoT**: The binary owns Langfuse setup logic. `install.sh` only sequences build, setup, and service restart.
  - **System limits**: One setup call per install. Langfuse API rate limits are `"Unknown - not available in local context."`
  - **Trade-offs**: Best correctness and observability. A transient Langfuse outage blocks service restart even though trace export could otherwise work.

- `Option B`: Install binary but skip restart on setup failure
  - **Rubrics**: `Conf:75% | Invest:ii | Blast:ii | Reversal:i | Fit:ii | Lib:i | Obs:ii | Surface:ii | Perf:na`
  - **Approach**: Build and install the binary and service file, run pricing setup, and if setup fails, exit nonzero before restart while leaving installed files in place.
  - **Architecture**: Mostly matches the current script but introduces a partially installed state.
  - **SSoT**: Setup logic still lives in the binary; install state becomes less atomic.
  - **System limits**: One setup call per install. Langfuse API rate limits are `"Unknown - not available in local context."`
  - **Trade-offs**: Easier retry after fixing credentials, but the installed files may be newer than the running service.

- `Option C`: Continue install and restart watcher with a warning
  - **Rubrics**: `Conf:70% | Invest:iii | Blast:iii | Reversal:iii | Fit:iii | Lib:iii | Obs:iii | Surface:iii | Perf:na`
  - **Approach**: Run pricing setup, print a warning on failure, and restart the watcher anyway.
  - **Architecture**: Keeps trace export uptime higher but accepts a known broken cost precondition.
  - **SSoT**: Setup logic remains in the binary, but the operational contract no longer guarantees cost readiness after install.
  - **System limits**: One setup call per install. Langfuse API rate limits are `"Unknown - not available in local context."`
  - **Trade-offs**: Pragmatic for export availability, but weak for verified cost correctness and easy to miss in unattended installs.

### 3.4 Recommendation

Recommend `Option A`. The purpose of this work is reliable cost visibility, so install should fail before restarting a watcher that is not cost-ready. This follows correctness, fail-fast behavior, and the one-way setup path.

### 4.1 Question

Should pricing values be source-dated constants in the repository, or should pricing be loaded from a required config file?

### 4.2 Context & clarification

- Pricing is external policy owned by OpenAI, while Langfuse model definitions are local project state.
- The repository currently loads only Langfuse connection settings from `[mcp_servers.langfuse.env]` in `~/.codex/config.toml` through `internal/config.Load`.
- Adding pricing to runtime config would expand user-managed config and increase validation surface.
- Keeping pricing in a source-dated catalog makes changes reviewable and testable, but a price update requires a code/documentation change.

### 4.3 Options

- `Option A`: Repo-owned source-dated pricing data file
  - **Rubrics**: `Conf:70% | Invest:i | Blast:i | Reversal:ii | Fit:ii | Lib:i | Obs:i | Surface:ii | Perf:na`
  - **Approach**: Add a small repo-owned JSON or TOML pricing file and parse it during sync. Tests validate schema, source date, and required keys.
  - **Architecture**: Treats pricing as data rather than code, but adds parser and schema handling.
  - **SSoT**: The pricing file is the only pricing source. Code only validates and applies it.
  - **System limits**: File parse is local and bounded by catalog size. External pricing-source limits are `"Unknown - not available in local context."`
  - **Trade-offs**: Easier price edits without Go changes, but more moving parts and another file format to maintain.

- `Option B`: Go catalog with source URL and source date
  - **Rubrics**: `Conf:85% | Invest:ii | Blast:ii | Reversal:iii | Fit:i | Lib:ii | Obs:ii | Surface:i | Perf:na`
  - **Approach**: Store required prices in one `internal/langfuse` catalog with source URL/date constants and table-driven tests.
  - **Architecture**: Fits the current Go-only repository and avoids new config shape.
  - **SSoT**: One catalog function or variable owns model prices. Tests and docs refer to that catalog.
  - **System limits**: Runtime is O(number of catalog entries). External pricing-source limits are `"Unknown - not available in local context."`
  - **Trade-offs**: Smallest maintainable path. Price changes require a code review, which is useful for billing-affecting behavior.

- `Option C`: Required user config pricing
  - **Rubrics**: `Conf:60% | Invest:iii | Blast:iii | Reversal:i | Fit:iii | Lib:iii | Obs:iii | Surface:iii | Perf:na`
  - **Approach**: Extend config loading so users must provide price values in `~/.codex/config.toml`.
  - **Architecture**: Reuses `internal/config.Load`, but mixes local credentials with pricing policy.
  - **SSoT**: User config becomes the pricing source, making repository tests rely on fixtures or default examples.
  - **System limits**: Local config parse only. External pricing-source limits are `"Unknown - not available in local context."`
  - **Trade-offs**: Flexible, but higher setup burden and more ways for costs to be wrong or missing.

### 4.4 Recommendation

Recommend `Option B`. Source-dated Go catalog values keep billing-affecting behavior reviewable, typed, and covered by the normal Go test suite without expanding user config or adding a parser.

### 5.1 Question

How should cached input and reasoning output be represented in Langfuse `usage_details` so cost is accurate and not double-counted?

### 5.2 Context & clarification

- Current source has `TokenUsage` fields for total input/output plus detailed cached and reasoning buckets.
- Current `transcriptAttributes` emits `input`, `output`, `total`, `cached_input`, and `reasoning_output`.
- The cost plan proposes a canonical projection with Langfuse pricing keys: `input`, `input_cached_tokens`, `output`, and `output_reasoning_tokens`.
- The key semantic decision is whether parent buckets are gross values or net values. If Langfuse multiplies every usage key that has a price, gross parent buckets plus detailed buckets can double-count.
- External contract assumption: Langfuse cost calculation sums priced usage-detail keys independently. This should be verified against local Langfuse source or API behavior before implementation.

### 5.3 Options

- `Option A`: Net parent buckets plus priced detail buckets
  - **Rubrics**: `Conf:80% | Invest:i | Blast:i | Reversal:ii | Fit:i | Lib:i | Obs:i | Surface:i | Perf:na`
  - **Approach**: `input = InputTokens - CachedInputTokens`, `input_cached_tokens = CachedInputTokens`, `output = OutputTokens - ReasoningOutputTokens`, and `output_reasoning_tokens = ReasoningOutputTokens`. Clamp negative derived buckets to zero or fail test fixtures that would produce invalid buckets.
  - **Architecture**: Centralizes logic in `TokenUsage.LangfuseUsageDetails()` and removes duplicated usage maps from exporter and contract code.
  - **SSoT**: `TokenUsage.LangfuseUsageDetails()` is the only source for cost usage projection.
  - **System limits**: Pure in-memory O(1) mapping per turn. External Langfuse cost semantics are `"Unknown - not available in local context."`
  - **Trade-offs**: Best protection against double charging and keeps detailed cost keys available. Requires clear tests for subtraction semantics.

- `Option B`: Gross parent buckets, detail buckets as unpriced metadata
  - **Rubrics**: `Conf:65% | Invest:ii | Blast:ii | Reversal:i | Fit:ii | Lib:ii | Obs:ii | Surface:ii | Perf:na`
  - **Approach**: Keep `input` and `output` as raw totals in `usage_details`, but move cached/reasoning details to metadata or unpriced keys.
  - **Architecture**: Simpler cost shape, but separates cost usage from detailed token observability.
  - **SSoT**: `TokenUsage.LangfuseUsageDetails()` owns cost usage; metadata projection would need a second, clearly named helper.
  - **System limits**: Pure in-memory O(1) mapping per turn. External Langfuse cost semantics are `"Unknown - not available in local context."`
  - **Trade-offs**: Avoids double-counting if only parent keys are priced, but loses discounted cached input unless Langfuse prices are modeled elsewhere.

- `Option C`: Gross parent buckets plus priced detail buckets
  - **Rubrics**: `Conf:45% | Invest:iii | Blast:iii | Reversal:iii | Fit:iii | Lib:iii | Obs:iii | Surface:iii | Perf:na`
  - **Approach**: Emit raw `input` and `output` totals and also emit detailed cached/reasoning keys with prices.
  - **Architecture**: Smallest transformation from current code, but likely violates the one-cost-path correctness invariant.
  - **SSoT**: Projection is still centralized if implemented in one helper, but the semantics are weak.
  - **System limits**: Pure in-memory O(1) mapping per turn. External Langfuse cost semantics are `"Unknown - not available in local context."`
  - **Trade-offs**: Easy to implement but likely double-counts whenever detailed buckets also have prices.

### 5.4 Recommendation

Recommend `Option A`. It is the only option that cleanly supports separate cached/reasoning prices while avoiding double-counting. The implementation should encode this in one pure helper and table-driven tests before touching export code.

### 6.1 Question

Is one live cost proof against session `019de6d8-fb40-74a0-af20-46b088397c53` enough, or should validation include a fresh new session after install?

### 6.2 Context & clarification

- The observed failure was that new sessions did not show `Total Cost` in the Langfuse UI.
- The planned live test uses a known session and verifies cost-relevant Langfuse API fields.
- Re-exporting an old session proves the pricing and usage path, but a fresh session would also prove install-time setup plus watcher behavior for future sessions.
- Generating a fresh Codex session inside an automated test can be slow, brittle, and dependent on external service behavior.

### 6.3 Options

- `Option A`: Historical re-export plus fresh post-install session
  - **Rubrics**: `Conf:65% | Invest:i | Blast:i | Reversal:ii | Fit:ii | Lib:iii | Obs:i | Surface:iii | Perf:iii`
  - **Approach**: Keep the target session test and add an operational live check that creates or captures one fresh post-install session and verifies positive cost.
  - **Architecture**: Exercises the real watcher/install path beyond unit and integration tests.
  - **SSoT**: Deterministic tests still define expected data shape; live test only proves deployed local behavior.
  - **System limits**: External Codex and Langfuse latency/rate limits are `"Unknown - not available in local context."` Runtime is operationally variable.
  - **Trade-offs**: Strongest end-to-end proof but adds flaky external dependency and operational overhead.

- `Option B`: One known-session live proof plus deterministic setup/export tests
  - **Rubrics**: `Conf:85% | Invest:ii | Blast:ii | Reversal:iii | Fit:i | Lib:ii | Obs:ii | Surface:i | Perf:ii`
  - **Approach**: Use the known session live test after install and re-export; rely on unit/integration tests for CLI, install ordering, model setup, and usage projection.
  - **Architecture**: Matches the existing env-gated `internal/langfuse/live_cost_test.go` pattern.
  - **SSoT**: Test fixtures define expected trace shape; live test verifies local Langfuse cost materialization.
  - **System limits**: Live test runtime is bounded by its Go test timeout budget. Langfuse API limits are `"Unknown - not available in local context."`
  - **Trade-offs**: Good balance of confidence and stability. It does not prove an automatically generated future Codex session inside the test itself.

- `Option C`: Deterministic tests only, no live cost gate
  - **Rubrics**: `Conf:60% | Invest:iii | Blast:iii | Reversal:i | Fit:iii | Lib:i | Obs:iii | Surface:ii | Perf:i`
  - **Approach**: Skip live Langfuse cost validation and rely on httptest plus contract fixtures.
  - **Architecture**: Fastest and most reproducible test suite.
  - **SSoT**: Repository fixtures and unit tests are the only verification source.
  - **System limits**: No external API limits. Runtime is bounded by local Go tests.
  - **Trade-offs**: Stable, but it does not prove the actual UI/API cost field that triggered this work.

### 6.4 Recommendation

Recommend `Option B`. It directly verifies the broken user-visible cost field without adding a fragile fresh-session generator. If cost still fails after this path is green, a fresh-session operational check can be added as a separate targeted investigation.

### 7.1 Question

Should the ISO-style plan remain as-is for traceability, or should it be simplified before implementation?

### 7.2 Context & clarification

- The plan is intentionally rigorous: requirements, phases, evals, tests, data contract, RTM, execution log, and ADR index.
- The implementation should still be small: one usage projection, one model setup path, one CLI setup mode, one install invocation, and one live proof.
- The risk is confusing document rigor with code complexity. The plan can be formal while the implementation stays direct.
- Existing repo patterns already include formal plans under `plans/` and ask-me records under `ask_me/`.

### 7.3 Options

- `Option A`: Keep the formal plan but execute the smallest direct code path
  - **Rubrics**: `Conf:90% | Invest:i | Blast:ii | Reversal:ii | Fit:i | Lib:ii | Obs:i | Surface:ii | Perf:na`
  - **Approach**: Keep the current requirements and RTM structure, but enforce the one-way implementation rules during coding.
  - **Architecture**: Fits the repository's existing plan-heavy workflow without adding runtime abstractions.
  - **SSoT**: The plan remains the implementation contract; code centralizes logic in the planned helpers.
  - **System limits**: Documentation size has no runtime impact. Test runtime limits are defined in the plan.
  - **Trade-offs**: Strong traceability for future LLM maintenance, but more document overhead for a focused code change.

- `Option B`: Reduce to a lean plan with requirements, tests, and RTM only
  - **Rubrics**: `Conf:80% | Invest:ii | Blast:i | Reversal:i | Fit:ii | Lib:i | Obs:ii | Surface:i | Perf:na`
  - **Approach**: Remove most process sections while keeping canonical requirements, test definitions, and traceability.
  - **Architecture**: Keeps useful verification structure with less ceremony.
  - **SSoT**: The lean plan still owns requirements and acceptance checks.
  - **System limits**: Documentation size has no runtime impact. Test runtime limits remain in the test definitions.
  - **Trade-offs**: Easier to read, but loses some ISO/12207 lifecycle context that may help future agents.

- `Option C`: Stop updating the plan and implement directly from tests
  - **Rubrics**: `Conf:65% | Invest:iii | Blast:iii | Reversal:iii | Fit:iii | Lib:iii | Obs:iii | Surface:iii | Perf:na`
  - **Approach**: Treat tests as the primary contract and do not maintain a detailed plan.
  - **Architecture**: Fastest implementation loop, but weaker for later audit and agent handoff.
  - **SSoT**: Tests become the only durable contract.
  - **System limits**: Documentation size has no runtime impact. Test runtime limits remain whatever the test suite enforces.
  - **Trade-offs**: Pragmatic now, but risks losing the rationale behind cost-path decisions.

### 7.4 Recommendation

Recommend `Option A`. The document can stay formal because it does not require overengineered code. The implementation should still follow the simpler architecture: no fallback exporter path, no local cost multiplication, no duplicate usage map logic, and no new framework.
