package langfuse

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/kirilligum/codex-langfuse-tracer/internal/agenttrace"
	"github.com/kirilligum/codex-langfuse-tracer/internal/config"
)

func CheckHealth(ctx context.Context, cfg config.LangfuseConfig) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(cfg.Host, "/")+"/api/public/health", nil)
	if err != nil {
		return 0, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return resp.StatusCode, fmt.Errorf("Langfuse health check failed with HTTP %d", resp.StatusCode)
	}
	return resp.StatusCode, nil
}

func CheckAuth(ctx context.Context, cfg config.LangfuseConfig) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(cfg.Host, "/")+"/api/public/models?page=1&limit=1", nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", AuthHeader(cfg))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return resp.StatusCode, fmt.Errorf("Langfuse auth check failed with HTTP %d", resp.StatusCode)
	}
	return resp.StatusCode, nil
}

func FetchProjectID(ctx context.Context, cfg config.LangfuseConfig) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(cfg.Host, "/")+"/api/public/projects", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", AuthHeader(cfg))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("Langfuse project lookup failed with HTTP %d", resp.StatusCode)
	}
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	if len(body.Data) == 0 || body.Data[0].ID == "" {
		return "", fmt.Errorf("Langfuse project lookup returned no project id")
	}
	return body.Data[0].ID, nil
}

func BuildTraceURL(cfg config.LangfuseConfig, projectID, traceID string) string {
	if projectID == "" || traceID == "" {
		return ""
	}
	return strings.TrimRight(cfg.Host, "/") + "/project/" + url.PathEscape(projectID) + "/traces/" + url.PathEscape(traceID)
}

type Observation struct {
	ID                   string         `json:"id"`
	TraceID              string         `json:"traceId"`
	ProjectID            string         `json:"projectId"`
	ParentObservationID  string         `json:"parentObservationId"`
	IsRootObservation    bool           `json:"isRootObservation"`
	Type                 string         `json:"type"`
	Name                 string         `json:"name"`
	StartTime            string         `json:"startTime"`
	EndTime              string         `json:"endTime"`
	Input                string         `json:"input"`
	Output               string         `json:"output"`
	Metadata             map[string]any `json:"metadata"`
	ProvidedModelName    string         `json:"providedModelName"`
	Model                string         `json:"model"`
	ModelID              string         `json:"modelId"`
	UsageDetails         map[string]any `json:"usageDetails"`
	CostDetails          map[string]any `json:"costDetails"`
	InputCost            float64        `json:"inputCost"`
	OutputCost           float64        `json:"outputCost"`
	TotalCost            float64        `json:"totalCost"`
	InputPrice           any            `json:"inputPrice"`
	OutputPrice          any            `json:"outputPrice"`
	TotalPrice           any            `json:"totalPrice"`
	UsagePricingTierName string         `json:"usagePricingTierName"`
	UserID               string         `json:"userId"`
	SessionID            string         `json:"sessionId"`
	Environment          string         `json:"environment"`
	Tags                 []string       `json:"tags"`
	Release              string         `json:"release"`
	TraceName            string         `json:"traceName"`
}

func (o Observation) ModelName() string {
	if o.ProvidedModelName != "" {
		return o.ProvidedModelName
	}
	return o.Model
}

type ObservationQuery struct {
	TraceID           string
	Name              string
	Fields            string
	Filter            string
	FromStartTime     string
	ToStartTime       string
	Cursor            string
	Limit             int
	IsRootObservation *bool
}

type observationPage struct {
	Data []Observation `json:"data"`
	Meta struct {
		Cursor string `json:"cursor"`
	} `json:"meta"`
}

type ObservationClient struct {
	cfg        config.LangfuseConfig
	httpClient *http.Client
}

func NewObservationClient(cfg config.LangfuseConfig) *ObservationClient {
	return &ObservationClient{cfg: cfg, httpClient: http.DefaultClient}
}

