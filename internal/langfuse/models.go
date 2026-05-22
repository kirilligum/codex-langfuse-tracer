package langfuse

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"

	"github.com/kirilligum/codex-langfuse-tracer/internal/config"
)

const (
	openAIPriceSourceURL     = "https://openai.com/api/pricing/"
	gpt53CodexSourceURL      = "https://developers.openai.com/api/docs/models/gpt-5.3-codex"
	anthropicPriceSourceURL  = "https://platform.claude.com/docs/en/about-claude/pricing"
	openAIPriceSourceDate    = "2026-05-02"
	gpt53CodexSourceDate     = "2026-05-02"
	anthropicPriceSourceDate = "2026-05-05"
)

type ModelSyncSummary struct {
	Existing    int
	Created     int
	Conflicting int
}

type modelPricing struct {
	ModelName    string
	MatchPattern string
	Unit         string
	SourceURL    string
	SourceDate   string
	Prices       map[string]float64
}

type pricingTierPayload struct {
	Name       string             `json:"name"`
	IsDefault  bool               `json:"isDefault"`
	Priority   int                `json:"priority"`
	Conditions []any              `json:"conditions"`
	Prices     map[string]float64 `json:"prices"`
}

type modelPayload struct {
	ModelName         string               `json:"modelName"`
	MatchPattern      string               `json:"matchPattern"`
	Unit              string               `json:"unit"`
	IsLangfuseManaged bool                 `json:"isLangfuseManaged,omitempty"`
	PricingTiers      []pricingTierPayload `json:"pricingTiers"`
}

func SyncModelPricing(ctx context.Context, cfg config.LangfuseConfig) (ModelSyncSummary, error) {
	existing, err := listModels(ctx, cfg)
	if err != nil {
		return ModelSyncSummary{}, err
	}
	var summary ModelSyncSummary
	for _, model := range modelPricingCatalog() {
		want := modelPayloadFromPricing(model)
		matches := modelsWithName(existing, model.ModelName)
		if len(matches) > 0 {
			conflicts := make([]string, 0)
			hasExactMatch := false
			for _, got := range matches {
				if mismatch := modelMismatchFields(want, got); len(mismatch) == 0 {
					hasExactMatch = true
				} else if !got.IsLangfuseManaged {
					conflicts = append(conflicts, mismatch...)
				}
			}
			if hasExactMatch {
				summary.Existing++
				continue
			}
			if len(conflicts) > 0 {
				summary.Conflicting++
				return summary, fmt.Errorf("Langfuse model definition conflict for %s: %s", model.ModelName, strings.Join(uniqueStrings(conflicts), ", "))
			}
		}
		if err := createModel(ctx, cfg, model); err != nil {
			return summary, err
		}
		summary.Created++
	}
	return summary, nil
}

func modelPricingCatalog() []modelPricing {
	return []modelPricing{
		{
			ModelName:    "gpt-5.5",
			MatchPattern: `(?i)^(openai/)?gpt-5[.]5$`,
			Unit:         "TOKENS",
			SourceURL:    openAIPriceSourceURL,
			SourceDate:   openAIPriceSourceDate,
			Prices: map[string]float64{
				"input":                   0.000005,
				"input_cached_tokens":     0.0000005,
				"output":                  0.00003,
				"output_reasoning_tokens": 0.00003,
			},
		},
		{
			ModelName:    "gpt-5.3-codex-spark",
			MatchPattern: `(?i)^(openai/)?gpt-5[.]3-codex-spark$`,
			Unit:         "TOKENS",
			SourceURL:    gpt53CodexSourceURL,
			SourceDate:   gpt53CodexSourceDate,
			Prices: map[string]float64{
				"input":                   0.00000175,
				"input_cached_tokens":     0.000000175,
				"output":                  0.000014,
				"output_reasoning_tokens": 0.000014,
			},
		},
		{
			ModelName:    "gpt-5.4",
			MatchPattern: `(?i)^(openai/)?gpt-5[.]4$`,
			Unit:         "TOKENS",
			SourceURL:    openAIPriceSourceURL,
			SourceDate:   openAIPriceSourceDate,
			Prices: map[string]float64{
				"input":                   0.0000025,
				"input_cached_tokens":     0.00000025,
				"output":                  0.000015,
				"output_reasoning_tokens": 0.000015,
			},
		},
		{
			ModelName:    "gpt-5.4-mini",
			MatchPattern: `(?i)^(openai/)?gpt-5[.]4-mini$`,
			Unit:         "TOKENS",
			SourceURL:    openAIPriceSourceURL,
			SourceDate:   openAIPriceSourceDate,
			Prices: map[string]float64{
				"input":                   0.00000075,
				"input_cached_tokens":     0.000000075,
				"output":                  0.0000045,
				"output_reasoning_tokens": 0.0000045,
			},
		},
		{
			ModelName:    "claude-opus-4-7",
			MatchPattern: `(?i)^claude-opus-4-7$`,
			Unit:         "TOKENS",
			SourceURL:    anthropicPriceSourceURL,
			SourceDate:   anthropicPriceSourceDate,
			Prices: map[string]float64{
				"input":                       0.000005,
				"cache_creation_input_tokens": 0.00000625,
				"cache_read_input_tokens":     0.00000050,
				"output":                      0.000025,
			},
		},
		{
			ModelName:    "claude-sonnet-4-6",
			MatchPattern: `(?i)^claude-sonnet-4-6$`,
			Unit:         "TOKENS",
			SourceURL:    anthropicPriceSourceURL,
			SourceDate:   anthropicPriceSourceDate,
			Prices: map[string]float64{
				"input":                       0.000003,
				"cache_creation_input_tokens": 0.00000375,
				"cache_read_input_tokens":     0.00000030,
				"output":                      0.000015,
			},
		},
		{
			ModelName:    "claude-haiku-4-5-20251001",
			MatchPattern: `(?i)^claude-haiku-4-5-20251001$`,
			Unit:         "TOKENS",
			SourceURL:    anthropicPriceSourceURL,
			SourceDate:   anthropicPriceSourceDate,
			Prices: map[string]float64{
				"input":                       0.000001,
				"cache_creation_input_tokens": 0.00000125,
				"cache_read_input_tokens":     0.00000010,
				"output":                      0.000005,
			},
		},
	}
}

