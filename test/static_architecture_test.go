package test

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TEST-502
func TestNoDuplicateAgentTraceLogic(t *testing.T) {
	t.Parallel()

	repoRoot := ".."
	requiredAgentTraceFiles := []string{
		filepath.Join(repoRoot, "internal", "agenttrace", "model.go"),
		filepath.Join(repoRoot, "internal", "agenttrace", "privacy.go"),
		filepath.Join(repoRoot, "internal", "agenttrace", "terminal.go"),
		filepath.Join(repoRoot, "internal", "agenttrace", "insight.go"),
	}
	for _, path := range requiredAgentTraceFiles {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("missing shared agenttrace file %s: %v", path, err)
		}
	}

	for _, owner := range []struct {
		name string
		want string
	}{
		{name: "type Turn struct", want: filepath.Join("internal", "agenttrace", "model.go")},
		{name: "func ExportText", want: filepath.Join("internal", "agenttrace", "privacy.go")},
		{name: "func StableTraceID", want: filepath.Join("internal", "agenttrace", "model.go")},
		{name: "func AddTerminalEntry", want: filepath.Join("internal", "agenttrace", "terminal.go")},
		{name: "func AddObservation", want: filepath.Join("internal", "agenttrace", "terminal.go")},
		{name: "func TerminalObservation", want: filepath.Join("internal", "agenttrace", "terminal.go")},
		{name: "func BuildInsightRollup", want: filepath.Join("internal", "agenttrace", "insight.go")},
		{name: "func FormatCommand", want: filepath.Join("internal", "agenttrace", "format.go")},
		{name: "func ObservationBounds", want: filepath.Join("internal", "agenttrace", "time.go")},
	} {
		got := sourceFilesContaining(t, repoRoot, owner.name)
		if len(got) != 1 || filepath.ToSlash(got[0]) != filepath.ToSlash(owner.want) {
			t.Fatalf("%s owners = %#v, want only %s", owner.name, got, owner.want)
		}
	}

	agenttraceFiles := goFilesUnder(t, filepath.Join(repoRoot, "internal", "agenttrace"))
	for _, path := range agenttraceFiles {
		text := readText(t, path)
		for _, forbidden := range []string{
			`"github.com/kirilligum/codex-langfuse-tracer/internal/codextrace"`,
			`"github.com/kirilligum/codex-langfuse-tracer/internal/claudetrace"`,
			`"github.com/kirilligum/codex-langfuse-tracer/internal/langfuse"`,
		} {
			if strings.Contains(text, forbidden) {
				t.Fatalf("%s imports forbidden provider/runtime package %s", path, forbidden)
			}
		}
	}
}

// EVAL-002
func TestEvalAgentTraceOwnershipSurface(t *testing.T) {
	t.Parallel()

	repoRoot := ".."
	agenttraceFiles := goFilesUnder(t, filepath.Join(repoRoot, "internal", "agenttrace"))
	codextraceFiles := goFilesUnder(t, filepath.Join(repoRoot, "internal", "codextrace"))
	if len(agenttraceFiles) < 7 {
		t.Fatalf("agenttrace implementation file count = %d, want at least 7", len(agenttraceFiles))
	}
	for _, path := range codextraceFiles {
		text := readText(t, path)
		for _, forbidden := range []string{
			"type Turn struct",
			"type Observation struct",
			"func ExportText",
			"func StableTraceID",
			"func StableSpanID",
			"func TerminalObservation",
			"func BuildInsightRollup",
		} {
			if strings.Contains(text, forbidden) {
				t.Fatalf("%s still owns shared helper %q", path, forbidden)
			}
		}
	}
}