func (c *ObservationClient) List(ctx context.Context, query ObservationQuery) ([]Observation, error) {
	if query.Limit == 0 {
		query.Limit = 1000
	}
	if query.Limit < 1 || query.Limit > 1000 {
		return nil, fmt.Errorf("observation query limit %d outside [1,1000]", query.Limit)
	}
	if query.Fields == "" {
		query.Fields = "core,basic"
	}
	observations := make([]Observation, 0)
	for {
		values := url.Values{}
		values.Set("fields", query.Fields)
		values.Set("limit", fmt.Sprint(query.Limit))
		if query.TraceID != "" {
			values.Set("traceId", query.TraceID)
		}
		if query.Name != "" {
			values.Set("name", query.Name)
		}
		if query.Filter != "" {
			values.Set("filter", query.Filter)
		}
		if query.FromStartTime != "" {
			values.Set("fromStartTime", query.FromStartTime)
		}
		if query.ToStartTime != "" {
			values.Set("toStartTime", query.ToStartTime)
		}
		if query.Cursor != "" {
			values.Set("cursor", query.Cursor)
		}
		if query.IsRootObservation != nil {
			values.Set("isRootObservation", fmt.Sprint(*query.IsRootObservation))
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.cfg.Host, "/")+"/api/public/v2/observations?"+values.Encode(), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", AuthHeader(c.cfg))
		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, err
		}
		var page observationPage
		decodeErr := json.NewDecoder(resp.Body).Decode(&page)
		closeErr := resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return nil, fmt.Errorf("Langfuse observations v2 fetch failed with HTTP %d", resp.StatusCode)
		}
		if decodeErr != nil {
			return nil, decodeErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		observations = append(observations, page.Data...)
		if page.Meta.Cursor == "" {
			return observations, nil
		}
		query.Cursor = page.Meta.Cursor
	}
}

func stringFilter(column, value string) string {
	raw, _ := json.Marshal([]map[string]any{{
		"type":     "string",
		"column":   column,
		"operator": "=",
		"value":    value,
	}})
	return string(raw)
}

type TraceVerification struct {
	HasInput  bool
	HasOutput bool
	Root      Observation
}

// Observations v2 returns stored I/O as raw strings. Our OTLP text attributes
// contain JSON string values, so decode exactly one layer before comparison.
// Null, structured JSON, malformed data, and unencoded text cannot confirm it.
func observationTextMatches(raw, expected string) bool {
	var text *string
	return json.Unmarshal([]byte(raw), &text) == nil && text != nil && *text == expected
}

func VerifyTrace(ctx context.Context, cfg config.LangfuseConfig, turn agenttrace.Turn, timeout, interval time.Duration) (TraceVerification, error) {
	root := true
	deadline := time.Now().Add(timeout)
	client := NewObservationClient(cfg)
	var lastErr error
	for {
		observations, err := client.List(ctx, ObservationQuery{
			TraceID:           turn.TraceID,
			Fields:            "core,basic,io",
			Limit:             2,
			IsRootObservation: &root,
		})
		if err != nil {
			lastErr = err
		} else if len(observations) > 1 {
			return TraceVerification{}, fmt.Errorf("trace %s has %d logical root observations", turn.TraceID, len(observations))
		} else if len(observations) == 1 {
			observation := observations[0]
			verification := TraceVerification{
				HasInput:  observation.TraceID == turn.TraceID && observation.IsRootObservation && observationTextMatches(observation.Input, agenttrace.ExportText(turn.InputText())),
				HasOutput: observation.TraceID == turn.TraceID && observation.IsRootObservation && observationTextMatches(observation.Output, agenttrace.ExportText(turn.OutputText())),
				Root:      observation,
			}
			if verification.HasInput && verification.HasOutput {
				return verification, nil
			}
		}
		if time.Now().After(deadline) {
			if lastErr != nil {
				return TraceVerification{}, lastErr
			}
			return TraceVerification{}, nil
		}
		select {
		case <-ctx.Done():
			return TraceVerification{}, ctx.Err()
		case <-time.After(maxDuration(interval, 100*time.Millisecond)):
		}
	}
}

func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}
