package codextrace

import (
	"fmt"
	"regexp"

	"github.com/kirilligum/codex-langfuse-tracer/internal/buildinfo"
)

var secretPatterns = []struct {
	pattern     *regexp.Regexp
	replacement string
}{
	{regexp.MustCompile(`Basic [A-Za-z0-9+/=]{32,}`), "Basic <redacted>"},
	{regexp.MustCompile(`sk-lf-[A-Za-z0-9-]+`), "sk-lf-<redacted>"},
	{regexp.MustCompile(`pk-lf-[A-Za-z0-9-]+`), "pk-lf-<redacted>"},
	{regexp.MustCompile(`sk-or-v1-[A-Za-z0-9]+`), "sk-or-v1-<redacted>"},
	{regexp.MustCompile(`gsk_[A-Za-z0-9]+`), "gsk_<redacted>"},
	{regexp.MustCompile(`gh[pousr]_[A-Za-z0-9_]+`), "gh<redacted>"},
	{regexp.MustCompile(`(?i)(api[_-]?key|secret[_-]?key|access[_-]?token|bearer[_-]?token)(["' :=]+)([A-Za-z0-9_./+=:-]{16,})`), `$1$2<redacted>`},
}

func RedactText(value string) string {
	for _, item := range secretPatterns {
		value = item.pattern.ReplaceAllString(value, item.replacement)
	}
	return value
}

func LimitText(value string) string {
	if len(value) <= buildinfo.MaxFieldChars {
		return value
	}
	return value[:buildinfo.MaxFieldChars] + fmt.Sprintf("\n\n[truncated to %d characters]", buildinfo.MaxFieldChars)
}

func ExportText(value string) string {
	return LimitText(RedactText(value))
}
