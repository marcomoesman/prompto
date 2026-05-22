package command

import (
	"context"

	"github.com/marcomoesman/prompto/internal/agent"
	"github.com/marcomoesman/prompto/internal/api"
	"github.com/marcomoesman/prompto/internal/compact"
	"github.com/marcomoesman/prompto/internal/permission"
	"github.com/marcomoesman/prompto/internal/store"
)

// Env is the host surface commands operate against. The TUI implements it
// with adapters that capture pointers to live AppModel state. Commands
// never import internal/tui — the dependency direction is one-way.
//
// The interface intentionally exposes concrete subsystem pointers
// (Store, Compactor, Evaluator) because commands already depend on those
// packages for types; bridging through more interfaces buys no isolation.
type Env interface {
	// AppendSystemMessage renders a one-shot system message in the chat
	// view.
	AppendSystemMessage(text string)

	// Session lifecycle. Each method drives the same path the host uses
	// at startup or on adopt — declarative requests, not raw DB writes.
	EndCurrentSession(ctx context.Context) error
	StartNewSession(ctx context.Context) error
	AdoptSession(ctx context.Context, sessionID string) error

	// Live host state.
	SessionID() string
	AgentName() string
	Model() string
	Cwd() string
	Version() string

	// Conversation returns the live conversation. Commands inspect it for
	// token estimates (/context) or feed it to the compactor (/compact).
	Conversation() *agent.Conversation

	// SystemPromptText returns the rendered system prompt the agent will
	// send on the next turn. Used by /context to estimate the prompt's
	// contribution to the limit.
	SystemPromptText() string
	// AGENTSMdText returns the AGENTS.md content (passed as ExtraSystem
	// in RunInput). Used by /context.
	AGENTSMdText() string

	// Subsystem accessors.
	Store() *store.Store
	Compactor() *compact.Compactor
	Evaluator() *permission.Evaluator
	Agent() *agent.Agent
	Registry() *agent.AgentRegistry
	Notifier() agent.RemindNotifier
	ToolDefinitions() []api.ToolDefinition

	// SwitchAgent sets the active primary. The TUI mirrors the existing
	// Tab-cycle wiring (status bar, plan rules, BUILD_SWITCH reminder).
	SwitchAgent(name string) error

	// SetModel changes the active model on the live agent. The host
	// validates the name against the configured providers and, when
	// the target model lives under a different provider entry,
	// constructs a fresh provider for the swap. Returns a descriptive
	// error when the name isn't configured.
	SetModel(name string) error

	// Models returns the flat list of every model configured across
	// every provider. Used by /model to render an available-models
	// list with the current model marked. Empty when the host has no
	// loaded config (e.g. tests that construct AppModel directly).
	Models() []ModelInfo
}

// ModelInfo describes one configured model entry, flattened from the
// provider/models nesting. /model renders these one-per-line, marking
// the active model. The struct lives in the command package — not
// internal/config — so commands stay free of the config import and
// the host can populate it from any source (config file, env, etc.).
type ModelInfo struct {
	Name                      string  // model identifier as the provider expects it
	Provider                  string  // provider key (e.g. "llamacpp", "openrouter")
	ProviderKind              string  // provider API kind (e.g. "openai", "anthropic")
	MaxTokens                 int     // configured output cap
	Temperature               float64 // displayed/effective temperature
	PresencePenalty           float64 // displayed/effective presence_penalty
	TemperatureConfigured     bool    // true when explicitly configured
	PresencePenaltyConfigured bool    // true when explicitly configured
}
