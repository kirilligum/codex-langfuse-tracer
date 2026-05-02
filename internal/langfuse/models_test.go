package langfuse

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kirilligum/codex-langfuse-tracer/internal/codextrace"
	"github.com/kirilligum/codex-langfuse-tracer/internal/config"
)

// TEST-404
func TestModelDefinitionSyncCreatesMissingModels(t *testing.T) {
	t.Parallel()

	cfg := config.LangfuseConfig{PublicKey: "pk-test", SecretKey: "sk-test"}
	var posts []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte("pk-test:sk-test")) {
			t.Fatalf("authorization header = %q", r.Header.Get("Authorization"))
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/public/models":
			if r.URL.Query().Get("page") != "1" || r.URL.Query().Get("limit") != "100" {
				t.Fatalf("query = %s", r.URL.RawQuery)
			}
			writeJSON(t, w, map[string]any{"data": []any{}, "meta": map[string]any{}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/public/models":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode create body: %v", err)
			}
			posts = append(posts, body)
			writeJSON(t, w, body)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()
	cfg.Host = server.URL

	summary, err := SyncModelPricing(context.Background(), cfg)
	if err != nil {
		t.Fatalf("SyncModelPricing: %v", err)
	}
	if summary.Created != 3 || summary.Existing != 0 || summary.Conflicting != 0 {
		t.Fatalf("summary = %+v", summary)
	}
	if len(posts) != 3 {
		t.Fatalf("POST count = %d, want 3", len(posts))
	}

	seen := map[string]bool{}
	for _, post := range posts {
		modelName := stringValue(post["modelName"])
		seen[modelName] = true
		if post["unit"] != "TOKENS" {
			t.Fatalf("%s unit = %#v", modelName, post["unit"])
		}
		if _, ok := post["inputPrice"]; ok {
			t.Fatalf("%s used deprecated inputPrice in %#v", modelName, post)
		}
		if _, ok := post["outputPrice"]; ok {
			t.Fatalf("%s used deprecated outputPrice in %#v", modelName, post)
		}
		if _, ok := post["totalPrice"]; ok {
			t.Fatalf("%s used deprecated totalPrice in %#v", modelName, post)
		}
		tier := defaultTier(t, post)
		prices := mapValue(tier["prices"])
		for _, key := range []string{"input", "input_cached_tokens", "output", "output_reasoning_tokens"} {
			if _, ok := prices[key]; !ok {
				t.Fatalf("%s missing price key %s in %#v", modelName, key, prices)
			}
		}
		if _, ok := prices["total"]; ok {
			t.Fatalf("%s has total price in %#v", modelName, prices)
		}
	}
	for _, model := range []string{"gpt-5.5", "gpt-5.4", "gpt-5.4-mini"} {
		if !seen[model] {
			t.Fatalf("missing created model %s in %#v", model, posts)
		}
	}
}

