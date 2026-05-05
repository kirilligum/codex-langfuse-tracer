package test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
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
	if err := os.WriteFile(systemctl, []byte("#!/usr/bin/env bash\nprintf '%s\\n' \"$*\" >> \"$SYSTEMCTL_LOG\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/public/models":
			appendInstallLog("sync get models")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[],"meta":{}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/public/models":
			modelPosts++
			appendInstallLog("sync post model")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Fatalf("unexpected Langfuse request %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()
	env := append(os.Environ(),
		"HOME="+home,
		"CODEX_HOME="+codexHome,
		"XDG_CONFIG_HOME="+xdgConfig,
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"SYSTEMCTL_LOG="+systemctlLog,
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
	if modelPosts != 6 {
		t.Fatalf("model sync POST count = %d, want 6\nlog=%s", modelPosts, systemctlText)
	}
	syncIndex := strings.Index(systemctlText, "sync post model")
	restartIndex := strings.Index(systemctlText, "restart codex-langfuse-watch.service")
	if syncIndex < 0 || restartIndex < 0 || syncIndex > restartIndex {
		t.Fatalf("model sync did not happen before restart:\n%s", systemctlText)
	}

	uninstall := exec.Command("bash", "../uninstall.sh")
	uninstall.Env = env
	output, err = uninstall.CombinedOutput()
	if err != nil {
		t.Fatalf("uninstall failed: %v\n%s", err, output)
	}
	for _, path := range []string{binary, servicePath, filepath.Join(codexHome, "langfuse-export-state.json")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s still exists after uninstall", path)
		}
	}

	failingHome := t.TempDir()
	failingBinDir := filepath.Join(failingHome, "fakebin")
	if err := os.MkdirAll(failingBinDir, 0o755); err != nil {
		t.Fatal(err)
	}
	failingLog := filepath.Join(failingHome, "systemctl.log")
	failingSystemctl := filepath.Join(failingBinDir, "systemctl")
	if err := os.WriteFile(failingSystemctl, []byte("#!/usr/bin/env bash\nprintf '%s\\n' \"$*\" >> \"$SYSTEMCTL_LOG\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	failingCodexHome := filepath.Join(failingHome, ".codex")
	failingServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "pricing setup failed", http.StatusInternalServerError)
	}))
	defer failingServer.Close()
	writeInstallLangfuseConfig(t, failingCodexHome, failingServer.URL)
	failingEnv := append(os.Environ(),
		"HOME="+failingHome,
		"CODEX_HOME="+failingCodexHome,
		"XDG_CONFIG_HOME="+filepath.Join(failingHome, ".config"),
		"PATH="+failingBinDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"SYSTEMCTL_LOG="+failingLog,
		"GOMODCACHE="+goModCache,
		"GOCACHE="+filepath.Join(failingHome, "gocache"),
	)
	failingInstall := exec.Command("bash", "../install.sh")
	failingInstall.Env = failingEnv
	output, err = failingInstall.CombinedOutput()
	if err == nil {
		t.Fatalf("failing install succeeded:\n%s", output)
	}
	failingRaw, readErr := os.ReadFile(failingLog)
	if readErr == nil && strings.Contains(string(failingRaw), "restart codex-langfuse-watch.service") {
		t.Fatalf("install restarted service after pricing setup failure:\n%s", string(failingRaw))
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
