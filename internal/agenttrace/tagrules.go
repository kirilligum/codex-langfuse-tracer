package agenttrace

import "strings"

// TagRule is a compiled-in source customization hook for trace tags.
//
// Rules must return fixed, low-cardinality tag names. Do not build tag values
// from raw prompts, command output, file paths, IDs, URLs, or other sensitive
// strings.
type TagRule struct {
	Name string
	Tags func(turn Turn, rollup InsightRollup) []string
}

// tagRules is intentionally empty by default. Add local compiled rules here,
// then rebuild and reinstall the exporter.
var tagRules = []TagRule{}

func BuildTraceTags(turn Turn) []string {
	return buildTraceTags(turn, BuildInsightRollup(turn), tagRules)
}

func buildTraceTags(turn Turn, rollup InsightRollup, rules []TagRule) []string {
	values := rollup.Tags()
	for _, rule := range rules {
		if rule.Tags == nil {
			continue
		}
		values = append(values, cleanRuleTags(rule.Tags(turn, rollup))...)
	}
	return sortedUnique(values)
}

func fixedRequestContainsRule(name, tag string, needles ...string) TagRule {
	return TagRule{
		Name: name,
		Tags: func(turn Turn, _ InsightRollup) []string {
			input := strings.ToLower(turn.InputText())
			for _, needle := range needles {
				needle = strings.ToLower(strings.TrimSpace(needle))
				if needle != "" && strings.Contains(input, needle) {
					return []string{tag}
				}
			}
			return nil
		},
	}
}

func cleanRuleTags(tags []string) []string {
	values := make([]string, 0, len(tags))
	for _, tag := range tags {
		if tag := normalizeRuleTag(tag); tag != "" {
			values = append(values, tag)
		}
	}
	return values
}

func normalizeRuleTag(tag string) string {
	tag = strings.ToLower(strings.TrimSpace(tag))
	if tag == "" {
		return ""
	}
	for _, r := range tag {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == ':' || r == '_' || r == '-' || r == '.' {
			continue
		}
		return ""
	}
	return tag
}
