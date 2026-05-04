package providers

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/kirilligum/codex-langfuse-tracer/internal/agenttrace"
)

// TEST-513
func TestProviderRegistryParsesCodexAndClaude(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		provider string
		path     string
	}{
		{provider: agenttrace.ProviderCodex, path: filepath.Join("..", "..", "testdata", "sources", "codex", "complete-no-tools.jsonl")},
		{provider: agenttrace.ProviderClaude, path: filepath.Join("..", "..", "testdata", "sources", "claude", "no-tools.jsonl")},
	} {
		tc := tc
		t.Run(tc.provider, func(t *testing.T) {
			t.Parallel()
			turns, err := ParseTurns(tc.provider, tc.path)
			if err != nil {
				t.Fatalf("ParseTurns(%s): %v", tc.provider, err)
			}
			exportable := agenttrace.ExportableTurns(turns)
			if len(exportable) != 1 || exportable[0].Provider != tc.provider {
				t.Fatalf("exportable turns = %+v", exportable)
			}
		})
	}
}

func TestProviderRegistryMetadata(t *testing.T) {
	t.Parallel()

	if Normalize("") != agenttrace.ProviderCodex {
		t.Fatalf("empty provider must normalize to Codex")
	}
	if RequiresExplicitPath(agenttrace.ProviderCodex) {
		t.Fatal("Codex should keep latest/session-id discovery")
	}
	if !RequiresExplicitPath(agenttrace.ProviderClaude) {
		t.Fatal("Claude should use explicit transcript paths only")
	}
	if DisplayName(agenttrace.ProviderClaude) != "Claude" {
		t.Fatalf("Claude display name = %q", DisplayName(agenttrace.ProviderClaude))
	}
	if _, err := Get("unknown"); !errors.Is(err, ErrUnsupportedProvider) {
		t.Fatalf("unknown provider error = %v", err)
	}
}