func modelPayloadFromPricing(model modelPricing) modelPayload {
	return modelPayload{
		ModelName:    model.ModelName,
		MatchPattern: model.MatchPattern,
		Unit:         model.Unit,
		PricingTiers: []pricingTierPayload{
			{
				Name:       "Standard",
				IsDefault:  true,
				Priority:   0,
				Conditions: []any{},
				Prices:     model.Prices,
			},
		},
	}
}

func modelsWithName(models []modelPayload, name string) []modelPayload {
	matches := make([]modelPayload, 0, 1)
	for _, model := range models {
		if model.ModelName == name {
			matches = append(matches, model)
		}
	}
	return matches
}

func modelMismatchFields(want, got modelPayload) []string {
	var fields []string
	if got.MatchPattern != want.MatchPattern {
		fields = append(fields, "matchPattern")
	}
	if got.Unit != want.Unit {
		fields = append(fields, "unit")
	}
	if !samePricingTiers(got.PricingTiers, want.PricingTiers) {
		fields = append(fields, "pricingTiers")
	}
	return fields
}

func samePricingTiers(got, want []pricingTierPayload) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range want {
		if got[index].Name != want[index].Name || got[index].IsDefault != want[index].IsDefault || got[index].Priority != want[index].Priority || len(got[index].Conditions) != len(want[index].Conditions) {
			return false
		}
		if !samePrices(got[index].Prices, want[index].Prices) {
			return false
		}
	}
	return true
}

func samePrices(got, want map[string]float64) bool {
	if len(got) != len(want) {
		return false
	}
	for key, wantValue := range want {
		gotValue, ok := got[key]
		if !ok || math.Abs(gotValue-wantValue) > 0.000000000000001 {
			return false
		}
	}
	return true
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	unique := make([]string, 0, len(values))
	for _, value := range values {
		if seen[value] {
			continue
		}
		seen[value] = true
		unique = append(unique, value)
	}
	return unique
}

func listModels(ctx context.Context, cfg config.LangfuseConfig) ([]modelPayload, error) {
	const limit = 100
	var models []modelPayload
	for page := 1; ; page++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/api/public/models?page=%d&limit=%d", strings.TrimRight(cfg.Host, "/"), page, limit), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", AuthHeader(cfg))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		var body struct {
			Data []modelPayload `json:"data"`
			Meta struct {
				TotalItems int `json:"totalItems"`
			} `json:"meta"`
		}
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			_ = resp.Body.Close()
			return nil, fmt.Errorf("Langfuse model list /api/public/models failed with HTTP %d", resp.StatusCode)
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			_ = resp.Body.Close()
			return nil, err
		}
		_ = resp.Body.Close()
		models = append(models, body.Data...)
		if len(body.Data) < limit || (body.Meta.TotalItems > 0 && len(models) >= body.Meta.TotalItems) {
			return models, nil
		}
	}
}

func createModel(ctx context.Context, cfg config.LangfuseConfig, model modelPricing) error {
	payload := modelPayloadFromPricing(model)
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(cfg.Host, "/")+"/api/public/models", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", AuthHeader(cfg))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("Langfuse model create /api/public/models failed with HTTP %d", resp.StatusCode)
	}
	return nil
}
