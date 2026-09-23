package codextrace

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type discoveryError struct {
	count int
	first error
}

func (e *discoveryError) Error() string {
	if e == nil || e.count == 0 {
		return ""
	}
	return fmt.Sprintf("Codex session discovery encountered %d filesystem error(s): %v", e.count, e.first)
}

func (e *discoveryError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.first
}

func (e *discoveryError) ErrorCount() int {
	if e == nil {
		return 0
	}
	return e.count
}

type walkDirFunc func(string, fs.WalkDirFunc) error

func SessionPaths(root string) ([]string, error) {
	return sessionPathsWith(filepath.Join(root, "sessions"), filepath.WalkDir)
}

func sessionPathsWith(sessionsDir string, walk walkDirFunc) ([]string, error) {
	var matches []string
	var walkFailure discoveryError
	addWalkError := func(err error) {
		if err == nil {
			return
		}
		walkFailure.count++
		if walkFailure.first == nil {
			walkFailure.first = err
		}
	}
	walkErr := walk(sessionsDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			addWalkError(err)
			return nil
		}
		if entry == nil || entry.IsDir() {
			return nil
		}
		name := entry.Name()
		if strings.HasPrefix(name, "rollout-") && strings.HasSuffix(name, ".jsonl") {
			matches = append(matches, path)
		}
		return nil
	})
	addWalkError(walkErr)
	sort.Strings(matches)
	if walkFailure.count != 0 {
		return matches, &walkFailure
	}
	return matches, nil
}

func FindSessionByID(sessionID, root string) (string, error) {
	paths, err := SessionPaths(root)
	if err != nil {
		return "", fmt.Errorf("discover Codex sessions: %w", err)
	}
	var matches []string
	for _, path := range paths {
		if strings.Contains(filepath.Base(path), sessionID) {
			matches = append(matches, path)
		}
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("no Codex rollout JSONL found for session id %s", sessionID)
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("multiple Codex rollout files matched; pass --path explicitly")
	}
	return matches[0], nil
}

func LatestSession(root string) (string, error) {
	paths, err := SessionPaths(root)
	if err != nil {
		return "", fmt.Errorf("discover Codex sessions: %w", err)
	}
	if len(paths) == 0 {
		return "", fmt.Errorf("no Codex rollout JSONL files found under %s", filepath.Join(root, "sessions"))
	}

	latest := paths[0]
	latestInfo, err := os.Stat(latest)
	if err != nil {
		return "", err
	}
	for _, path := range paths[1:] {
		info, err := os.Stat(path)
		if err != nil {
			return "", err
		}
		if info.ModTime().After(latestInfo.ModTime()) || info.ModTime().Equal(latestInfo.ModTime()) && path > latest {
			latest = path
			latestInfo = info
		}
	}
	return latest, nil
}
