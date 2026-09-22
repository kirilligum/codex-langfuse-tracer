//go:build unix

package watch

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kirilligum/codex-langfuse-tracer/internal/agenttrace"
	"github.com/kirilligum/codex-langfuse-tracer/internal/buildinfo"
	"github.com/kirilligum/codex-langfuse-tracer/internal/config"
	"github.com/kirilligum/codex-langfuse-tracer/internal/exportstate"
	"github.com/kirilligum/codex-langfuse-tracer/internal/langfuse"
)

type deliveryChild struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	lines  chan string
	stderr chan string
	waited bool
}

func startDeliveryChild(t *testing.T, ctx context.Context, mode, root, statePath, host string, now time.Time) *deliveryChild {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestWatchDeliveryProcessHelper$", "-test.v")
	cmd.Env = append(os.Environ(),
		"CLT_WATCH_DELIVERY_HELPER_MODE="+mode,
		"CLT_WATCH_DELIVERY_ROOT="+root,
		"CLT_WATCH_DELIVERY_STATE="+statePath,
		"CLT_WATCH_DELIVERY_HOST="+host,
		fmt.Sprintf("CLT_WATCH_DELIVERY_NOW_NS=%d", now.UnixNano()),
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	child := &deliveryChild{cmd: cmd, stdin: stdin, lines: make(chan string, 128), stderr: make(chan string, 1)}
	t.Cleanup(func() {
		if !child.waited && child.cmd.Process != nil {
			_ = child.cmd.Process.Kill()
			_ = child.cmd.Wait()
			child.waited = true
		}
		_ = child.stdin.Close()
	})
	go func() {
		defer close(child.lines)
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 4096), 64*1024)
		for scanner.Scan() {
			child.lines <- scanner.Text()
		}
	}()
	go func() {
		data, _ := io.ReadAll(stderr)
		child.stderr <- string(data)
	}()
	return child
}

func (child *deliveryChild) waitForLine(t *testing.T, prefix string) []string {
	t.Helper()
	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()
	var lines []string
	for {
		select {
		case line, open := <-child.lines:
			if !open {
				stderr := <-child.stderr
				t.Fatalf("delivery child exited before %q; stdout=%v stderr=%s", prefix, lines, stderr)
			}
			lines = append(lines, line)
			if strings.Contains(line, prefix) {
				return lines
			}
		case <-timer.C:
			_ = child.cmd.Process.Kill()
			err := child.cmd.Wait()
			child.waited = true
			stderr := <-child.stderr
			t.Fatalf("timed out waiting for delivery child line %q: wait=%v stdout=%v stderr=%s", prefix, err, lines, stderr)
		}
	}
}

func (child *deliveryChild) wait(t *testing.T) error {
	t.Helper()
	err := child.cmd.Wait()
	child.waited = true
	return err
}

type blockingDeliveryWriter struct{}

func (blockingDeliveryWriter) Write(data []byte) (int, error) {
	written, err := os.Stdout.Write(data)
	if err != nil || !bytes.Contains(data, []byte("span_export_succeeded")) {
		return written, err
	}
	var release [1]byte
	_, err = os.Stdin.Read(release[:])
	return written, err
}

// TestWatchDeliveryProcessHelper is run as a subprocess by the kill-boundary
// test below. A normal package test run leaves it as a no-op.
func TestWatchDeliveryProcessHelper(t *testing.T) {
	mode := os.Getenv("CLT_WATCH_DELIVERY_HELPER_MODE")
	if mode == "" {
		return
	}
	if mode != "block-before-checkpoint" && mode != "resume" {
		t.Fatalf("unknown delivery helper mode %q", mode)
	}
	root := os.Getenv("CLT_WATCH_DELIVERY_ROOT")
	statePath := os.Getenv("CLT_WATCH_DELIVERY_STATE")
	host := os.Getenv("CLT_WATCH_DELIVERY_HOST")
	nowNS, err := strconv.ParseInt(os.Getenv("CLT_WATCH_DELIVERY_NOW_NS"), 10, 64)
	if err != nil {
		t.Fatalf("parse fixed test time: %v", err)
	}
	now := time.Unix(0, nowNS)
	state, err := exportstate.Load(statePath)
	if err != nil || state == nil {
		t.Fatalf("load child state: state=%+v err=%v", state, err)
	}
	var stdout io.Writer = os.Stdout
	if mode == "block-before-checkpoint" {
		stdout = blockingDeliveryWriter{}
	}
	_, exported, err := ScanOnce(context.Background(), ScanOptions{
		Root: root, StatePath: statePath, Now: now, Stdout: stdout, Stderr: os.Stderr,
		ResolveWorkspace: testWorkspace,
		ExportSpans: func(ctx context.Context, turn agenttrace.Turn, environment string) (int, error) {
			return langfuse.ExportSpans(ctx, config.LangfuseConfig{
				Host: host, PublicKey: "pk-lf-delivery-test", SecretKey: "sk-lf-delivery-test",
			}, turn, environment, "delivery-test-host", buildinfo.DefaultServiceName)
		},
		ExportScores: func(context.Context, agenttrace.Turn, string) error {
			_, writeErr := fmt.Fprintln(os.Stdout, "test_score_callback_called")
			return writeErr
		},
	}, *state)
	if err != nil {
		t.Fatalf("child scan: %v", err)
	}
	if exported != 1 {
		t.Fatalf("child exported turns = %d, want 1", exported)
	}
	if mode == "resume" {
		persisted, loadErr := exportstate.Load(statePath)
		if loadErr != nil || persisted == nil || len(persisted.ProcessedTraceIDs) == 0 {
			t.Fatalf("resumed child did not persist processed state: state=%+v err=%v", persisted, loadErr)
		}
		_, _ = fmt.Fprintln(os.Stdout, "delivery_resume_complete")
	}
}

