package agenttrace

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
)

func StableJSON(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return StringValue(value)
	}
	return strings.TrimSuffix(buf.String(), "\n")
}

func FormatCommand(command any) string {
	parts := SliceValue(command)
	if len(parts) > 0 {
		if len(parts) >= 3 && StringValue(parts[len(parts)-2]) == "-lc" {
			return StringValue(parts[len(parts)-1])
		}
		rendered := make([]string, 0, len(parts))
		for _, part := range parts {
			rendered = append(rendered, StringValue(part))
		}
		return strings.Join(rendered, " ")
	}
	return StableJSON(command)
}

func CommandOutput(payload map[string]any) string {
	if value := payload["formatted_output"]; StringValue(value) != "" {
		return "## output\n" + StableJSON(value)
	}
	if value := payload["aggregated_output"]; StringValue(value) != "" {
		return "## output\n" + StableJSON(value)
	}
	var parts []string
	for _, item := range []struct {
		label string
		key   string
	}{
		{"stdout", "stdout"},
		{"stderr", "stderr"},
	} {
		if value := payload[item.key]; StringValue(value) != "" {
			parts = append(parts, "## "+item.label+"\n"+StableJSON(value))
		}
	}
	return strings.Join(parts, "\n\n")
}

func CommandTerminalText(payload map[string]any) string {
	parts := []string{"Command:\n" + FormatCommand(payload["command"])}
	if output := CommandOutput(payload); output != "" {
		parts = append(parts, "Output:\n"+output)
	}
	status := StringValue(payload["status"])
	exitCode := payload["exit_code"]
	if status != "" || exitCode != nil {
		if status == "" {
			status = "unknown"
		}
		parts = append(parts, "Status: "+status+" exit_code="+StringValue(exitCode))
	}
	return strings.Join(parts, "\n\n")
}

func PatchOutput(payload map[string]any) string {
	var parts []string
	if stdout := StringValue(payload["stdout"]); stdout != "" {
		parts = append(parts, "## stdout\n"+stdout)
	}
	if stderr := StringValue(payload["stderr"]); stderr != "" {
		parts = append(parts, "## stderr\n"+stderr)
	}
	for path, change := range MapValue(payload["changes"]) {
		entry := MapValue(change)
		parts = append(parts, "## "+path+" ("+StringOr(entry["type"], "change")+")")
		if movePath := StringValue(entry["move_path"]); movePath != "" {
			parts = append(parts, "moved to: "+movePath)
		}
		if diff := StringValue(entry["unified_diff"]); diff != "" {
			parts = append(parts, "```diff\n"+diff+"\n```")
		} else if content := StringValue(entry["content"]); content != "" {
			parts = append(parts, "```text\n"+content+"\n```")
		}
	}
	return strings.Join(parts, "\n\n")
}

func ToolTerminalText(input, output string) string {
	var parts []string
	if input != "" {
		parts = append(parts, "Input:\n"+input)
	}
	if output != "" {
		parts = append(parts, "Output:\n"+output)
	}
	return strings.Join(parts, "\n\n")
}

func ReasoningSummaryText(summary any) string {
	if text := strings.TrimSpace(StringValue(summary)); text != "" && text != "[]" {
		if _, ok := summary.(string); ok {
			return text
		}
	}
	var parts []string
	for _, item := range SliceValue(summary) {
		switch typed := item.(type) {
		case string:
			parts = append(parts, typed)
		default:
			entry := MapValue(typed)
			if text := StringOr(entry["text"], StringValue(entry["summary"])); text != "" {
				parts = append(parts, StableJSON(text))
			}
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func FileChangeMetadata(changes map[string]any) map[string]any {
	changedFiles := []string{}
	addedFiles := []string{}
	modifiedFiles := []string{}
	deletedFiles := []string{}
	movedFiles := []string{}
	fileChangeTypes := map[string]string{}

	for path, raw := range changes {
		change := MapValue(raw)
		changeType := StringOr(change["type"], "change")
		changedFiles = append(changedFiles, path)
		fileChangeTypes[path] = changeType
		switch changeType {
		case "add":
			addedFiles = append(addedFiles, path)
		case "delete":
			deletedFiles = append(deletedFiles, path)
		default:
			modifiedFiles = append(modifiedFiles, path)
		}
		if movePath := StringValue(change["move_path"]); movePath != "" {
			movedFiles = append(movedFiles, path+" -> "+movePath)
		}
	}
	sort.Strings(changedFiles)
	sort.Strings(addedFiles)
	sort.Strings(modifiedFiles)
	sort.Strings(deletedFiles)
	sort.Strings(movedFiles)
	return map[string]any{
		"changed_files":      changedFiles,
		"added_files":        addedFiles,
		"modified_files":     modifiedFiles,
		"deleted_files":      deletedFiles,
		"moved_files":        movedFiles,
		"file_change_types":  fileChangeTypes,
		"changed_file_count": len(changedFiles),
	}
}

func MetadataWithoutLargeFields(payload map[string]any, exclude map[string]bool) map[string]any {
	metadata := map[string]any{}
	for key, value := range payload {
		if exclude[key] {
			continue
		}
		switch value.(type) {
		case map[string]any, []any:
			metadata[key] = StableJSON(value)
		default:
			metadata[key] = value
		}
	}
	return metadata
}
