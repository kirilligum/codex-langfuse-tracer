package codextrace

import (
	"strings"
	"testing"

	"github.com/kirilligum/codex-langfuse-tracer/internal/buildinfo"
)

// TEST-006
func TestRedactionTruncationAndTerminal(t *testing.T) {
	t.Parallel()

	redacted := ExportText("Basic dGVzdGRhdGF0ZXN0ZGF0YXRlc3RkYXRh sk-lf-live-secret pk-lf-public ghp_live_secret api_key = abcdefghijklmnop")
	for _, forbidden := range []string{"dGVzdGRhdGF0ZXN0ZGF0YXRlc3RkYXRh", "sk-lf-live-secret", "pk-lf-public", "ghp_live_secret", "abcdefghijklmnop"} {
		if strings.Contains(redacted, forbidden) {
			t.Fatalf("redaction leaked %q in %q", forbidden, redacted)
		}
	}

	longText := strings.Repeat("x", buildinfo.MaxFieldChars+100)
	limited := ExportText(longText)
	if len(limited) <= buildinfo.MaxFieldChars {
		t.Fatalf("limited length = %d, want greater than cap due suffix", len(limited))
	}
	if !strings.Contains(limited, "[truncated to 50000 characters]") {
		t.Fatalf("missing truncation suffix")
	}

	turn := parseCompleteFixture(t)
	terminal := TerminalObservation(turn)
	if terminal == nil {
		t.Fatal("terminal observation missing")
	}
	if terminal.Metadata["event_count"] != len(turn.TerminalEntries) {
		t.Fatalf("terminal event_count = %#v entries=%d", terminal.Metadata["event_count"], len(turn.TerminalEntries))
	}
	if strings.Contains(terminal.Output, "## output\n## output") {
		t.Fatalf("terminal output duplicated section: %s", terminal.Output)
	}
}

// EVAL-003
func TestEvalRedactionCorpus(t *testing.T) {
	t.Parallel()
	redacted := ExportText("bearer_token: abcdefghijklmnop sk-or-v1-abcdefghijklmnop gsk_abcdefghijklmnop")
	for _, forbidden := range []string{"abcdefghijklmnop", "sk-or-v1-abcdefghijklmnop", "gsk_abcdefghijklmnop"} {
		if strings.Contains(redacted, forbidden) {
			t.Fatalf("redaction leaked %q in %q", forbidden, redacted)
		}
	}
}
