package codextrace

import (
	"path/filepath"
	"sort"
	"strings"
)

const (
	CommandKindTest    = "test"
	CommandKindBuild   = "build"
	CommandKindLint    = "lint"
	CommandKindFormat  = "format"
	CommandKindGit     = "git"
	CommandKindRead    = "read"
	CommandKindSearch  = "search"
	CommandKindInstall = "install"
	CommandKindSystemd = "systemd"
	CommandKindNetwork = "network"
	CommandKindOther   = "other"
)

var commandKinds = []string{
	CommandKindTest,
	CommandKindBuild,
	CommandKindLint,
	CommandKindFormat,
	CommandKindGit,
	CommandKindRead,
	CommandKindSearch,
	CommandKindInstall,
	CommandKindSystemd,
	CommandKindNetwork,
	CommandKindOther,
}

type InsightRollup struct {
	ToolCount                int
	CommandCount             int
	FailedCommandCount       int
	PatchCount               int
	ChangedFileCount         int
	WebSearchCount           int
	VerificationCommandCount int
	VerificationStatus       string
	LastVerificationCommand  string
	LastVerificationStatus   string
	CommandKindCounts        map[string]int
	ChangedExtensions        []string
	TouchedTestFiles         []string
}

func ClassifyCommand(command string) string {
	normalized := strings.TrimSpace(strings.ToLower(command))
	fields := strings.Fields(normalized)
	if len(fields) == 0 {
		return CommandKindOther
	}
	first := fields[0]
	contains := func(parts ...string) bool {
		for _, part := range parts {
			if !strings.Contains(normalized, part) {
				return false
			}
		}
		return true
	}

	switch {
	case contains("go test") || contains("cargo test") || contains("pytest") || contains("npm test") || contains("pnpm test") || contains("yarn test") || contains("make test"):
		return CommandKindTest
	case contains("go build") || contains("cargo build") || contains("npm run build") || contains("pnpm build") || contains("yarn build") || contains("make build"):
		return CommandKindBuild
	case first == "golangci-lint" || contains("npm run lint") || contains("pnpm lint") || contains("yarn lint") || first == "eslint" || contains("ruff check"):
		return CommandKindLint
	case first == "gofmt" || contains("go fmt") || first == "prettier" || first == "rustfmt" || contains("cargo fmt") || contains("npm run format"):
		return CommandKindFormat
	case first == "git":
		return CommandKindGit
	case first == "cat" || first == "sed" || first == "nl" || first == "head" || first == "tail" || first == "wc" || first == "ls":
		return CommandKindRead
	case first == "rg" || first == "grep" || first == "find" || first == "fd":
		return CommandKindSearch
	case contains("npm install") || contains("pnpm install") || contains("yarn install") || contains("pip install") || contains("go mod download") || contains("apt-get install") || contains("brew install"):
		return CommandKindInstall
	case first == "systemctl" || first == "journalctl" || first == "systemd-analyze":
		return CommandKindSystemd
	case first == "curl" || first == "wget" || first == "gh":
		return CommandKindNetwork
	default:
		return CommandKindOther
	}
}

func BuildInsightRollup(turn Turn) InsightRollup {
	rollup := InsightRollup{
		VerificationStatus: "not_applicable",
		CommandKindCounts:  newCommandKindCounts(),
	}
	changedFiles := map[string]bool{}
	verificationFailed := false

	for _, observation := range turn.Observations {
		if observation.Type == "tool" {
			rollup.ToolCount++
		}
		switch observation.Name {
		case "codex.tool.exec_command":
			rollup.CommandCount++
			commandKind := stringValue(observation.Metadata["command_kind"])
			if commandKind == "" {
				commandKind = ClassifyCommand(observation.Input)
			}
			commandKind = normalizeCommandKind(commandKind)
			rollup.CommandKindCounts[commandKind]++
			failureType := stringValue(observation.Metadata["failure_type"])
			if failureType == "" {
				failureType = commandFailureType(observation.Metadata)
			}
			if isFailedCommand(failureType) {
				rollup.FailedCommandCount++
			}
			if isVerificationCommand(commandKind) {
				rollup.VerificationCommandCount++
				rollup.LastVerificationCommand = ExportText(observation.Input)
				rollup.LastVerificationStatus = stringValue(observation.Metadata["status"])
				if isFailedCommand(failureType) {
					verificationFailed = true
				}
			}
		case "codex.tool.apply_patch":
			rollup.PatchCount++
			for _, path := range stringSliceValue(observation.Metadata["changed_files"]) {
				if path != "" {
					changedFiles[path] = true
				}
			}
		case "codex.tool.web_search":
			rollup.WebSearchCount++
		}
	}

	paths := sortedKeys(changedFiles)
	rollup.ChangedFileCount = len(paths)
	rollup.ChangedExtensions = changedExtensions(paths)
	rollup.TouchedTestFiles = touchedTestFiles(paths)
	switch {
	case rollup.VerificationCommandCount == 0 && rollup.PatchCount > 0:
		rollup.VerificationStatus = "not_run"
	case rollup.VerificationCommandCount == 0:
		rollup.VerificationStatus = "not_applicable"
	case verificationFailed:
		rollup.VerificationStatus = "failed"
	default:
		rollup.VerificationStatus = "passed"
	}
	return rollup
}

