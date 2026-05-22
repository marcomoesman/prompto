package tui

import (
	"context"
	"fmt"
	"sort"

	tea "charm.land/bubbletea/v2"

	"github.com/marcomoesman/prompto/internal/agent"
	"github.com/marcomoesman/prompto/internal/api"
	"github.com/marcomoesman/prompto/internal/command"
	"github.com/marcomoesman/prompto/internal/compact"
	"github.com/marcomoesman/prompto/internal/config"
	"github.com/marcomoesman/prompto/internal/permission"
	"github.com/marcomoesman/prompto/internal/provider"
	"github.com/marcomoesman/prompto/internal/store"
)

// appEnv is the TUI's command.Env adapter. It captures a pointer into the
// live AppModel so commands can mutate session state, swap the agent, etc.
// The pointer is only safe to use while Update is on the stack — every
// dispatched command runs synchronously inside one Update call.
type appEnv struct {
	m *AppModel
}

// newAppEnv wraps m for command dispatch.
func newAppEnv(m *AppModel) *appEnv { return &appEnv{m: m} }

// AppendSystemMessage adds a one-shot system message to the chat view.
func (e *appEnv) AppendSystemMessage(text string) {
	e.m.chat.AppendSystemMessage(text)
}

// EndCurrentSession marks the active session ended in the store. No-op when
// persistence is disabled or no session is active.
func (e *appEnv) EndCurrentSession(ctx context.Context) error {
	if e.m.store == nil || e.m.sessionID == "" {
		return nil
	}
	return e.m.store.SetSessionStatus(ctx, e.m.sessionID, "ended")
}

// StartNewSession creates a fresh session row, resets the conversation, and
// retargets the file-change sink at the new id. The chat viewport is
// cleared so the new session starts with an empty transcript.
func (e *appEnv) StartNewSession(ctx context.Context) error {
	if e.m.store == nil {
		// Headless mode: keep an in-memory conversation reset.
		e.m.conv = agent.NewConversation()
		e.m.chat = NewChatModel()
		e.m.chat.SetSize(e.m.width, e.m.height)
		e.m.hasUserMsg = false
		e.m.relayout()
		return nil
	}
	sess, err := e.m.store.CreateSession(ctx, store.CreateSessionInput{
		Model:     e.m.agent.Model(),
		AgentName: e.m.agentName,
	})
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	e.m.sessionID = sess.ID
	if scoped, ok := e.m.fileChanges.(agent.SessionScopedSink); ok {
		scoped.SetSessionID(sess.ID)
	}
	e.m.conv = agent.NewConversation()
	e.m.chat = NewChatModel()
	e.m.chat.SetSize(e.m.width, e.m.height)
	e.m.hasUserMsg = false
	prefix := sess.ID
	if len(prefix) > 8 {
		prefix = prefix[:8]
	}
	e.m.status.sessionPrefix = prefix
	e.m.status.inputTokens = 0
	e.m.status.outputTokens = 0
	e.m.status.contextTokens = 0
	e.m.todos = nil
	e.m.todoPanel.SetTodos(nil)
	e.m.relayout()
	return nil
}

