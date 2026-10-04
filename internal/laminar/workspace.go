package laminar

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/kirilligum/codex-langfuse-tracer/internal/agenttrace"
)

const (
	defaultWorkspaceEnvironment = "default"
	maxEnvironmentLength        = 40
	environmentHashLength       = 6
)

// ResolveWorkspace resolves the export-time Git worktree and branch and returns
// the turn metadata and environment that must be used for the export.
func ResolveWorkspace(ctx context.Context, turn agenttrace.Turn) (agenttrace.Turn, string, error) {
	turn.GitBranch = ""
	if turn.CWD == "" {
		return turn, defaultWorkspaceEnvironment, nil
	}
	info, err := os.Stat(turn.CWD)
	if err != nil || !info.IsDir() {
		return turn, defaultWorkspaceEnvironment, nil
	}

	gitCtx, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(gitCtx, "git", "-C", turn.CWD, "rev-parse", "--show-toplevel", "--abbrev-ref", "HEAD")
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
	output, err := cmd.Output()
	if err != nil {
		return turn, defaultWorkspaceEnvironment, nil
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) != 2 {
		return turn, defaultWorkspaceEnvironment, nil
	}

	repository := filepath.Base(filepath.Clean(strings.TrimSpace(lines[0])))
	branch := strings.TrimSpace(lines[1])
	if repository == "" || repository == "." || branch == "" {
		return turn, defaultWorkspaceEnvironment, nil
	}
	if branch == "HEAD" {
		branch = "detached"
	} else {
		turn.GitBranch = trimMetadataValue(branch, 200)
	}
	environment, err := workspaceEnvironment(repository, branch)
	if err != nil {
		return turn, "", err
	}
	remote, remoteErr := exec.CommandContext(gitCtx, "git", "-C", turn.CWD, "config", "--get", "remote.origin.url").Output()
	if remoteErr == nil {
		turn.Repository = repositoryName(strings.TrimSpace(string(remote)))
	}
	return turn, environment, nil
}

func repositoryName(remote string) string {
	var path string
	if parsed, err := url.Parse(remote); err == nil && parsed.Host != "" {
		switch parsed.Scheme {
		case "https", "http", "ssh", "git":
			path = parsed.Path
		default:
			return ""
		}
	} else if colon := strings.Index(remote, ":"); colon >= 0 {
		path = strings.SplitN(strings.SplitN(remote[colon+1:], "?", 2)[0], "#", 2)[0]
	}
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	if !strings.Contains(path, "/") || strings.ContainsAny(path, "@ \t\r\n") {
		return ""
	}
	return path
}

func workspaceEnvironment(repository, branch string) (string, error) {
	repositoryPart := normalizeEnvironmentComponent(repository, "repo")
	branchPart := normalizeEnvironmentComponent(branch, "branch")
	readable := repositoryPart + "--" + branchPart
	if strings.HasPrefix(readable, "langfuse") {
		readable = "repo-" + readable
	}

	maxReadableLength := maxEnvironmentLength - environmentHashLength - 1
	if len(readable) > maxReadableLength {
		readable = strings.TrimRight(readable[:maxReadableLength], "-_")
	}
	if readable == "" {
		readable = "repo"
	}
	hash := sha256.Sum256([]byte(repository + "\x00" + branch))
	environment := fmt.Sprintf("%s-%x", readable, hash[:environmentHashLength/2])
	if err := validateWorkspaceEnvironment(environment); err != nil {
		return "", err
	}
	return environment, nil
}

func normalizeEnvironmentComponent(value, empty string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var normalized strings.Builder
	separator := false
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '-' || char == '_' {
			normalized.WriteRune(char)
			separator = false
			continue
		}
		if normalized.Len() > 0 && !separator {
			normalized.WriteByte('-')
			separator = true
		}
	}
	result := strings.Trim(normalized.String(), "-_")
	if result == "" {
		return empty
	}
	return result
}

func validateWorkspaceEnvironment(environment string) error {
	if environment == "" || len(environment) > maxEnvironmentLength {
		return fmt.Errorf("invalid workspace environment length %d", len(environment))
	}
	if strings.HasPrefix(environment, "langfuse") {
		return fmt.Errorf("workspace environment uses reserved prefix")
	}
	for _, char := range environment {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' && char != '_' {
			return fmt.Errorf("workspace environment contains invalid character %q", char)
		}
	}
	return nil
}

// HostnameUserID returns the process-scoped hostname used for the Laminar user association.
func HostnameUserID() (string, error) {
	return resolveHostnameUserID(os.Hostname)
}

func resolveHostnameUserID(getHostname func() (string, error)) (string, error) {
	hostname, err := getHostname()
	if err != nil {
		return "", fmt.Errorf("resolve hostname: %w", err)
	}
	hostname = strings.TrimSpace(hostname)
	if hostname == "" {
		return "", fmt.Errorf("resolve hostname: empty hostname")
	}
	return hostname, nil
}

func trimMetadataValue(value string, max int) string {
	value = strings.TrimSpace(value)
	if max <= 0 || len(value) <= max {
		return value
	}
	return strings.TrimSpace(value[:max])
}
