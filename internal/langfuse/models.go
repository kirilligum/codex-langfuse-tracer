package langfuse

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"

	"github.com/kirilligum/codex-langfuse-tracer/internal/config"
)

const (
	pricingSourceURL     = "https://openai.com/api/pricing/"
	gpt53CodexSourceURL  = "https://developers.openai.com/api/docs/models/gpt-5.3-codex"
	pricingSourceDate    = "2026-05-02"
	gpt53CodexSourceDate = "2026-05-02"
)

type ModelSyncSummary struct {
	Existing    int
	Created     int
	Conflicting int
}

type codexModelPricing struct {
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
	ModelName    string               `json:"modelName"`
	MatchPattern string               `json:"matchPattern"`
	Unit         string               `json:"unit"`
	PricingTiers []pricingTierPayload `json:"pricingTiers"`
}

func SyncModelPricing(ctx context.Context, cfg config.LangfuseConfig) (ModelSyncSummary, error) {
	existing, err := listModels(ctx, cfg)
	if err != nil {
		return ModelSyncSummary{}, err
	}
	var summary ModelSyncSummary
	for _, model := range codexModelPricingCatalog() {
		want := modelPayloadFromPricing(model)
		matches := modelsWithName(existing, model.ModelName)
		if len(matches) > 0 {
			conflicts := make([]string, 0)
			for _, got := range matches {
				if mismatch := modelMismatchFields(want, got); len(mismatch) == 0 {
					summary.Existing++
				} else {
					conflicts = append(conflicts, mismatch...)
				}
			}
			if len(conflicts) > 0 {
				summary.Conflicting++
				return summary, fmt.Errorf("Langfuse model definition conflict for %s: %s", model.ModelName, strings.Join(uniqueStrings(conflicts), ", "))
			}
			continue
		}
		if err := createModel(ctx, cfg, model); err != nil {
			return summary, err
		}
		summary.Created++
	}
	return summary, nil
}

func codexModelPricingCatalog() []codexModelPricing {
	return []codexModelPricing{
		{
			ModelName:    "gpt-5.5",
			MatchPattern: `(?i)^(openai/)?gpt-5[.]5$`,
			Unit:         "TOKENS",
			SourceURL:    pricingSourceURL,
			SourceDate:   pricingSourceDate,
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
			SourceURL:    pricingSourceURL,
			SourceDate:   pricingSourceDate,
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
			SourceURL:    pricingSourceURL,
			SourceDate:   pricingSourceDate,
			Prices: map[string]float64{
				"input":                   0.00000075,
				"input_cached_tokens":     0.000000075,
				"output":                  0.0000045,
				"output_reasoning_tokens": 0.0000045,
			},
		},
	}
}

func modelPayloadFromPricing(model codexModelPricing) modelPayload {
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
	if !reflect.DeepEqual(got.PricingTiers, want.PricingTiers) {
		fields = append(fields, "pricingTiers")
	}
	return fields
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(cfg.Host, "/")+"/api/public/models?page=1&limit=100", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", AuthHeader(cfg))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("Langfuse model list /api/public/models failed with HTTP %d", resp.StatusCode)
	}
	var body struct {
		Data []modelPayload `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	return body.Data, nil
}

func createModel(ctx context.Context, cfg config.LangfuseConfig, model codexModelPricing) error {
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
