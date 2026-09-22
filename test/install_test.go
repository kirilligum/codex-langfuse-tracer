package test

import (
	"bytes"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// TEST-013
// TEST-407
func TestInstallUninstallScripts(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	goModCacheOutput, err := exec.Command("go", "env", "GOMODCACHE").Output()
	if err != nil {
		t.Fatalf("go env GOMODCACHE: %v", err)
	}
	goModCache := strings.TrimSpace(string(goModCacheOutput))
	binDir := filepath.Join(home, "fakebin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	systemctlLog := filepath.Join(home, "systemctl.log")
	systemctl := filepath.Join(binDir, "systemctl")
	writeFakeSystemctl(t, systemctl)

	codexHome := filepath.Join(home, ".codex")
	xdgConfig := filepath.Join(home, ".config")
	var logMu sync.Mutex
	appendInstallLog := func(line string) {
		logMu.Lock()
		defer logMu.Unlock()
		file, err := os.OpenFile(systemctlLog, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			t.Fatalf("open install log: %v", err)
		}
		defer file.Close()
		if _, err := file.WriteString(line + "\n"); err != nil {
			t.Fatalf("write install log: %v", err)
		}
	}
	modelPosts := 0
	var loopbackProbes atomic.Int32
	var requestMu sync.Mutex
	var unexpectedRequests []string
	recordUnexpected := func(method, path string) {
		requestMu.Lock()
		defer requestMu.Unlock()
		unexpectedRequests = append(unexpectedRequests, method+" "+path)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/public/models":
			username, password, ok := r.BasicAuth()
			if !ok || username != "pk-lf-test" || password != "sk-lf-test" {
				recordUnexpected("unauthenticated", r.URL.Path)
				http.Error(w, "authorization required", http.StatusUnauthorized)
				return
			}
			appendInstallLog("sync get models")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[],"meta":{}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/public/models":
			username, password, ok := r.BasicAuth()
			if !ok || username != "pk-lf-test" || password != "sk-lf-test" {
				recordUnexpected("unauthenticated", r.URL.Path)
				http.Error(w, "authorization required", http.StatusUnauthorized)
				return
			}
			modelPosts++
			appendInstallLog("sync post model")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{}`))
		case isUnrelatedLoopbackProbe(r):
			loopbackProbes.Add(1)
			w.WriteHeader(http.StatusOK)
		default:
			recordUnexpected(r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	listener, err := net.Listen("tcp4", "127.0.0.2:0")
	if err != nil {
		t.Fatalf("listen on isolated loopback address: %v", err)
	}
	server.Listener = listener
	server.Start()
	defer server.Close()
	env := append(os.Environ(),
		"HOME="+home,
		"CODEX_HOME="+codexHome,
		"XDG_CONFIG_HOME="+xdgConfig,
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"SYSTEMCTL_LOG="+systemctlLog,
		"SYSTEMCTL_LOAD_STATE=not-found",
		"GOMODCACHE="+goModCache,
		"GOCACHE="+filepath.Join(home, "gocache"),
	)

	writeInstallLangfuseConfig(t, codexHome, server.URL)

	install := exec.Command("bash", "../install.sh")
	install.Env = env
	output, err := install.CombinedOutput()
	if err != nil {
		t.Fatalf("install failed: %v\n%s", err, output)
	}
	binary := filepath.Join(codexHome, "bin", "codex-langfuse-exporter")
	if info, err := os.Stat(binary); err != nil || info.Mode()&0o111 == 0 {
		t.Fatalf("installed binary invalid info=%v err=%v", info, err)
	}
	servicePath := filepath.Join(xdgConfig, "systemd", "user", "codex-langfuse-watch.service")
	assertInstallStagesClean(t, binary, servicePath)
	serviceRaw, err := os.ReadFile(servicePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(serviceRaw), ".codex/bin/codex-langfuse-exporter --watch") {
		t.Fatalf("service does not use Go binary:\n%s", serviceRaw)
	}
	systemctlRaw, err := os.ReadFile(systemctlLog)
	if err != nil {
		t.Fatal(err)
	}
	systemctlText := string(systemctlRaw)
	if !strings.Contains(systemctlText, "enable codex-langfuse-watch.service") ||
		!strings.Contains(systemctlText, "restart codex-langfuse-watch.service") {
		t.Fatalf("install did not enable and restart service:\n%s", systemctlText)
	}
	if strings.Contains(systemctlText, "--user stop codex-langfuse-watch.service") {
		t.Fatalf("fresh install tried to stop a unit systemd reported as absent:\n%s", systemctlText)
	}
	if modelPosts != 7 {
		t.Fatalf("model sync POST count = %d, want 7\nlog=%s", modelPosts, systemctlText)
	}
	requestMu.Lock()
	unexpected := append([]string(nil), unexpectedRequests...)
	requestMu.Unlock()
	if len(unexpected) != 0 {
		t.Fatalf("unexpected Langfuse requests: %v", unexpected)
	}
	t.Logf("classified %d unauthenticated loopback GET / health probes as unrelated to model sync", loopbackProbes.Load())
	syncIndex := strings.Index(systemctlText, "sync post model")
	restartIndex := strings.Index(systemctlText, "restart codex-langfuse-watch.service")
	if syncIndex < 0 || restartIndex < 0 || syncIndex > restartIndex {
		t.Fatalf("model sync did not happen before restart:\n%s", systemctlText)
	}

	oldBinary := []byte("previous exporter must remain in place until preflight and stop succeed")
	if err := os.WriteFile(binary, oldBinary, 0o700); err != nil {
		t.Fatal(err)
	}
	existingEnv := replaceEnvValue(env, "SYSTEMCTL_LOAD_STATE", "loaded")
	existingInstall := exec.Command("bash", "../install.sh")
	existingInstall.Env = existingEnv
	output, err = existingInstall.CombinedOutput()
	if err != nil {
		t.Fatalf("existing install failed: %v\n%s", err, output)
	}
	installedBinary, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(installedBinary, oldBinary) {
		t.Fatal("existing installation retained the sentinel binary after successful promotion")
	}
	systemctlRaw, err = os.ReadFile(systemctlLog)
	if err != nil {
		t.Fatal(err)
	}
	systemctlText = string(systemctlRaw)
	syncIndex = strings.LastIndex(systemctlText, "sync post model")
	stopIndex := strings.LastIndex(systemctlText, "--user stop codex-langfuse-watch.service")
	restartIndex = strings.LastIndex(systemctlText, "restart codex-langfuse-watch.service")
	if syncIndex < 0 || stopIndex < 0 || restartIndex < 0 || !(syncIndex < stopIndex && stopIndex < restartIndex) {
		t.Fatalf("existing install did not preflight, stop, and then restart in order:\n%s", systemctlText)
	}
	if modelPosts != 14 {
		t.Fatalf("model sync POST count after existing install = %d, want 14", modelPosts)
	}
	statePath := filepath.Join(codexHome, "langfuse-export-state.json")
	lockPath := statePath + ".lock"
	for _, path := range []string{statePath, lockPath} {
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	uninstall := exec.Command("bash", "../uninstall.sh")
	uninstall.Env = env
	output, err = uninstall.CombinedOutput()
	if err != nil {
		t.Fatalf("uninstall failed: %v\n%s", err, output)
	}
	for _, path := range []string{binary, servicePath, statePath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s still exists after uninstall", path)
		}
	}
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("persistent state lock sidecar after uninstall: %v", err)
	}

	failingHome := t.TempDir()
	failingBinDir := filepath.Join(failingHome, "fakebin")
	if err := os.MkdirAll(failingBinDir, 0o755); err != nil {
		t.Fatal(err)
	}
	failingLog := filepath.Join(failingHome, "systemctl.log")
	failingSystemctl := filepath.Join(failingBinDir, "systemctl")
	writeFakeSystemctl(t, failingSystemctl)
	failingCodexHome := filepath.Join(failingHome, ".codex")
	oldFailingBinary := []byte("preserve old binary after pricing failure")
	if err := os.MkdirAll(filepath.Join(failingCodexHome, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	failingBinary := filepath.Join(failingCodexHome, "bin", "codex-langfuse-exporter")
	if err := os.WriteFile(failingBinary, oldFailingBinary, 0o700); err != nil {
		t.Fatal(err)
	}
	failingService := filepath.Join(failingHome, ".config", "systemd", "user", "codex-langfuse-watch.service")
	if err := os.MkdirAll(filepath.Dir(failingService), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(failingService, []byte("old service"), 0o600); err != nil {
		t.Fatal(err)
	}
	failingServer := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "pricing setup failed", http.StatusInternalServerError)
	}))
	failingListener, err := net.Listen("tcp4", "127.0.0.2:0")
	if err != nil {
		t.Fatalf("listen on isolated loopback address: %v", err)
	}
	failingServer.Listener = failingListener
	failingServer.Start()
	defer failingServer.Close()
	writeInstallLangfuseConfig(t, failingCodexHome, failingServer.URL)
	failingEnv := append(os.Environ(),
		"HOME="+failingHome,
		"CODEX_HOME="+failingCodexHome,
		"XDG_CONFIG_HOME="+filepath.Join(failingHome, ".config"),
		"PATH="+failingBinDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"SYSTEMCTL_LOG="+failingLog,
		"SYSTEMCTL_LOAD_STATE=loaded",
		"GOMODCACHE="+goModCache,
		"GOCACHE="+filepath.Join(home, "gocache"),
	)
	failingInstall := exec.Command("bash", "../install.sh")
	failingInstall.Env = failingEnv
	output, err = failingInstall.CombinedOutput()
	if err == nil {
		t.Fatalf("failing install succeeded:\n%s", output)
	}
	if got, readErr := os.ReadFile(failingBinary); readErr != nil || !bytes.Equal(got, oldFailingBinary) {
		t.Fatalf("pricing failure changed installed binary: bytes_equal=%v err=%v", bytes.Equal(got, oldFailingBinary), readErr)
	}
	assertInstallStagesClean(t, failingBinary, failingService)
	failingRaw, readErr := os.ReadFile(failingLog)
	if readErr == nil && (strings.Contains(string(failingRaw), "--user stop codex-langfuse-watch.service") || strings.Contains(string(failingRaw), "restart codex-langfuse-watch.service")) {
		t.Fatalf("install touched the service before pricing preflight passed:\n%s", string(failingRaw))
	}

	stopFailureHome := t.TempDir()
	stopFailureBinDir := filepath.Join(stopFailureHome, "fakebin")
	if err := os.MkdirAll(stopFailureBinDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stopFailureSystemctl := filepath.Join(stopFailureBinDir, "systemctl")
	writeFakeSystemctl(t, stopFailureSystemctl)
	stopFailureCodexHome := filepath.Join(stopFailureHome, ".codex")
	if err := os.MkdirAll(filepath.Join(stopFailureCodexHome, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	stopFailureBinary := filepath.Join(stopFailureCodexHome, "bin", "codex-langfuse-exporter")
	oldStopFailureBinary := []byte("preserve old binary when service stop fails")
	if err := os.WriteFile(stopFailureBinary, oldStopFailureBinary, 0o700); err != nil {
		t.Fatal(err)
	}
	writeInstallLangfuseConfig(t, stopFailureCodexHome, server.URL)
	stopFailureLog := filepath.Join(stopFailureHome, "systemctl.log")
	stopFailureEnv := append(os.Environ(),
		"HOME="+stopFailureHome,
		"CODEX_HOME="+stopFailureCodexHome,
		"XDG_CONFIG_HOME="+filepath.Join(stopFailureHome, ".config"),
		"PATH="+stopFailureBinDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"SYSTEMCTL_LOG="+stopFailureLog,
		"SYSTEMCTL_LOAD_STATE=loaded",
		"SYSTEMCTL_FAIL_STOP=1",
		"GOMODCACHE="+goModCache,
		"GOCACHE="+filepath.Join(home, "gocache"),
	)
	stopFailureInstall := exec.Command("bash", "../install.sh")
	stopFailureInstall.Env = stopFailureEnv
	output, err = stopFailureInstall.CombinedOutput()
	if err == nil {
		t.Fatalf("install succeeded despite service stop failure:\n%s", output)
	}
	if got, readErr := os.ReadFile(stopFailureBinary); readErr != nil || !bytes.Equal(got, oldStopFailureBinary) {
		t.Fatalf("stop failure changed installed binary: bytes_equal=%v err=%v", bytes.Equal(got, oldStopFailureBinary), readErr)
	}
	stopFailureRaw, readErr := os.ReadFile(stopFailureLog)
	if readErr != nil || !strings.Contains(string(stopFailureRaw), "--user stop codex-langfuse-watch.service") || strings.Contains(string(stopFailureRaw), "restart codex-langfuse-watch.service") {
		t.Fatalf("stop failure was masked or install restarted:\n%s err=%v", string(stopFailureRaw), readErr)
	}
	assertInstallStagesClean(t, stopFailureBinary, filepath.Join(stopFailureHome, ".config", "systemd", "user", "codex-langfuse-watch.service"))
}

// A failed restart after promotion leaves the new executable staged in place,
// reports the actual service state, and provides a recovery action.
func TestInstallReportsPostStopFailureState(t *testing.T) {
	home := t.TempDir()
	binDir := filepath.Join(home, "fakebin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	systemctl := filepath.Join(binDir, "systemctl")
	writeFakeSystemctl(t, systemctl)
	codexHome := filepath.Join(home, ".codex")
	if err := os.MkdirAll(filepath.Join(codexHome, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(codexHome, "bin", "codex-langfuse-exporter")
	oldBinary := []byte("old executable")
	if err := os.WriteFile(binary, oldBinary, 0o700); err != nil {
		t.Fatal(err)
	}
	var unexpected []string
	var unexpectedMu sync.Mutex
	modelPosts := 0
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if (r.Method == http.MethodGet || r.Method == http.MethodPost) && r.URL.Path == "/api/public/models" {
			username, password, ok := r.BasicAuth()
			if !ok || username != "pk-lf-test" || password != "sk-lf-test" {
				http.Error(w, "authorization required", http.StatusUnauthorized)
				return
			}
			if r.Method == http.MethodGet {
				_, _ = w.Write([]byte(`{"data":[],"meta":{}}`))
				return
			}
			modelPosts++
			_, _ = w.Write([]byte(`{}`))
			return
		}
		if isUnrelatedLoopbackProbe(r) {
			w.WriteHeader(http.StatusOK)
			return
		}
		unexpectedMu.Lock()
		unexpected = append(unexpected, r.Method+" "+r.URL.Path)
		unexpectedMu.Unlock()
		http.NotFound(w, r)
	}))
	listener, err := net.Listen("tcp4", "127.0.0.2:0")
	if err != nil {
		t.Fatalf("listen on isolated loopback address: %v", err)
	}
	server.Listener = listener
	server.Start()
	defer server.Close()
	writeInstallLangfuseConfig(t, codexHome, server.URL)
	cacheOutput, err := exec.Command("go", "env", "GOCACHE").Output()
	if err != nil {
		t.Fatal(err)
	}
	moduleCacheOutput, err := exec.Command("go", "env", "GOMODCACHE").Output()
	if err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(),
		"HOME="+home,
		"CODEX_HOME="+codexHome,
		"XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"SYSTEMCTL_LOG="+filepath.Join(home, "systemctl.log"),
		"SYSTEMCTL_LOAD_STATE=loaded",
		"SYSTEMCTL_ACTIVE_STATE=failed",
		"SYSTEMCTL_FAIL_RESTART=1",
		"GOCACHE="+strings.TrimSpace(string(cacheOutput)),
		"GOMODCACHE="+strings.TrimSpace(string(moduleCacheOutput)),
	)
	install := exec.Command("bash", "../install.sh")
	install.Env = env
	output, err := install.CombinedOutput()
	if err == nil {
		t.Fatalf("install succeeded despite service restart failure:\n%s", output)
	}
	if !strings.Contains(string(output), "current service state: failed") || !strings.Contains(string(output), "rerun ./install.sh") {
		t.Fatalf("post-stop failure omitted service state or recovery action:\n%s", output)
	}
	if got, err := os.ReadFile(binary); err != nil || bytes.Equal(got, oldBinary) {
		t.Fatalf("post-stop failure did not promote the staged executable: bytes_equal_old=%v err=%v", bytes.Equal(got, oldBinary), err)
	}
	assertInstallStagesClean(t, binary, filepath.Join(home, ".config", "systemd", "user", "codex-langfuse-watch.service"))
	if modelPosts != 7 || len(unexpected) != 0 {
		t.Fatalf("pricing requests=%d unexpected=%v", modelPosts, unexpected)
	}
}

// EVAL-006
func TestEvalInstallRuntimeSurface(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("../install.sh")
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if strings.Contains(text, "exporter_src=\"$repo_dir/bin/export_codex_session_to_langfuse.py\"") ||
		strings.Contains(text, "install -m 755 \"$exporter_src\"") {
		t.Fatal("install script still installs Python exporter")
	}
	if !strings.Contains(text, "go build") {
		t.Fatal("install script must build Go binary")
	}
}

func writeInstallLangfuseConfig(t *testing.T, codexHome, host string) {
	t.Helper()
	if err := os.MkdirAll(codexHome, 0o755); err != nil {
		t.Fatal(err)
	}
	raw := fmt.Sprintf(`
[mcp_servers.langfuse.env]
LANGFUSE_HOST = %q
LANGFUSE_PUBLIC_KEY = "pk-lf-test"
LANGFUSE_SECRET_KEY = "sk-lf-test"
`, host)
	if err := os.WriteFile(filepath.Join(codexHome, "config.toml"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeFakeSystemctl(t *testing.T, path string) {
	t.Helper()
	script := `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "$SYSTEMCTL_LOG"
if [ "${2:-}" = "show" ]; then
    printf '%s\n' "${SYSTEMCTL_LOAD_STATE:-not-found}"
fi
if [ "${2:-}" = "stop" ] && [ "${SYSTEMCTL_FAIL_STOP:-0}" = "1" ]; then
    echo "simulated systemctl stop failure" >&2
    exit 1
fi
if [ "${2:-}" = "restart" ] && [ "${SYSTEMCTL_FAIL_RESTART:-0}" = "1" ]; then
    echo "simulated systemctl restart failure" >&2
    exit 1
fi
if [ "${2:-}" = "is-active" ]; then
    printf '%s\n' "${SYSTEMCTL_ACTIVE_STATE:-inactive}"
fi
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func replaceEnvValue(env []string, key, value string) []string {
	prefix := key + "="
	filtered := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if !strings.HasPrefix(entry, prefix) {
			filtered = append(filtered, entry)
		}
	}
	return append(filtered, prefix+value)
}

func isUnrelatedLoopbackProbe(request *http.Request) bool {
	// The test environment has sent unauthenticated Go-client GET / probes to
	// disposable loopback listeners. Keep this exact signature separate; every
	// model API route and every other unexpected request remains an assertion.
	if request.Method != http.MethodGet || request.URL.Path != "/" || request.Header.Get("Authorization") != "" || !strings.HasPrefix(request.UserAgent(), "Go-http-client/") {
		return false
	}
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		return false
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}

func assertInstallStagesClean(t *testing.T, binaryPath, servicePath string) {
	t.Helper()
	for _, pattern := range []string{binaryPath + ".stage.*", servicePath + ".stage.*"} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatalf("glob staged path %s: %v", pattern, err)
		}
		if len(matches) != 0 {
			t.Fatalf("installer left staged paths after completion: %v", matches)
		}
	}
}
