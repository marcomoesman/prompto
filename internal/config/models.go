package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

// UnboundedParallel is the effective concurrency cap when neither the model
// nor the provider declares one. Cloud providers (Anthropic, OpenAI) typically
// allow many concurrent requests; this constant lets us treat "unset" as
// "effectively unlimited" without sprinkling magic numbers through the code.
const UnboundedParallel = 128

// ModelEntry describes a single model entry under a provider. Object
// form is required because max_tokens is mandatory:
//
//	"models": [
//	  { "name": "gpt-4o", "max_tokens": 8192 },
//	  { "name": "qwen-coder:30b", "max_tokens": 16384, "max_parallel": 1 }
//	]
//
// The string-shorthand form (`"models": ["gpt-4o"]`) is still parsed
// for legibility in test fixtures and old configs, but a string entry
// has MaxTokens=0 and will fail validation in Load(). New configs
// should always use the object form.
type ModelEntry struct {
	Name string `json:"name"`
	// MaxParallel overrides the provider-level MaxParallel for this model.
	// 0 means "inherit from provider".
	MaxParallel int `json:"max_parallel,omitzero"`
	// MaxTokens is the output-token cap used when this model is the
	// active model. Required (validated > 0 in Load()). The webfetch
	// summarizer reads this to size its LLM calls; reasoning models
	// that emit <think> tokens before the answer need 8192+ to avoid
	// mid-response truncation.
	MaxTokens int `json:"max_tokens"`
	// ContextLimit is the input-context window in tokens. Optional.
	// When > 0, overrides the provider's reported context size for
	// this model — useful for OpenAI-compatible / local servers whose
	// `/v1/models` shim doesn't expose a context size, and for cloud
	// variants (e.g. the 1M-context Anthropic beta) that need a
	// different ceiling than the family default. 0 inherits the
	// provider's value (or context.default_limit when the provider
	// also reports 0). The resolved value is still capped at
	// context.max_override.
	ContextLimit int `json:"context_limit,omitzero"`
	// Temperature controls sampling randomness for providers that support it.
	// nil means "omit from the API request and use the server default".
	Temperature *float64 `json:"temperature,omitzero"`
	// PresencePenalty discourages repeating already-mentioned topics for
	// OpenAI-compatible providers. nil means "omit from the API request and use
	// the server default"; Anthropic ignores this field.
	PresencePenalty *float64 `json:"presence_penalty,omitzero"`
}

type ModelSampling struct {
	Temperature               float64
	PresencePenalty           float64
	TemperatureConfigured     bool
	PresencePenaltyConfigured bool
}

const (
	DefaultTemperature     = 1.0
	DefaultPresencePenalty = 0.0
)

// UnmarshalJSON accepts either a JSON string (legacy form) or a JSON object
// with name + optional max_parallel. Empty strings are rejected.
func (m *ModelEntry) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return fmt.Errorf("empty model entry")
	}
	if trimmed[0] == '"' {
		var name string
		if err := json.Unmarshal(trimmed, &name); err != nil {
			return fmt.Errorf("model entry string: %w", err)
		}
		m.Name = name
		m.MaxParallel = 0
		return nil
	}
	// Object form. Use a sibling type to avoid recursive UnmarshalJSON.
	type alias ModelEntry
	var a alias
	if err := json.Unmarshal(trimmed, &a); err != nil {
		return fmt.Errorf("model entry object: %w", err)
	}
	*m = ModelEntry(a)
	return nil
}

func ResolveModelSampling(cfg *Config, providerName, modelName string) ModelSampling {
	out := ModelSampling{
		Temperature:     DefaultTemperature,
		PresencePenalty: DefaultPresencePenalty,
	}
	if cfg == nil {
		return out
	}
	prov, ok := cfg.Providers[providerName]
	if !ok {
		return out
	}
	for _, m := range prov.Models {
		if m.Name != modelName {
			continue
		}
		if m.Temperature != nil {
			out.Temperature = *m.Temperature
			out.TemperatureConfigured = true
		}
		if m.PresencePenalty != nil {
			out.PresencePenalty = *m.PresencePenalty
			out.PresencePenaltyConfigured = true
		}
		return out
	}
	return out
}

