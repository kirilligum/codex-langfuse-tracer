package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/kirilligum/codex-langfuse-tracer/internal/buildinfo"
)

type LangfuseConfig struct {
	Host       string
	PublicKey  string
	SecretKey  string
	UserIDMode string
}

const (
	UserIDModeDefault   = ""
	UserIDModeWorkspace = "workspace"
)

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

func DefaultConfigPath() string {
	return filepath.Join(CodexHome(), "config.toml")
}

func DefaultStatePath() string {
	return filepath.Join(CodexHome(), buildinfo.DefaultStateFileName)
}

type langfuseTOML struct {
	MCPServers map[string]mcpServer `toml:"mcp_servers"`
}

type mcpServer struct {
	Env map[string]string `toml:"env"`
}

func Load(path string) (LangfuseConfig, error) {
	var parsed langfuseTOML
	if _, err := toml.DecodeFile(path, &parsed); err != nil {
		return LangfuseConfig{}, fmt.Errorf("missing Langfuse host/public key/secret key in [mcp_servers.langfuse.env] in %s: %w", path, err)
	}

	env := map[string]string(nil)
	if parsed.MCPServers != nil {
		env = parsed.MCPServers["langfuse"].Env
	}
	host := strings.TrimRight(env["LANGFUSE_HOST"], "/")
	publicKey := env["LANGFUSE_PUBLIC_KEY"]
	secretKey := env["LANGFUSE_SECRET_KEY"]
	userIDMode, err := parseUserIDMode(env["LANGFUSE_USER_ID_MODE"])
	if err != nil {
		return LangfuseConfig{}, fmt.Errorf("%w in [mcp_servers.langfuse.env] in %s", err, path)
	}

	missing := make([]string, 0, 3)
	if host == "" {
		missing = append(missing, "host")
	}
	if publicKey == "" {
		missing = append(missing, "public key")
	}
	if secretKey == "" {
		missing = append(missing, "secret key")
	}
	if len(missing) > 0 {
		return LangfuseConfig{}, fmt.Errorf("missing Langfuse %s in [mcp_servers.langfuse.env] in %s", strings.Join(missing, "/"), path)
	}

	return LangfuseConfig{Host: host, PublicKey: publicKey, SecretKey: secretKey, UserIDMode: userIDMode}, nil
}

func parseUserIDMode(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "default":
		return UserIDModeDefault, nil
	case UserIDModeWorkspace:
		return UserIDModeWorkspace, nil
	default:
		return "", fmt.Errorf("invalid LANGFUSE_USER_ID_MODE %q; use \"workspace\" or leave it unset", value)
	}
}
