package codextrace

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func SessionPaths(root string) []string {
	matches, _ := filepath.Glob(filepath.Join(root, "sessions", "**", "rollout-*.jsonl"))
	if len(matches) == 0 {
		matches = recursiveSessionPaths(filepath.Join(root, "sessions"))
	}
	sort.Strings(matches)
	return matches
}

func recursiveSessionPaths(sessionsDir string) []string {
	var matches []string
	_ = filepath.WalkDir(sessionsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d == nil || d.IsDir() {
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, "rollout-") && strings.HasSuffix(name, ".jsonl") {
			matches = append(matches, path)
		}
		return nil
	})
	sort.Strings(matches)
	return matches
}

func FindSessionByID(sessionID, root string) (string, error) {
	var matches []string
	for _, path := range SessionPaths(root) {
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
	paths := SessionPaths(root)
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
