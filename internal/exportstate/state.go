package exportstate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type State struct {
	Version           int                     `json:"version"`
	ScanWatermarkNS   int64                   `json:"scan_watermark_ns"`
	ProcessedTraceIDs []string                `json:"processed_trace_ids"`
	TurnProgress      map[string]TurnProgress `json:"turn_progress,omitempty"`
	Queue             []QueueRequest          `json:"queue,omitempty"`
}

type TurnProgress struct {
	ExportedObservationCount int  `json:"exported_observation_count"`
	FinalSpansExported       bool `json:"final_spans_exported"`
}

type QueueRequest struct {
	Provider   string `json:"provider"`
	SourcePath string `json:"source_path"`
	SessionID  string `json:"session_id,omitempty"`
	CWD        string `json:"cwd,omitempty"`
	EnqueuedAt string `json:"enqueued_at"`
}

func Load(path string) (*State, error) {
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
	state.normalize()
	return &state, nil
}

func Save(path string, state State) error {
	state.Version = 1
	state.normalize()
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

func Update(path string, mutate func(*State) error) (State, error) {
	unlock, err := lock(path)
	if err != nil {
		return State{}, err
	}
	defer unlock()

	state, err := Load(path)
	if err != nil {
		return State{}, err
	}
	if state == nil {
		state = &State{Version: 1}
	}
	if err := mutate(state); err != nil {
		return State{}, err
	}
	if err := Save(path, *state); err != nil {
		return State{}, err
	}
	return *state, nil
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
	delete(s.TurnProgress, traceID)
}

func (s State) ProgressFor(traceID string) TurnProgress {
	return s.TurnProgress[traceID]
}

func (s *State) SetProgress(traceID string, progress TurnProgress) {
	if s.TurnProgress == nil {
		s.TurnProgress = map[string]TurnProgress{}
	}
	s.TurnProgress[traceID] = progress
}

func Enqueue(path string, request QueueRequest) error {
	if request.Provider == "" || request.SourcePath == "" {
		return fmt.Errorf("queue request requires provider and source_path")
	}
	if request.EnqueuedAt == "" {
		request.EnqueuedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	enqueuedAt, err := time.Parse(time.RFC3339Nano, request.EnqueuedAt)
	if err != nil {
		return fmt.Errorf("queue request has invalid enqueued_at: %w", err)
	}
	_, err = Update(path, func(state *State) error {
		if state.ScanWatermarkNS == 0 {
			state.ScanWatermarkNS = enqueuedAt.UnixNano()
		}
		for _, existing := range state.Queue {
			if existing.Provider == request.Provider && existing.SourcePath == request.SourcePath {
				return nil
			}
		}
		state.Queue = append(state.Queue, request)
		return nil
	})
	return err
}

func (s *State) RemoveQueued(request QueueRequest) {
	kept := s.Queue[:0]
	for _, existing := range s.Queue {
		if existing.Provider == request.Provider && existing.SourcePath == request.SourcePath {
			continue
		}
		kept = append(kept, existing)
	}
	s.Queue = kept
}

func (s *State) normalize() {
	s.ProcessedTraceIDs = uniqueSorted(s.ProcessedTraceIDs)
	if s.TurnProgress == nil {
		s.TurnProgress = map[string]TurnProgress{}
	}
	for _, traceID := range s.ProcessedTraceIDs {
		delete(s.TurnProgress, traceID)
	}
	s.Queue = uniqueQueue(s.Queue)
}

func uniqueQueue(values []QueueRequest) []QueueRequest {
	seen := map[string]bool{}
	result := make([]QueueRequest, 0, len(values))
	for _, value := range values {
		if value.Provider == "" || value.SourcePath == "" {
			continue
		}
		key := value.Provider + "\x00" + value.SourcePath
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].EnqueuedAt == result[j].EnqueuedAt {
			return result[i].Provider+"\x00"+result[i].SourcePath < result[j].Provider+"\x00"+result[j].SourcePath
		}
		return result[i].EnqueuedAt < result[j].EnqueuedAt
	})
	return result
}

func lock(path string) (func(), error) {
	lockPath := path + ".lock"
	deadline := time.Now().Add(2 * time.Second)
	for {
		file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_ = file.Close()
			return func() { _ = os.Remove(lockPath) }, nil
		}
		if !os.IsExist(err) || time.Now().After(deadline) {
			return nil, err
		}
		time.Sleep(10 * time.Millisecond)
	}
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
