package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// configLayer identifies which file is being merged so merge() can
// restrict what the project layer is allowed to override. Project
// configs check into source control and may travel with untrusted
// repos, so they must not be able to redirect provider credentials
// or base URLs.
type configLayer int

const (
	layerGlobal configLayer = iota
	layerProject
)

// Config is the merged configuration from global + project files.
type Config struct {
	Providers     map[string]ProviderEntry `json:"providers"`
	Default       DefaultConfig            `json:"default"`
	Rules         RulesConfig              `json:"rules,omitzero"`
	Context       ContextConfig            `json:"context,omitzero"`
	Compact       CompactConfig            `json:"compact,omitzero"`
	Agent         AgentConfig              `json:"agent,omitzero"`
	ModelGuidance ModelGuidanceConfig      `json:"model_guidance,omitzero"`
	// Search configures the optional websearch tool. nil means "no
	// search backend configured" — the websearch tool is not
	// registered and any agent that allowlists "websearch" simply
	// won't see it. Set in JSON as `"search": {...}`.
	Search *SearchConfig `json:"search,omitempty"`
}

// SearchConfig configures the websearch tool's backend. Exactly one
// provider is active at a time. APIKey supports the "$ENV_VAR" syntax
// already used by ProviderEntry.APIKey. BaseURL is REQUIRED for
// searxng (no public default exists) and OPTIONAL for tavily/exa/
// firecrawl (each ships with a vendor default).
type SearchConfig struct {
	Provider string `json:"provider"`          // tavily | exa | firecrawl | searxng
	APIKey   string `json:"api_key,omitzero"`  // literal or "$ENV_VAR"; not used by searxng
	BaseURL  string `json:"base_url,omitzero"` // required for searxng; optional override for others
}

// ContextConfig controls how prompto interprets model context-window sizes.
// Both fields have defaults when zero; see defaults.go.
type ContextConfig struct {
	// DefaultLimit is the fallback input-context size when the provider
	// reports 0 (unknown model). In tokens. Default 200000.
	DefaultLimit int `json:"default_limit,omitzero"`
	// MaxOverride caps any provider-reported context limit. In tokens.
	// Default 400000. Prevents runaway summarization cost when pointing
	// at a 1M-context variant.
	MaxOverride int `json:"max_override,omitzero"`
}

// CompactConfig controls threshold-based summarization.
type CompactConfig struct {
	// Model overrides the summarizer model. Empty means use the session
	// model. Prefer a cheap model here when cost matters.
	Model string `json:"model,omitzero"`
	// ThresholdPct is the % of effective context at which summarization
	// triggers. Default 80.
	ThresholdPct int `json:"threshold_pct,omitzero"`
	// KeepRecentMessages preserves the last N **messages** verbatim
	// when summarizing — not turns. One user-visible turn typically
	// produces 2–4 messages (assistant text, tool_use blocks,
	// tool_result blocks), so a value of 8 buys roughly 2–4 round-
	// trips. The field used to be called KeepRecentTurns; that name
	// lied about the unit, so it was renamed. Legacy
	// `keep_recent_turns` JSON keys are still accepted for
	// backward-compat — see UnmarshalJSON below.
	KeepRecentMessages int `json:"keep_recent_messages,omitzero"`
}

// AgentConfig controls per-Run knobs the agent loop respects. Currently
// only MaxSteps — added because the default ceiling fires mid-task on
// long autonomous runs (file scan + edits + verification can easily
// exceed the original 25). LoopGuard already catches repeated-tool-call
// stalls, so this cap is now a runaway-cost ceiling rather than the
// primary "stuck loop" defence.
type AgentConfig struct {
	// MaxSteps is the per-turn ceiling on provider round-trips a
	// primary agent may issue before the run terminates with
	// ErrMaxSteps. 0 inherits the package default (100). Set lower
	// to bound cost; higher for unattended long-running runs.
	MaxSteps int `json:"max_steps,omitzero"`
}

