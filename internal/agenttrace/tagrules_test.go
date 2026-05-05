package agenttrace

import (
	"reflect"
	"slices"
	"testing"
)

func TestBuildTraceTagsAppliesCompiledRules(t *testing.T) {
	t.Parallel()

	turn := Turn{
		UserMessages: []string{"Please review MCP usage."},
		Observations: []Observation{
			{Name: ToolObservationName(ProviderCodex, ToolFamilyCommand), Type: "tool", Input: "rg -n MCP README.md", Metadata: map[string]any{"command_kind": CommandKindSearch}},
		},
	}
	rollup := BuildInsightRollup(turn)
	tags := buildTraceTags(turn, rollup, []TagRule{
		fixedRequestContainsRule("request_mentions_mcp", "request:mentions_mcp", "mcp", "model context protocol"),
		{
			Name: "uses_rollup",
			Tags: func(_ Turn, rollup InsightRollup) []string {
				if rollup.ToolCount > 0 {
					return []string{"turn:has_tools"}
				}
				return nil
			},
		},
		{
			Name: "normalizes_and_rejects_invalid_tags",
			Tags: func(Turn, InsightRollup) []string {
				return []string{"REQUEST:MENTIONS_MCP", "bad/path", "bad tag"}
			},
		},
	})

	for _, want := range []string{"request:mentions_mcp", "turn:has_tools", "tool:command", "command:search"} {
		if !slices.Contains(tags, want) {
			t.Fatalf("tags missing %q in %#v", want, tags)
		}
	}
	for _, forbidden := range []string{"REQUEST:MENTIONS_MCP", "bad/path", "bad tag"} {
		if slices.Contains(tags, forbidden) {
			t.Fatalf("tags contain invalid rule tag %q in %#v", forbidden, tags)
		}
	}
}

func TestBuildTraceTagsDefaultMatchesRollupTags(t *testing.T) {
	t.Parallel()

	turn := Turn{
		Observations: []Observation{
			{Name: ToolObservationName(ProviderCodex, ToolFamilyMCP), Type: "tool", Metadata: map[string]any{"mcp_server": "github", "mcp_tool": "issues/list"}},
		},
	}
	if got, want := BuildTraceTags(turn), BuildInsightRollup(turn).Tags(); !reflect.DeepEqual(got, want) {
		t.Fatalf("default trace tags = %#v, want %#v", got, want)
	}
}
