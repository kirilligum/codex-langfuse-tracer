package langfuse

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/kirilligum/codex-langfuse-tracer/internal/agenttrace"
	"github.com/kirilligum/codex-langfuse-tracer/internal/config"
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

func userIDAttribute(turn agenttrace.Turn, opts exportOptions) string {
	if opts.UserIDMode != config.UserIDModeWorkspace {
		return ""
	}
	return workspaceUserID(turn.CWD, turn.GitBranch)
}

func workspaceUserID(cwd, gitBranch string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	return formatWorkspaceUserID(normalizeHomePath(cwd, home), gitBranch)
}

func formatWorkspaceUserID(path, gitBranch string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	gitBranch = trimMetadataValue(gitBranch, 80)
	if gitBranch == "" {
		return trimMetadataValue(path, 200)
	}
	suffix := " (" + gitBranch + ")"
	if len(path)+len(suffix) <= 200 {
		return path + suffix
	}
	maxPath := 200 - len(suffix)
	if maxPath <= 0 {
		return trimMetadataValue(path, 200)
	}
	return strings.TrimSpace(path[:maxPath]) + suffix
}

func normalizeHomePath(cwd, home string) string {
	cwd = filepath.Clean(strings.TrimSpace(cwd))
	if cwd == "." {
		return ""
	}
	if home == "" {
		return filepath.ToSlash(cwd)
	}
	home = filepath.Clean(strings.TrimSpace(home))
	if home == "." {
		return filepath.ToSlash(cwd)
	}
	rel, err := filepath.Rel(home, cwd)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return filepath.ToSlash(cwd)
	}
	if rel == "." {
		return "~"
	}
	return "~/" + filepath.ToSlash(rel)
}

func trimMetadataValue(value string, max int) string {
	value = strings.TrimSpace(value)
	if max <= 0 || len(value) <= max {
		return value
	}
	return strings.TrimSpace(value[:max])
}