// ModelGuidanceConfig controls deterministic reliability aids for model
// behavior. String modes keep defaults overrideable:
// tool_call_recovery/compact_tool_schemas: auto|on|off;
// workspace_hints/loop_guards: on|off.
type ModelGuidanceConfig struct {
	ToolCallRecovery   string `json:"tool_call_recovery,omitzero"`
	WorkspaceHints     string `json:"workspace_hints,omitzero"`
	LoopGuards         string `json:"loop_guards,omitzero"`
	CompactToolSchemas string `json:"compact_tool_schemas,omitzero"`
}

// UnmarshalJSON accepts both `keep_recent_messages` (canonical) and
// the legacy `keep_recent_turns` key so existing configs keep
// working after the rename. The canonical key wins when both are
// present in the same object — explicit beats implicit.
func (c *CompactConfig) UnmarshalJSON(data []byte) error {
	type alias CompactConfig
	aux := struct {
		alias
		LegacyKeepRecent int `json:"keep_recent_turns,omitzero"`
	}{}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	*c = CompactConfig(aux.alias)
	if c.KeepRecentMessages == 0 && aux.LegacyKeepRecent != 0 {
		c.KeepRecentMessages = aux.LegacyKeepRecent
	}
	return nil
}

// ProviderEntry configures a single LLM provider.
type ProviderEntry struct {
	Kind    string       `json:"kind"`              // "anthropic" or "openai"
	BaseURL string       `json:"base_url,omitzero"` // custom URL for Ollama, LM Studio, etc.
	APIKey  string       `json:"api_key,omitzero"`  // literal or "$ENV_VAR"
	Models  []ModelEntry `json:"models,omitzero"`   // available models for the picker
	// MaxParallel caps concurrent Provider.Complete calls + concurrent task
	// subagent spawns for any model under this provider. 0 means "no cap";
	// individual ModelEntry.MaxParallel values override this.
	MaxParallel int `json:"max_parallel,omitzero"`
	// LocalProvider, when true, signals that this entry points at a local
	// LLM (Ollama, LM Studio, llama.cpp, vLLM, etc.). The agent uses this
	// to opt the session into prompt sections that help weaker models —
	// most notably the anti-injection warning against textual tool calls.
	// When false (default), prompto auto-detects via BaseURL host. Set
	// explicitly when running behind a proxy or non-standard host that
	// the auto-detector misses.
	LocalProvider bool `json:"local_provider,omitzero"`
}

// DefaultConfig specifies which provider and model to use by default.
type DefaultConfig struct {
	Provider string `json:"provider"` // key into Providers map
	Model    string `json:"model"`
}

// RulesConfig specifies which rules files to load.
type RulesConfig struct {
	Files []string `json:"files,omitzero"` // e.g. ["AGENTS.md", "CLAUDE.md"]
	// RespectRobotsTxt controls whether webfetch honours `/robots.txt`
	// for the target host. Default false: a coding agent fetches docs
	// for users who already have permission, and surprise blocks on
	// `Disallow: /` would be unhelpful. Set true to opt in.
	RespectRobotsTxt bool `json:"respect_robots_txt,omitzero"`
}

// Load reads config from global (~/.config/prompto/config.json) and project
// (.prompto/config.json) files, merges them, expands env vars, and validates.
func Load() (*Config, error) {
	cfg := &Config{
		Providers: make(map[string]ProviderEntry),
	}

	// Layer 1: global config
	globalPath := GlobalConfigPath()
	if err := loadFile(globalPath, cfg, layerGlobal); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("reading global config %s: %w", globalPath, err)
	}

	// Layer 2: project config (overlays global, but restricted —
	// project layer cannot override provider api_key or base_url so
	// a hostile .prompto/config.json from a cloned repo can't
	// redirect requests).
	projectPath := ProjectConfigPath()
	if err := loadFile(projectPath, cfg, layerProject); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("reading project config %s: %w", projectPath, err)
	}

	// Expand environment variables in API keys
	expandEnvVars(cfg)

	// Apply defaults for unset tunables (context, compact).
	ApplyDefaults(cfg)

	// Validate
	if err := validate(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// globalConfigPaths returns all candidate paths for the global config file,
// in priority order. The first existing file wins.
func globalConfigPaths() []string {
	var paths []string

	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		paths = append(paths, filepath.Join(xdg, "prompto", "config.json"))
	}

	if home := os.Getenv("HOME"); home != "" {
		paths = append(paths, filepath.Join(home, ".config", "prompto", "config.json"))
	}

	if dir, err := os.UserConfigDir(); err == nil {
		paths = append(paths, filepath.Join(dir, "prompto", "config.json"))
	}

	return paths
}

