//go:build unix

package main

import (
	"bufio"
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
	os.Args = []string{
		"codex-langfuse-exporter",
		"--watch",
		"--config", os.Getenv("CLT_CLI_SIGNAL_CONFIG"),
		"--state-file", os.Getenv("CLT_CLI_SIGNAL_STATE"),
		"--poll-interval-seconds", "0.001",
	}
	main()
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