// TEST-404
func TestModelPricingCatalogUsesOpenAIKeys(t *testing.T) {
	t.Parallel()

	catalog := codexModelPricingCatalog()
	if len(catalog) != 3 {
		t.Fatalf("catalog length = %d, want 3", len(catalog))
	}
	seen := map[string]bool{}
	for _, model := range catalog {
		seen[model.ModelName] = true
		if model.Unit != "TOKENS" {
			t.Fatalf("%s unit = %q", model.ModelName, model.Unit)
		}
		if model.SourceURL != "https://openai.com/api/pricing/" || model.SourceDate != "2026-05-02" {
			t.Fatalf("%s source = %s %s", model.ModelName, model.SourceURL, model.SourceDate)
		}
		keys := make([]string, 0, len(model.Prices))
		for key := range model.Prices {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		wantKeys := []string{"input", "input_cached_tokens", "output", "output_reasoning_tokens"}
		if !slices.Equal(keys, wantKeys) {
			t.Fatalf("%s price keys = %#v, want %#v", model.ModelName, keys, wantKeys)
		}
	}
	if !seen["gpt-5.5"] || !seen["gpt-5.4"] || !seen["gpt-5.4-mini"] {
		t.Fatalf("catalog models = %#v", seen)
	}
	if price := catalogByName(catalog, "gpt-5.5").Prices["input"]; price != 0.000005 {
		t.Fatalf("gpt-5.5 input price = %.12f", price)
	}
	if price := catalogByName(catalog, "gpt-5.5").Prices["input_cached_tokens"]; price != 0.0000005 {
		t.Fatalf("gpt-5.5 cached input price = %.12f", price)
	}
	if price := catalogByName(catalog, "gpt-5.5").Prices["output"]; price != 0.00003 {
		t.Fatalf("gpt-5.5 output price = %.12f", price)
	}
	if price := catalogByName(catalog, "gpt-5.5").Prices["output_reasoning_tokens"]; price != 0.00003 {
		t.Fatalf("gpt-5.5 reasoning output price = %.12f", price)
	}
}

// TEST-404
func TestModelPricingCatalogCoversRepositoryFixtures(t *testing.T) {
	t.Parallel()

	catalog := codexModelPricingCatalog()
	covered := map[string]bool{}
	for _, model := range catalog {
		covered[model.ModelName] = true
	}

	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Fixtures []struct {
			Rollout string `json:"rollout"`
		} `json:"fixtures"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range manifest.Fixtures {
		turns, err := codextrace.ParseTurns(filepath.Join("..", "..", fixture.Rollout))
		if err != nil {
			continue
		}
		for _, turn := range turns {
			if turn.Model != "" && !covered[turn.Model] {
				t.Fatalf("fixture model %q from %s is not covered by pricing catalog", turn.Model, fixture.Rollout)
			}
		}
	}
}

// TEST-405
func TestModelDefinitionSyncIsIdempotent(t *testing.T) {
	t.Parallel()

	cfg := config.LangfuseConfig{PublicKey: "pk-test", SecretKey: "sk-test"}
	posts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/public/models":
			models := make([]modelPayload, 0, len(codexModelPricingCatalog()))
			for _, model := range codexModelPricingCatalog() {
				models = append(models, modelPayloadFromPricing(model))
			}
			writeJSON(t, w, map[string]any{"data": models, "meta": map[string]any{}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/public/models":
			posts++
			w.WriteHeader(http.StatusInternalServerError)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()
	cfg.Host = server.URL

	summary, err := SyncModelPricing(context.Background(), cfg)
	if err != nil {
		t.Fatalf("SyncModelPricing: %v", err)
	}
	if posts != 0 {
		t.Fatalf("POST count = %d, want 0", posts)
	}
	if summary.Existing != 3 || summary.Created != 0 || summary.Conflicting != 0 {
		t.Fatalf("summary = %+v", summary)
	}
}

// TEST-405
func TestModelDefinitionSyncRejectsConflictingModel(t *testing.T) {
	t.Parallel()

	cfg := config.LangfuseConfig{PublicKey: "pk-test", SecretKey: "sk-test"}
	posts := 0
	conflict := modelPayloadFromPricing(catalogByName(codexModelPricingCatalog(), "gpt-5.5"))
	conflict.MatchPattern = `(?i)^wrong-model$`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/public/models":
			writeJSON(t, w, map[string]any{"data": []modelPayload{conflict}, "meta": map[string]any{}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/public/models":
			posts++
			w.WriteHeader(http.StatusInternalServerError)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()
	cfg.Host = server.URL

	summary, err := SyncModelPricing(context.Background(), cfg)
	if err == nil {
		t.Fatal("SyncModelPricing succeeded, want conflict")
	}
	if posts != 0 {
		t.Fatalf("POST count = %d, want 0", posts)
	}
	errText := err.Error()
	if !strings.Contains(errText, "gpt-5.5") || !strings.Contains(errText, "matchPattern") {
		t.Fatalf("conflict error = %q", errText)
	}
	if summary.Conflicting != 1 || summary.Created != 0 {
		t.Fatalf("summary = %+v", summary)
	}
}

// TEST-411
func TestModelDefinitionSyncDoesNotLeakSecrets(t *testing.T) {
	t.Parallel()

	cfg := config.LangfuseConfig{
		Host:      "",
		PublicKey: "pk-lf-test-public",
		SecretKey: "sk-lf-test-secret",
	}
	authHeader := "Basic " + base64.StdEncoding.EncodeToString([]byte(cfg.PublicKey+":"+cfg.SecretKey))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "server message contains sk-lf-test-secret and "+authHeader, http.StatusInternalServerError)
	}))
	defer server.Close()
	cfg.Host = server.URL

	summary, err := SyncModelPricing(context.Background(), cfg)
	if err == nil {
		t.Fatal("SyncModelPricing succeeded, want error")
	}
	for _, text := range []string{err.Error(), string(mustJSON(t, summary))} {
		if strings.Contains(text, cfg.SecretKey) || strings.Contains(text, authHeader) {
			t.Fatalf("secret leaked in %q", text)
		}
	}
	if !strings.Contains(err.Error(), "/api/public/models") || !strings.Contains(err.Error(), "500") {
		t.Fatalf("error lacks endpoint/status diagnostic: %q", err.Error())
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("write json: %v", err)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func defaultTier(t *testing.T, model map[string]any) map[string]any {
	t.Helper()
	rawTiers, ok := model["pricingTiers"].([]any)
	if !ok {
		t.Fatalf("pricingTiers = %#v", model["pricingTiers"])
	}
	if len(rawTiers) != 1 {
		t.Fatalf("pricingTiers length = %d", len(rawTiers))
	}
	tier := mapValue(rawTiers[0])
	if tier["name"] != "Standard" || tier["isDefault"] != true || tier["priority"] != float64(0) {
		t.Fatalf("bad default tier: %#v", tier)
	}
	if conditions, ok := tier["conditions"].([]any); !ok || len(conditions) != 0 {
		t.Fatalf("default tier conditions = %#v", tier["conditions"])
	}
	return tier
}

func catalogByName(catalog []codexModelPricing, name string) codexModelPricing {
	for _, model := range catalog {
		if model.ModelName == name {
			return model
		}
	}
	return codexModelPricing{}
}