// AdoptSession swaps the active session to the given id, loading prior
// messages from the store and seeding the chat view. Used by /resume.
func (e *appEnv) AdoptSession(ctx context.Context, sessionID string) error {
	if e.m.store == nil {
		return fmt.Errorf("persistence disabled")
	}
	sess, err := e.m.store.GetSession(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("load session: %w", err)
	}
	prior, err := e.m.store.LoadMessages(ctx, sess.ID)
	if err != nil {
		return fmt.Errorf("load messages: %w", err)
	}

	e.m.sessionID = sess.ID
	if scoped, ok := e.m.fileChanges.(agent.SessionScopedSink); ok {
		scoped.SetSessionID(sess.ID)
	}
	e.m.conv = agent.NewConversation()
	e.m.chat = NewChatModel()
	e.m.chat.SetSize(e.m.width, e.m.height)
	hasUser := false
	for _, msg := range prior {
		e.m.conv.Append(msg)
		seedChatWithMessage(&e.m.chat, msg)
		if msg.Role == api.RoleUser {
			hasUser = true
		}
	}
	e.m.hasUserMsg = hasUser
	prefix := sess.ID
	if len(prefix) > 8 {
		prefix = prefix[:8]
	}
	e.m.status.sessionPrefix = prefix
	if reg := e.m.agent.Registry(); reg != nil {
		if def, ok := reg.Resolve(sess.AgentName); ok {
			e.m.agentName = sess.AgentName
			e.m.previousAgent = sess.AgentName
			e.m.status.agent = def.Name
			e.m.status.agentColor = def.Color
			e.m.welcome.Agent = def.Name
		}
	}
	// Restore the session's stored model. SetModel handles the
	// provider swap when the model lives under a different provider
	// entry. An unknown stored model falls back to the configured
	// default, mirroring the --resume CLI path; the fallback is
	// persisted so the warning doesn't repeat.
	if sess.Model != "" && e.m.cfg != nil {
		if _, _, ok := config.FindModel(e.m.cfg, sess.Model); ok {
			if err := e.SetModel(sess.Model); err != nil {
				e.m.chat.AppendSystemMessage("warning: restoring session model: " + err.Error())
			}
		} else {
			e.m.chat.AppendSystemMessage(fmt.Sprintf("warning: session model %q is no longer in config; using %q", sess.Model, e.m.cfg.Default.Model))
			_ = e.m.store.SetModel(ctx, sess.ID, e.m.cfg.Default.Model)
			if err := e.SetModel(e.m.cfg.Default.Model); err != nil {
				e.m.chat.AppendSystemMessage("warning: applying fallback model: " + err.Error())
			}
		}
	}
	// Reseed the panel's todo cache from the resumed session.
	e.m.todos = nil
	if todoStore := e.m.agent.Todos(); todoStore != nil {
		if loaded, err := todoStore.LoadTodos(ctx, sess.ID); err == nil {
			e.m.todos = loaded
		}
	}
	e.m.todoPanel.SetTodos(e.m.todos)
	e.m.relayout()
	return nil
}

// SessionID returns the active session id.
func (e *appEnv) SessionID() string { return e.m.sessionID }

// AgentName returns the active primary agent name.
func (e *appEnv) AgentName() string { return e.m.agentName }

// Model returns the active model identifier.
func (e *appEnv) Model() string { return e.m.agent.Model() }

// Cwd returns the working directory captured at startup.
func (e *appEnv) Cwd() string { return e.m.cwd }

// Version returns the prompto version.
func (e *appEnv) Version() string { return e.m.version }

// Conversation exposes the live conversation pointer.
func (e *appEnv) Conversation() *agent.Conversation { return e.m.conv }

// SystemPromptText returns the rendered system prompt for the active
// agent. Used by /context to estimate the prompt's token weight.
func (e *appEnv) SystemPromptText() string {
	in := agent.BuildSystemPromptInput{
		Cwd:                 e.m.cwd,
		Model:               e.m.agent.Model(),
		ProjectInstructions: e.m.extra,
		LocalProvider:       e.m.agent.LocalProvider(),
	}
	// Tools allowlist resolves through the active agent's definition.
	if reg := e.m.agent.Registry(); reg != nil {
		if def, ok := reg.Resolve(e.m.agentName); ok {
			in.Tools = def.EffectiveTools()
		}
	}
	if store := e.m.agent.Todos(); store != nil && e.m.sessionID != "" {
		if loaded, err := store.LoadTodos(context.Background(), e.m.sessionID); err == nil {
			in.Todos = loaded
		}
	}
	var blocks []api.SystemBlock
	if e.m.agentName == "plan" {
		blocks = agent.PlanSystemPrompt(in)
	} else {
		blocks = agent.BuildSystemPrompt(in)
	}
	out := ""
	for _, b := range blocks {
		out += b.Text
	}
	return out
}

// AGENTSMdText returns the AGENTS.md content fed as ExtraSystem each turn.
func (e *appEnv) AGENTSMdText() string { return e.m.extra }

// Store returns the persistence handle. Nil when the TUI runs ephemerally.
func (e *appEnv) Store() *store.Store { return e.m.store }

// Compactor returns the compactor. Nil when compaction is disabled.
func (e *appEnv) Compactor() *compact.Compactor { return e.m.compactor }

// Evaluator returns the permission evaluator. Nil when permission was not
// configured.
func (e *appEnv) Evaluator() *permission.Evaluator { return e.m.evaluator }

