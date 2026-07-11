package agenttrace

type DeterministicScore struct {
	Name     string
	DataType string
	Value    any
	Comment  string
}

const (
	ScoreDataTypeBoolean     = "BOOLEAN"
	ScoreDataTypeCategorical = "CATEGORICAL"
	ScoreDataTypeNumeric     = "NUMERIC"
)

func BuildDeterministicScores(turn Turn) []DeterministicScore {
	rollup := BuildInsightRollup(turn)
	return []DeterministicScore{
		booleanScore("verification_run", rollup.VerificationCommandCount > 0, "True when at least one deterministic verification command was observed."),
		booleanScore("verification_passed", rollup.VerificationStatus == "passed", "True when observed verification commands completed without a failed verification command."),
		booleanScore("had_failed_command", rollup.FailedCommandCount > 0, "True when any observed command failed or timed out."),
		booleanScore("had_file_changes", rollup.ChangedFileCount > 0, "True when the turn changed at least one file."),
		booleanScore("changed_tests", len(rollup.TouchedTestFiles) > 0, "True when changed files include test-like paths."),
		booleanScore("docs_only", containsString(rollup.Outcomes, "docs_only"), "True when all changed files are documentation-like files."),
		{
			Name:     "changed_file_count",
			DataType: ScoreDataTypeNumeric,
			Value:    rollup.ChangedFileCount,
			Comment:  "Number of unique changed files observed in file-change tool metadata.",
		},
		{
			Name:     "outcome",
			DataType: ScoreDataTypeCategorical,
			Value:    rollup.Outcome,
			Comment:  "Primary deterministic outcome derived from commands, file changes, and verification metadata.",
		},
	}
}

func booleanScore(name string, value bool, comment string) DeterministicScore {
	numeric := 0
	if value {
		numeric = 1
	}
	return DeterministicScore{
		Name:     name,
		DataType: ScoreDataTypeBoolean,
		Value:    numeric,
		Comment:  comment,
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
