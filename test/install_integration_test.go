package test

import (
	"bytes"
	"context"
	"debug/buildinfo"
	"encoding/json"
	"encoding/pem"
	"fmt"
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
// Run the actual installer, Go compiler, and exporter. Only systemd is stubbed.
// TLS with explicit trust isolates the mock API from plain HTTP port probes;
// every HTTP request reaching the handler must satisfy the exporter contract.
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
		name                  string
		existing, failPricing bool
	}{
		{"fresh install", false, false},
		{"existing install", true, false},
		{"pricing failure", true, true},
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
			gets, posts := 0, 0
			models := map[string]bool{}
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				user, password, ok := r.BasicAuth()
				if !ok || user != "pk-lf-test" || password != "sk-lf-test" {
					t.Error("model request missing expected BasicAuth")
					http.Error(w, "unauthorized", http.StatusUnauthorized)
					return
				}
				if r.URL.Path != "/api/public/models" || (r.Method != http.MethodGet && r.Method != http.MethodPost) {
					t.Errorf("unexpected model API request: %s %s", r.Method, r.URL)
					http.Error(w, "unexpected request", http.StatusBadRequest)
					return
				}
				if tc.existing {
					if raw, err := os.ReadFile(binary); err != nil || !bytes.Equal(raw, oldBinary) {
						t.Error("installer replaced executable before pricing preflight completed")
					}
				}
				log, err := os.OpenFile(eventPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
				if err != nil {
					t.Error(err)
					http.Error(w, "log failure", http.StatusInternalServerError)
					return
				}
				_, err = fmt.Fprintln(log, "pricing", r.Method)
				closeErr := log.Close()
				if err != nil || closeErr != nil {
					t.Errorf("pricing log: write=%v close=%v", err, closeErr)
				}
				w.Header().Set("Content-Type", "application/json")
				if r.Method == http.MethodGet {
					gets++
					if r.URL.RawQuery != "page=1&limit=100" {
						t.Errorf("model list query = %q", r.URL.RawQuery)
					}
					fmt.Fprint(w, `{"data":[],"meta":{}}`)
					return
				}
				posts++
				var model struct {
					Name  string           `json:"modelName"`
					Unit  string           `json:"unit"`
					Tiers []map[string]any `json:"pricingTiers"`
				}
				if err := json.NewDecoder(r.Body).Decode(&model); err != nil || model.Name == "" || model.Unit != "TOKENS" || len(model.Tiers) == 0 || models[model.Name] {
					t.Errorf("invalid or duplicate model payload: %+v err=%v", model, err)
					http.Error(w, "invalid payload", http.StatusBadRequest)
					return
				}
				models[model.Name] = true
				if tc.failPricing {
					http.Error(w, "pricing unavailable", http.StatusServiceUnavailable)
					return
				}
				fmt.Fprint(w, `{}`)
			}))
			defer server.Close()
			certPath := filepath.Join(home, "pricing-ca.pem")
			if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0o600); err != nil {
				t.Fatal(err)
			}
			writeInstallLangfuseConfig(t, codexHome, server.URL)
			env := append(os.Environ(), "HOME="+home, "CODEX_HOME="+codexHome, "XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
				"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"), "SYSTEMCTL_LOG="+filepath.Join(home, "systemctl.log"),
				"INSTALL_EVENT_LOG="+eventPath, "GOMODCACHE="+caches[0], "GOCACHE="+caches[1], "SSL_CERT_FILE="+certPath,
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
			server.Close()
			mu.Lock()
			defer mu.Unlock()
			wantPosts := 7 // The full catalogue; exact pricing is checked in internal/langfuse.
			if tc.failPricing {
				wantPosts = 1
			}
			if gets != 1 || posts != wantPosts {
				t.Fatalf("real pricing requests: GET=%d POST=%d, want 1/%d; install=%v\n%s", gets, posts, wantPosts, installErr, output)
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
			if tc.failPricing {
				if installErr == nil || !strings.Contains(string(output), "HTTP 503") || strings.Contains(string(events), "systemctl ") {
					t.Fatalf("pricing failure did not stop installation before systemd: err=%v events=%s\n%s", installErr, events, output)
				}
				for path, want := range map[string][]byte{binary: oldBinary, service: oldService} {
					if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, want) {
						t.Fatalf("pricing failure changed %s: err=%v", path, err)
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
			lastPricing := strings.LastIndex(text, "pricing POST")
			show := strings.Index(text, "systemctl --user show ")
			stop := strings.Index(text, "systemctl --user stop ")
			restart := strings.Index(text, "systemctl --user restart ")
			if !(lastPricing >= 0 && show > lastPricing && restart > show) || (tc.existing && !(stop > show && restart > stop)) || (!tc.existing && stop >= 0) {
				t.Fatalf("wrong real pricing/cutover ordering:\n%s", events)
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
