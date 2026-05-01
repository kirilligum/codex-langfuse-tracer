package test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kirilligum/codex-langfuse-tracer/internal/codextrace"
	"github.com/kirilligum/codex-langfuse-tracer/internal/tracecontract"
)

// TEST-021
func TestGoldenTraceContract(t *testing.T) {
	t.Parallel()

	manifest := loadManifest(t)
	for _, fixture := range manifest.Fixtures {
		fixture := fixture
		t.Run(fixture.ID, func(t *testing.T) {
			t.Parallel()
			golden := loadGolden(t, fixture.Golden)
			rolloutPath := filepath.Join("..", fixture.Rollout)
			turns, err := codextrace.ParseTurns(rolloutPath)
			if golden.ParseError {
				if err == nil {
					t.Fatalf("ParseTurns(%s) succeeded, want parse error", rolloutPath)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseTurns(%s): %v", rolloutPath, err)
			}
			exportable := codextrace.ExportableTurns(turns)
			if !golden.Exportable {
				if len(exportable) != 0 {
					t.Fatalf("fixture should not be exportable, got %d turns", len(exportable))
				}
				return
			}
			if len(exportable) != 1 {
				t.Fatalf("exportable turns = %d", len(exportable))
			}
			actual := tracecontract.FromTurn(exportable[0])
			compareTraceContract(t, golden, actual)
		})
	}
}

func loadManifest(t *testing.T) fixtureManifest {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "testdata", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest fixtureManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	return manifest
}

func loadGolden(t *testing.T, path string) tracecontract.Trace {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", path))
	if err != nil {
		t.Fatal(err)
	}
	var golden tracecontract.Trace
	if err := json.Unmarshal(raw, &golden); err != nil {
		t.Fatal(err)
	}
	return golden
}

func compareTraceContract(t *testing.T, golden, actual tracecontract.Trace) {
	t.Helper()
	if golden.SchemaVersion != actual.SchemaVersion || golden.Name != actual.Name || golden.TraceID != actual.TraceID || golden.SessionID != actual.SessionID || golden.TurnID != actual.TurnID {
		t.Fatalf("identity mismatch\ngolden=%+v\nactual=%+v", golden, actual)
	}
	if golden.Input != actual.Input || golden.Output != actual.Output || golden.Model != actual.Model || golden.CWD != actual.CWD {
		t.Fatalf("preview mismatch\ngolden=%+v\nactual=%+v", golden, actual)
	}
	if canonicalJSON(golden.TokenUsage) != canonicalJSON(actual.TokenUsage) {
		t.Fatalf("token usage mismatch\ngolden=%s\nactual=%s", canonicalJSON(golden.TokenUsage), canonicalJSON(actual.TokenUsage))
	}
	if canonicalJSON(golden.Metadata) != canonicalJSON(actual.Metadata) {
		t.Fatalf("metadata mismatch\ngolden=%s\nactual=%s", canonicalJSON(golden.Metadata), canonicalJSON(actual.Metadata))
	}

	actualByName := map[string]tracecontract.Observation{}
	for _, observation := range actual.Observations {
		actualByName[observation.Name] = observation
	}
	for _, expected := range golden.Observations {
		observed, ok := actualByName[expected.Name]
		if !ok {
			t.Fatalf("missing observation %s", expected.Name)
		}
		if expected.Type != "" && expected.Type != observed.Type {
			t.Fatalf("%s type = %q want %q", expected.Name, observed.Type, expected.Type)
		}
		if expected.Input != "" && expected.Input != observed.Input {
			t.Fatalf("%s input = %q want %q", expected.Name, observed.Input, expected.Input)
		}
		if expected.Output != "" && expected.Output != observed.Output {
			t.Fatalf("%s output = %q want %q", expected.Name, observed.Output, expected.Output)
		}
		for _, part := range expected.OutputContains {
			if !strings.Contains(observed.Output, part) {
				t.Fatalf("%s output missing %q in %q", expected.Name, part, observed.Output)
			}
		}
		for key, expectedValue := range expected.Metadata {
			if canonicalJSON(observed.Metadata[key]) != canonicalJSON(expectedValue) {
				t.Fatalf("%s metadata[%s] = %s want %s", expected.Name, key, canonicalJSON(observed.Metadata[key]), canonicalJSON(expectedValue))
			}
		}
	}
}

func canonicalJSON(value any) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}
