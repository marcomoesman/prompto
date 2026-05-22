package agent

import (
	"github.com/marcomoesman/prompto/internal/api"
)

// Agent is the coordinator that binds a provider to a tool resolver. Run
// turns execute against this binding; RunInput carries per-turn state.
type Agent struct {
	provider        api.Provider
	model           string
	maxTokens       int
	temperature     *float64
	presencePenalty *float64
	tools           ToolResolver
	logger          *RequestLogger
	evaluator       PermissionEvaluator // optional; nil means "everything asks"
	compactor       Compactor           // optional; nil disables compaction
	registry        *AgentRegistry      // optional; nil falls back to DefaultRegistry
	gate            *ProviderGate       // optional; nil = no concurrency cap
	notifier        RemindNotifier      // optional; nil disables system-reminder injection
	todos           TodoStore           // optional; nil disables todo persistence/rendering
	localProvider   bool                // true when the bound provider points at a local LLM; opts the prompt into weak-model guidance
	modelGuidance   ModelGuidanceOptions
}

// NewAgentInput bundles the inputs to New so the signature stays small.
type NewAgentInput struct {
	Provider        api.Provider
	Model           string
	MaxTokens       int
	Temperature     *float64
	PresencePenalty *float64
	Tools           ToolResolver
	Logger          *RequestLogger      // optional; nil disables request logging
	Evaluator       PermissionEvaluator // optional; nil means ask for every tool call
	Compactor       Compactor           // optional; nil disables pre-call + reactive compaction
	Registry        *AgentRegistry      // optional; nil falls back to DefaultRegistry
	Gate            *ProviderGate       // optional; nil disables the concurrency cap
	Notifier        RemindNotifier      // optional; nil disables per-turn reminder injection
	Todos           TodoStore           // optional; nil disables todowrite persistence + render
	LocalProvider   bool                // true when the bound provider is a local LLM; the prompt builder uses this to opt-in weak-model guidance
	ModelGuidance   ModelGuidanceOptions
}

// ModelGuidanceOptions controls deterministic reliability aids. Modes mirror
// config.model_guidance: ToolCallRecovery and CompactToolSchemas are
// auto|on|off; WorkspaceHints and LoopGuards are on|off. Empty fields take
// the defaults.
type ModelGuidanceOptions struct {
	ToolCallRecovery   string
	WorkspaceHints     string
	LoopGuards         string
	CompactToolSchemas string
}

// New constructs an Agent. MaxTokens defaults to 8192 when unset.
func New(in NewAgentInput) *Agent {
	maxTokens := in.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 8192
	}
	registry := in.Registry
	if registry == nil {
		registry = DefaultRegistry()
	}
	modelGuidance := normalizeModelGuidanceOptions(in.ModelGuidance)
	tools := in.Tools
	if tools == nil {
		tools = emptyToolResolver{}
	}
	return &Agent{
		provider:        in.Provider,
		model:           in.Model,
		maxTokens:       maxTokens,
		temperature:     in.Temperature,
		presencePenalty: in.PresencePenalty,
		tools:           tools,
		logger:          in.Logger,
		evaluator:       in.Evaluator,
		compactor:       in.Compactor,
		registry:        registry,
		gate:            in.Gate,
		notifier:        in.Notifier,
		todos:           in.Todos,
		localProvider:   in.LocalProvider,
		modelGuidance:   modelGuidance,
	}
}

func normalizeModelGuidanceOptions(in ModelGuidanceOptions) ModelGuidanceOptions {
	if in.ToolCallRecovery == "" {
		in.ToolCallRecovery = "auto"
	}
	if in.WorkspaceHints == "" {
		in.WorkspaceHints = "on"
	}
	if in.LoopGuards == "" {
		in.LoopGuards = "on"
	}
	if in.CompactToolSchemas == "" {
		in.CompactToolSchemas = "auto"
	}
	return in
}

func (a *Agent) toolCallRecoveryEnabled() bool {
	switch a.modelGuidance.ToolCallRecovery {
	case "on":
		return true
	case "off":
		return false
	default:
		return a.localProvider
	}
}

func (a *Agent) workspaceHintsEnabled() bool {
	return a.modelGuidance.WorkspaceHints != "off"
}

func (a *Agent) loopGuardsEnabled() bool {
	return a.modelGuidance.LoopGuards != "off"
}

func (a *Agent) compactToolSchemasEnabled() bool {
	switch a.modelGuidance.CompactToolSchemas {
	case "on":
		return true
	case "off":
		return false
	default:
		return a.localProvider
	}
}

// Model returns the agent's model identifier.
func (a *Agent) Model() string { return a.model }

// SetModel updates the active model identifier. Used by /model to switch
// mid-session. Validation against the provider's known list is the
// caller's responsibility — Agent does not gatekeep here.
func (a *Agent) SetModel(name string) { a.model = name }

// SetProvider replaces the bound provider. Used by /model when the
// target model lives under a different provider entry: the caller
// constructs a fresh provider via provider.New and hands it in,
// followed by SetModel + SetMaxTokens to complete the swap.
func (a *Agent) SetProvider(p api.Provider) { a.provider = p }

// SetLocalProvider updates the local-provider flag. Called alongside
// SetProvider when /model swaps to a different provider entry whose
// LocalProvider classification differs. The next turn's prompt
// reflects the new value.
func (a *Agent) SetLocalProvider(local bool) { a.localProvider = local }

// LocalProvider reports whether the bound provider points at a local
// LLM. Read by the run loop to populate BuildSystemPromptInput.
func (a *Agent) LocalProvider() bool { return a.localProvider }

// SetMaxTokens updates the per-call output cap. /model uses this when
// switching to a model whose configured max_tokens differs from the
// previous model's. Zero is rejected — the cap is mandatory.
func (a *Agent) SetMaxTokens(n int) {
	if n > 0 {
		a.maxTokens = n
	}
}

func (a *Agent) SetSampling(temperature, presencePenalty *float64) {
	a.temperature = temperature
	a.presencePenalty = presencePenalty
}

// Provider exposes the underlying provider. Used by /model to query the
// supported-model list and by /context to read the operative limit
// without going through the Compactor wrapper.
func (a *Agent) Provider() api.Provider { return a.provider }

// Registry exposes the agent registry the Agent was constructed with.
// Used by main.go and the TUI to resolve definitions for Tab cycling.
func (a *Agent) Registry() *AgentRegistry { return a.registry }

// Gate exposes the provider concurrency gate. Used by the SpawnTask closure
// so children share the same semaphore as the primary.
func (a *Agent) Gate() *ProviderGate { return a.gate }

// Tools exposes the underlying ToolResolver. Used by the SpawnTask closure
// to pass the inner resolver into a filtered wrapper for the child.
func (a *Agent) Tools() ToolResolver { return a.tools }

// Notifier exposes the RemindNotifier the Agent runs each turn. The TUI
// calls QueueOneShot on it to nudge the model on agent switches and
// other events the run loop can't observe directly. Returns nil when
// the agent was constructed without a notifier.
func (a *Agent) Notifier() RemindNotifier { return a.notifier }

// Todos exposes the persistent TodoStore. Returns nil when the agent
// was constructed without one (tests / providers without persistence).
func (a *Agent) Todos() TodoStore { return a.todos }