// Agent returns the underlying agent.
func (e *appEnv) Agent() *agent.Agent { return e.m.agent }

// Registry returns the agent registry.
func (e *appEnv) Registry() *agent.AgentRegistry { return e.m.agent.Registry() }

// Notifier returns the reminder notifier the run loop consumes each turn.
func (e *appEnv) Notifier() agent.RemindNotifier { return e.m.agent.Notifier() }

// ToolDefinitions is unimplemented for now — /context (Task #3) will need
// the live []api.ToolDefinition the agent sends each turn. Returning nil
// keeps the interface satisfied without claiming functionality we don't
// yet have.
func (e *appEnv) ToolDefinitions() []api.ToolDefinition { return nil }

// SwitchAgent sets the active primary by name. Returns an error when the
// name doesn't resolve in the registry. Mirrors the per-cycle wiring done
// by Tab (status bar, plan rules, BUILD_SWITCH reminder).
func (e *appEnv) SwitchAgent(name string) error {
	reg := e.m.agent.Registry()
	if reg == nil {
		return fmt.Errorf("registry unavailable")
	}
	def, ok := reg.Resolve(name)
	if !ok {
		return fmt.Errorf("unknown agent %q", name)
	}
	if def.Name == e.m.agentName {
		return nil
	}
	e.m.previousAgent = e.m.agentName
	e.m.agentName = def.Name
	e.m.status.agent = def.Name
	e.m.status.agentColor = def.Color
	e.m.welcome.Agent = def.Name
	if e.m.evaluator != nil {
		if def.PlanMode {
			// Plan rules glob `.prompto/plans/*.md`,
			// so they only need cwd — the model picks the slug.
			e.m.evaluator.SetAgentRules(permission.PlanRules(e.m.cwd))
			// Bash fast-path mirrors the Tab path in app.go.
			e.m.evaluator.SetBashClassifier(permission.ClassifyBash)
		} else {
			e.m.evaluator.SetAgentRules(nil)
			e.m.evaluator.SetBashClassifier(nil)
		}
	}
	e.m.chat.AppendSystemMessage("agent: " + def.Name)
	return nil
}

// SetModel applies a new model identifier to the live agent. When the
// target model lives under a different provider entry in config, a
// fresh provider is constructed and swapped in (along with its
// max_tokens). When config is unavailable (e.g. tests that construct
// AppModel directly without one), behaviour falls back to the legacy
// "rename only, leave provider in place" path.
//
// The status bar and welcome banner pick up the change on next paint.
func (e *appEnv) SetModel(name string) error {
	if name == "" {
		return fmt.Errorf("model name is required")
	}

	// Config-aware path: validate against the configured list and
	// swap the provider if the new model lives elsewhere.
	if e.m.cfg != nil {
		match, ok := lookupModel(e.m.cfg, name)
		if !ok {
			return fmt.Errorf("unknown model %q — run /model with no args to see the configured list", name)
		}
		if match.Provider != e.m.currentProvider() {
			entry := e.m.cfg.Providers[match.Provider]
			newProv, err := provider.New(api.ProviderConfig{
				Kind:    entry.Kind,
				BaseURL: entry.BaseURL,
				APIKey:  entry.APIKey,
				Model:   name,
			})
			if err != nil {
				return fmt.Errorf("constructing provider %q: %w", match.Provider, err)
			}
			e.m.agent.SetProvider(newProv)
			e.m.agent.SetLocalProvider(agent.LooksLikeLocalProvider(entry))
			e.m.currentProviderName = match.Provider
		}
		e.m.agent.SetMaxTokens(match.MaxTokens)
		e.m.agent.SetSampling(modelInfoTemperaturePtr(match), modelInfoPresencePenaltyPtr(match))
		if e.m.healthCheck != nil {
			entry := e.m.cfg.Providers[match.Provider]
			if msg := e.m.healthCheck(context.Background(), match.Provider, entry, name); msg != "" {
				e.m.chat.AppendSystemMessage(msg)
			}
		}
	}

	e.m.agent.SetModel(name)
	e.m.status.modelName = name
	e.m.welcome.Model = name
	if e.m.compactor != nil {
		e.m.status.contextLimit = e.m.compactor.ContextLimit(name)
	}
	// Persist the choice so --resume picks up the same model next time.
	// Best-effort: a failed write is reported but doesn't abort the swap
	// (the in-memory agent is already pointing at the new model).
	if e.m.store != nil && e.m.sessionID != "" {
		if err := e.m.store.SetModel(context.Background(), e.m.sessionID, name); err != nil {
			e.m.chat.AppendSystemMessage("warning: persisting model: " + err.Error())
		}
	}
	return nil
}

