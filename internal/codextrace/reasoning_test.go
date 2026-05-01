package codextrace

import (
	"strings"
	"testing"
)

// TEST-016
func TestVisibleReasoningOnly(t *testing.T) {
	t.Parallel()

	turn := parseCompleteFixture(t)
	found := false
	for _, observation := range turn.Observations {
		all := observation.Input + observation.Output + StableJSON(observation.Metadata)
		if strings.Contains(all, "HIDDEN_REASONING_SENTINEL") || strings.Contains(all, "ENCRYPTED_REASONING_SENTINEL") {
			t.Fatalf("hidden reasoning leaked in %s: %s", observation.Name, all)
		}
		if observation.Name == "codex.reasoning.summary" {
			found = true
			if observation.Output != "Need inspect files before editing." {
				t.Fatalf("reasoning summary = %q", observation.Output)
			}
		}
	}
	if !found {
		t.Fatal("visible reasoning summary observation missing")
	}
}
