package langfuse

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kirilligum/codex-langfuse-tracer/internal/buildinfo"
)

// EVAL-004
func TestEvalOTLPPayloadSizeAndLatency(t *testing.T) {
	t.Parallel()

	start := time.Now()
	exporter := &memoryExporter{}
	turn := completeTurn(t)
	if err := EmitSpans(context.Background(), turn, 0, true, "default", "test-host", buildinfo.DefaultServiceName, exporter); err != nil {
		t.Fatalf("EmitSpans: %v", err)
	}
	if len(exporter.Snapshots()) == 0 {
		t.Fatal("no spans exported")
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("EmitSpans elapsed = %s, want <= 500ms", elapsed)
	}
}

// EVAL-701
func TestEvalEnvironmentCorpus(t *testing.T) {
	const seed = 701
	started := time.Now()
	tests := []struct {
		repository string
		branch     string
	}{
		{repository: "codex-langfuse-tracer", branch: "main"},
		{repository: "Repo", branch: "Feature/One"},
		{repository: "repo", branch: "feature-one"},
		{repository: "repo", branch: "feature/one"},
		{repository: "LANGFUSE", branch: "production"},
		{repository: "R\u00e9po", branch: "F\u00e9ature/\u0394"},
		{repository: "\u0394", branch: "\u00e9"},
		{repository: "linked-worktree", branch: "detached"},
		{repository: strings.Repeat("repository", 20), branch: strings.Repeat("branch", 40)},
		{repository: strings.Repeat("r", 80), branch: "main"},
		{repository: "repo_name", branch: "feature_with_underscores"},
		{repository: "repo.name", branch: "release+candidate"},
	}

	valid := 0
	stable := 0
	maximumLength := 0
	environments := make([]string, 0, len(tests))
	for _, test := range tests {
		first, err := workspaceEnvironment(test.repository, test.branch)
		if err != nil {
			t.Fatalf("workspaceEnvironment(%q, %q): %v", test.repository, test.branch, err)
		}
		second, err := workspaceEnvironment(test.repository, test.branch)
		if err != nil {
			t.Fatalf("repeat workspaceEnvironment(%q, %q): %v", test.repository, test.branch, err)
		}
		if err := validateWorkspaceEnvironment(first); err == nil {
			valid++
		}
		hash := sha256.Sum256([]byte(test.repository + "\x00" + test.branch))
		if first == second && strings.HasSuffix(first, fmt.Sprintf("-%x", hash[:3])) {
			stable++
		}
		if len(first) > maximumLength {
			maximumLength = len(first)
		}
		environments = append(environments, first)
	}

	collisionPairs := [][2]int{{1, 2}, {2, 3}}
	collisions := 0
	for _, pair := range collisionPairs {
		if environments[pair[0]] == environments[pair[1]] {
			collisions++
		}
	}
	validityRate := float64(valid) / float64(len(tests))
	stableHashRate := float64(stable) / float64(len(tests))
	elapsed := time.Since(started)
	t.Logf("seed=%d environment_validity_rate=%.1f stable_hash_rate=%.1f defined_pair_collision_count=%d maximum_environment_length=%d runtime=%s", seed, validityRate, stableHashRate, collisions, maximumLength, elapsed)
	if validityRate != 1 {
		t.Fatalf("environment validity rate = %f, want 1", validityRate)
	}
	if stableHashRate != 1 {
		t.Fatalf("stable hash rate = %f, want 1", stableHashRate)
	}
	if collisions != 0 {
		t.Fatalf("defined pair collisions = %d, want 0", collisions)
	}
	if maximumLength != maxEnvironmentLength {
		t.Fatalf("maximum environment length = %d, want %d", maximumLength, maxEnvironmentLength)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("environment corpus runtime = %s, want <= 5s", elapsed)
	}
}
