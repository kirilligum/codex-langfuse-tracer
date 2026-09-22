package langfuse

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kirilligum/codex-langfuse-tracer/internal/agenttrace"
	"github.com/kirilligum/codex-langfuse-tracer/internal/config"
)

// TEST-009
func TestTraceVerificationClient(t *testing.T) {
	t.Parallel()

	turn := completeTurn(t)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/public/v2/observations" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if r.URL.Query().Get("traceId") != turn.TraceID || r.URL.Query().Get("fields") != "core,basic,io" || r.URL.Query().Get("isRootObservation") != "true" || r.URL.Query().Get("limit") != "2" {
			t.Fatalf("query = %s", r.URL.RawQuery)
		}
		if r.Header.Get("Authorization") == "" {
			t.Fatal("missing auth")
		}
		calls++
		output := "not yet"
		if calls > 1 {
			output = agenttrace.ExportText(turn.OutputText())
		}
		inputJSON, _ := json.Marshal(agenttrace.ExportText(turn.InputText()))
		outputJSON, _ := json.Marshal(output)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{
				"id": "root-observation", "traceId": turn.TraceID, "projectId": "project-test",
				"isRootObservation": true, "name": "codex.agent", "input": string(inputJSON), "output": string(outputJSON),
			}},
			"meta": map[string]any{},
		})
	}))
	defer server.Close()
	verification, err := VerifyTrace(context.Background(), config.LangfuseConfig{
		Host: server.URL, PublicKey: "pk-lf-test", SecretKey: "sk-lf-test",
	}, turn, time.Second, time.Millisecond)
	if err != nil {
		t.Fatalf("VerifyTrace: %v", err)
	}
	if !verification.HasInput || !verification.HasOutput || verification.Root.ProjectID != "project-test" || calls < 2 {
		t.Fatalf("verification = %+v calls=%d", verification, calls)
	}
}

func TestObservationTextMatchesSerializedStringOnly(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, raw, expected string
		want                bool
	}{
		{"plain text value", `"hello"`, "hello", true},
		{"escaped text", `"line\n\"quoted\"\t雪"`, "line\n\"quoted\"\t雪", true},
		{"equivalent JSON escapes", `"\u003cvalue\u003e"`, "<value>", true},
		{"literal quotes", `"\"hello\""`, `"hello"`, true},
		{"do not remove user quotes", `"hello"`, `"hello"`, false},
		{"different text", `"other"`, "hello", false},
		{"unencoded text", "hello", "hello", false},
		{"object", `{"value":"hello"}`, `{"value":"hello"}`, false},
		{"array", `["hello"]`, `["hello"]`, false},
		{"number", "123", "123", false},
		{"null", "null", "", false},
		{"missing", "", "", false},
		{"empty string", `""`, "", true},
		{"trailing data", `"hello" "extra"`, "hello", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := observationTextMatches(tc.raw, tc.expected); got != tc.want {
				t.Fatalf("observationTextMatches(%q, %q) = %v, want %v", tc.raw, tc.expected, got, tc.want)
			}
		})
	}
}

func TestObservationClientPagination(t *testing.T) {
	t.Parallel()

	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			if r.URL.Query().Get("cursor") != "" {
				t.Fatalf("first page cursor = %q", r.URL.Query().Get("cursor"))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": "one", "traceId": "trace"}},
				"meta": map[string]any{"cursor": "next-page"},
			})
			return
		}
		if r.URL.Query().Get("cursor") != "next-page" {
			t.Fatalf("second page cursor = %q", r.URL.Query().Get("cursor"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"id": "two", "traceId": "trace"}},
			"meta": map[string]any{},
		})
	}))
	defer server.Close()

	observations, err := NewObservationClient(config.LangfuseConfig{Host: server.URL}).List(context.Background(), ObservationQuery{TraceID: "trace", Limit: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(observations) != 2 || observations[0].ID != "one" || observations[1].ID != "two" {
		t.Fatalf("observations = %+v", observations)
	}
}

func TestObservationClientPassesV2Filter(t *testing.T) {
	t.Parallel()

	wantFilter := stringFilter("sessionId", "session with spaces")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("filter"); got != wantFilter {
			t.Fatalf("filter = %q, want %q", got, wantFilter)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "observation"}}, "meta": map[string]any{}})
	}))
	defer server.Close()

	observations, err := NewObservationClient(config.LangfuseConfig{Host: server.URL}).List(context.Background(), ObservationQuery{Filter: wantFilter, Limit: 1})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(observations) != 1 || observations[0].ID != "observation" {
		t.Fatalf("observations = %+v", observations)
	}
}

func TestObservationClientHTTPFailures(t *testing.T) {
	t.Parallel()

	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests, http.StatusInternalServerError} {
		status := status
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
			}))
			defer server.Close()
			_, err := NewObservationClient(config.LangfuseConfig{Host: server.URL}).List(context.Background(), ObservationQuery{TraceID: "trace"})
			if err == nil || !strings.Contains(err.Error(), "observations v2 fetch failed with HTTP") {
				t.Fatalf("List accepted HTTP %d: %v", status, err)
			}
		})
	}
}

func TestTraceVerificationMalformedAndCanceled(t *testing.T) {
	t.Parallel()

	turn := completeTurn(t)
	malformed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":`))
	}))
	defer malformed.Close()
	_, err := VerifyTrace(context.Background(), config.LangfuseConfig{Host: malformed.URL}, turn, 0, time.Millisecond)
	if err == nil {
		t.Fatal("VerifyTrace accepted malformed observation response")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = VerifyTrace(ctx, config.LangfuseConfig{Host: malformed.URL}, turn, time.Second, time.Millisecond)
	if err == nil {
		t.Fatal("VerifyTrace with canceled context succeeded, want error")
	}
}

func TestTraceVerificationRejectsMultipleRoots(t *testing.T) {
	t.Parallel()

	turn := completeTurn(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"id": "root-one", "traceId": turn.TraceID, "isRootObservation": true},
				{"id": "root-two", "traceId": turn.TraceID, "isRootObservation": true},
			},
			"meta": map[string]any{},
		})
	}))
	defer server.Close()
	_, err := VerifyTrace(context.Background(), config.LangfuseConfig{Host: server.URL}, turn, time.Second, time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "logical root observations") {
		t.Fatalf("multiple roots error = %v", err)
	}
}
