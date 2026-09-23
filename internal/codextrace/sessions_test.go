package codextrace

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSessionPathsIncludesMixedDepths(t *testing.T) {
	root := t.TempDir()
	sessionsDir := filepath.Join(root, "sessions")
	shallow := filepath.Join(sessionsDir, "shallow", "rollout-shallow.jsonl")
	deep := filepath.Join(sessionsDir, "2026", "09", "22", "rollout-deep.jsonl")
	for _, path := range []string{shallow, deep} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	got, err := SessionPaths(root)
	if err != nil {
		t.Fatalf("SessionPaths: %v", err)
	}
	want := []string{deep, shallow}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SessionPaths = %#v, want %#v", got, want)
	}
}

func TestSessionPathsReturnsPartialResultsAndError(t *testing.T) {
	sessionsDir := filepath.Join(t.TempDir(), "sessions")
	blockedDir := filepath.Join(sessionsDir, "blocked")
	blockedPath := filepath.Join(blockedDir, "rollout-hidden.jsonl")
	visiblePath := filepath.Join(sessionsDir, "visible", "rollout-visible.jsonl")
	for _, path := range []string{blockedPath, visiblePath} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	walkFailure := errors.New("injected directory read failure")
	walk := func(root string, visit fs.WalkDirFunc) error {
		return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if path == blockedDir && err == nil {
				if visitErr := visit(path, nil, walkFailure); visitErr != nil {
					return visitErr
				}
				return fs.SkipDir
			}
			return visit(path, entry, err)
		})
	}

	got, err := sessionPathsWith(sessionsDir, walk)
	if !errors.Is(err, walkFailure) {
		t.Fatalf("discovery error = %v, want injected failure", err)
	}
	if !reflect.DeepEqual(got, []string{visiblePath}) {
		t.Fatalf("partial paths = %#v, want only readable sibling %#v", got, []string{visiblePath})
	}
}

func TestSessionPathsMissingRootIsAnError(t *testing.T) {
	got, err := SessionPaths(t.TempDir())
	if err == nil {
		t.Fatalf("SessionPaths returned a successful inventory for a missing root: %#v", got)
	}
}

func TestFindSessionByIDPropagatesMissingRoot(t *testing.T) {
	_, err := FindSessionByID("known-session", t.TempDir())
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("FindSessionByID error = %v, want a wrapped discovery error", err)
	}
}

func TestLatestSessionPropagatesMissingRoot(t *testing.T) {
	_, err := LatestSession(t.TempDir())
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("LatestSession error = %v, want a wrapped discovery error", err)
	}
}
