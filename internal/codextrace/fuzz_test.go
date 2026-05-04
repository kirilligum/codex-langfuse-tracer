package codextrace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kirilligum/codex-langfuse-tracer/internal/agenttrace"
)

func FuzzParseTurnsDoesNotPanic(f *testing.F) {
	for _, name := range []string{
		"complete-tools.jsonl",
		"incomplete-turn.jsonl",
		"corrupt-rollout.jsonl",
	} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "sources", "codex", name))
		if err != nil {
			f.Fatal(err)
		}
		f.Add(string(raw))
	}
	f.Add("{}\n")

	f.Fuzz(func(t *testing.T, raw string) {
		path := filepath.Join(t.TempDir(), "rollout.jsonl")
		if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		_, _ = ParseTurns(path)
	})
}

func FuzzExportTextRedactsSentinels(f *testing.F) {
	f.Add("", "")
	f.Add("prefix ", " suffix")
	f.Add(strings.Repeat("x", 128), strings.Repeat("y", 128))

	f.Fuzz(func(t *testing.T, prefix, suffix string) {
		input := prefix + " Basic dGVzdGRhdGF0ZXN0ZGF0YXRlc3RkYXRh sk-lf-live-secret pk-lf-public ghp_live_secret api_key = abcdefghijklmnop " + suffix
		output := agenttrace.ExportText(input)
		for _, forbidden := range []string{
			"dGVzdGRhdGF0ZXN0ZGF0YXRlc3RkYXRh",
			"sk-lf-live-secret",
			"pk-lf-public",
			"ghp_live_secret",
			"abcdefghijklmnop",
		} {
			if strings.Contains(output, forbidden) {
				t.Fatalf("ExportText leaked %q in %q", forbidden, output)
			}
		}
	})
}
