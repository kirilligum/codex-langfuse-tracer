package exportstate

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kirilligum/codex-langfuse-tracer/internal/agenttrace"
)

// TEST-LOCK-001
func TestStateLockProcessHelper(t *testing.T) {
	if os.Getenv("CLT_STATE_LOCK_HELPER") != "1" {
		return
	}
	path := os.Getenv("CLT_STATE_LOCK_PATH")
	switch os.Getenv("CLT_STATE_LOCK_MODE") {
	case "hold":
		file, err := acquireLock(context.Background(), path)
		if err != nil {
			fmt.Fprintln(os.Stdout, "error", err)
			os.Exit(2)
		}
		fmt.Fprintln(os.Stdout, "ready")
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		closeErr := file.Close()
		if err != nil || strings.TrimSpace(line) != "release" || closeErr != nil {
			fmt.Fprintln(os.Stdout, "error", errors.Join(err, closeErr))
			os.Exit(2)
		}
		fmt.Fprintln(os.Stdout, "released")
		os.Exit(0)
	case "interrupt-write", "commit-hold":
		file, err := acquireLock(context.Background(), path)
		if err != nil {
			fmt.Fprintln(os.Stdout, "error", err)
			os.Exit(2)
		}
		state, err := Load(path)
		if err != nil || state == nil {
			fmt.Fprintln(os.Stdout, "error", err)
			_ = file.Close()
			os.Exit(2)
		}
		state.AddProcessed("commit-under-test")
		writeTemp := os.WriteFile
		if os.Getenv("CLT_STATE_LOCK_MODE") == "interrupt-write" {
			writeTemp = func(tempPath string, raw []byte, perm os.FileMode) error {
				if err := os.WriteFile(tempPath, raw[:len(raw)/2], perm); err != nil {
					return err
				}
				fmt.Fprintln(os.Stdout, "partial")
				_, err := bufio.NewReader(os.Stdin).ReadString('\n')
				return err
			}
		}
		if err := writeStateFile(path, *state, writeTemp, os.Rename); err != nil {
			fmt.Fprintln(os.Stdout, "error", err)
			_ = file.Close()
			os.Exit(2)
		}
		fmt.Fprintln(os.Stdout, "committed")
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		closeErr := file.Close()
		if err != nil || strings.TrimSpace(line) != "release" || closeErr != nil {
			fmt.Fprintln(os.Stdout, "error", errors.Join(err, closeErr))
			os.Exit(2)
		}
		fmt.Fprintln(os.Stdout, "released")
		os.Exit(0)
	case "update":
		worker, err := strconv.Atoi(os.Getenv("CLT_STATE_LOCK_WORKER"))
		if err != nil {
			fmt.Fprintln(os.Stdout, "error", err)
			os.Exit(2)
		}
		count, err := strconv.Atoi(os.Getenv("CLT_STATE_LOCK_COUNT"))
		if err != nil {
			fmt.Fprintln(os.Stdout, "error", err)
			os.Exit(2)
		}
		fmt.Fprintln(os.Stdout, "ready")
		for index := 0; index < count; index++ {
			_, err := Update(context.Background(), path, func(state *State) error {
				state.AddProcessed(fmt.Sprintf("worker-%02d-%03d", worker, index))
				state.ScanWatermarkNS++
				state.SetPendingScore(fmt.Sprintf("pending-%02d", worker), fmt.Sprintf("env-%02d", worker))
				time.Sleep(time.Millisecond)
				return nil
			})
			if err != nil {
				fmt.Fprintln(os.Stdout, "error", err)
				os.Exit(2)
			}
			err = Enqueue(context.Background(), path, QueueRequest{
				Provider:   agenttrace.ProviderClaude,
				SourcePath: fmt.Sprintf("/tmp/worker-%02d-%03d.jsonl", worker, index),
				EnqueuedAt: time.Date(2026, 9, 21, 12, 0, index, 0, time.UTC).Format(time.RFC3339Nano),
			})
			if err != nil {
				fmt.Fprintln(os.Stdout, "error", err)
				os.Exit(2)
			}
		}
		fmt.Fprintln(os.Stdout, "done")
		os.Exit(0)
	default:
		fmt.Fprintln(os.Stdout, "error unknown helper mode")
		os.Exit(2)
	}
}

