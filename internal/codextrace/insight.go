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

var toolFamilies = []string{
	"exec_command",
	"apply_patch",
	"web_search",
	"mcp",
	"tool_search",
}

type InsightRollup struct {
	ToolCount                int
	CommandCount             int
	FailedCommandCount       int
	PatchCount               int
	ChangedFileCount         int
	VerificationCommandCount int
	VerificationStatus       string
	LastVerificationCommand  string
	LastVerificationStatus   string
	CommandKindCounts        map[string]int
	ToolFamilyCounts         map[string]int
	MCPServerCounts          map[string]int
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
		ToolFamilyCounts:   newToolFamilyCounts(),
		MCPServerCounts:    map[string]int{},
	}
	changedFiles := map[string]bool{}
	verificationFailed := false

	for _, observation := range turn.Observations {
		if observation.Type == "tool" {
			rollup.ToolCount++
			if family := toolFamily(observation.Name); family != "" {
				rollup.ToolFamilyCounts[family]++
			}
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
		case "codex.tool.mcp":
			if server := normalizeMCPServer(stringValue(observation.Metadata["mcp_server"])); server != "" {
				rollup.MCPServerCounts[server]++
			}
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
		"changed_extensions":         r.ChangedExtensions,
		"touched_test_files":         r.TouchedTestFiles,
		"navigation":                 strings.Join(r.navigationValues(), " "),
	}
	for _, kind := range commandKinds {
		metadata[kind+"_command_count"] = r.CommandKindCounts[kind]
	}
	for _, family := range toolFamilies {
		metadata[family+"_tool_count"] = r.ToolFamilyCounts[family]
	}
	return metadata
}

func (r InsightRollup) navigationValues() []string {
	values := []string{"verification:" + r.VerificationStatus}
	if r.PatchCount > 0 || r.ChangedFileCount > 0 {
		values = append(values, "files:changed")
	} else {
		values = append(values, "files:read_only")
	}
	for _, kind := range commandKinds {
		if r.CommandKindCounts[kind] > 0 {
			values = append(values, "command:"+kind)
		}
	}
	for _, family := range toolFamilies {
		if r.ToolFamilyCounts[family] > 0 {
			values = append(values, "tool:"+family)
		}
	}
	sort.Strings(values)
	return values
}

func (r InsightRollup) Tags() []string {
	values := append([]string{}, r.navigationValues()...)
	for _, server := range sortedMCPServers(r.MCPServerCounts) {
		values = append(values, "mcp:"+server)
	}
	return sortedUnique(values)
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

func MCPToolMetadata(payload map[string]any) map[string]any {
	invocation := mapValue(payload["invocation"])
	metadata := map[string]any{}
	if server := normalizeMCPServer(stringValue(invocation["server"])); server != "" {
		metadata["mcp_server"] = server
	}
	if tool := strings.TrimSpace(stringValue(invocation["tool"])); tool != "" {
		metadata["mcp_tool"] = tool
	}
	return metadata
}

func normalizeMCPServer(server string) string {
	server = strings.ToLower(strings.TrimSpace(server))
	if server == "" {
		return ""
	}
	for i, r := range server {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			continue
		}
		if i > 0 && (r == '.' || r == '_' || r == '-') {
			continue
		}
		return ""
	}
	return server
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

func newToolFamilyCounts() map[string]int {
	counts := make(map[string]int, len(toolFamilies))
	for _, family := range toolFamilies {
		counts[family] = 0
	}
	return counts
}

func toolFamily(name string) string {
	switch name {
	case "codex.tool.exec_command":
		return "exec_command"
	case "codex.tool.apply_patch":
		return "apply_patch"
	case "codex.tool.web_search":
		return "web_search"
	case "codex.tool.mcp":
		return "mcp"
	case "codex.tool.tool_search":
		return "tool_search"
	default:
		return ""
	}
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

func sortedMCPServers(values map[string]int) []string {
	result := make([]string, 0, len(values))
	for value, count := range values {
		if count > 0 {
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func sortedUnique(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
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
