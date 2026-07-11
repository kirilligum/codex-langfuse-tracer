package langfuse

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

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

func TraceURLFromBody(cfg config.LangfuseConfig, traceID string, traceBody map[string]any) string {
	if htmlPath := strings.TrimSpace(optionalString(traceBody["htmlPath"])); strings.HasPrefix(htmlPath, "/") {
		return strings.TrimRight(cfg.Host, "/") + htmlPath
	}
	if projectID := strings.TrimSpace(optionalString(traceBody["projectId"])); projectID != "" {
		return BuildTraceURL(cfg, projectID, traceID)
	}
	return ""
}

func BuildTraceURL(cfg config.LangfuseConfig, projectID, traceID string) string {
	if projectID == "" || traceID == "" {
		return ""
	}
	return strings.TrimRight(cfg.Host, "/") + "/project/" + url.PathEscape(projectID) + "/traces/" + url.PathEscape(traceID)
}

func optionalString(value any) string {
	text, _ := value.(string)
	return text
}
