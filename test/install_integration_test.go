package test

import (
	"bytes"
	"context"
	"debug/buildinfo"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TEST-013
// TEST-407
// Run the actual installer, Go compiler, and authenticated receiver preflight.
// Only systemd is stubbed.
func TestInstallUninstallScripts(t *testing.T) {
	cacheOutput, err := exec.Command("go", "env", "GOMODCACHE", "GOCACHE").Output()
	if err != nil {
		t.Fatal(err)
	}
	caches := strings.Split(strings.TrimSpace(string(cacheOutput)), "\n")
	if len(caches) != 2 {
		t.Fatalf("unexpected Go cache paths: %q", cacheOutput)
	}
	for _, tc := range []struct {
		name                   string
		existing, failReceiver bool
	}{
		{"fresh install", false, false},
		{"existing install", true, false},
		{"receiver preflight failure", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			codexHome := filepath.Join(home, ".codex")
			binDir := filepath.Join(home, "fakebin")
			binary := filepath.Join(codexHome, "bin", "codex-langfuse-exporter")
			service := filepath.Join(home, ".config", "systemd", "user", "codex-langfuse-watch.service")
			statePath := filepath.Join(codexHome, "langfuse-export-state.json")
			for _, dir := range []string{binDir, filepath.Dir(binary), filepath.Dir(service)} {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			oldBinary := []byte("previous installed executable")
			oldService := []byte("previous installed unit")
			state := []byte(`{"version":3,"scan_watermark_ns":17,"processed_trace_ids":["retained"]}`)
			oldBinaryPath := filepath.Join(home, "old-exporter")
			if tc.existing {
				for path, raw := range map[string][]byte{binary: oldBinary, oldBinaryPath: oldBinary, service: oldService} {
					if err := os.WriteFile(path, raw, 0o700); err != nil {
						t.Fatal(err)
					}
				}
			}
			for path, raw := range map[string][]byte{statePath: state, statePath + ".lock": {}} {
				if err := os.WriteFile(path, raw, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			lockBefore, err := os.Stat(statePath + ".lock")
			if err != nil {
				t.Fatal(err)
			}
			writeFakeSystemctl(t, filepath.Join(binDir, "systemctl"))
			eventPath := filepath.Join(home, "events")
			var mu sync.Mutex
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				// A local Moshi health probe periodically sends an unauthenticated
				// GET / to ephemeral loopback listeners. It is not an exporter
				// request; the required authenticated OTLP POST is still counted
				// exactly once below.
				if r.Method == http.MethodGet && r.URL.Path == "/" && r.Header.Get("Authorization") == "" {
					http.NotFound(w, r)
					return
				}
				if r.Method != http.MethodPost || r.URL.Path != "/v1/traces" || r.Header.Get("Authorization") != "Bearer "+strings.Repeat("a", 64) {
					t.Errorf("invalid receiver preflight request: %s %s from %s user_agent=%q referer=%q", r.Method, r.URL, r.RemoteAddr, r.UserAgent(), r.Referer())
					http.Error(w, "invalid request", http.StatusBadRequest)
					return
				}
				body, err := io.ReadAll(r.Body)
				if err != nil || len(body) != 0 {
					t.Errorf("receiver preflight included span data: bytes=%d err=%v", len(body), err)
					http.Error(w, "unexpected payload", http.StatusBadRequest)
					return
				}
				requests++
				if tc.existing {
					if raw, err := os.ReadFile(binary); err != nil || !bytes.Equal(raw, oldBinary) {
						t.Error("installer replaced executable before receiver preflight completed")
					}
				}
				log, err := os.OpenFile(eventPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
				if err != nil {
					t.Error(err)
					http.Error(w, "log failure", http.StatusInternalServerError)
					return
				}
				_, err = fmt.Fprintln(log, "receiver POST")
				closeErr := log.Close()
				if err != nil || closeErr != nil {
					t.Errorf("receiver log: write=%v close=%v", err, closeErr)
				}
				if tc.failReceiver {
					http.Error(w, "receiver unavailable", http.StatusServiceUnavailable)
					return
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()
			writeInstallLaminarConfig(t, codexHome, server.URL)
			env := append(os.Environ(), "HOME="+home, "CODEX_HOME="+codexHome, "XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
				"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"), "SYSTEMCTL_LOG="+filepath.Join(home, "systemctl.log"),
				"INSTALL_EVENT_LOG="+eventPath, "GOMODCACHE="+caches[0], "GOCACHE="+caches[1],
				"SYSTEMCTL_LOAD_STATE=not-found", "SYSTEMCTL_EXPECT_OLD_BINARY=")
			if tc.existing {
				env = replaceEnvValue(env, "SYSTEMCTL_LOAD_STATE", "loaded")
				env = replaceEnvValue(env, "SYSTEMCTL_EXPECT_OLD_BINARY", oldBinaryPath)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			install := exec.CommandContext(ctx, "bash", "../install.sh")
			install.Env = env
			output, installErr := install.CombinedOutput()
			mu.Lock()
			requestCount := requests
			mu.Unlock()
			if requestCount != 1 {
				t.Fatalf("receiver preflight requests=%d want=1; install=%v\n%s", requestCount, installErr, output)
			}
			events, err := os.ReadFile(eventPath)
			if err != nil {
				t.Fatal(err)
			}
			assertInstallStagesClean(t, binary, service)
			if raw, err := os.ReadFile(statePath); err != nil || !bytes.Equal(raw, state) {
				t.Fatalf("installer changed state: err=%v", err)
			}
			if lockAfter, err := os.Stat(statePath + ".lock"); err != nil || !os.SameFile(lockBefore, lockAfter) {
				t.Fatalf("installer replaced lock sidecar: err=%v", err)
			}
			if tc.failReceiver {
				if installErr == nil || !strings.Contains(string(output), "HTTP 503") || strings.Contains(string(events), "systemctl ") {
					t.Fatalf("receiver failure did not stop installation before systemd: err=%v events=%s\n%s", installErr, events, output)
				}
				for path, want := range map[string][]byte{binary: oldBinary, service: oldService} {
					if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, want) {
						t.Fatalf("receiver failure changed %s: err=%v", path, err)
					}
				}
				return
			}
			if installErr != nil {
				t.Fatalf("real install failed: %v\n%s", installErr, output)
			}
			info, err := buildinfo.ReadFile(binary)
			if err != nil || info.Path != "github.com/kirilligum/codex-langfuse-tracer/cmd/codex-langfuse-exporter" {
				t.Fatalf("installed Go executable: info=%v err=%v", info, err)
			}
			text := string(events)
			receiver := strings.Index(text, "receiver POST")
			show := strings.Index(text, "systemctl --user show ")
			stop := strings.Index(text, "systemctl --user stop ")
			restart := strings.Index(text, "systemctl --user restart ")
			if !(receiver >= 0 && show > receiver && restart > show) || (tc.existing && !(stop > show && restart > stop)) || (!tc.existing && stop >= 0) {
				t.Fatalf("wrong real receiver preflight/cutover ordering:\n%s", events)
			}
			uninstall := exec.CommandContext(ctx, "bash", "../uninstall.sh")
			uninstall.Env = env
			if output, err := uninstall.CombinedOutput(); err != nil {
				t.Fatalf("uninstall: %v\n%s", err, output)
			}
			for _, path := range []string{binary, service, statePath} {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("uninstall left %s: err=%v", path, err)
				}
			}
			if after, err := os.Stat(statePath + ".lock"); err != nil || !os.SameFile(lockBefore, after) {
				t.Fatalf("uninstall replaced lock sidecar: err=%v", err)
			}
		})
	}
}
