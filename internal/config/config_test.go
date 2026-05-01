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

	configPath := filepath.Join(home, "config.toml")
	err := os.WriteFile(configPath, []byte(`
[mcp_servers.langfuse]
command = "uvx"

[mcp_servers.langfuse.env]
LANGFUSE_HOST = "http://localhost:3000/"
LANGFUSE_PUBLIC_KEY = "pk-lf-test"
LANGFUSE_SECRET_KEY = "sk-lf-test"
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

	missingPath := filepath.Join(home, "missing.toml")
	_, err = Load(missingPath)
	if err == nil {
		t.Fatal("Load(missing) succeeded, want error")
	}
	if !strings.Contains(err.Error(), "[mcp_servers.langfuse.env]") {
		t.Fatalf("missing error lacks config path hint: %v", err)
	}
}
