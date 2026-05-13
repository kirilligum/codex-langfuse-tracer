package langfuse

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/kirilligum/codex-langfuse-tracer/internal/agenttrace"
)

func enrichWorkspaceMetadata(ctx context.Context, turn agenttrace.Turn) agenttrace.Turn {
	if turn.GitBranch == "" {
		turn.GitBranch = gitBranch(ctx, turn.CWD)
	}
	return turn
}

func gitBranch(ctx context.Context, cwd string) string {
	if cwd == "" {
		return ""
	}
	info, err := os.Stat(cwd)
	if err != nil || !info.IsDir() {
		return ""
	}
	gitCtx, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
	defer cancel()

	cmd := exec.CommandContext(gitCtx, "git", "-C", cwd, "symbolic-ref", "--quiet", "--short", "HEAD")
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return trimMetadataValue(string(output), 200)
}

func trimMetadataValue(value string, max int) string {
	value = strings.TrimSpace(value)
	if max <= 0 || len(value) <= max {
		return value
	}
	return strings.TrimSpace(value[:max])
}