func (e *appEnv) SetModelSampling(model command.ModelInfo) error {
	if e.m.cfg == nil {
		return fmt.Errorf("config unavailable")
	}
	sampling := config.ModelSampling{
		Temperature:               model.Temperature,
		PresencePenalty:           model.PresencePenalty,
		TemperatureConfigured:     model.TemperatureConfigured,
		PresencePenaltyConfigured: model.PresencePenaltyConfigured,
	}
	if err := config.SetModelSampling(e.m.cfg, model.Provider, model.Name, sampling); err != nil {
		return err
	}
	refreshModelSampling(e.m, model.Name, sampling)
	return nil
}

func (e *appEnv) ResetModelSampling(model command.ModelInfo) error {
	if e.m.cfg == nil {
		return fmt.Errorf("config unavailable")
	}
	if err := config.ResetModelSampling(e.m.cfg, model.Provider, model.Name); err != nil {
		return err
	}
	refreshModelSampling(e.m, model.Name, config.ModelSampling{
		Temperature:     config.DefaultTemperature,
		PresencePenalty: config.DefaultPresencePenalty,
	})
	return nil
}

// Models returns the flat list of configured models across all
// providers, sorted by (provider, name) for stable rendering.
// Returns nil when the host has no loaded config.
func (e *appEnv) Models() []command.ModelInfo {
	if e.m.cfg == nil {
		return nil
	}
	var out []command.ModelInfo
	for pname, prov := range e.m.cfg.Providers {
		for _, m := range prov.Models {
			sampling := config.ResolveModelSampling(e.m.cfg, pname, m.Name)
			out = append(out, command.ModelInfo{
				Name:                      m.Name,
				Provider:                  pname,
				ProviderKind:              prov.Kind,
				MaxTokens:                 m.MaxTokens,
				Temperature:               sampling.Temperature,
				PresencePenalty:           sampling.PresencePenalty,
				TemperatureConfigured:     sampling.TemperatureConfigured,
				PresencePenaltyConfigured: sampling.PresencePenaltyConfigured,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Provider != out[j].Provider {
			return out[i].Provider < out[j].Provider
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// lookupModel finds the first configured ModelInfo whose name matches
// exactly. Provider names are tried in alphabetical order so the
// match is deterministic when the same model id is listed under two
// providers (rare; the user gets the alphabetically-first one and
// can re-list providers to disambiguate).
func lookupModel(cfg *config.Config, name string) (command.ModelInfo, bool) {
	pnames := make([]string, 0, len(cfg.Providers))
	for k := range cfg.Providers {
		pnames = append(pnames, k)
	}
	sort.Strings(pnames)
	for _, pname := range pnames {
		prov := cfg.Providers[pname]
		for _, m := range prov.Models {
			if m.Name == name {
				sampling := config.ResolveModelSampling(cfg, pname, m.Name)
				return command.ModelInfo{
					Name:                      m.Name,
					Provider:                  pname,
					ProviderKind:              prov.Kind,
					MaxTokens:                 m.MaxTokens,
					Temperature:               sampling.Temperature,
					PresencePenalty:           sampling.PresencePenalty,
					TemperatureConfigured:     sampling.TemperatureConfigured,
					PresencePenaltyConfigured: sampling.PresencePenaltyConfigured,
				}, true
			}
		}
	}
	return command.ModelInfo{}, false
}

func refreshModelSampling(m *AppModel, modelName string, sampling config.ModelSampling) {
	if modelName != m.agent.Model() {
		return
	}
	m.agent.SetSampling(sampling.TemperaturePtr(), sampling.PresencePenaltyPtr())
}

func modelInfoTemperaturePtr(mi command.ModelInfo) *float64 {
	if !mi.TemperatureConfigured {
		return nil
	}
	v := mi.Temperature
	return &v
}

func modelInfoPresencePenaltyPtr(mi command.ModelInfo) *float64 {
	if !mi.PresencePenaltyConfigured {
		return nil
	}
	v := mi.PresencePenalty
	return &v
}

// dispatchSlash parses text as a slash command and runs it synchronously.
// Returns any tea.Cmds the result implies (e.g. tea.Quit). On unknown
// command it shows an inline help hint and returns nothing.
func (m *AppModel) dispatchSlash(text string) []tea.Cmd {
	name, args := command.Parse(text)
	if name == "" {
		return nil
	}
	cmd, ok := m.cmdRegistry.Resolve(name)
	if !ok {
		m.chat.AppendSystemMessage(fmt.Sprintf("unknown command: /%s — try /help", name))
		return nil
	}

	env := newAppEnv(m)
	res, err := cmd.Exec(context.Background(), args, env)
	if err != nil {
		m.chat.AppendSystemMessage(fmt.Sprintf("/%s: %v", name, err))
		return nil
	}

	var out []tea.Cmd
	if res.Message != "" {
		if res.MessageMarkdown {
			m.chat.AppendSystemMarkdown(res.Message)
		} else {
			m.chat.AppendSystemMessage(res.Message)
		}
	}
	if res.Quit {
		m.quitting = true
		out = append(out, tea.Quit)
	}
	if res.OpenHelp {
		m.thinkingVisible = false
		m.helpVisible = true
	}
	if res.OpenModelPicker {
		// Build the picker fresh from current env state so newly-
		// added config models show up without a restart. Other
		// overlays close to keep the screen single-purpose.
		env := newAppEnv(m)
		m.modelPicker = NewModelPickerModel(env.Models(), env.Model())
		m.modelPicker.SetSize(m.chat.width, m.chat.height)
		m.thinkingVisible = false
		m.helpVisible = false
		m.modelPickerVisible = true
	}
	if res.OpenPlanApproval {
		// User-driven approval (`/plan approve`). Build the
		// overlay from the active session's plan path; mark the user-
		// driven flag so handlePlanApprovalKey takes the no-pending
		// branch on y/n. If we can't read the plan file, surface the
		// error inline rather than silently dropping the approval.
		if approval, ok := m.buildPlanApproval(); ok {
			m.planApproval = approval
			m.planApprovalVisible = true
			m.planApprovalUserDriven = true
			m.thinkingVisible = false
			m.helpVisible = false
			m.modelPickerVisible = false
			m.relayout()
			out = append(out, terminalPingCmd())
		} else {
			m.chat.AppendSystemMessage("/plan approve: failed to load plan file")
		}
	}
	if res.Prompt != "" {
		// Expanding command (or any KindLocal that returns Prompt,
		// e.g. /plan revise): feed the synthesised text through the
		// same path as the Enter key.
		out = append(out, m.feedPrompt(res.Prompt)...)
	}
	return out
}

// feedPrompt synthesizes a user submission from text and starts a model
// turn, mirroring the "enter" key path. Used by KindExpanding command
// results.
func (m *AppModel) feedPrompt(text string) []tea.Cmd {
	m.input.SetDisabled(true)
	m.streaming = true
	m.streamStarted = false
	m.status.streaming = true
	m.hasUserMsg = true
	m.relayout()

	userMsg := api.NewUserMessage(text)
	m.conv.Append(userMsg)
	m.chat.AppendUserMessage(text)

	if m.store != nil && m.sessionID != "" {
		if err := m.store.AppendMessage(context.Background(), m.sessionID, userMsg, nil); err != nil {
			m.chat.AppendSystemMessage("persist user message: " + err.Error())
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.cancelFn = cancel
	result := m.agent.Run(ctx, agent.RunInput{
		Conversation:     m.conv,
		MaxSteps:         m.cfg.Agent.MaxSteps,
		CanUseTool:       m.canUseTool,
		ExtraSystem:      m.extra,
		SessionID:        m.sessionID,
		Store:            m.store,
		FileChanges:      m.fileChanges,
		AgentName:        m.agentName,
		SpawnTask:        m.spawnTask,
		AgentsMDLoadRoot: m.agentsMDLoadRoot,
	})
	m.events = result.Events
	m.done = result.Done

	stateCmd := m.setState(StateThinking, "")
	return []tea.Cmd{
		waitForAgentEvent(m.events),
		waitForAgentDone(m.done),
		elapsedTickCmd(),
		stateCmd,
	}
}