// TEST-709
func TestWatchRestartAfterSpanSuccessBeforeCheckpoint(t *testing.T) {
	root, statePath, rolloutPath := watchFixture(t)
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	setMTime(t, rolloutPath, now.Add(-time.Second))
	initial := exportstate.State{Version: exportstate.Version, ScanWatermarkNS: now.Add(-time.Minute).UnixNano()}
	if err := exportstate.Save(context.Background(), statePath, initial); err != nil {
		t.Fatal(err)
	}
	initialBytes, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	mock := newDeliveryOTLPServer(t, false)

	firstCtx, cancelFirst := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancelFirst()
	first := startDeliveryChild(t, firstCtx, "block-before-checkpoint", root, statePath, mock.server.URL, now)
	firstLines := first.waitForLine(t, "span_export_succeeded trace=")
	requestsBeforeKill := mock.requestSnapshot()
	if len(requestsBeforeKill) != 1 {
		t.Fatalf("successful callback had %d mock requests before checkpoint barrier, want one", len(requestsBeforeKill))
	}
	if err := first.cmd.Process.Kill(); err != nil {
		t.Fatalf("kill child at checkpoint barrier: %v", err)
	}
	if err := first.wait(t); err == nil {
		t.Fatal("child kill unexpectedly exited successfully")
	}
	mock.checkErrors(t)
	firstOutput := strings.Join(firstLines, "\n")
	if strings.Count(firstOutput, "span_export_succeeded") != 1 || strings.Contains(firstOutput, "exported trace=") || strings.Contains(firstOutput, "scored trace=") || strings.Contains(firstOutput, "test_score_callback_called") {
		t.Fatalf("child did not stop between export and checkpoint: %s", firstOutput)
	}
	afterKill, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(afterKill, initialBytes) {
		t.Fatalf("killed child changed durable state before checkpoint: before=%s after=%s", initialBytes, afterKill)
	}

	resumeCtx, cancelResume := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancelResume()
	resume := startDeliveryChild(t, resumeCtx, "resume", root, statePath, mock.server.URL, now)
	resumeLines := resume.waitForLine(t, "delivery_resume_complete")
	if err := resume.wait(t); err != nil {
		stderr := <-resume.stderr
		t.Fatalf("resume child failed: %v stderr=%s stdout=%v", err, stderr, resumeLines)
	}
	mock.checkErrors(t)
	resumeOutput := strings.Join(resumeLines, "\n")
	if strings.Count(resumeOutput, "span_export_succeeded") != 1 || !strings.Contains(resumeOutput, "exported trace=") || !strings.Contains(resumeOutput, "scored trace=") || strings.Count(resumeOutput, "test_score_callback_called") != 1 {
		t.Fatalf("resume child logs do not show one complete retry: %s", resumeOutput)
	}
	traceID := completeTraceID(t, rolloutPath)
	persisted, err := exportstate.Load(statePath)
	if err != nil || persisted == nil || !persisted.HasProcessed(traceID) || persisted.PendingScoreEnvironment(traceID) != "" {
		t.Fatalf("resumed state = %+v err=%v", persisted, err)
	}
	requests := mock.requestSnapshot()
	if len(requests) != 2 || !reflect.DeepEqual(requests[0], requests[1]) {
		t.Fatalf("restart submissions = %v; want two copies with identical span identities", requests)
	}

	var spanCalls, scoreCalls int
	_, exported, err := ScanOnce(context.Background(), ScanOptions{
		Root: root, StatePath: statePath, Now: now.Add(time.Second), ResolveWorkspace: testWorkspace,
		ExportSpans: func(context.Context, agenttrace.Turn, string) (int, error) {
			spanCalls++
			return http.StatusOK, nil
		},
		ExportScores: func(context.Context, agenttrace.Turn, string) error {
			scoreCalls++
			return nil
		},
	}, *persisted)
	if err != nil || exported != 0 || spanCalls != 0 || scoreCalls != 0 || len(mock.requestSnapshot()) != 2 {
		t.Fatalf("processed follow-up scan repeated work: exported=%d spans=%d scores=%d requests=%d err=%v", exported, spanCalls, scoreCalls, len(mock.requestSnapshot()), err)
	}
}
