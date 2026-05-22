package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kirilligum/codex-langfuse-tracer/internal/buildinfo"
)

// TEST-003
func TestLoadConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex-custom"))
	if got := CodexHome(); got != filepath.Join(home, ".codex-custom") {
		t.Fatalf("CodexHome() = %q", got)
	}
	if got := DefaultStatePath(); got != filepath.Join(home, ".codex-custom", buildinfo.DefaultStateFileName) {
		t.Fatalf("DefaultStatePath() = %q", got)
	}
	if got := DefaultConfigPath(); got != filepath.Join(home, ".codex-custom", "config.toml") {
		t.Fatalf("DefaultConfigPath() = %q", got)
	}

	configPath := filepath.Join(home, "config.toml")
	err := os.WriteFile(configPath, []byte(`
[mcp_servers.langfuse]
command = "uvx"

[mcp_servers.langfuse.env]
LANGFUSE_HOST = "http://localhost:3000/"
LANGFUSE_PUBLIC_KEY = "pk-lf-test"
LANGFUSE_SECRET_KEY = "sk-lf-test"
LANGFUSE_USER_ID_MODE = "workspace"
`), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Host != "http://localhost:3000" {
		t.Fatalf("host = %q", cfg.Host)
	}
	if cfg.PublicKey != "pk-lf-test" || cfg.SecretKey != "sk-lf-test" {
		t.Fatalf("keys not parsed: %+v", cfg)
	}
	if cfg.UserIDMode != UserIDModeWorkspace {
		t.Fatalf("user id mode = %q", cfg.UserIDMode)
	}

	missingPath := filepath.Join(home, "missing.toml")
	_, err = Load(missingPath)
	if err == nil {
		t.Fatal("Load(missing) succeeded, want error")
	}
	if !strings.Contains(err.Error(), "[mcp_servers.langfuse.env]") {
		t.Fatalf("missing error lacks config path hint: %v", err)
	}

	incompletePath := filepath.Join(home, "incomplete.toml")
	if err := os.WriteFile(incompletePath, []byte(`
[mcp_servers.langfuse.env]
LANGFUSE_HOST = "http://localhost:3000"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = Load(incompletePath)
	if err == nil {
		t.Fatal("Load(incomplete) succeeded, want error")
	}
	if !strings.Contains(err.Error(), "public key/secret key") {
		t.Fatalf("incomplete error = %v", err)
	}

	invalidModePath := filepath.Join(home, "invalid-mode.toml")
	if err := os.WriteFile(invalidModePath, []byte(`
[mcp_servers.langfuse.env]
LANGFUSE_HOST = "http://localhost:3000"
LANGFUSE_PUBLIC_KEY = "pk-lf-test"
LANGFUSE_SECRET_KEY = "sk-lf-test"
LANGFUSE_USER_ID_MODE = "cwd"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = Load(invalidModePath)
	if err == nil {
		t.Fatal("Load(invalid mode) succeeded, want error")
	}
	if !strings.Contains(err.Error(), "LANGFUSE_USER_ID_MODE") {
		t.Fatalf("invalid mode error = %v", err)
	}

	t.Setenv("CODEX_HOME", "")
	if got := CodexHome(); !strings.HasSuffix(got, ".codex") {
		t.Fatalf("CodexHome() with no CODEX_HOME = %q", got)
	}
}
