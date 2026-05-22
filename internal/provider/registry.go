package provider

import (
	"fmt"

	"github.com/marcomoesman/prompto/internal/api"
	"github.com/marcomoesman/prompto/internal/provider/anthropic"
	"github.com/marcomoesman/prompto/internal/provider/openai"
)

// New creates a provider from the given config, dispatching on Kind.
func New(cfg api.ProviderConfig) (api.Provider, error) {
	switch cfg.Kind {
	case "anthropic":
		return anthropic.New(cfg), nil
	case "openai":
		return openai.New(cfg), nil
	default:
		return nil, fmt.Errorf("unknown provider kind: %q (supported: anthropic, openai)", cfg.Kind)
	}
}
