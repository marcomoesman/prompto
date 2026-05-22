package config

// Defaults applied when the merged config leaves a field at its zero value.
// These live as package constants so compaction tunables stay
// visible in one place.
const (
	DefaultContextLimit       = 200_000
	DefaultMaxOverride        = 400_000
	DefaultThresholdPct       = 80
	DefaultKeepRecentMessages = 8
	// DefaultMaxSteps caps a single primary-agent run at 100 provider
	// round-trips before returning ErrMaxSteps. Previous value of 25
	// fired during legitimate autonomous work (file scan + edits +
	// verification). Configurable via agent.max_steps in config.json.
	DefaultMaxSteps           = 100
	DefaultToolCallRecovery   = "auto"
	DefaultWorkspaceHints     = "on"
	DefaultLoopGuards         = "on"
	DefaultCompactToolSchemas = "auto"
)

// Default returns a config with sensible defaults but no providers.
// The user must configure at least one provider.
func Default() Config {
	return Config{
		Providers: make(map[string]ProviderEntry),
		Default: DefaultConfig{
			Provider: "anthropic",
			Model:    "claude-sonnet-4-20250514",
		},
		Rules: RulesConfig{
			Files: []string{"AGENTS.md"},
		},
		Context: ContextConfig{
			DefaultLimit: DefaultContextLimit,
			MaxOverride:  DefaultMaxOverride,
		},
		Compact: CompactConfig{
			ThresholdPct:       DefaultThresholdPct,
			KeepRecentMessages: DefaultKeepRecentMessages,
		},
		Agent: AgentConfig{
			MaxSteps: DefaultMaxSteps,
		},
		ModelGuidance: ModelGuidanceConfig{
			ToolCallRecovery:   DefaultToolCallRecovery,
			WorkspaceHints:     DefaultWorkspaceHints,
			LoopGuards:         DefaultLoopGuards,
			CompactToolSchemas: DefaultCompactToolSchemas,
		},
	}
}

// ApplyDefaults fills zero-valued compaction fields with the package
// defaults. Called by Load after merging so users don't have to set every
// field explicitly.
func ApplyDefaults(cfg *Config) {
	if cfg.Context.DefaultLimit == 0 {
		cfg.Context.DefaultLimit = DefaultContextLimit
	}
	if cfg.Context.MaxOverride == 0 {
		cfg.Context.MaxOverride = DefaultMaxOverride
	}
	if cfg.Compact.ThresholdPct == 0 {
		cfg.Compact.ThresholdPct = DefaultThresholdPct
	}
	if cfg.Compact.KeepRecentMessages == 0 {
		cfg.Compact.KeepRecentMessages = DefaultKeepRecentMessages
	}
	if cfg.Agent.MaxSteps == 0 {
		cfg.Agent.MaxSteps = DefaultMaxSteps
	}
	if cfg.ModelGuidance.ToolCallRecovery == "" {
		cfg.ModelGuidance.ToolCallRecovery = DefaultToolCallRecovery
	}
	if cfg.ModelGuidance.WorkspaceHints == "" {
		cfg.ModelGuidance.WorkspaceHints = DefaultWorkspaceHints
	}
	if cfg.ModelGuidance.LoopGuards == "" {
		cfg.ModelGuidance.LoopGuards = DefaultLoopGuards
	}
	if cfg.ModelGuidance.CompactToolSchemas == "" {
		cfg.ModelGuidance.CompactToolSchemas = DefaultCompactToolSchemas
	}
}