// GlobalConfigPath returns the path to the global config file.
// Returns the first candidate path that exists, or the preferred default.
func GlobalConfigPath() string {
	for _, p := range globalConfigPaths() {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	// No file found — return preferred default for error messages
	if home := os.Getenv("HOME"); home != "" {
		return filepath.Join(home, ".config", "prompto", "config.json")
	}
	dir, _ := os.UserConfigDir()
	return filepath.Join(dir, "prompto", "config.json")
}

// ProjectConfigPath returns the path to the project-level config file.
func ProjectConfigPath() string {
	cwd, _ := os.Getwd()
	return filepath.Join(cwd, ".prompto", "config.json")
}

func loadFile(path string, cfg *Config, layer configLayer) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	// Parse into a temporary config to merge
	var overlay Config
	if err := json.Unmarshal(data, &overlay); err != nil {
		return fmt.Errorf("parsing JSON: %w", err)
	}

	merge(cfg, &overlay, layer)
	return nil
}

// merge applies overlay on top of base. For maps, overlay keys replace base keys.
// For scalar fields, non-zero overlay values replace base values.
//
// When layer == layerProject, provider entries get a restricted merge:
// api_key and base_url from the project layer are dropped (with a stderr
// warning) so a hostile project config cannot redirect API requests or
// steal credentials. All other fields (kind, models, max_parallel,
// local_provider) merge normally.
func merge(base, overlay *Config, layer configLayer) {
	for k, v := range overlay.Providers {
		if layer == layerProject {
			base.Providers[k] = mergeProjectProviderEntry(k, base.Providers[k], v)
			continue
		}
		base.Providers[k] = v
	}
	if overlay.Default.Provider != "" {
		base.Default.Provider = overlay.Default.Provider
	}
	if overlay.Default.Model != "" {
		base.Default.Model = overlay.Default.Model
	}
	if len(overlay.Rules.Files) > 0 {
		base.Rules.Files = overlay.Rules.Files
	}
	if overlay.Rules.RespectRobotsTxt {
		base.Rules.RespectRobotsTxt = true
	}
	if overlay.Context.DefaultLimit != 0 {
		base.Context.DefaultLimit = overlay.Context.DefaultLimit
	}
	if overlay.Context.MaxOverride != 0 {
		base.Context.MaxOverride = overlay.Context.MaxOverride
	}
	if overlay.Compact.Model != "" {
		base.Compact.Model = overlay.Compact.Model
	}
	if overlay.Compact.ThresholdPct != 0 {
		base.Compact.ThresholdPct = overlay.Compact.ThresholdPct
	}
	if overlay.Compact.KeepRecentMessages != 0 {
		base.Compact.KeepRecentMessages = overlay.Compact.KeepRecentMessages
	}
	if overlay.Agent.MaxSteps != 0 {
		base.Agent.MaxSteps = overlay.Agent.MaxSteps
	}
	mergeModelGuidance(&base.ModelGuidance, overlay.ModelGuidance)
	if overlay.Search != nil {
		// Whole-block replacement: a project config that wants to
		// override the search provider almost certainly wants its
		// own api_key/base_url too. Field-level merge would let a
		// stale global APIKey leak into a project that switched
		// providers.
		s := *overlay.Search
		base.Search = &s
	}
}

