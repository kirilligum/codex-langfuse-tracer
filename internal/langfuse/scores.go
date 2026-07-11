package langfuse

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/kirilligum/codex-langfuse-tracer/internal/agenttrace"
	"github.com/kirilligum/codex-langfuse-tracer/internal/config"
)

type scorePayload struct {
	ID          string         `json:"id,omitempty"`
	TraceID     string         `json:"traceId"`
	Name        string         `json:"name"`
	Value       any            `json:"value"`
	DataType    string         `json:"dataType"`
	Comment     string         `json:"comment,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	Environment string         `json:"environment,omitempty"`
}

func CreateDeterministicScores(ctx context.Context, cfg config.LangfuseConfig, turn agenttrace.Turn, environment string) error {
	for _, score := range agenttrace.BuildDeterministicScores(turn) {
		payload := scorePayload{
			ID:       stableScoreID(turn.TraceID, score.Name),
			TraceID:  turn.TraceID,
			Name:     score.Name,
			Value:    score.Value,
			DataType: score.DataType,
			Comment:  score.Comment,
			Metadata: map[string]any{
				"provider": turn.Profile().Provider,
				"source":   "codex-langfuse-tracer",
				"kind":     "deterministic",
			},
		}
		if isValidScoreEnvironment(environment) {
			payload.Environment = environment
		}
		if err := createScore(ctx, cfg, payload); err != nil {
			return err
		}
	}
	return nil
}

func createScore(ctx context.Context, cfg config.LangfuseConfig, payload scorePayload) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(cfg.Host, "/")+"/api/public/scores", bytes.NewReader(raw))
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
		return fmt.Errorf("Langfuse score %s export failed with HTTP %d", payload.Name, resp.StatusCode)
	}
	return nil
}

func stableScoreID(traceID, name string) string {
	sum := sha256.Sum256([]byte("score:" + traceID + ":" + name))
	return fmt.Sprintf("%x", sum)[:32]
}

func isValidScoreEnvironment(environment string) bool {
	environment = strings.TrimSpace(environment)
	if environment == "" || strings.HasPrefix(environment, "langfuse") {
		return false
	}
	for _, r := range environment {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			continue
		}
		return false
	}
	return true
}
