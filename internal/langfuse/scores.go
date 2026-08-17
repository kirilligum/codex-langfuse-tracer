package langfuse

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

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

type scoreIngestionEvent struct {
	ID        string       `json:"id"`
	Type      string       `json:"type"`
	Timestamp string       `json:"timestamp"`
	Body      scorePayload `json:"body"`
}

type scoreIngestionBatch struct {
	Batch []scoreIngestionEvent `json:"batch"`
}

type scoreIngestionResponse struct {
	Successes []struct {
		ID     string `json:"id"`
		Status int    `json:"status"`
	} `json:"successes"`
	Errors []struct {
		ID     string `json:"id"`
		Status int    `json:"status"`
	} `json:"errors"`
}

func CreateDeterministicScores(ctx context.Context, cfg config.LangfuseConfig, turn agenttrace.Turn, environment string) error {
	timestamp := time.Now().UTC().Format(time.RFC3339Nano)
	batch := scoreIngestionBatch{Batch: make([]scoreIngestionEvent, 0, len(agenttrace.BuildDeterministicScores(turn)))}
	for _, score := range agenttrace.BuildDeterministicScores(turn) {
		payload := scorePayload{
			ID:          stableScoreID(turn.TraceID, score.Name),
			TraceID:     turn.TraceID,
			Name:        score.Name,
			Value:       score.Value,
			DataType:    score.DataType,
			Comment:     score.Comment,
			Environment: environment,
			Metadata: map[string]any{
				"provider": turn.Profile().Provider,
				"source":   "codex-langfuse-tracer",
				"kind":     "deterministic",
			},
		}
		batch.Batch = append(batch.Batch, scoreIngestionEvent{
			ID:        stableScoreEventID(turn.TraceID, score.Name),
			Type:      "score-create",
			Timestamp: timestamp,
			Body:      payload,
		})
	}
	return createScoreBatch(ctx, cfg, batch)
}

func createScoreBatch(ctx context.Context, cfg config.LangfuseConfig, batch scoreIngestionBatch) error {
	raw, err := json.Marshal(batch)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(cfg.Host, "/")+"/api/public/ingestion", bytes.NewReader(raw))
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
		return fmt.Errorf("Langfuse deterministic score batch failed with HTTP %d", resp.StatusCode)
	}
	var result scoreIngestionResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode Langfuse deterministic score batch response: %w", err)
	}
	if len(result.Errors) > 0 || len(result.Successes) != len(batch.Batch) {
		return fmt.Errorf("Langfuse deterministic score batch incomplete: accepted=%d errors=%d expected=%d", len(result.Successes), len(result.Errors), len(batch.Batch))
	}
	return nil
}

func stableScoreID(traceID, name string) string {
	sum := sha256.Sum256([]byte("score:" + traceID + ":" + name))
	return fmt.Sprintf("%x", sum)[:32]
}

func stableScoreEventID(traceID, name string) string {
	sum := sha256.Sum256([]byte("score-event:" + traceID + ":" + name))
	return fmt.Sprintf("%x", sum)[:32]
}
