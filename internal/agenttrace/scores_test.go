package agenttrace

import "testing"

func TestDeterministicScores(t *testing.T) {
	t.Parallel()

	turn := Turn{
		Observations: []Observation{
			{Name: ToolObservationName(ProviderCodex, ToolFamilyCommand), Type: "tool", Input: "go test ./...", Metadata: map[string]any{"command_kind": "test", "failure_type": "none", "status": "completed"}},
			{Name: ToolObservationName(ProviderCodex, ToolFamilyFileChange), Type: "tool", Metadata: map[string]any{"changed_files": []string{"internal/example_test.go"}}},
		},
	}
	scores := BuildDeterministicScores(turn)
	values := map[string]DeterministicScore{}
	for _, score := range scores {
		values[score.Name] = score
	}
	for _, name := range []string{"verification_run", "verification_passed", "had_failed_command", "had_file_changes", "changed_tests", "docs_only", "changed_file_count", "outcome"} {
		if _, ok := values[name]; !ok {
			t.Fatalf("missing score %q in %#v", name, scores)
		}
	}
	if values["verification_run"].Value != 1 || values["verification_passed"].Value != 1 {
		t.Fatalf("verification scores = %#v %#v", values["verification_run"], values["verification_passed"])
	}
	if values["had_failed_command"].Value != 0 || values["had_file_changes"].Value != 1 || values["changed_tests"].Value != 1 {
		t.Fatalf("boolean scores = %#v", values)
	}
	if values["changed_file_count"].DataType != ScoreDataTypeNumeric || values["changed_file_count"].Value != 1 {
		t.Fatalf("changed_file_count = %#v", values["changed_file_count"])
	}
	if values["outcome"].DataType != ScoreDataTypeCategorical || values["outcome"].Value != "verification_passed" {
		t.Fatalf("outcome = %#v", values["outcome"])
	}
}
