package langfuse

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kirilligum/codex-langfuse-tracer/internal/agenttrace"
	"github.com/kirilligum/codex-langfuse-tracer/internal/config"
)

func TestCreateDeterministicScores(t *testing.T) {
	t.Parallel()

	turn := completeTurn(t)
	var posts []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/public/scores" {
			t.Fatalf("unexpected request %s", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode score: %v", err)
		}
		posts = append(posts, body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := config.LangfuseConfig{Host: server.URL, PublicKey: "pk-test", SecretKey: "sk-test"}
	if err := CreateDeterministicScores(context.Background(), cfg, turn, "default"); err != nil {
		t.Fatalf("CreateDeterministicScores: %v", err)
	}
	if len(posts) != len(agenttrace.BuildDeterministicScores(turn)) {
		t.Fatalf("posts = %d, want %d", len(posts), len(agenttrace.BuildDeterministicScores(turn)))
	}
	seen := map[string]map[string]any{}
	for _, post := range posts {
		name, _ := post["name"].(string)
		seen[name] = post
		if post["id"] == "" || post["traceId"] != turn.TraceID || post["environment"] != "default" {
			t.Fatalf("score post incomplete: %#v", post)
		}
	}
	if seen["verification_passed"]["dataType"] != agenttrace.ScoreDataTypeBoolean {
		t.Fatalf("verification_passed = %#v", seen["verification_passed"])
	}
	if seen["outcome"]["dataType"] != agenttrace.ScoreDataTypeCategorical || seen["outcome"]["value"] == "" {
		t.Fatalf("outcome = %#v", seen["outcome"])
	}
	if seen["changed_file_count"]["dataType"] != agenttrace.ScoreDataTypeNumeric {
		t.Fatalf("changed_file_count = %#v", seen["changed_file_count"])
	}
}
