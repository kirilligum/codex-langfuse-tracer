package test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Stubbed commands let this test exercise each installer failure boundary.
// TestInstallUninstallScripts separately runs real builds and pricing preflight.
func TestInstallOrderingAndFailures(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	binDir := filepath.Join(home, "fakebin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	systemctlLog := filepath.Join(home, "systemctl.log")
	installEventLog := filepath.Join(home, "install.events")
	systemctl := filepath.Join(binDir, "systemctl")
	writeFakeSystemctl(t, systemctl)
	writeFakeGo(t, filepath.Join(binDir, "go"))

	codexHome := filepath.Join(home, ".codex")
	xdgConfig := filepath.Join(home, ".config")
	env := append(os.Environ(),
		"HOME="+home,
		"CODEX_HOME="+codexHome,
		"XDG_CONFIG_HOME="+xdgConfig,
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"SYSTEMCTL_LOG="+systemctlLog,
		"INSTALL_EVENT_LOG="+installEventLog,
		"SYSTEMCTL_LOAD_STATE=not-found",
	)

	writeInstallLangfuseConfig(t, codexHome, "https://langfuse.invalid")

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
	eventRaw, err := os.ReadFile(installEventLog)
	if err != nil {
		t.Fatal(err)
	}
	eventText := string(eventRaw)
	buildIndex := strings.Index(eventText, "go build output=")
	syncIndex := strings.Index(eventText, "exporter --sync-model-pricing --quiet")
	loadIndex := strings.Index(eventText, "systemctl --user show --property=LoadState --value codex-langfuse-watch.service")
	restartIndex := strings.Index(eventText, "systemctl --user restart codex-langfuse-watch.service")
	if buildIndex < 0 || syncIndex < buildIndex || loadIndex < syncIndex || restartIndex < loadIndex {
		t.Fatalf("installer did not build, preflight, inspect service state, and restart in order:\n%s", eventText)
	}
	if !strings.Contains(eventText[buildIndex:syncIndex], ".stage.") {
		t.Fatalf("preflight executable was not built in a staging path:\n%s", eventText)
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
	eventRaw, err = os.ReadFile(installEventLog)
	if err != nil {
		t.Fatal(err)
	}
	eventText = string(eventRaw)
	syncIndex = strings.LastIndex(eventText, "exporter --sync-model-pricing --quiet")
	stopIndex := strings.LastIndex(eventText, "systemctl --user stop codex-langfuse-watch.service")
	restartIndex = strings.LastIndex(eventText, "systemctl --user restart codex-langfuse-watch.service")
	if syncIndex < 0 || stopIndex < 0 || restartIndex < 0 || !(syncIndex < stopIndex && stopIndex < restartIndex) {
		t.Fatalf("existing install did not preflight, stop, and then restart in order:\n%s", eventText)
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
	writeFakeGo(t, filepath.Join(failingBinDir, "go"))
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
	writeInstallLangfuseConfig(t, failingCodexHome, "https://langfuse.invalid")
	failingEnv := append(os.Environ(),
		"HOME="+failingHome,
		"CODEX_HOME="+failingCodexHome,
		"XDG_CONFIG_HOME="+filepath.Join(failingHome, ".config"),
		"PATH="+failingBinDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"SYSTEMCTL_LOG="+failingLog,
		"INSTALL_EVENT_LOG="+filepath.Join(failingHome, "install.events"),
		"SYSTEMCTL_LOAD_STATE=loaded",
		"FAKE_EXPORTER_SYNC_FAIL=1",
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
	if !strings.Contains(string(output), "simulated pricing preflight failure") {
		t.Fatalf("install failed for a reason other than the staged preflight:\n%s", output)
	}

	stopFailureHome := t.TempDir()
	stopFailureBinDir := filepath.Join(stopFailureHome, "fakebin")
	if err := os.MkdirAll(stopFailureBinDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stopFailureSystemctl := filepath.Join(stopFailureBinDir, "systemctl")
	writeFakeSystemctl(t, stopFailureSystemctl)
	writeFakeGo(t, filepath.Join(stopFailureBinDir, "go"))
	stopFailureCodexHome := filepath.Join(stopFailureHome, ".codex")
	if err := os.MkdirAll(filepath.Join(stopFailureCodexHome, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	stopFailureBinary := filepath.Join(stopFailureCodexHome, "bin", "codex-langfuse-exporter")
	oldStopFailureBinary := []byte("preserve old binary when service stop fails")
	if err := os.WriteFile(stopFailureBinary, oldStopFailureBinary, 0o700); err != nil {
		t.Fatal(err)
	}
	writeInstallLangfuseConfig(t, stopFailureCodexHome, "https://langfuse.invalid")
	stopFailureLog := filepath.Join(stopFailureHome, "systemctl.log")
	stopFailureEventLog := filepath.Join(stopFailureHome, "install.events")
	stopFailureEnv := append(os.Environ(),
		"HOME="+stopFailureHome,
		"CODEX_HOME="+stopFailureCodexHome,
		"XDG_CONFIG_HOME="+filepath.Join(stopFailureHome, ".config"),
		"PATH="+stopFailureBinDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"SYSTEMCTL_LOG="+stopFailureLog,
		"INSTALL_EVENT_LOG="+stopFailureEventLog,
		"SYSTEMCTL_LOAD_STATE=loaded",
		"SYSTEMCTL_FAIL_STOP=1",
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
	writeFakeGo(t, filepath.Join(binDir, "go"))
	codexHome := filepath.Join(home, ".codex")
	if err := os.MkdirAll(filepath.Join(codexHome, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(codexHome, "bin", "codex-langfuse-exporter")
	oldBinary := []byte("old executable")
	if err := os.WriteFile(binary, oldBinary, 0o700); err != nil {
		t.Fatal(err)
	}
	writeInstallLangfuseConfig(t, codexHome, "https://langfuse.invalid")
	env := append(os.Environ(),
		"HOME="+home,
		"CODEX_HOME="+codexHome,
		"XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"SYSTEMCTL_LOG="+filepath.Join(home, "systemctl.log"),
		"INSTALL_EVENT_LOG="+filepath.Join(home, "install.events"),
		"SYSTEMCTL_LOAD_STATE=loaded",
		"SYSTEMCTL_ACTIVE_STATE=failed",
		"SYSTEMCTL_FAIL_RESTART=1",
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
if [ -n "${INSTALL_EVENT_LOG:-}" ]; then
    printf 'systemctl %s\n' "$*" >> "$INSTALL_EVENT_LOG"
fi
if [ -n "${SYSTEMCTL_EXPECT_OLD_BINARY:-}" ] && { [ "${2:-}" = "show" ] || [ "${2:-}" = "stop" ]; }; then
    cmp "$SYSTEMCTL_EXPECT_OLD_BINARY" "$CODEX_HOME/bin/codex-langfuse-exporter"
fi
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

func writeFakeGo(t *testing.T, path string) {
	t.Helper()
	script := `#!/usr/bin/env bash
set -euo pipefail
output=""
build_args="$*"
while (($#)); do
    if [ "$1" = "-o" ]; then
        shift
        output="${1:-}"
        break
    fi
    shift
done
if [ -z "$output" ]; then
    echo "fake go build requires -o" >&2
    exit 2
fi
case "$build_args" in
    *"./cmd/codex-langfuse-exporter") ;;
    *) echo "fake go received unexpected build target: $build_args" >&2; exit 2 ;;
esac
cat > "$output" <<'FAKE_EXPORTER'
#!/usr/bin/env bash
set -euo pipefail
printf 'exporter %s\n' "$*" >> "$INSTALL_EVENT_LOG"
if [ "${FAKE_EXPORTER_SYNC_FAIL:-0}" = "1" ]; then
    echo "simulated pricing preflight failure" >&2
    exit 1
fi
FAKE_EXPORTER
chmod 755 "$output"
printf 'go build output=%s\n' "$output" >> "$INSTALL_EVENT_LOG"
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