// TEST-LOCK-002
func TestStateLockRecoversAfterKilledOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	initial := State{
		Version:           Version,
		ScanWatermarkNS:   17,
		ProcessedTraceIDs: []string{"already-processed"},
		PendingScores:     map[string]string{"pending": "repository--main-a1b2c3"},
		Queue:             []QueueRequest{{Provider: agenttrace.ProviderClaude, SourcePath: "/tmp/queued.jsonl", EnqueuedAt: "2026-09-21T12:00:00Z"}},
	}
	if err := Save(context.Background(), path, initial); err != nil {
		t.Fatal(err)
	}
	lockPath := path + ".lock"
	if err := os.WriteFile(lockPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	owner := startStateLockHelper(t, path, "hold", -1, 0)
	owner.waitFor(t, "ready")
	if err := owner.cmd.Process.Kill(); err != nil {
		t.Fatalf("kill lock owner: %v", err)
	}
	if err := owner.cmd.Wait(); err == nil {
		t.Fatal("killed lock owner exited successfully")
	}

	updated, err := Update(context.Background(), path, func(state *State) error {
		state.AddProcessed("after-recovery")
		return nil
	})
	if err != nil {
		t.Fatalf("update after killed owner: %v", err)
	}
	if updated.ScanWatermarkNS != initial.ScanWatermarkNS || len(updated.Queue) != 1 || !updated.HasProcessed("already-processed") || !updated.HasProcessed("after-recovery") {
		t.Fatalf("recovery lost state: %+v", updated)
	}
	if updated.PendingScoreEnvironment("pending") != initial.PendingScoreEnvironment("pending") {
		t.Fatalf("recovery lost pending score: %+v", updated.PendingScores)
	}
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("persistent lock file missing after recovery: %v", err)
	}
	if info, err := os.Stat(path); err != nil {
		t.Fatal(err)
	} else if info.Mode().Perm() != 0o600 {
		t.Fatalf("state mode = %o, want 600", info.Mode().Perm())
	}
}

// TEST-LOCK-008
func TestStateInterruptedWritePreservesCommittedJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	initial := State{Version: Version, ScanWatermarkNS: 41, ProcessedTraceIDs: []string{"before-kill"}}
	if err := Save(context.Background(), path, initial); err != nil {
		t.Fatal(err)
	}
	owner := startStateLockHelper(t, path, "interrupt-write", -1, 0)
	owner.waitFor(t, "partial")
	if err := owner.cmd.Process.Kill(); err != nil {
		t.Fatalf("kill interrupted writer: %v", err)
	}
	if err := owner.cmd.Wait(); err == nil {
		t.Fatal("interrupted writer exited successfully")
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("load committed state after interrupted write: %v", err)
	}
	if got.ScanWatermarkNS != initial.ScanWatermarkNS || len(got.ProcessedTraceIDs) != 1 || !got.HasProcessed("before-kill") || got.HasProcessed("commit-under-test") {
		t.Fatalf("interrupted temporary write replaced committed state: %+v", got)
	}
	if _, err := os.Stat(path + ".tmp"); err != nil {
		t.Fatalf("expected abandoned temporary file for overwrite test: %v", err)
	}
	updated, err := Update(context.Background(), path, func(state *State) error {
		state.AddProcessed("after-kill")
		return nil
	})
	if err != nil {
		t.Fatalf("update after interrupted write: %v", err)
	}
	if !updated.HasProcessed("before-kill") || !updated.HasProcessed("after-kill") || updated.HasProcessed("commit-under-test") {
		t.Fatalf("successful update did not replace abandoned temporary data: %+v", updated)
	}
}

