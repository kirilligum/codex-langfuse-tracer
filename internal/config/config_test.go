package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kirilligum/codex-langfuse-tracer/internal/buildinfo"
)

// TEST-003
func TestLoadLaminarConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex-custom"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config-custom"))
	if got := CodexHome(); got != filepath.Join(home, ".codex-custom") {
		t.Fatalf("CodexHome() = %q", got)
	}
	if got := DefaultStatePath(); got != filepath.Join(home, ".codex-custom", buildinfo.DefaultStateFileName) {
		t.Fatalf("DefaultStatePath() = %q", got)
	}
	if got := DefaultLaminarConfigPath(); got != filepath.Join(home, ".config-custom", "lmnr", "codex-tracer.json") {
		t.Fatalf("DefaultLaminarConfigPath() = %q", got)
	}

	configDir := filepath.Join(home, ".config-custom", "lmnr")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(configDir, "codex-tracer.json")
	writeConfig := func(t *testing.T, body string, mode os.FileMode) {
		t.Helper()
		if err := os.WriteFile(configPath, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(configPath, mode); err != nil {
			t.Fatal(err)
		}
	}

	writeConfig(t, `{"projectApiKey":"`+strings.Repeat("a", 64)+`","baseUrl":"http://127.0.0.1:14320"}`+"\n", 0o600)
	cfg, err := LoadLaminar(configPath)
	if err != nil {
		t.Fatalf("LoadLaminar() error: %v", err)
	}
	if cfg.BaseURL != "http://127.0.0.1:14320" || len(cfg.Token) != 64 {
		t.Fatalf("unexpected config shape: baseURL=%q token_length=%d", cfg.BaseURL, len(cfg.Token))
	}

	tests := []struct {
		name string
		body string
		mode os.FileMode
	}{
		{name: "missing token", body: `{"baseUrl":"http://127.0.0.1:14320"}`, mode: 0o600},
		{name: "malformed token", body: `{"projectApiKey":"bad","baseUrl":"http://127.0.0.1:14320"}`, mode: 0o600},
		{name: "remote target rejected", body: `{"projectApiKey":"` + strings.Repeat("a", 64) + `","baseUrl":"https://example.invalid"}`, mode: 0o600},
		{name: "unknown secret fields rejected", body: `{"projectApiKey":"` + strings.Repeat("a", 64) + `","baseUrl":"http://127.0.0.1:14320","secret":"hidden"}`, mode: 0o600},
		{name: "trailing JSON rejected", body: `{"projectApiKey":"` + strings.Repeat("a", 64) + `","baseUrl":"http://127.0.0.1:14320"}{}`, mode: 0o600},
		{name: "permissions rejected", body: `{"projectApiKey":"` + strings.Repeat("a", 64) + `","baseUrl":"http://127.0.0.1:14320"}`, mode: 0o644},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			writeConfig(t, test.body, test.mode)
			if _, err := LoadLaminar(configPath); err == nil {
				t.Fatal("LoadLaminar() succeeded, want error")
			}
		})
	}

	if _, err := LoadLaminar(filepath.Join(home, "missing.json")); err == nil {
		t.Fatal("LoadLaminar(missing) succeeded, want error")
	}
}