func (r InsightRollup) Metadata() map[string]any {
	hasFileChanges := r.PatchCount > 0 || r.ChangedFileCount > 0
	metadata := map[string]any{
		"tool_count":                 r.ToolCount,
		"command_count":              r.CommandCount,
		"failed_command_count":       r.FailedCommandCount,
		"patch_count":                r.PatchCount,
		"changed_file_count":         r.ChangedFileCount,
		"verification_command_count": r.VerificationCommandCount,
		"verification_status":        r.VerificationStatus,
		"last_verification_command":  r.LastVerificationCommand,
		"last_verification_status":   r.LastVerificationStatus,
		"has_file_changes":           hasFileChanges,
		"is_read_only":               !hasFileChanges,
		"command_kinds":              commandKindsWithActivity(r.CommandKindCounts),
		"used_web_search":            r.WebSearchCount > 0,
		"web_search_count":           r.WebSearchCount,
		"changed_extensions":         r.ChangedExtensions,
		"touched_test_files":         r.TouchedTestFiles,
	}
	for _, kind := range commandKinds {
		count := r.CommandKindCounts[kind]
		metadata[kind+"_command_count"] = count
		metadata["ran_"+kind+"_command"] = count > 0
	}
	return metadata
}

func CommandInsightMetadata(payload map[string]any) map[string]any {
	metadata := map[string]any{
		"command_kind": ClassifyCommand(FormatCommand(payload["command"])),
		"failure_type": commandFailureType(payload),
	}
	if status := stringValue(payload["status"]); status != "" {
		metadata["status"] = status
	}
	if _, ok := payload["exit_code"]; ok {
		metadata["exit_code"] = intValue(payload["exit_code"])
	}
	if _, ok := payload["duration"]; ok {
		metadata["duration_ms"] = int(durationToNS(payload["duration"]) / 1_000_000)
	}
	return metadata
}

func isVerificationCommand(kind string) bool {
	switch kind {
	case CommandKindTest, CommandKindBuild, CommandKindLint, CommandKindFormat:
		return true
	default:
		return false
	}
}

func newCommandKindCounts() map[string]int {
	counts := make(map[string]int, len(commandKinds))
	for _, kind := range commandKinds {
		counts[kind] = 0
	}
	return counts
}

func normalizeCommandKind(kind string) string {
	kind = strings.ToLower(strings.TrimSpace(kind))
	for _, candidate := range commandKinds {
		if kind == candidate {
			return candidate
		}
	}
	return CommandKindOther
}

func commandKindsWithActivity(counts map[string]int) []string {
	active := make([]string, 0, len(commandKinds))
	for _, kind := range commandKinds {
		if counts[kind] > 0 {
			active = append(active, kind)
		}
	}
	sort.Strings(active)
	return active
}

func isFailedCommand(failureType string) bool {
	return failureType == "nonzero_exit" || failureType == "timeout"
}

func commandFailureType(payload map[string]any) string {
	status := strings.ToLower(strings.TrimSpace(stringValue(payload["status"])))
	if strings.Contains(status, "timeout") || strings.Contains(status, "timed_out") {
		return "timeout"
	}
	exitCode, hasExitCode := payload["exit_code"]
	if status == "" || !hasExitCode {
		return "unknown"
	}
	if intValue(exitCode) != 0 {
		return "nonzero_exit"
	}
	if status == "completed" || status == "success" || status == "succeeded" {
		return "none"
	}
	return "unknown"
}

func stringSliceValue(value any) []string {
	switch typed := value.(type) {
	case []string:
		return typed
	case []any:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := stringValue(item); text != "" {
				result = append(result, text)
			}
		}
		return result
	default:
		return nil
	}
}

func sortedKeys(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func changedExtensions(paths []string) []string {
	values := map[string]bool{}
	for _, path := range paths {
		extension := strings.ToLower(filepath.Ext(path))
		if extension != "" {
			values[extension] = true
		}
	}
	return sortedKeys(values)
}

func touchedTestFiles(paths []string) []string {
	values := map[string]bool{}
	for _, path := range paths {
		clean := filepath.ToSlash(path)
		base := filepath.Base(clean)
		if strings.Contains(clean, "/test/") || strings.HasPrefix(clean, "test/") || strings.Contains(clean, "/tests/") || strings.HasPrefix(clean, "tests/") || strings.HasSuffix(base, "_test.go") {
			values[path] = true
		}
	}
	return sortedKeys(values)
}