// TEST-LOCK-009
func TestStateCommitSurvivesKillBeforeUnlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := Save(context.Background(), path, State{Version: Version, ScanWatermarkNS: 73}); err != nil {
		t.Fatal(err)
	}
	owner := startStateLockHelper(t, path, "commit-hold", -1, 0)
	owner.waitFor(t, "committed")
	committed, err := Load(path)
	if err != nil || !committed.HasProcessed("commit-under-test") {
		t.Fatalf("atomic rename did not make complete state visible: state=%+v err=%v", committed, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := acquireLock(ctx, path); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lock was not retained before owner exit: %v", err)
	}
	if err := owner.cmd.Process.Kill(); err != nil {
		t.Fatalf("kill owner after commit: %v", err)
	}
	if err := owner.cmd.Wait(); err == nil {
		t.Fatal("killed owner exited successfully")
	}
	updated, err := Update(context.Background(), path, func(state *State) error {
		state.AddProcessed("after-owner-kill")
		return nil
	})
	if err != nil {
		t.Fatalf("update after committed owner was killed: %v", err)
	}
	if !updated.HasProcessed("commit-under-test") || !updated.HasProcessed("after-owner-kill") {
		t.Fatalf("committed state was lost after lock-owner death: %+v", updated)
	}
}

// TEST-LOCK-003
func TestStateLockDoesNotStealLiveOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := Save(context.Background(), path, State{Version: Version, ScanWatermarkNS: 23}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	owner := startStateLockHelper(t, path, "hold", -1, 0)
	owner.waitFor(t, "ready")
	called := false
	started := time.Now()
	_, err = Update(context.Background(), path, func(state *State) error {
		called = true
		state.ScanWatermarkNS++
		return nil
	})
	if !errors.Is(err, ErrLockBusy) {
		t.Fatalf("contended update error = %v, want ErrLockBusy", err)
	}
	if elapsed := time.Since(started); elapsed < stateLockTimeout-stateLockPoll || elapsed > stateLockTimeout+time.Second {
		t.Fatalf("contention wait = %s, want about %s", elapsed, stateLockTimeout)
	}
	if called {
		t.Fatal("mutation ran without acquiring the live lock")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("state changed during failed acquisition: before=%s after=%s", before, after)
	}
	if _, err := acquireLock(context.Background(), path); !errors.Is(err, ErrLockBusy) {
		t.Fatalf("live owner was not retained after contender timeout: %v", err)
	}
	owner.release(t)
	if _, err := Update(context.Background(), path, func(state *State) error {
		state.ScanWatermarkNS++
		return nil
	}); err != nil {
		t.Fatalf("update after release: %v", err)
	}
}

// TEST-LOCK-004
func TestStateUpdatesSerializeAcrossProcesses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := Save(context.Background(), path, State{Version: Version}); err != nil {
		t.Fatal(err)
	}
	const workers, iterations = 4, 12
	children := make([]*stateLockHelper, 0, workers)
	for worker := 0; worker < workers; worker++ {
		child := startStateLockHelper(t, path, "update", worker, iterations)
		child.waitFor(t, "ready")
		children = append(children, child)
	}
	for _, child := range children {
		child.waitFor(t, "done")
		child.waitExit(t)
	}
	state, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	want := workers * iterations
	if len(state.ProcessedTraceIDs) != want || len(state.Queue) != want || state.ScanWatermarkNS != int64(want) {
		t.Fatalf("serialized updates lost data: processed=%d queue=%d watermark=%d, want %d each", len(state.ProcessedTraceIDs), len(state.Queue), state.ScanWatermarkNS, want)
	}
	if len(state.PendingScores) != workers {
		t.Fatalf("pending scores = %d, want %d: %+v", len(state.PendingScores), workers, state.PendingScores)
	}
}

