package providers

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/kirilligum/codex-langfuse-tracer/internal/agenttrace"
	"github.com/kirilligum/codex-langfuse-tracer/internal/claudetrace"
	"github.com/kirilligum/codex-langfuse-tracer/internal/codextrace"
)

var ErrUnsupportedProvider = errors.New("unsupported provider")

type Parser func(string) ([]agenttrace.Turn, error)

type Provider struct {
	Name             string
	DisplayName      string
	ExplicitPathOnly bool
	ParseTurns       Parser
}

var registry = map[string]Provider{
	agenttrace.ProviderCodex: {
		Name:        agenttrace.ProviderCodex,
		DisplayName: "Codex",
		ParseTurns:  codextrace.ParseTurns,
	},
	agenttrace.ProviderClaude: {
		Name:             agenttrace.ProviderClaude,
		DisplayName:      "Claude",
		ExplicitPathOnly: true,
		ParseTurns:       claudetrace.ParseTurns,
	},
}

func Normalize(provider string) string {
	provider = strings.TrimSpace(provider)
	if provider == "" {
		return agenttrace.ProviderCodex
	}
	return provider
}

func Names() []string {
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func Get(provider string) (Provider, error) {
	provider = Normalize(provider)
	spec, ok := registry[provider]
	if !ok {
		return Provider{}, fmt.Errorf("%w %q: expected %s", ErrUnsupportedProvider, provider, strings.Join(Names(), " or "))
	}
	return spec, nil
}

func ParseTurns(provider, path string) ([]agenttrace.Turn, error) {
	spec, err := Get(provider)
	if err != nil {
		return nil, err
	}
	return spec.ParseTurns(path)
}

func DisplayName(provider string) string {
	spec, err := Get(provider)
	if err != nil {
		return Normalize(provider)
	}
	return spec.DisplayName
}

func RequiresExplicitPath(provider string) bool {
	spec, err := Get(provider)
	if err != nil {
		return false
	}
	return spec.ExplicitPathOnly
}