// mergeProjectProviderEntry merges an overlay provider entry from the
// project layer onto a base entry. api_key and base_url are dropped
// from the overlay (a stderr warning fires when either was set) so a
// hostile project config can't redirect API requests or swap
// credentials. Other fields merge normally — project configs may
// extend a provider with extra models, raise max_parallel, etc.
//
// The returned entry has base's api_key and base_url and overlay's
// non-zero values for every other field, with overlay's models
// REPLACING base's models when present (so a project can declare a
// narrowed allowlist).
func mergeProjectProviderEntry(name string, base, overlay ProviderEntry) ProviderEntry {
	out := base
	if overlay.APIKey != "" && overlay.APIKey != base.APIKey {
		fmt.Fprintf(os.Stderr, "warning: ignoring project config attempt to override providers[%q].api_key\n", name)
	}
	if overlay.BaseURL != "" && overlay.BaseURL != base.BaseURL {
		fmt.Fprintf(os.Stderr, "warning: ignoring project config attempt to override providers[%q].base_url\n", name)
	}
	if overlay.Kind != "" {
		out.Kind = overlay.Kind
	}
	if len(overlay.Models) > 0 {
		out.Models = overlay.Models
	}
	if overlay.MaxParallel != 0 {
		out.MaxParallel = overlay.MaxParallel
	}
	if overlay.LocalProvider {
		out.LocalProvider = true
	}
	return out
}

// expandEnvVars replaces "$VAR_NAME" values in API keys with os.Getenv("VAR_NAME").
func expandEnvVars(cfg *Config) {
	for k, entry := range cfg.Providers {
		if strings.HasPrefix(entry.APIKey, "$") {
			envVar := entry.APIKey[1:]
			entry.APIKey = os.Getenv(envVar)
			cfg.Providers[k] = entry
		}
	}
	if cfg.Search != nil && strings.HasPrefix(cfg.Search.APIKey, "$") {
		cfg.Search.APIKey = os.Getenv(cfg.Search.APIKey[1:])
	}
}

func validate(cfg *Config) error {
	if len(cfg.Providers) == 0 {
		return fmt.Errorf("no providers configured; create %s with at least one provider", GlobalConfigPath())
	}
	if cfg.Default.Provider == "" {
		return errors.New("default.provider is required")
	}
	if _, ok := cfg.Providers[cfg.Default.Provider]; !ok {
		return fmt.Errorf("default provider %q not found in providers", cfg.Default.Provider)
	}
	if cfg.Default.Model == "" {
		return errors.New("default.model is required")
	}
	// Every listed model must declare max_tokens. Validation runs
	// before any code path that resolves a per-model cap, so an
	// unset max_tokens fails fast with a clear message instead of
	// surfacing as a mid-stream truncation later.
	for pname, prov := range cfg.Providers {
		for _, m := range prov.Models {
			if m.MaxTokens <= 0 {
				return fmt.Errorf("provider %q model %q: max_tokens is required and must be > 0", pname, m.Name)
			}
			if m.Temperature != nil && (*m.Temperature < 0 || *m.Temperature > 2) {
				return fmt.Errorf("provider %q model %q: temperature must be between 0.0 and 2.0", pname, m.Name)
			}
			if m.PresencePenalty != nil && (*m.PresencePenalty < -2 || *m.PresencePenalty > 2) {
				return fmt.Errorf("provider %q model %q: presence_penalty must be between -2.0 and 2.0", pname, m.Name)
			}
			if m.ContextLimit < 0 {
				return fmt.Errorf("provider %q model %q: context_limit must be >= 0 (got %d); omit to inherit the provider/default value", pname, m.Name, m.ContextLimit)
			}
		}
	}
	// The default model must be listed under its provider so its
	// max_tokens is resolvable. Empty models list is allowed for
	// providers that aren't carrying the default model — the picker
	// just has nothing to offer there.
	defProv := cfg.Providers[cfg.Default.Provider]
	defaultModelListed := false
	for _, m := range defProv.Models {
		if m.Name == cfg.Default.Model {
			defaultModelListed = true
			break
		}
	}
	if !defaultModelListed {
		return fmt.Errorf("default model %q not found under providers[%q].models — list it as an object with a max_tokens value", cfg.Default.Model, cfg.Default.Provider)
	}
	if cfg.Agent.MaxSteps < 0 {
		return fmt.Errorf("agent.max_steps must be >= 0 (got %d); use 0 to inherit the default", cfg.Agent.MaxSteps)
	}
	if err := validateBaseURLs(cfg); err != nil {
		return err
	}
	if err := validateSearch(cfg.Search); err != nil {
		return err
	}
	if err := validateModelGuidance(cfg.ModelGuidance, "model_guidance"); err != nil {
		return err
	}
	return nil
}

