package watch

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

type State struct {
	Version           int      `json:"version"`
	ScanWatermarkNS   int64    `json:"scan_watermark_ns"`
	ProcessedTraceIDs []string `json:"processed_trace_ids"`
}

func LoadState(path string) (*State, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var state State
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, err
	}
	if state.Version != 1 {
		return nil, fmt.Errorf("unsupported watch state version in %s", path)
	}
	state.ProcessedTraceIDs = uniqueSorted(state.ProcessedTraceIDs)
	return &state, nil
}

func SaveState(path string, state State) error {
	state.Version = 1
	state.ProcessedTraceIDs = uniqueSorted(state.ProcessedTraceIDs)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmpPath := filepath.Join(filepath.Dir(path), filepath.Base(path)+".tmp")
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(tmpPath, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func (s State) HasProcessed(traceID string) bool {
	for _, existing := range s.ProcessedTraceIDs {
		if existing == traceID {
			return true
		}
	}
	return false
}

func (s *State) AddProcessed(traceID string) {
	s.ProcessedTraceIDs = append(s.ProcessedTraceIDs, traceID)
	s.ProcessedTraceIDs = uniqueSorted(s.ProcessedTraceIDs)
}

func uniqueSorted(values []string) []string {
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