// TEST-512
func TestNoLegacyDuplicateClaudePaths(t *testing.T) {
	t.Parallel()

	claudeBudgetFlag := strings.Join([]string{"--max", "budget", "usd"}, "-")
	for _, path := range []string{
		filepath.Join("..", "cmd", "codex-langfuse-exporter", "main.go"),
		filepath.Join("..", "internal", "claudehook", "hook.go"),
		filepath.Join("..", "internal", "watch", "watch.go"),
		filepath.Join("..", "install.sh"),
		filepath.Join("..", "uninstall.sh"),
		filepath.Join("..", "README.md"),
		filepath.Join("..", "AGENTS.md"),
		filepath.Join("..", "TESTING.md"),
	} {
		text := readText(t, path)
		for _, forbidden := range []string{
			"old_wrapper_dst",
			"export_codex_session_to_langfuse.py",
			"claude -p",
			"~/.claude/projects",
			claudeBudgetFlag,
		} {
			if strings.Contains(text, forbidden) {
				t.Fatalf("%s contains legacy or duplicate path marker %q", path, forbidden)
			}
		}
	}
}

// TEST-513
func TestProviderParserDispatchHasOneOwner(t *testing.T) {
	t.Parallel()

	for _, path := range []string{
		filepath.Join("..", "cmd", "codex-langfuse-exporter", "main.go"),
		filepath.Join("..", "internal", "watch", "watch.go"),
		filepath.Join("..", "test", "contract_test.go"),
	} {
		text := readText(t, path)
		for _, forbidden := range []string{
			`"github.com/kirilligum/codex-langfuse-tracer/internal/claudetrace"`,
			"claudetrace.ParseTurns",
		} {
			if strings.Contains(text, forbidden) {
				t.Fatalf("%s owns provider parser dispatch through %q", path, forbidden)
			}
		}
	}
	providers := readText(t, filepath.Join("..", "internal", "providers", "providers.go"))
	for _, required := range []string{
		"func ParseTurns",
		"ProviderCodex",
		"ProviderClaude",
		"codextrace.ParseTurns",
		"claudetrace.ParseTurns",
	} {
		if !strings.Contains(providers, required) {
			t.Fatalf("provider registry missing %q", required)
		}
	}
}

// TEST-521
func TestNoLegacySemanticToolNames(t *testing.T) {
	t.Parallel()

	repoRoot := ".."
	forbidden := []string{
		strings.Join([]string{"codex", "tool", "exec_command"}, "."),
		strings.Join([]string{"codex", "tool", "apply_patch"}, "."),
		strings.Join([]string{"claude", "tool", "bash"}, "."),
		strings.Join([]string{"exec_command", "tool", "count"}, "_"),
		strings.Join([]string{"apply_patch", "tool", "count"}, "_"),
		strings.Join([]string{"bash", "tool", "count"}, "_"),
	}
	for _, path := range semanticContractFiles(t, repoRoot) {
		text := readText(t, path)
		for _, value := range forbidden {
			if strings.Contains(text, value) {
				t.Fatalf("%s contains legacy semantic tool name %q", path, value)
			}
		}
	}
}

func semanticContractFiles(t *testing.T, repoRoot string) []string {
	t.Helper()
	var paths []string
	for _, root := range []string{
		filepath.Join(repoRoot, "cmd"),
		filepath.Join(repoRoot, "internal"),
		filepath.Join(repoRoot, "test"),
		filepath.Join(repoRoot, "testdata", "golden"),
	} {
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			if strings.HasSuffix(path, ".go") || strings.HasSuffix(path, ".json") {
				paths = append(paths, path)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{
		filepath.Join(repoRoot, "README.md"),
		filepath.Join(repoRoot, "TESTING.md"),
		filepath.Join(repoRoot, "AGENTS.md"),
	} {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func sourceFilesContaining(t *testing.T, repoRoot, needle string) []string {
	t.Helper()
	var matches []string
	for _, dir := range []string{
		filepath.Join(repoRoot, "internal", "agenttrace"),
		filepath.Join(repoRoot, "internal", "codextrace"),
	} {
		for _, path := range goFilesUnder(t, dir) {
			if strings.Contains(readText(t, path), needle) {
				rel, err := filepath.Rel(repoRoot, path)
				if err != nil {
					t.Fatal(err)
				}
				matches = append(matches, rel)
			}
		}
	}
	sort.Strings(matches)
	return matches
}

func goFilesUnder(t *testing.T, root string) []string {
	t.Helper()
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(paths)
	return paths
}

func readText(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