// validateBaseURLs parses every non-empty provider BaseURL (and the
// optional search BaseURL) and rejects anything that isn't a
// well-formed http/https URL with a host. Without this a typo or
// malicious project config that slipped past the project-layer
// restriction would surface mid-stream as a confusing HTTP error
// instead of failing at startup.
func validateBaseURLs(cfg *Config) error {
	for pname, prov := range cfg.Providers {
		if prov.BaseURL == "" {
			continue
		}
		if err := checkBaseURL(prov.BaseURL); err != nil {
			return fmt.Errorf("provider %q: %w", pname, err)
		}
	}
	if cfg.Search != nil && cfg.Search.BaseURL != "" {
		if err := checkBaseURL(cfg.Search.BaseURL); err != nil {
			return fmt.Errorf("search: %w", err)
		}
	}
	return nil
}

func checkBaseURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid base_url %q: %w", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("invalid base_url %q: scheme must be http or https", raw)
	}
	if u.Host == "" {
		return fmt.Errorf("invalid base_url %q: missing host", raw)
	}
	return nil
}

// validateSearch enforces that the optional search block is internally
// consistent before any provider constructor sees it. The websearch
// tool is the only consumer; failing fast here produces a clear
// startup error instead of a mid-conversation tool failure.
func validateSearch(s *SearchConfig) error {
	if s == nil {
		return nil
	}
	if s.Provider == "" {
		return errors.New("search.provider is required when search is configured")
	}
	switch s.Provider {
	case "searxng":
		if s.BaseURL == "" {
			return errors.New("search.base_url is required for searxng (point at your self-hosted instance)")
		}
	case "tavily", "exa", "firecrawl":
		if s.APIKey == "" {
			return fmt.Errorf("search.api_key is required for %s", s.Provider)
		}
	default:
		return fmt.Errorf("unknown search provider %q (supported: tavily, exa, firecrawl, searxng)", s.Provider)
	}
	return nil
}

func mergeModelGuidance(base *ModelGuidanceConfig, overlay ModelGuidanceConfig) {
	if overlay.ToolCallRecovery != "" {
		base.ToolCallRecovery = overlay.ToolCallRecovery
	}
	if overlay.WorkspaceHints != "" {
		base.WorkspaceHints = overlay.WorkspaceHints
	}
	if overlay.LoopGuards != "" {
		base.LoopGuards = overlay.LoopGuards
	}
	if overlay.CompactToolSchemas != "" {
		base.CompactToolSchemas = overlay.CompactToolSchemas
	}
}

func validateModelGuidance(s ModelGuidanceConfig, name string) error {
	switch s.ToolCallRecovery {
	case "", "auto", "on", "off":
	default:
		return fmt.Errorf("%s.tool_call_recovery must be auto, on, or off (got %q)", name, s.ToolCallRecovery)
	}
	switch s.WorkspaceHints {
	case "", "on", "off":
	default:
		return fmt.Errorf("%s.workspace_hints must be on or off (got %q)", name, s.WorkspaceHints)
	}
	switch s.LoopGuards {
	case "", "on", "off":
	default:
		return fmt.Errorf("%s.loop_guards must be on or off (got %q)", name, s.LoopGuards)
	}
	switch s.CompactToolSchemas {
	case "", "auto", "on", "off":
	default:
		return fmt.Errorf("%s.compact_tool_schemas must be auto, on, or off (got %q)", name, s.CompactToolSchemas)
	}
	return nil
}
