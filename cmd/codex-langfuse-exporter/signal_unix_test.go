//go:build unix

package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestCLISignalSubprocessHelper(t *testing.T) {
	if os.Getenv("CLT_CLI_SIGNAL_HELPER") != "1" {
		return
	}
	if os.Getenv("CLT_CLI_SIGNAL_MODE") == "hook" {
		stdin = &hookSignalReader{Reader: os.Stdin}
		os.Args = []string{"codex-langfuse-exporter", "--claude-hook", "--state-file", os.Getenv("CLT_CLI_SIGNAL_STATE")}
		main()
		return
	}
	os.Args = []string{
		"codex-langfuse-exporter",
		"--watch",
		"--config", os.Getenv("CLT_CLI_SIGNAL_CONFIG"),
		"--state-file", os.Getenv("CLT_CLI_SIGNAL_STATE"),
		"--poll-interval-seconds", "0.001",
	}
	main()
}

// A pipe handshake proves the hook has consumed input before its parent sends a signal.
type hookSignalReader struct {
	io.Reader
	announced bool
}

func (r *hookSignalReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if n > 0 && !r.announced {
		r.announced = true
		fmt.Fprintln(os.Stderr, "hook input consumed")
	}
	return n, err
}

func TestCLIHookSignalsTerminatePendingInputAndEnqueue(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGINT} {
		for _, input := range []struct {
			name, payload string
		}{
			{"partial input", `{"hook_event_name":`},
			{"pending enqueue", `{"hook_event_name":"Stop","session_id":"signal-test","transcript_path":"/tmp/signal-test.jsonl"}`},
		} {
			t.Run(sig.String()+"/"+input.name, func(t *testing.T) {
				statePath := filepath.Join(t.TempDir(), "state.json")
				original := []byte(`{"version":3,"scan_watermark_ns":7,"processed_trace_ids":["retained"]}`)
				if err := os.WriteFile(statePath, original, 0o600); err != nil {
					t.Fatal(err)
				}
				lock, err := os.OpenFile(statePath+".lock", os.O_CREATE|os.O_RDWR, 0o600)
				if err != nil {
					t.Fatal(err)
				}
				defer lock.Close()
				if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
					t.Fatal(err)
				}
				cmd := exec.Command(os.Args[0], "-test.run=^TestCLISignalSubprocessHelper$")
				cmd.Env = append(os.Environ(), "CLT_CLI_SIGNAL_HELPER=1", "CLT_CLI_SIGNAL_MODE=hook", "CLT_CLI_SIGNAL_STATE="+statePath)
				inputPipe, err := cmd.StdinPipe()
				if err != nil {
					t.Fatal(err)
				}
				defer inputPipe.Close()
				stderr, err := cmd.StderrPipe()
				if err != nil {
					t.Fatal(err)
				}
				var stdout bytes.Buffer
				cmd.Stdout = &stdout
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
				done := make(chan error, 1)
				go func() { done <- cmd.Wait() }()
				reaped := false
				defer func() {
					if !reaped {
						_ = cmd.Process.Kill()
						<-done
					}
				}()
				ready := make(chan string, 1)
				go func() {
					line, _ := bufio.NewReader(stderr).ReadString('\n')
					ready <- line
				}()
				if _, err := io.WriteString(inputPipe, input.payload); err != nil {
					t.Fatal(err)
				}
				select {
				case line := <-ready:
					if line != "hook input consumed\n" {
						t.Fatalf("hook readiness = %q", line)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("hook did not consume input")
				}
				if err := cmd.Process.Signal(sig); err != nil {
					t.Fatal(err)
				}
				select {
				case err := <-done:
					reaped = true
					var exitErr *exec.ExitError
					if !errors.As(err, &exitErr) {
						t.Fatalf("hook exit = %v, want signal termination", err)
					}
					status, ok := exitErr.Sys().(syscall.WaitStatus)
					if !ok || !status.Signaled() || status.Signal() != sig {
						t.Fatalf("hook exit = %v, want termination by %v", err, sig)
					}
				case <-time.After(2 * time.Second):
					t.Fatalf("%v did not terminate hook while %s", sig, input.name)
				}
				if stdout.Len() != 0 {
					t.Fatalf("terminated hook acknowledged enqueue: %q", stdout.String())
				}
				if got, err := os.ReadFile(statePath); err != nil || !bytes.Equal(got, original) {
					t.Fatalf("terminated hook changed state: err=%v", err)
				}
			})
		}
	}
}

func TestCLISignalCancelsStateWait(t *testing.T) {
	home := t.TempDir()
	codexHome := filepath.Join(home, "codex")
	statePath := filepath.Join(home, "state.json")
	if err := os.MkdirAll(codexHome, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := writeLangfuseConfig(t, codexHome, "http://127.0.0.1")
	lockFile, err := os.OpenFile(statePath+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.Flock(int(lockFile.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = unix.Flock(int(lockFile.Fd()), unix.LOCK_UN)
		_ = lockFile.Close()
	}()

	cmd := exec.Command(os.Args[0], "-test.run=^TestCLISignalSubprocessHelper$")
	cmd.Env = append(os.Environ(),
		"CLT_CLI_SIGNAL_HELPER=1",
		"CLT_CLI_SIGNAL_CONFIG="+configPath,
		"CLT_CLI_SIGNAL_STATE="+statePath,
		"CODEX_HOME="+codexHome,
	)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()

	lineCh := make(chan string, 8)
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			lineCh <- scanner.Text()
		}
		close(lineCh)
	}()
	select {
	case line, ok := <-lineCh:
		if !ok || !strings.Contains(line, "ERROR: export state lock busy") {
			t.Fatalf("subprocess did not report its bounded lock timeout: %q", line)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("subprocess did not reach the state lock retry loop")
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("send SIGTERM: %v", err)
	}
	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()
	select {
	case err := <-waitCh:
		if err != nil {
			t.Fatalf("watch subprocess exit = %v, want clean cancellation", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SIGTERM did not cancel the lock retry promptly")
	}
	select {
	case <-readerDone:
	case <-time.After(time.Second):
		t.Fatal("subprocess stderr did not close after clean shutdown")
	}
	for line := range lineCh {
		if strings.Contains(line, "ERROR:") {
			t.Fatalf("clean signal shutdown logged an additional error: %q", line)
		}
	}
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Fatalf("state file after canceled startup = err %v, want absent", err)
	}
}
