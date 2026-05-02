package codextrace

import (
	"reflect"
	"testing"
)

// TEST-401
func TestTokenUsageLangfuseUsageDetails(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		usage TokenUsage
		want  map[string]int
	}{
		{
			name: "empty",
		},
		{
			name: "normal usage",
			usage: TokenUsage{
				InputTokens:  100,
				OutputTokens: 40,
				TotalTokens:  140,
			},
			want: map[string]int{
				"input":  100,
				"output": 40,
				"total":  140,
			},
		},
		{
			name: "cached input and reasoning output are separate priced buckets",
			usage: TokenUsage{
				InputTokens:           100,
				OutputTokens:          40,
				TotalTokens:           140,
				CachedInputTokens:     20,
				ReasoningOutputTokens: 10,
			},
			want: map[string]int{
				"input":                   80,
				"input_cached_tokens":     20,
				"output":                  30,
				"output_reasoning_tokens": 10,
				"total":                   140,
			},
		},
		{
			name: "detail buckets clamp parent buckets at zero",
			usage: TokenUsage{
				InputTokens:           10,
				OutputTokens:          5,
				TotalTokens:           15,
				CachedInputTokens:     20,
				ReasoningOutputTokens: 7,
			},
			want: map[string]int{
				"input_cached_tokens":     20,
				"output_reasoning_tokens": 7,
				"total":                   15,
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := test.usage.LangfuseUsageDetails()
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("LangfuseUsageDetails() = %#v, want %#v", got, test.want)
			}
		})
	}
}
