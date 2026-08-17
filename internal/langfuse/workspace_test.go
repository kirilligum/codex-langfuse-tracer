package langfuse

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kirilligum/codex-langfuse-tracer/internal/agenttrace"
)

// TEST-701
func TestWorkspaceIdentity(t *testing.T) {
	t.Run("attached nested worktree", func(t *testing.T) {
		root := initWorkspaceRepository(t)
		runGit(t, root, "checkout", "-b", "Feature/One")
		nested := filepath.Join(root, "internal", "nested")
		if err := os.MkdirAll(nested, 0o755); err != nil {
			t.Fatal(err)
		}

		turn, environment, err := ResolveWorkspace(context.Background(), agenttrace.Turn{CWD: nested})
		if err != nil {
			t.Fatal(err)
		}
		if turn.GitBranch != "Feature/One" {
			t.Fatalf("git branch = %q, want Feature/One", turn.GitBranch)
		}
		assertEnvironment(t, environment)
		assertEnvironmentHash(t, environment, filepath.Base(root), "Feature/One")
		if !strings.HasPrefix(environment, normalizeEnvironmentComponent(filepath.Base(root), "repo")+"--feature-one-") {
			t.Fatalf("environment %q does not identify worktree root and branch", environment)
		}
	})

	t.Run("detached head", func(t *testing.T) {
		root := initWorkspaceRepository(t)
		runGit(t, root, "checkout", "--detach")

		turn, environment, err := ResolveWorkspace(context.Background(), agenttrace.Turn{CWD: root, GitBranch: "stale"})
		if err != nil {
			t.Fatal(err)
		}
		if turn.GitBranch != "" {
			t.Fatalf("detached branch metadata = %q, want empty", turn.GitBranch)
		}
		assertEnvironment(t, environment)
		assertEnvironmentHash(t, environment, filepath.Base(root), "detached")
		if !strings.Contains(environment, "--detached-") {
			t.Fatalf("detached environment = %q", environment)
		}
	})

	for _, test := range []struct {
		name string
		cwd  func(*testing.T) string
	}{
		{name: "non git", cwd: func(t *testing.T) string { return t.TempDir() }},
		{name: "missing cwd", cwd: func(t *testing.T) string { return filepath.Join(t.TempDir(), "missing") }},
	} {
		t.Run(test.name, func(t *testing.T) {
			turn, environment, err := ResolveWorkspace(context.Background(), agenttrace.Turn{CWD: test.cwd(t), GitBranch: "stale"})
			if err != nil {
				t.Fatal(err)
			}
			if environment != "default" {
				t.Fatalf("environment = %q, want default", environment)
			}
			if turn.GitBranch != "" {
				t.Fatalf("git branch = %q, want empty", turn.GitBranch)
			}
		})
	}

	t.Run("normalization", func(t *testing.T) {
		tests := []struct {
			name       string
			repository string
			branch     string
			prefix     string
		}{
			{name: "ordinary", repository: "codex-langfuse-tracer", branch: "main", prefix: "codex-langfuse-tracer--main-"},
			{name: "case and slash", repository: "Repo", branch: "Feature/One", prefix: "repo--feature-one-"},
			{name: "unicode", repository: "R\u00e9po", branch: "F\u00e9ature/\u0394", prefix: "r-po--f-ature-"},
			{name: "empty components", repository: "\u0394", branch: "\u00e9", prefix: "repo--branch-"},
			{name: "long", repository: strings.Repeat("repository", 10), branch: strings.Repeat("branch", 20), prefix: "repositoryrepository"},
			{name: "reserved", repository: "Langfuse-App", branch: "main", prefix: "repo-langfuse-app--main-"},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				first, err := workspaceEnvironment(test.repository, test.branch)
				if err != nil {
					t.Fatal(err)
				}
				second, err := workspaceEnvironment(test.repository, test.branch)
				if err != nil {
					t.Fatal(err)
				}
				if first != second {
					t.Fatalf("environment is unstable: %q != %q", first, second)
				}
				assertEnvironment(t, first)
				assertEnvironmentHash(t, first, test.repository, test.branch)
				if !strings.HasPrefix(first, test.prefix) {
					t.Fatalf("environment %q does not have prefix %q", first, test.prefix)
				}
			})
		}
	})

	t.Run("hash distinguishes normalized collisions", func(t *testing.T) {
		first, err := workspaceEnvironment("repo", "feature/one")
		if err != nil {
			t.Fatal(err)
		}
		second, err := workspaceEnvironment("repo", "feature-one")
		if err != nil {
			t.Fatal(err)
		}
		if first == second {
			t.Fatalf("normalized collision: %q", first)
		}
	})

	t.Run("hostname", func(t *testing.T) {
		got, err := resolveHostnameUserID(func() (string, error) { return "  devbox-01  ", nil })
		if err != nil {
			t.Fatal(err)
		}
		if got != "devbox-01" {
			t.Fatalf("hostname = %q, want devbox-01", got)
		}

		for _, getHostname := range []func() (string, error){
			func() (string, error) { return "", nil },
			func() (string, error) { return "", errors.New("hostname unavailable") },
		} {
			if _, err := resolveHostnameUserID(getHostname); err == nil {
				t.Fatal("expected hostname error")
			}
		}
	})
}

func initWorkspaceRepository(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	runGit(t, root, "init")
	runGit(t, root,
		"-c", "user.name=Test",
		"-c", "user.email=test@example.com",
		"commit", "--allow-empty", "-m", "initial",
	)
	return root
}

func assertEnvironment(t *testing.T, environment string) {
	t.Helper()
	if environment == "" || len(environment) > 40 {
		t.Fatalf("invalid environment length %d: %q", len(environment), environment)
	}
	if strings.HasPrefix(environment, "langfuse") {
		t.Fatalf("environment uses reserved prefix: %q", environment)
	}
	for _, char := range environment {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' && char != '_' {
			t.Fatalf("environment contains invalid character %q: %q", char, environment)
		}
	}
}

func assertEnvironmentHash(t *testing.T, environment, repository, branch string) {
	t.Helper()
	hash := sha256.Sum256([]byte(repository + "\x00" + branch))
	want := fmt.Sprintf("-%x", hash[:3])
	if !strings.HasSuffix(environment, want) {
		t.Fatalf("environment %q does not end in stable hash %q", environment, want)
	}
}
