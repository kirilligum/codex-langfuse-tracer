package config

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"

	"github.com/kirilligum/codex-langfuse-tracer/internal/buildinfo"
)

type LaminarConfig struct {
	BaseURL string
	Token   string
}

func CodexHome() string {
	if value := os.Getenv("CODEX_HOME"); value != "" {
		return value
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".codex"
	}
	return filepath.Join(home, ".codex")
}

func DefaultLaminarConfigPath() string {
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return filepath.Join(".config", "lmnr", "codex-tracer.json")
		}
		configHome = filepath.Join(home, ".config")
	}
	return filepath.Join(configHome, "lmnr", "codex-tracer.json")
}

func DefaultStatePath() string {
	return filepath.Join(CodexHome(), buildinfo.DefaultStateFileName)
}

type laminarPluginConfig struct {
	ProjectAPIKey string `json:"projectApiKey"`
	BaseURL       string `json:"baseUrl"`
}

func LoadLaminar(path string) (LaminarConfig, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return LaminarConfig{}, fmt.Errorf("read Laminar receiver config %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return LaminarConfig{}, fmt.Errorf("Laminar receiver config %s must be a regular file", path)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return LaminarConfig{}, fmt.Errorf("Laminar receiver config %s must not be accessible by group or other users", path)
	}
	parent, err := os.Stat(filepath.Dir(path))
	if err != nil || !parent.IsDir() || parent.Mode().Perm()&0o077 != 0 {
		return LaminarConfig{}, fmt.Errorf("Laminar receiver config directory %s must be private", filepath.Dir(path))
	}
	file, err := os.Open(path)
	if err != nil {
		return LaminarConfig{}, fmt.Errorf("open Laminar receiver config %s: %w", path, err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var parsed laminarPluginConfig
	if err := decoder.Decode(&parsed); err != nil {
		return LaminarConfig{}, fmt.Errorf("decode Laminar receiver config %s: %w", path, err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return LaminarConfig{}, fmt.Errorf("Laminar receiver config %s contains trailing JSON", path)
	}
	if len(parsed.ProjectAPIKey) != 64 {
		return LaminarConfig{}, fmt.Errorf("Laminar receiver token in %s must be a 32-byte hexadecimal token", path)
	}
	if _, err := hex.DecodeString(parsed.ProjectAPIKey); err != nil {
		return LaminarConfig{}, fmt.Errorf("Laminar receiver token in %s must be a 32-byte hexadecimal token", path)
	}
	endpoint, err := url.Parse(parsed.BaseURL)
	if err != nil || endpoint.Scheme != "http" || endpoint.User != nil || endpoint.Path != "" || endpoint.RawQuery != "" || endpoint.Fragment != "" || endpoint.Port() == "" {
		return LaminarConfig{}, fmt.Errorf("Laminar receiver config %s must target a local HTTP receiver", path)
	}
	if hostname := endpoint.Hostname(); hostname != "localhost" {
		ip := net.ParseIP(hostname)
		if ip == nil || !ip.IsLoopback() {
			return LaminarConfig{}, fmt.Errorf("Laminar receiver config %s must target a local HTTP receiver", path)
		}
	}
	return LaminarConfig{BaseURL: parsed.BaseURL, Token: parsed.ProjectAPIKey}, nil
}
