package test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TEST-013
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
	env := append(os.Environ(),
		"HOME="+home,
		"CODEX_HOME="+codexHome,
		"XDG_CONFIG_HOME="+xdgConfig,
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"SYSTEMCTL_LOG="+systemctlLog,
		"GOMODCACHE="+goModCache,
		"GOCACHE="+filepath.Join(home, "gocache"),
	)

	oldWrapper := filepath.Join(codexHome, "bin", "codex")
	if err := os.MkdirAll(filepath.Dir(oldWrapper), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldWrapper, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}

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
	if _, err := os.Stat(oldWrapper); !os.IsNotExist(err) {
		t.Fatalf("old wrapper still exists")
	}
	servicePath := filepath.Join(xdgConfig, "systemd", "user", "codex-langfuse-watch.service")
	serviceRaw, err := os.ReadFile(servicePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(serviceRaw), ".codex/bin/codex-langfuse-exporter --watch") {
		t.Fatalf("service does not use Go binary:\n%s", serviceRaw)
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