// TEST-LOCK-005
func TestStateLockKeepsSidecarInode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	first, err := acquireLock(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	infoBefore, err := os.Stat(path + ".lock")
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := acquireLock(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	infoAfter, err := os.Stat(path + ".lock")
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(infoBefore, infoAfter) {
		t.Fatal("lock sidecar changed inode across transactions")
	}
}

// TEST-LOCK-006
func TestStateLoadOrCreatePreservesEnqueueInEitherOrder(t *testing.T) {
	t.Run("hook commits first", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "nested", "state.json")
		enqueuedAt := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
		if err := Enqueue(context.Background(), path, QueueRequest{
			Provider: agenttrace.ProviderClaude, SourcePath: "/tmp/kept.jsonl", EnqueuedAt: enqueuedAt.Format(time.RFC3339Nano),
		}); err != nil {
			t.Fatal(err)
		}
		got, created, err := LoadOrCreate(context.Background(), path, State{Version: Version, ScanWatermarkNS: 999})
		if err != nil {
			t.Fatal(err)
		}
		if created || got.ScanWatermarkNS != enqueuedAt.UnixNano() || len(got.Queue) != 1 || got.Queue[0].SourcePath != "/tmp/kept.jsonl" {
			t.Fatalf("LoadOrCreate did not preserve hook state: created=%v state=%+v", created, got)
		}
	})

	t.Run("watcher creates first", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "nested", "state.json")
		initial := State{Version: Version, ScanWatermarkNS: 31, ProcessedTraceIDs: []string{"trace-existing"}}
		if _, created, err := LoadOrCreate(context.Background(), path, initial); err != nil || !created {
			t.Fatalf("initial LoadOrCreate created=%v err=%v", created, err)
		}
		if err := os.Chmod(path+".lock", 0o644); err != nil {
			t.Fatal(err)
		}
		tempPath := path + ".tmp"
		if err := os.WriteFile(tempPath, []byte("stale partial state"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(tempPath, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := Enqueue(context.Background(), path, QueueRequest{
			Provider: agenttrace.ProviderClaude, SourcePath: "/tmp/kept.jsonl", EnqueuedAt: "2026-09-21T12:00:00Z",
		}); err != nil {
			t.Fatal(err)
		}
		got, created, err := LoadOrCreate(context.Background(), path, State{Version: Version, ScanWatermarkNS: 999})
		if err != nil {
			t.Fatal(err)
		}
		if created || got.ScanWatermarkNS != initial.ScanWatermarkNS || !got.HasProcessed("trace-existing") || len(got.Queue) != 1 {
			t.Fatalf("later initialization changed watcher state: created=%v state=%+v", created, got)
		}
		for _, statePath := range []string{path, path + ".lock"} {
			info, err := os.Stat(statePath)
			if err != nil {
				t.Fatal(err)
			}
			if gotMode := info.Mode().Perm(); gotMode != 0o600 {
				t.Errorf("%s mode = %04o, want 0600", statePath, gotMode)
			}
		}
	})
}

func TestStateInvalidJSONIsNeverReset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	invalid := []byte("{not-valid-json")
	if err := os.WriteFile(path, invalid, 0o600); err != nil {
		t.Fatal(err)
	}
	initial := State{Version: Version, ScanWatermarkNS: 999}
	if _, _, err := LoadOrCreate(context.Background(), path, initial); err == nil {
		t.Fatal("LoadOrCreate accepted corrupt state")
	}
	if _, err := Update(context.Background(), path, func(state *State) error {
		state.ScanWatermarkNS = 42
		return nil
	}); err == nil {
		t.Fatal("Update accepted corrupt state")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, invalid) {
		t.Fatalf("invalid state was changed: %q", got)
	}
	unsupported := []byte(`{"version":99}`)
	if err := os.WriteFile(path, unsupported, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Update(context.Background(), path, func(*State) error { return nil }); err == nil || !strings.Contains(err.Error(), "unsupported watch state version") {
		t.Fatalf("unsupported state error = %v", err)
	}
	got, err = os.ReadFile(path)
	if err != nil || !bytes.Equal(got, unsupported) {
		t.Fatalf("unsupported state was changed: bytes=%q err=%v", got, err)
	}
}

func TestStateWriteErrorsPreserveCommittedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	committed := State{Version: Version, ScanWatermarkNS: 7, ProcessedTraceIDs: []string{"committed"}}
	if err := Save(context.Background(), path, committed); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	file, err := acquireLock(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	next := State{Version: Version, ScanWatermarkNS: 99, ProcessedTraceIDs: []string{"uncommitted"}}
	writeFailure := errors.New("injected temporary write failure")
	err = writeStateFile(path, next, func(string, []byte, os.FileMode) error {
		return writeFailure
	}, os.Rename)
	if !errors.Is(err, writeFailure) || !strings.Contains(err.Error(), path+".tmp") {
		t.Fatalf("temporary write failure context = %v", err)
	}
	got, readErr := os.ReadFile(path)
	if readErr != nil || !bytes.Equal(got, original) {
		t.Fatalf("temporary write failure changed committed state: bytes_equal=%v err=%v", bytes.Equal(got, original), readErr)
	}
	renameFailure := errors.New("injected state rename failure")
	err = writeStateFile(path, next, writeStateTemp, func(string, string) error {
		return renameFailure
	})
	if !errors.Is(err, renameFailure) || !strings.Contains(err.Error(), "replace export state "+path) {
		t.Fatalf("rename failure context = %v", err)
	}
	got, readErr = os.ReadFile(path)
	if readErr != nil || !bytes.Equal(got, original) {
		t.Fatalf("rename failure changed committed state: bytes_equal=%v err=%v", bytes.Equal(got, original), readErr)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	updated, err := Update(context.Background(), path, func(state *State) error {
		state.ScanWatermarkNS++
		return nil
	})
	if err != nil || updated.ScanWatermarkNS != 8 {
		t.Fatalf("update after failed writes = state %+v err %v", updated, err)
	}
}

// TEST-LOCK-007
func TestStateLockCancellationAndCallbackFailureRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := Save(context.Background(), path, State{Version: Version}); err != nil {
		t.Fatal(err)
	}
	if _, err := Update(context.Background(), path, func(*State) error { return errors.New("callback failed") }); err == nil || !strings.Contains(err.Error(), "callback failed") {
		t.Fatalf("callback error = %v", err)
	}
	if _, err := Update(context.Background(), path, func(state *State) error {
		state.ScanWatermarkNS++
		return nil
	}); err != nil {
		t.Fatalf("lock was retained after callback failure: %v", err)
	}
	owner := startStateLockHelper(t, path, "hold", -1, 0)
	owner.waitFor(t, "ready")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := Update(ctx, path, func(*State) error { t.Error("mutation ran after cancellation"); return nil }); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("canceled acquisition error = %v, want deadline exceeded", err)
	}
	owner.release(t)
	if _, err := Update(context.Background(), path, func(state *State) error {
		state.ScanWatermarkNS++
		return nil
	}); err != nil {
		t.Fatalf("lock was retained after cancellation: %v", err)
	}
}

type stateLockHelper struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Scanner
	stderr *bytes.Buffer
}

func startStateLockHelper(t *testing.T, path, mode string, worker, count int) *stateLockHelper {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestStateLockProcessHelper$")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	stderr := &bytes.Buffer{}
	cmd.Stderr = stderr
	cmd.Env = append(os.Environ(),
		"CLT_STATE_LOCK_HELPER=1",
		"CLT_STATE_LOCK_PATH="+path,
		"CLT_STATE_LOCK_MODE="+mode,
		"CLT_STATE_LOCK_WORKER="+strconv.Itoa(worker),
		"CLT_STATE_LOCK_COUNT="+strconv.Itoa(count),
	)
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	helper := &stateLockHelper{cmd: cmd, stdin: stdin, stdout: bufio.NewScanner(stdout), stderr: stderr}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
		_ = stdin.Close()
		cancel()
	})
	return helper
}

func (helper *stateLockHelper) waitFor(t *testing.T, want string) {
	t.Helper()
	if !helper.stdout.Scan() {
		t.Fatalf("helper ended before %q: scan_error=%v stderr=%s", want, helper.stdout.Err(), helper.stderr.String())
	}
	if got := helper.stdout.Text(); got != want {
		t.Fatalf("helper line = %q, want %q; stderr=%s", got, want, helper.stderr.String())
	}
}

func (helper *stateLockHelper) release(t *testing.T) {
	t.Helper()
	if _, err := fmt.Fprintln(helper.stdin, "release"); err != nil {
		t.Fatalf("release helper: %v", err)
	}
	helper.waitFor(t, "released")
	helper.waitExit(t)
}

func (helper *stateLockHelper) waitExit(t *testing.T) {
	t.Helper()
	if err := helper.cmd.Wait(); err != nil {
		t.Fatalf("helper process: %v; stderr=%s", err, helper.stderr.String())
	}
}