func (s ModelSampling) TemperaturePtr() *float64 {
	if !s.TemperatureConfigured {
		return nil
	}
	v := s.Temperature
	return &v
}

func (s ModelSampling) PresencePenaltyPtr() *float64 {
	if !s.PresencePenaltyConfigured {
		return nil
	}
	v := s.PresencePenalty
	return &v
}

// ResolveModelContextLimit returns the configured ContextLimit for the
// named model — walking every provider's models list. Used by the
// compactor to override a provider's reported context size when the
// user has declared one in config (which is the only sensible source
// for OpenAI-compatible / local servers).
//
// Returns 0 when no model matches or the matching entry has no
// ContextLimit set; the caller should treat 0 as "no override" and
// fall back to provider.ContextLimit + cfg.Context.DefaultLimit.
//
// Provider keys are walked alphabetically so the result is
// deterministic when the same model id appears under more than one
// provider — the alphabetically-first provider wins. Mirrors FindModel.
func ResolveModelContextLimit(cfg *Config, modelName string) int {
	if cfg == nil || modelName == "" {
		return 0
	}
	pnames := make([]string, 0, len(cfg.Providers))
	for k := range cfg.Providers {
		pnames = append(pnames, k)
	}
	sort.Strings(pnames)
	for _, pname := range pnames {
		for _, m := range cfg.Providers[pname].Models {
			if m.Name == modelName && m.ContextLimit > 0 {
				return m.ContextLimit
			}
		}
	}
	return 0
}

// ResolveMaxTokens returns the configured output-token cap for
// (providerName, modelName). Returns 0 when the model is not listed
// under the provider — the caller should treat that as a configuration
// error, since max_tokens is required by validation in Load(). A nil
// config also returns 0.
func ResolveMaxTokens(cfg *Config, providerName, modelName string) int {
	if cfg == nil {
		return 0
	}
	prov, ok := cfg.Providers[providerName]
	if !ok {
		return 0
	}
	for _, m := range prov.Models {
		if m.Name == modelName {
			return m.MaxTokens
		}
	}
	return 0
}

// FindModel locates the (provider key, ModelEntry) pair for the named model.
// Provider keys are walked alphabetically so the result is deterministic
// when the same model id is listed under more than one provider — the
// alphabetically-first provider wins. Used on --resume to map a stored
// session model back to its provider entry; an unknown model returns
// ok=false and the caller falls back to the default model.
func FindModel(cfg *Config, name string) (string, ModelEntry, bool) {
	if cfg == nil || name == "" {
		return "", ModelEntry{}, false
	}
	pnames := make([]string, 0, len(cfg.Providers))
	for k := range cfg.Providers {
		pnames = append(pnames, k)
	}
	sort.Strings(pnames)
	for _, pname := range pnames {
		for _, m := range cfg.Providers[pname].Models {
			if m.Name == name {
				return pname, m, true
			}
		}
	}
	return "", ModelEntry{}, false
}

// ResolveModelLimits returns the concurrency cap for (providerName, modelName).
//
// Resolution order:
//  1. ModelEntry.MaxParallel > 0 → use it.
//  2. ProviderEntry.MaxParallel > 0 → use it.
//  3. UnboundedParallel.
//
// Unknown provider or model falls through to UnboundedParallel — the caller
// is expected to have validated the model elsewhere; this function is only
// concerned with concurrency caps.
func ResolveModelLimits(cfg *Config, providerName, modelName string) int {
	if cfg == nil {
		return UnboundedParallel
	}
	prov, ok := cfg.Providers[providerName]
	if !ok {
		return UnboundedParallel
	}
	for _, m := range prov.Models {
		if m.Name == modelName && m.MaxParallel > 0 {
			return m.MaxParallel
		}
	}
	if prov.MaxParallel > 0 {
		return prov.MaxParallel
	}
	return UnboundedParallel
}
