package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/marcomoesman/prompto/internal/agent"
	"github.com/marcomoesman/prompto/internal/api"
	"github.com/marcomoesman/prompto/internal/command"
	"github.com/marcomoesman/prompto/internal/compact"
	"github.com/marcomoesman/prompto/internal/config"
	"github.com/marcomoesman/prompto/internal/permission"
	"github.com/marcomoesman/prompto/internal/store"
)

const (
	// Layout constants for the rendered View. The indicator row is
	// always reserved (two lines: one blank spacer + one label row) so
	// input doesn't jump as state changes and the sticky label never
	// sits flush against the chat transcript.
	indicatorHeight = 2
	statusHeight    = 1
	gapHeight       = 1
)

// PendingApproval is the in-flight tool-approval request handed to the
// TUI via ToolApprovalRequestMsg. The TUI writes the user's decision to
// Done and clears its pointer.
//
// Key serves double duty: it's both the human-readable string the user
// sees AND the rule pattern persisted when they pick session/project
// scope. Callers must produce a value that's safe and meaningful for
// both.
type PendingApproval struct {
	Name       string
	Key        string
	Input      []byte
	Disp       string              // FormatForDisplay output; empty → chat-side fallback
	IsReadOnly bool                // gates the `[f] all files` option (only safe for non-mutating tools)
	Subagent   string              // non-empty when approval is for a spawned child run
	Done       chan agent.Decision // buffered(1)
}

// ToolApprovalRequestMsg is sent from the CanUseTool closure (running on the
// agent goroutine) into the Tea event loop.
type ToolApprovalRequestMsg struct {
	Req *PendingApproval
}

// AppModelInput bundles the AppModel constructor parameters.
type AppModelInput struct {
	Agent      *agent.Agent
	CanUseTool agent.CanUseTool
	Extra      string // AGENTS.md content; passed into every RunInput
	// AgentsMDLoadRoot is the directory the eager AGENTS.md walk used as
	// its starting point. Tools (notably read) walk up to but not past
	// this directory when surfacing nested AGENTS.md as one-shot
	// reminders. Empty disables the lazy walk.
	AgentsMDLoadRoot string

	// Persistence (optional): Store + SessionID enable DB writes for
	// every message and file change. When Store is nil the TUI runs
	// ephemerally.
	Store       *store.Store
	SessionID   string
	FileChanges agent.FileChangeSink
	// Prior is the list of messages to seed the conversation (used when
	// resuming from the DB). nil for a fresh session.
	Prior []api.Message

	// Permission wiring (optional). Ruleset is mutated when the user
	// picks "session" or "project" scope on an approval prompt. Evaluator
	// carries the mode and is read for the status-bar indicator and cycled
	// via Ctrl+Y.
	Ruleset   *permission.Ruleset
	Evaluator *permission.Evaluator

	// Compactor is consulted for the operative context limit, used by the
	// status bar's `context: NN%` segment. Optional — when nil, the segment is
	// hidden.
	Compactor *compact.Compactor

	// Version + AgentName drive the welcome banner.
	Version   string
	AgentName string

	// SpawnTask is the closure that the task tool dispatches to. main.go
	// builds this via agent.NewSpawner. Subagents never see it (the task
	// tool is stripped from their resolver), so this is a primary-only
	// concern. nil disables subagent spawning entirely.
	SpawnTask agent.TaskSpawner

	// Commands is the registry of slash commands. main.go populates it
	// with built-ins via command.RegisterBuiltins (and any future custom
	// commands). nil disables slash dispatch — input starting with `/`
	// is then sent to the model as ordinary text.
	Commands *command.Registry

	// Config is the loaded global+project config. /model uses it to
	// resolve cross-provider switches and to render the available-
	// models list. nil leaves /model in single-provider mode (no
	// listing, switches assumed to stay on the current provider).
	Config *config.Config

	// StartupMessages are appended to the transcript before the first user
	// turn. ProviderHealthCheck is called after /model switches; it should
	// return an empty string when the selected provider/model is ready.
	StartupMessages     []string
	ProviderHealthCheck func(context.Context, string, config.ProviderEntry, string) string
}

// AppModel is the root Bubbletea model composing chat + input + status.
type AppModel struct {
	chat     ChatModel
	thinking ThinkingModel
	input    InputModel
	status   StatusModel

	agent            *agent.Agent
	conv             *agent.Conversation
	canUseTool       agent.CanUseTool
	extra            string
	agentsMDLoadRoot string

	store       *store.Store
	sessionID   string
	fileChanges agent.FileChangeSink
	ruleset     *permission.Ruleset
	evaluator   *permission.Evaluator
	compactor   *compact.Compactor
	spawnTask   agent.TaskSpawner

	welcome    WelcomeData
	hasUserMsg bool // becomes true on first user submission; banner hides forever after

	// Agent cycling. agentName is the active primary; previousAgent
	// tracks the prior agent so plan→build transitions can queue the
	// BUILD_SWITCH one-shot reminder on the agent's notifier (consumed
	// at the next provider call by the run loop).
	agentName     string
	previousAgent string
	cwd           string
	version       string

	cmdRegistry *command.Registry
	cfg         *config.Config
	healthCheck func(context.Context, string, config.ProviderEntry, string) string
	// currentProviderName tracks which config.Providers entry the
	// active agent.Provider was constructed from. /model uses it to
	// detect cross-provider switches and avoid rebuilding the
	// provider unnecessarily on same-provider model swaps.
	currentProviderName string

	// helpVisible toggles the floating help overlay. Set by /help; cleared
	// by ESC. The overlay replaces the chat region while visible.
	helpVisible bool

	// thinkingVisible toggles the extended-thinking overlay. Ctrl+O opens
	// it; ESC or Ctrl+O again closes it. Independent from helpVisible —
	// only one overlay can be active at once (open guards check the other).
	thinkingVisible bool

	// modelPickerVisible toggles the arrow-key model selection
	// overlay. Set by /model with no args; cleared by Enter (commits
	// a switch via env.SetModel) or ESC (cancels). When visible,
	// KeyPressMsg routes to the picker exclusively — the input is
	// frozen.
	modelPickerVisible bool
	modelPicker        ModelPickerModel

	// suggestions is the slash-command autocomplete popup that
	// appears above the input box while the user is typing a
	// command name. Activated/deactivated implicitly via Update
	// based on the current input text; never visible while another
	// overlay is open or while a turn is streaming.
	suggestions SuggestionsModel

	// planApproval is the full-screen overlay shown when
	// the plan agent calls plan_exit. The overlay renders the plan
	// markdown body so the user can review before approving.
	// planApprovalVisible toggles routing — when true, ToolApproval
	// keys (y/n/esc) are interpreted as plan-approval decisions and
	// scroll keys drive the overlay's viewport.
	planApprovalVisible bool
	planApproval        PlanApprovalModel

	// planApprovalUserDriven distinguishes the `/plan
	// approve` path from the model-driven `plan_exit` path. When
	// true, there is no pending tool approval — `y` runs the
	// flip-to-build sequence directly via completePlanApproval; `n`
	// just closes the overlay. The flag is cleared on either exit
	// path so subsequent model-driven approvals route normally.
	planApprovalUserDriven bool

	// pendingPlanApproval carries the plan path between
	// EventPlanApproved and the subsequent agentDoneMsg. Set when
	// plan_exit succeeds; consumed (and cleared) by the done-handler
	// to perform the agent flip + synthesised user message + new
	// build run. Empty when no flip is pending.
	pendingPlanApproval string

	// mouseCapture controls whether the View() emits a cell-motion
	// MouseMode. True (default) = mouse wheel scrolls the chat;
	// false = terminal-native click-drag text selection works. The
	// user toggles via Ctrl+T per session, or holds Shift / Option
	// to bypass capture on a single drag without flipping the flag.
	mouseCapture bool

	// Captured args from the most recent EventToolCallStarted for
	// "todowrite" — applied to status counts on the matching
	// EventToolCallDone (when not error).
	pendingTodoArgs string

	// todos is the live cache of the session's todo list, used by the
	// sticky todo panel. Seeded from the store on --resume, replaced
	// atomically when a successful todowrite tool call lands. nil when
	// no session-level todo list exists (subagents, fresh sessions
	// before the model has invoked todowrite, or after /clear).
	todos     []agent.Todo
	todoPanel TodoPanel

	// lastToolCallSig is the signature of the most recently rendered tool
	// call, used to dedupe between EventToolCallStarted (events channel)
	// and ToolApprovalRequestMsg (p.Send) — the two race through different
	// paths to the Tea msg loop. Signature-based dedup handles both:
	//  - The race within one call (started+approval pair).
	//  - The planning loop emitting multiple Started events back-to-back
	//    when calls are project-allowed (no approval in between).
	// Cleared on agentDoneMsg so the next turn starts fresh.
	lastToolCallSig string

	events   <-chan agent.Event
	done     <-chan error
	cancelFn context.CancelFunc

	streaming bool
	pending   *PendingApproval

	// Working-state machine. workingState drives the indicator above the
	// input; preCompactState records what to restore after a CompactDone.
	workingState     WorkingState
	workingDetail    string
	preCompactState  WorkingState
	preCompactDetail string
	// thinkingWord is the rotating promptism shown in place of "Thinking…"
	// while StateThinking is active. Picked once per transition INTO
	// StateThinking so the indicator stays stable across spinner ticks
	// instead of flickering between words on every render.
	// thinkingWordPickedAt records when the current word was chosen;
	// maybeRotatePromptism rolls a fresh one once it has been on screen
	// past promptismRotateAfter so a long thinking burst doesn't fixate
	// on a single word for minutes.
	thinkingWord         string
	thinkingWordPickedAt time.Time
	// shimmerFrame is the per-spinner-tick counter that drives the
	// promptism shimmer in renderIndicator. Reset whenever a fresh
	// word is picked so each new word starts with the highlight at
	// the leading edge instead of mid-sweep.
	shimmerFrame int
	// Per-tool elapsed accounting. toolStartedAt is set when entering
	// StateToolRunning and zeroed on exit; the indicator render appends
	// "(Xs)" / "(Xm Ys)" so a stalled tool is visually distinct from a
	// slow one. activeToolCallID pairs the running indicator with the
	// EventToolStatus events that should refresh its label — mismatched
	// IDs mean the status arrived for an already-finished call.
	// activeToolSubagent is the spawning subagent's name (empty for
	// primary calls); the indicator prefixes the label with the agent's
	// display name so the user can tell when work is happening on
	// behalf of a child run.
	toolStartedAt      time.Time
	activeToolCallID   string
	activeToolSubagent string
	// Thinking-time accounting. The displayed elapsed timer measures
	// time the agent is actually working — it pauses while we sit in
	// StateAwaitingApproval. thinkingAccum holds time banked from prior
	// active intervals; thinkingResumedAt is the start of the current
	// active interval, or the zero value when paused/idle.
	thinkingAccum     time.Duration
	thinkingResumedAt time.Time
	streamStarted     bool // first text delta of the current turn flips this

	spinner spinner.Model

	quitting     bool
	ctrlCPressed bool
	width        int
	height       int
}

// NewAppModel creates the root model.
func NewAppModel(in AppModelInput) AppModel {
	transcript := seedTranscriptFromPrior(in.Prior)

	agentName := in.AgentName
	if agentName == "" {
		agentName = "build"
	}
	version := in.Version
	if version == "" {
		version = "v0"
	}

	status := initStatusFromInput(in, agentName)
	if len(in.Prior) > 0 {
		transcript.chat.AppendSystemMessage(fmt.Sprintf("Resumed session %s · %d prior messages", status.sessionPrefix, len(in.Prior)))
	}
	for _, msg := range in.StartupMessages {
		if msg != "" {
			transcript.chat.AppendSystemMessage(msg)
		}
	}

	input := NewInputModel()
	if in.Evaluator != nil {
		input.SetMode(in.Evaluator.Mode())
	}

	cwd, _ := os.Getwd()

	seededTodos := seedTodosFromStore(in)
	todoPanel := NewTodoPanel()
	todoPanel.SetTodos(seededTodos)

	return AppModel{
		chat:             transcript.chat,
		thinking:         transcript.thinking,
		input:            input,
		status:           status,
		agent:            in.Agent,
		conv:             transcript.conv,
		canUseTool:       in.CanUseTool,
		extra:            in.Extra,
		agentsMDLoadRoot: in.AgentsMDLoadRoot,
		store:            in.Store,
		sessionID:        in.SessionID,
		fileChanges:      in.FileChanges,
		ruleset:          in.Ruleset,
		evaluator:        in.Evaluator,
		compactor:        in.Compactor,
		spawnTask:        in.SpawnTask,
		welcome: WelcomeData{
			Version: version,
			Agent:   agentName,
			Model:   in.Agent.Model(),
		},
		hasUserMsg:          transcript.hasUserMsg,
		agentName:           agentName,
		previousAgent:       agentName,
		cwd:                 cwd,
		version:             version,
		cmdRegistry:         in.Commands,
		cfg:                 in.Config,
		healthCheck:         in.ProviderHealthCheck,
		spinner:             newSpinner(),
		mouseCapture:        true, // wheel scroll on; Ctrl+T toggles for selection
		currentProviderName: configDefaultProvider(in.Config),
		todos:               seededTodos,
		todoPanel:           todoPanel,
		suggestions:         NewSuggestionsModel(in.Commands),
	}
}

// seedTodosFromStore loads the persisted todo list for the resumed
// session so the sticky panel shows the right state on startup before
// the model has had a chance to invoke todowrite. Best-effort: a
// failed load returns nil rather than aborting the session.
func seedTodosFromStore(in AppModelInput) []agent.Todo {
	if in.SessionID == "" {
		return nil
	}
	store := in.Agent.Todos()
	if store == nil {
		return nil
	}
	loaded, err := store.LoadTodos(context.Background(), in.SessionID)
	if err != nil {
		return nil
	}
	return loaded
}

// transcriptInit bundles the chat/thinking/conversation triplet built
// from a session's prior messages. Returned by seedTranscriptFromPrior
// so NewAppModel can plug them into the AppModel struct without a
// dozen returned values.
type transcriptInit struct {
	conv       *agent.Conversation
	chat       ChatModel
	thinking   ThinkingModel
	hasUserMsg bool
}

// seedTranscriptFromPrior replays a resumed session's persisted messages
// into a fresh chat + thinking pair. Replaying thinking blocks here is
// what makes Ctrl+O after a `--resume` show the full reasoning trail
// instead of starting empty.
func seedTranscriptFromPrior(prior []api.Message) transcriptInit {
	t := transcriptInit{
		conv:     agent.NewConversation(),
		chat:     NewChatModel(),
		thinking: NewThinkingModel(),
	}
	for _, m := range prior {
		t.conv.Append(m)
		seedChatWithMessage(&t.chat, m)
		seedThinkingWithMessage(&t.thinking, m)
		if m.Role == api.RoleUser {
			t.hasUserMsg = true
		}
	}
	return t
}

// initStatusFromInput builds the initial status-bar state — model,
// session prefix, mode, context-limit, agent color, and any persisted
// todo counts. Centralising the lookups here keeps NewAppModel focused
// on assembling AppModel rather than chasing every status-bar field.
func initStatusFromInput(in AppModelInput, agentName string) StatusModel {
	status := StatusModel{
		modelName: in.Agent.Model(),
		agent:     agentName,
	}
	if in.SessionID != "" {
		prefix := in.SessionID
		if len(prefix) > 8 {
			prefix = prefix[:8]
		}
		status.sessionPrefix = prefix
	}
	if in.Evaluator != nil {
		status.mode = in.Evaluator.Mode().String()
	}
	if in.Compactor != nil {
		status.contextLimit = in.Compactor.ContextLimit(in.Agent.Model())
	}
	if reg := in.Agent.Registry(); reg != nil {
		if def, ok := reg.Resolve(agentName); ok {
			status.agentColor = def.Color
		}
	}
	return status
}

// configDefaultProvider returns the config's default provider key, or ""
// when the host runs without a loaded config (tests). Pulled out of the
// AppModel struct literal so the field reads as a single value.
func configDefaultProvider(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	return cfg.Default.Provider
}

// currentProvider returns the config provider key that the active
// agent.Provider was built from. Empty when the host runs without
// config (tests). /model reads it to decide whether a switch crosses
// provider boundaries.
func (m *AppModel) currentProvider() string { return m.currentProviderName }

// replaceCancel installs a fresh cancellable context for the next agent
// run. Any prior cancelFn that hasn't fired yet (e.g. a plan→build
// transition that overwrites m.cancelFn before EventTurnComplete is
// processed) is invoked first so its parent context tree is released.
// Returns the new context.
func (m *AppModel) replaceCancel() context.Context {
	if m.cancelFn != nil {
		m.cancelFn()
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancelFn = cancel
	return ctx
}

// seedChatWithMessage mirrors the display logic of the live event pump for
// one prior message. We only render the text portion + tool-call display;
// tool_result content inlines as a plain collapsed row so the user has
// orientation without seeing megabytes of old output.
func seedChatWithMessage(chat *ChatModel, m api.Message) {
	switch m.Role {
	case api.RoleUser:
		t := m.Text()
		if strings.HasPrefix(strings.TrimSpace(t), "<compact_summary>") {
			chat.AppendSystemMessage("[conversation summarized]")
			return
		}
		if t != "" {
			chat.AppendUserMessage(t)
		}
	case api.RoleAssistant:
		if t := m.Text(); t != "" {
			chat.AppendDelta(t)
			chat.FinishStreaming()
		}
		for _, b := range m.Content {
			if b.Type == api.BlockToolUse && b.ToolCall != nil {
				// Resume path: no live tool resolver, so the chat
				// falls back to its legacy summarizer. New-style
				// rows appear from the next turn onward.
				chat.AppendToolCall(b.ToolCall.ID, b.ToolCall.Name, string(b.ToolCall.Input), "")
			}
		}
	case api.RoleTool:
		for _, b := range m.Content {
			if b.Type == api.BlockToolResult && b.ToolResult != nil {
				// Summaries aren't stored, so resumed sessions show
				// only the call header for prior turns.
				chat.AppendToolResult(b.ToolResult.ToolCallID, "", b.ToolResult.Content, b.ToolResult.IsError, "")
			}
		}
	}
}

// toolCallSig is the dedup key for a single tool invocation. Name+args is
// unique enough in practice; same-tool-same-args twice in one turn is rare
// and the only effect of a collision is a hidden duplicate row.
func toolCallSig(name, argsJSON string) string { return name + "\x00" + argsJSON }

// seedThinkingWithMessage replays one persisted assistant message's
// thinking blocks into the overlay. Redacted blocks render as a fixed
// placeholder line — the encrypted payload itself isn't human-readable.
// MarkInactive is called between messages so each turn gets its own
// divider, matching the live-stream rendering.
func seedThinkingWithMessage(t *ThinkingModel, m api.Message) {
	if m.Role != api.RoleAssistant {
		return
	}
	for _, b := range m.Content {
		if b.Type != api.BlockThinking || b.Thinking == nil {
			continue
		}
		if b.Thinking.Redacted {
			t.AppendDelta("[redacted thinking]")
			continue
		}
		if b.Thinking.Text == "" {
			continue
		}
		t.AppendDelta(b.Thinking.Text)
	}
	t.MarkInactive()
}

// appendApprovalRule records a rule allowing the pending tool's key for
// the given scope. No-op when ruleset is nil (tests / headless mode).
// handleKeyPress is the single entry point for the tea.KeyPressMsg case.
// Returns (handled, cmd): when handled is false the outer Update() falls
// through to the textarea-feed path, preserving the previous "unhandled
// keys reach the input" semantics. Specialized sub-handlers cover the
// model-picker / approval-prompt / thinking-overlay states; the main key
// switch handles the global keymap (ctrl+o, ctrl+y, esc, ctrl+c, enter, …).
func (m *AppModel) handleKeyPress(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	// PgUp/PgDn always page-scroll the chat, even with mouse capture off,
	// so the keys never reach the textarea.
	switch msg.String() {
	case "pgup", "pgdown":
		var cmd tea.Cmd
		m.chat, cmd = m.chat.Update(msg)
		return true, cmd
	case "ctrl+t":
		m.mouseCapture = !m.mouseCapture
		if m.mouseCapture {
			m.chat.AppendSystemMessage("mouse capture: ON  (wheel scrolls; Shift/Option+drag for one-off selection)")
		} else {
			m.chat.AppendSystemMessage("mouse capture: OFF (drag to select; PgUp/PgDn to scroll; Ctrl+T to re-enable wheel)")
		}
		return true, nil
	}

	if m.modelPickerVisible {
		return m.handleModelPickerKey(msg)
	}
	if m.planApprovalVisible {
		return m.handlePlanApprovalKey(msg)
	}
	if m.pending != nil {
		return m.handleApprovalKey(msg)
	}

	key := msg.String()
	if key != "ctrl+c" && m.ctrlCPressed {
		m.ctrlCPressed = false
	}
	if m.thinkingVisible {
		if handled, cmd := m.handleThinkingOverlayKey(msg, key); handled {
			return true, cmd
		}
	}

	return m.handleGlobalKey(msg, key)
}

// handleModelPickerKey runs while the /model picker overlay is open.
// Always returns handled=true: the picker takes input ownership until
// the user confirms or escapes.
func (m *AppModel) handleModelPickerKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+c":
		m.modelPickerVisible = false
	case "tab", "s":
		m.modelPicker.ToggleSettings()
	case "up", "k":
		m.modelPicker.Move(-1)
	case "down", "j":
		m.modelPicker.Move(1)
	case "left", "h":
		m.adjustModelSetting(-0.1)
	case "right", "l":
		m.adjustModelSetting(0.1)
	case "r":
		if sel, ok := m.modelPicker.ResetSettings(); ok {
			env := newAppEnv(m)
			if err := env.ResetModelSampling(sel); err != nil {
				m.chat.AppendSystemMessage(fmt.Sprintf("/model: %v", err))
			} else {
				m.chat.AppendSystemMessage("model settings reset: " + sel.Name)
			}
		}
	case "enter":
		sel := m.modelPicker.Selected()
		m.modelPickerVisible = false
		if sel.Name == "" {
			return true, nil
		}
		env := newAppEnv(m)
		if err := env.SetModel(sel.Name); err != nil {
			m.chat.AppendSystemMessage(fmt.Sprintf("/model: %v", err))
		} else {
			m.chat.AppendSystemMessage("model: " + sel.Name)
		}
	}
	return true, nil // picker swallows everything else while open
}

func (m *AppModel) adjustModelSetting(delta float64) {
	sel, ok := m.modelPicker.AdjustSetting(delta)
	if !ok {
		return
	}
	env := newAppEnv(m)
	if err := env.SetModelSampling(sel); err != nil {
		m.chat.AppendSystemMessage(fmt.Sprintf("/model: %v", err))
	}
}

// handlePlanApprovalKey runs while the plan-approval
// overlay is open. y/Y approves; n/N/esc denies; PgUp/PgDn/j/k/up/
// down scroll the overlay's viewport. Always returns handled=true
// so the underlying chat/textarea doesn't see the keystroke.
//
// Two routing modes share this handler:
//
//   - Model-driven (default): the plan agent called `plan_exit` and a
//     tool-approval is pending. `y/n` route through `m.pending.Done`
//     so the agent loop's CanUseTool closure unblocks; the run loop
//     then emits EventPlanApproved and the done-handler performs the
//     agent flip.
//   - User-driven (set by `/plan approve`): no pending
//     tool. `y` calls `completePlanApproval` directly to perform the
//     same flip + synthesised "execute the plan" user message + new
//     build run. `n/esc` simply closes the overlay.
func (m *AppModel) handlePlanApprovalKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		if m.planApprovalUserDriven {
			planPath := m.planApproval.PlanPath()
			m.planApprovalUserDriven = false
			m.planApprovalVisible = false
			m.chat.AppendSystemMessage(m.planApproval.describe())
			m.relayout()
			return true, m.completePlanApproval(planPath)
		}
		// Model-driven: route through the pending Done channel.
		if m.pending != nil {
			m.pending.Done <- agent.DecisionAllow
			m.pending = nil
		}
		m.planApprovalVisible = false
		m.chat.ResolveApproval(true)
		m.chat.AppendSystemMessage(m.planApproval.describe())
		m.resumeThinking(time.Now())
		m.relayout()
		return true, m.setState(StateThinking, "")
	case "n", "N", "esc":
		if m.planApprovalUserDriven {
			m.planApprovalUserDriven = false
			m.planApprovalVisible = false
			m.chat.AppendSystemMessage("plan approval cancelled")
			m.relayout()
			return true, nil
		}
		if m.pending != nil {
			m.pending.Done <- agent.DecisionDeny
			m.pending = nil
		}
		m.planApprovalVisible = false
		m.chat.ResolveApproval(false)
		m.resumeThinking(time.Now())
		m.relayout()
		return true, m.setState(StateThinking, "")
	case "ctrl+c":
		// Symmetric with the n/esc branch above: surface "plan
		// approval cancelled" in the chat so the user sees their
		// keypress was processed, then deny any pending tool prompt
		// and cancel the run.
		if m.planApprovalUserDriven {
			m.chat.AppendSystemMessage("plan approval cancelled")
		}
		if m.pending != nil {
			m.pending.Done <- agent.DecisionDeny
			m.pending = nil
		}
		if m.cancelFn != nil {
			m.cancelFn()
		}
		m.planApprovalUserDriven = false
		m.planApprovalVisible = false
		m.relayout()
		return true, nil
	}
	// Forward scroll keys to the overlay.
	var cmd tea.Cmd
	m.planApproval, cmd = m.planApproval.Update(msg)
	return true, cmd
}

// handleApprovalKey runs while a tool-approval prompt is pending. Always
// returns handled=true so unrelated keystrokes don't leak into the
// textarea (e.g. accidental "n" while a destructive call awaits sign-off).
func (m *AppModel) handleApprovalKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		m.handleApprovalAllow(agent.DecisionAllow, nil)
	case "a", "A":
		m.handleApprovalAllow(agent.DecisionAllow, func() { m.appendApprovalRule(permission.ScopeSession) })
	case "s", "S":
		if m.pending.Subagent == "" {
			return true, nil
		}
		m.handleApprovalAllow(agent.DecisionAllowForSubagent, nil)
	case "p", "P":
		m.handleApprovalAllow(agent.DecisionAllow, func() { m.appendApprovalRule(permission.ScopeProject) })
	case "f", "F":
		if !m.pending.IsReadOnly || m.pending.Subagent != "" {
			// [f] is only valid for read-only tools, and only on
			// primary-agent calls — the prompt hides the option for
			// subagents so a project-wide wildcard can't be installed
			// from a transient child run.
			return true, nil
		}
		m.handleApprovalAllow(agent.DecisionAllow, m.appendAllFilesRule)
	case "n", "N", "esc":
		m.handleApprovalDeny()
	case "ctrl+c":
		// Ctrl+C is a denial AND a run cancellation. Route through
		// the standard deny handler first so the chat shows the "X
		// denied" feedback and the thinking timer resumes, then
		// cancel the run so the agent loop exits cleanly. Without
		// this the overlay vanishes silently and the user has no
		// indication their key was even processed.
		m.handleApprovalDeny()
		if m.cancelFn != nil {
			m.cancelFn()
		}
	}
	return true, nil
}

// handleThinkingOverlayKey routes input to the thinking overlay's own
// Update when visible. Returns handled=true for keys the overlay
// consumes; toggle/escape keys (ctrl+o, esc, ctrl+c, ctrl+d) return
// handled=false so the global key switch can act on them.
func (m *AppModel) handleThinkingOverlayKey(msg tea.KeyPressMsg, key string) (bool, tea.Cmd) {
	switch key {
	case "ctrl+o", "esc", "ctrl+c", "ctrl+d":
		return false, nil
	}
	var cmd tea.Cmd
	m.thinking, cmd = m.thinking.Update(msg)
	return true, cmd
}

// handleGlobalKey is the main keymap branch reached when no overlay /
// approval is intercepting input. Unmatched keys return handled=false so
// they reach the textarea via the outer Update fall-through.
func (m *AppModel) handleGlobalKey(msg tea.KeyPressMsg, key string) (bool, tea.Cmd) {
	if m.suggestions.Visible() {
		switch key {
		case "up", "ctrl+p":
			m.suggestions.Move(-1)
			return true, nil
		case "down", "ctrl+n":
			m.suggestions.Move(1)
			return true, nil
		case "tab", "enter":
			m.acceptSuggestion()
			return true, nil
		case "esc":
			m.suggestions.Hide()
			m.relayout()
			return true, nil
		}
	}

	switch key {
	case "ctrl+y":
		m.cycleMode()
		return true, nil

	case "ctrl+o":
		// Toggle the thinking overlay. Help and thinking are mutually
		// exclusive — opening one closes the other. Relayout so the
		// chat/thinking viewport reclaims (or yields) the row the
		// sticky todo panel would otherwise occupy.
		if m.thinkingVisible {
			m.thinkingVisible = false
		} else {
			m.helpVisible = false
			m.thinkingVisible = true
		}
		m.relayout()
		return true, nil

	case "tab":
		// Tab cycles primaries (build ↔ plan); consumed so the textarea
		// doesn't insert a tab. Skipped mid-turn — switching during a
		// stream would race the runLoop.
		if m.streaming {
			return true, nil
		}
		m.cycleAgent()
		return true, nil

	case "ctrl+d":
		m.quitting = true
		return true, tea.Quit

	case "esc":
		// ESC closes whichever overlay is open first; otherwise cancels
		// the current turn. When idle with no overlay, leave the input
		// alone — pressing ESC shouldn't blow away a draft.
		if m.thinkingVisible {
			m.thinkingVisible = false
			m.relayout() // panel re-emerges; chat shrinks back
			return true, nil
		}
		if m.helpVisible {
			m.helpVisible = false
			return true, nil
		}
		if m.streaming && m.cancelFn != nil {
			m.cancelFn()
			return true, nil
		}
		return true, nil

	case "ctrl+c":
		if m.streaming && m.cancelFn != nil {
			m.cancelFn()
			return true, nil
		}
		if m.ctrlCPressed {
			m.quitting = true
			return true, tea.Quit
		}
		m.ctrlCPressed = true
		m.chat.AppendSystemMessage("Press Ctrl+C again to exit")
		return true, nil

	case "enter":
		return m.handleEnterKey()

	case "shift+enter", "alt+enter":
		m.input.textarea.InsertString("\n")
		return true, nil
	}
	return false, nil
}

// handleEnterKey submits the current input — either as a slash command
// or as a user message that starts an agent turn.
func (m *AppModel) handleEnterKey() (bool, tea.Cmd) {
	if m.streaming {
		return true, nil
	}
	text := m.input.Value()
	if strings.TrimSpace(text) == "" {
		return true, nil
	}

	if strings.HasPrefix(strings.TrimSpace(text), "/") && m.cmdRegistry != nil {
		m.input.Reset()
		m.relayout()
		cmds := m.dispatchSlash(text)
		return true, tea.Batch(cmds...)
	}

	m.input.Reset()
	m.input.SetDisabled(true)
	m.streaming = true
	m.streamStarted = false
	m.status.streaming = true
	m.hasUserMsg = true
	m.startThinking(time.Now())
	m.status.elapsedSec = 0
	m.relayout()

	userMsg := api.NewUserMessage(text)
	m.conv.Append(userMsg)
	m.chat.AppendUserMessage(text)

	// Persist the user message before starting the API call so --resume
	// works even if the process dies mid-stream.
	if m.store != nil && m.sessionID != "" {
		if err := m.store.AppendMessage(context.Background(), m.sessionID, userMsg, nil); err != nil {
			m.chat.AppendSystemMessage("persist user message: " + err.Error())
		}
	}

	ctx := m.replaceCancel()
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
	return true, tea.Batch(
		waitForAgentEvent(m.events),
		waitForAgentDone(m.done),
		elapsedTickCmd(),
		stateCmd,
	)
}

// acceptSuggestion commits the highlighted slash-command suggestion to
// the input, replacing whatever the user has typed with `/<name> ` and
// hiding the popup. The trailing space lets the user start typing args
// immediately and ensures the suggestions popup stays closed (slashPrefix
// rejects whitespace).
func (m *AppModel) acceptSuggestion() {
	sel := m.suggestions.Selected()
	if sel == nil {
		return
	}
	m.input.SetValue("/" + sel.Name() + " ")
	m.input.MoveToEnd()
	m.suggestions.Update(m.input.Value())
	m.relayout()
}

// handleApprovalAllow runs the user's "allow" decision for the pending
// approval. The optional appendRule callback is invoked first when the
// chosen scope persists a rule (a/A → session, p/P → project, f/F →
// all-files); plain y/Y and subagent-local s/S pass nil. The caller is
// responsible for any pre-checks (e.g. the IsReadOnly gate before [f]).
func (m *AppModel) handleApprovalAllow(decision agent.Decision, appendRule func()) {
	if m.pending == nil {
		return
	}
	detail := describeRunningTool(m.pending.Name, string(m.pending.Input))
	if appendRule != nil {
		appendRule()
	}
	m.pending.Done <- decision
	m.pending = nil
	m.chat.ResolveApproval(true)
	m.resumeThinking(time.Now())
	m.setState(StateToolRunning, detail)
}

// handleApprovalDeny runs the user's "deny" decision for the pending
// approval (n/N/esc).
func (m *AppModel) handleApprovalDeny() {
	if m.pending == nil {
		return
	}
	m.pending.Done <- agent.DecisionDeny
	m.pending = nil
	m.chat.ResolveApproval(false)
	m.resumeThinking(time.Now())
	m.setState(StateThinking, "")
}

func (m *AppModel) appendApprovalRule(scope permission.Scope) {
	if m.ruleset == nil || m.pending == nil {
		return
	}
	pattern := m.pending.Key
	if pattern == "" {
		pattern = "*" // no key → match all invocations of this tool
	}
	if err := m.ruleset.Append(permission.AppendInput{Rule: permission.Rule{
		Tool:    m.pending.Name,
		Pattern: pattern,
		Action:  agent.DecisionAllow,
		Scope:   scope,
	}}); err != nil {
		m.chat.AppendSystemMessage("could not persist rule: " + err.Error())
	}
}

// appendAllFilesRule records a project-scope wildcard rule that allows the
// pending tool to operate on any path. Only invoked for read-only tools, so
// it never grants write access. No-op when ruleset is nil.
func (m *AppModel) appendAllFilesRule() {
	if m.ruleset == nil || m.pending == nil {
		return
	}
	if err := m.ruleset.Append(permission.AppendInput{Rule: permission.Rule{
		Tool:    m.pending.Name,
		Pattern: "*",
		Action:  agent.DecisionAllow,
		Scope:   permission.ScopeProject,
		Reason:  "all files (read-only)",
	}}); err != nil {
		m.chat.AppendSystemMessage("could not persist rule: " + err.Error())
	}
}

// cycleMode advances the permission mode (default ↔ acceptEdits). Bypass is
// sticky — once entered via --yolo, Ctrl+Y doesn't leave it.
func (m *AppModel) cycleMode() {
	if m.evaluator == nil {
		return
	}
	next := permission.Cycle(m.evaluator.Mode())
	m.evaluator.SetMode(next)
	m.status.mode = next.String()
	m.input.SetMode(next)
	m.chat.AppendSystemMessage("permission mode: " + next.String())
}

// applyTodoUpdate parses a todowrite-tool args JSON payload and pushes
// the resulting list into m.todos for the sticky panel. No-op on empty
// or malformed input so a transient parse failure leaves the previous
// state in place rather than zeroing it.
func (m *AppModel) applyTodoUpdate(argsJSON string) {
	todos, ok := parseTodoArgs(argsJSON)
	if !ok {
		return
	}
	m.todos = todos
}

// parseTodoArgs decodes the todowrite tool's argument JSON into the
// canonical agent.Todo slice. Returns ok=false on empty or malformed
// input so callers can no-op rather than clobber existing state.
func parseTodoArgs(argsJSON string) ([]agent.Todo, bool) {
	if argsJSON == "" {
		return nil, false
	}
	var raw struct {
		Todos []struct {
			ID         string `json:"id"`
			Content    string `json:"content"`
			Status     string `json:"status"`
			ActiveForm string `json:"active_form"`
		} `json:"todos"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &raw); err != nil {
		return nil, false
	}
	out := make([]agent.Todo, len(raw.Todos))
	for i, t := range raw.Todos {
		out[i] = agent.Todo{
			ID:         t.ID,
			Content:    t.Content,
			Status:     t.Status,
			ActiveForm: t.ActiveForm,
		}
	}
	return out, true
}

// resolvePlanPath returns the canonical plan-file path for the
// active session, preferring the persisted `sessions.plan_path`
// value (set the first time the model writes a plan file in plan
// mode) and falling back to the legacy `<sessionID>.md` shape for
// pre-Phase-20 sessions or when no write has happened yet. Returns
// "" when neither a persisted path nor a session id is available
// (e.g. ephemeral runs without a store).
func (m *AppModel) resolvePlanPath() string {
	persisted := ""
	if m.store != nil && m.sessionID != "" {
		// Best-effort: a DB error here means we fall back to the
		// legacy filename rather than silently dropping the
		// reminder.
		if loaded, err := m.store.LoadPlanPath(context.Background(), m.sessionID); err == nil {
			persisted = loaded
		}
	}
	return agent.ResolvePlanPath(agent.ResolvePlanPathInput{
		Cwd:           m.cwd,
		SessionID:     m.sessionID,
		PersistedPath: persisted,
	})
}

// completePlanApproval finalises the plan→build flip after the
// plan agent's run has wound down. It (1) switches the active
// agent to build via the env's SwitchAgent path so permission
// rules + status bar + welcome banner stay consistent with the
// /plan and /build commands, (2) queues the BUILD_SWITCH reminder
// onto the notifier so the build agent's first turn orients on
// the plan file, (3) synthesises a user message instructing the
// build agent to execute the approved plan, (4) kicks off a
// fresh agent.Run with that conversation tail. The returned
// tea.Cmd batches the event/done listeners + state spinner so
// the caller can return it directly from agentDoneMsg.
//
// planPath is the absolute plan file path captured from
// EventPlanApproved. Empty path is a no-op (defensive guard).
func (m *AppModel) completePlanApproval(planPath string) tea.Cmd {
	if planPath == "" {
		return nil
	}

	// Flip to build via the same path that /build uses so the
	// permission rules, status bar, welcome banner, and notifier
	// reminder all stay consistent. SwitchAgent ignores no-op
	// switches, but here we're definitely coming from plan.
	env := newAppEnv(m)
	if err := env.SwitchAgent("build"); err != nil {
		m.chat.AppendSystemMessage("plan approved but agent switch failed: " + err.Error())
		return nil
	}

	// Queue the build-switch reminder so the build agent's first
	// turn opens with "read the plan file at <path>." This is the
	// same one-shot the Tab keybinding uses; the notifier dedups
	// nothing, so calling it once here is correct.
	if n := m.agent.Notifier(); n != nil {
		n.QueueOneShot(agent.BuildSwitchReminderBody(planPath))
	}

	// Synthesise the user message. Using a real RoleUser message
	// (vs. a system reminder) lets the build agent treat it as a
	// turn it must respond to — that's exactly the behaviour we
	// want, since "execute the plan" is the prompt.
	rel := planPath
	if m.cwd != "" {
		if r, err := filepath.Rel(m.cwd, planPath); err == nil && !strings.HasPrefix(r, "..") {
			rel = r
		}
	}
	body := fmt.Sprintf("The plan at `%s` was approved. Execute it.", rel)
	userMsg := api.NewUserMessage(body)
	m.conv.Append(userMsg)
	m.chat.AppendUserMessage(body)
	if m.store != nil && m.sessionID != "" {
		if err := m.store.AppendMessage(context.Background(), m.sessionID, userMsg, nil); err != nil {
			m.chat.AppendSystemMessage("persist plan-approval user message: " + err.Error())
		}
	}

	// Start a fresh run as the build agent.
	m.input.SetDisabled(true)
	m.streaming = true
	m.streamStarted = false
	m.status.streaming = true
	m.hasUserMsg = true
	m.startThinking(time.Now())
	m.status.elapsedSec = 0
	m.relayout()

	ctx := m.replaceCancel()
	result := m.agent.Run(ctx, agent.RunInput{
		Conversation:     m.conv,
		MaxSteps:         m.cfg.Agent.MaxSteps,
		CanUseTool:       m.canUseTool,
		ExtraSystem:      m.extra,
		SessionID:        m.sessionID,
		Store:            m.store,
		FileChanges:      m.fileChanges,
		AgentName:        m.agentName, // now "build"
		SpawnTask:        m.spawnTask,
		AgentsMDLoadRoot: m.agentsMDLoadRoot,
	})
	m.events = result.Events
	m.done = result.Done

	stateCmd := m.setState(StateThinking, "")
	return tea.Batch(
		waitForAgentEvent(m.events),
		waitForAgentDone(m.done),
		elapsedTickCmd(),
		stateCmd,
	)
}

// buildPlanApproval reads the active session's plan markdown body
// and constructs the plan-approval overlay. Returns ok=false
// when no plan path is resolvable or the file can't be read — the
// caller falls back to the standard one-line approval prompt rather
// than blocking the run on a transient filesystem error.
func (m *AppModel) buildPlanApproval() (PlanApprovalModel, bool) {
	planPath := m.resolvePlanPath()
	if planPath == "" {
		return PlanApprovalModel{}, false
	}
	body, err := os.ReadFile(planPath)
	if err != nil {
		return PlanApprovalModel{}, false
	}
	approval := NewPlanApprovalModel(m.cwd, planPath, string(body))
	approval.SetDark(m.chat.dark)
	approval.SetSize(m.width, m.height)
	return approval, true
}

// cycleAgent advances the active primary agent in registry order (build →
// plan → build). Updates status, welcome banner, and rewires plan-mode
// permission rules. Returns the new agent name (unchanged when there's
// nothing to cycle to).
func (m *AppModel) cycleAgent() string {
	reg := m.agent.Registry()
	if reg == nil {
		return m.agentName
	}
	primaries := reg.Primaries()
	if len(primaries) < 2 {
		return m.agentName
	}

	idx := -1
	for i, def := range primaries {
		if def.Name == m.agentName {
			idx = i
			break
		}
	}
	next := primaries[0]
	if idx >= 0 {
		next = primaries[(idx+1)%len(primaries)]
	}

	m.previousAgent = m.agentName
	m.agentName = next.Name

	m.status.agent = next.Name
	m.status.agentColor = next.Color
	m.welcome.Agent = next.Name

	// Plan-mode rule wiring. When entering plan, layer agent-scope rules
	// onto the evaluator. When leaving, clear them so the next primary
	// runs under the project/session ruleset alone.
	if m.evaluator != nil {
		if next.PlanMode {
			// Plan rules glob `.prompto/plans/*.md`, so they
			// only need cwd — the model picks the slug.
			m.evaluator.SetAgentRules(permission.PlanRules(m.cwd))
			// Bash fast-path. Read-only commands auto-allow,
			// mutating ones auto-deny, unknown fall through to the
			// `bash *: Ask` rule installed above.
			m.evaluator.SetBashClassifier(permission.ClassifyBash)
		} else {
			m.evaluator.SetAgentRules(nil)
			m.evaluator.SetBashClassifier(nil)
		}
	}

	// Leaving plan → build with an existing plan file: queue the
	// BUILD_SWITCH reminder onto the agent's notifier so the next turn
	// orients on the plan file. Notifier may be nil in headless tests.
	if m.previousAgent == "plan" && next.Name == "build" {
		planFile := m.resolvePlanPath()
		if planFile != "" {
			if _, err := os.Stat(planFile); err == nil {
				if n := m.agent.Notifier(); n != nil {
					n.QueueOneShot(agent.BuildSwitchReminderBody(planFile))
				}
			}
		}
	}

	m.chat.AppendSystemMessage("agent: " + next.Name)
	return next.Name
}

// setState transitions the working-state machine. Detail is the verbose
// label suffix (tool name, approval target, etc.); pass "" when not
// applicable. Restarts the spinner ticker on first activation. Also
// gates the per-tool elapsed timer: entering StateToolRunning starts the
// clock, leaving it (or transitioning to a different active state)
// stops and resets it.
func (m *AppModel) setState(s WorkingState, detail string) tea.Cmd {
	wasIdle := !m.workingState.IsActive()
	prev := m.workingState
	m.workingState = s
	m.workingDetail = detail
	switch {
	case s == StateToolRunning && prev != StateToolRunning:
		m.toolStartedAt = time.Now()
	case s != StateToolRunning && prev == StateToolRunning:
		m.toolStartedAt = time.Time{}
		m.activeToolCallID = ""
		m.activeToolSubagent = ""
	}
	// Roll a fresh promptism on every transition into StateThinking —
	// not on every render — so the indicator stays stable across
	// spinner ticks instead of flickering through the list each frame.
	if s == StateThinking && prev != StateThinking {
		m.thinkingWord = pickPromptism()
		m.thinkingWordPickedAt = time.Now()
		m.shimmerFrame = 0
	}
	var cmds []tea.Cmd
	if s.IsActive() && wasIdle {
		// Activation kicks off the spinner ticker. The ticker is
		// self-perpetuating: spinner.Update returns a new Tick cmd
		// every frame while we still feed it TickMsgs.
		cmds = append(cmds, m.spinner.Tick)
	}
	if s == StateAwaitingApproval && prev != StateAwaitingApproval {
		cmds = append(cmds, terminalPingCmd())
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

// promptismRotateAfter is how long a single promptism stays on screen
// before maybeRotatePromptism swaps in a fresh one. Tuned to "long
// enough that the user notices the change but short enough that a
// multi-minute reasoning burst doesn't feel stuck on one word."
const promptismRotateAfter = 30 * time.Second

// maybeRotatePromptism swaps in a fresh promptism once the current
// pick has been on screen for promptismRotateAfter while StateThinking
// is still active. Driven from the spinner-tick handler so it rides
// the existing render cadence — no extra ticker. No-op for non-
// thinking states; the next transition into StateThinking re-rolls
// via setState regardless of this method's last action.
func (m *AppModel) maybeRotatePromptism(now time.Time) {
	if m.workingState != StateThinking {
		return
	}
	if !m.thinkingWordPickedAt.IsZero() && now.Sub(m.thinkingWordPickedAt) < promptismRotateAfter {
		return
	}
	m.thinkingWord = pickPromptismExcluding(m.thinkingWord)
	m.thinkingWordPickedAt = now
	m.shimmerFrame = 0
}

// startThinking begins a fresh thinking-time accounting interval at the
// start of a turn. Any banked time from a prior turn is discarded.
func (m *AppModel) startThinking(now time.Time) {
	m.thinkingAccum = 0
	m.thinkingResumedAt = now
}

// pauseThinking banks the time elapsed since the last resume into the
// accumulator and marks the timer paused. No-op when already paused.
func (m *AppModel) pauseThinking(now time.Time) {
	if m.thinkingResumedAt.IsZero() {
		return
	}
	m.thinkingAccum += now.Sub(m.thinkingResumedAt)
	m.thinkingResumedAt = time.Time{}
}

// resumeThinking starts a new active interval. No-op when already
// running, so duplicate resumes (e.g., a key handler that fires while
// the timer is already live) cannot double-count.
func (m *AppModel) resumeThinking(now time.Time) {
	if !m.thinkingResumedAt.IsZero() {
		return
	}
	m.thinkingResumedAt = now
}

// thinkingElapsed returns the total time the agent has spent in active
// states this turn — banked past intervals plus the live interval since
// the last resume, when thinkingResumedAt is non-zero.
func (m *AppModel) thinkingElapsed(now time.Time) time.Duration {
	if m.thinkingResumedAt.IsZero() {
		return m.thinkingAccum
	}
	return m.thinkingAccum + now.Sub(m.thinkingResumedAt)
}

// Init returns the initial commands.
func (m AppModel) Init() tea.Cmd {
	return m.input.Focus()
}

// Update handles all messages.
func (m AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.BackgroundColorMsg:
		m.chat.SetDark(msg.IsDark())
		m.thinking.SetDark(msg.IsDark())
		return m, nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.relayout()
		return m, nil

	case ToolApprovalRequestMsg:
		m.pending = msg.Req
		// Force the chat view back so the approval prompt is visible and
		// ESC keeps its "deny" semantics without conflicting with the
		// overlay's "close" semantics. Re-opening with Ctrl+O is always
		// one keystroke away.
		wasThinking := m.thinkingVisible
		m.thinkingVisible = false
		m.helpVisible = false
		if wasThinking {
			m.relayout() // sticky panel re-emerges; chat shrinks back
		}

		// plan_exit gets a dedicated full-screen overlay
		// rendering the plan markdown so the user can review before
		// approving. Pre-flight validation has already passed (run.go
		// short-circuits invalid plans into tool errors before this
		// point), so reading the file here is safe; on filesystem
		// errors we fall back to the standard one-line approval prompt
		// rather than blocking the run.
		if msg.Req.Name == "plan_exit" {
			if approval, ok := m.buildPlanApproval(); ok {
				m.planApproval = approval
				m.planApprovalVisible = true
				m.relayout()
				m.pauseThinking(time.Now())
				cmd := m.setState(StateAwaitingApproval, "plan_exit")
				return m, cmd
			}
			// Fall through: building the overlay failed; surface the
			// standard one-line approval below so the user can still
			// decide.
		}

		sig := toolCallSig(msg.Req.Name, string(msg.Req.Input))
		if m.lastToolCallSig != sig {
			// ToolApprovalRequestMsg arrives without an id (CanUseTool
			// doesn't carry one). Pass "" — the row gets keyed only on
			// its signature; when EventToolCallStarted lands later
			// with the real id, AppendToolCall promotes the existing
			// row instead of duplicating.
			m.chat.AppendToolCallWithOrigin("", msg.Req.Name, string(msg.Req.Input), msg.Req.Disp, msg.Req.Subagent)
			m.lastToolCallSig = sig
		}
		m.chat.AppendApprovalPrompt(msg.Req.IsReadOnly, msg.Req.Subagent)
		m.pauseThinking(time.Now())
		cmd := m.setState(StateAwaitingApproval, summarizeToolCall(msg.Req.Name, string(msg.Req.Input)))
		return m, cmd

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		// Only keep ticking while we're in an active state. When idle,
		// drop the cmd so the chain stops.
		if !m.workingState.IsActive() {
			return m, nil
		}
		// Piggyback the periodic promptism rotation on the spinner
		// tick — it already fires while a state is active, so no
		// extra ticker is needed. The helper is a cheap time-since
		// check for non-thinking states; the actual roll only happens
		// once per promptismRotateAfter window.
		m.maybeRotatePromptism(time.Now())
		// Advance the shimmer frame on every spinner tick so the
		// highlight sweeps across the promptism at the same cadence
		// as the spinner glyph rotation. Bounded by re-pick (setState
		// / maybeRotatePromptism) which zero it out for a fresh sweep.
		m.shimmerFrame++
		return m, cmd

	case elapsedTickMsg:
		if m.streaming {
			elapsed := m.thinkingElapsed(time.Now())
			m.status.elapsedSec = int(elapsed.Seconds())
			m.todoPanel.SetMeter(true, elapsed, m.status.inputTokens)
			return m, elapsedTickCmd()
		}
		return m, nil

	case tea.KeyPressMsg:
		if handled, cmd := m.handleKeyPress(msg); handled {
			return m, cmd
		}
		// Unhandled key — fall through to the textarea-feed path below.

	case agentEventMsg:
		var stateCmd tea.Cmd
		switch msg.event.Type {
		case agent.EventTextDelta:
			m.chat.AppendDelta(msg.event.Delta)
			m.thinking.MarkInactive()
			if !m.streamStarted {
				m.streamStarted = true
				stateCmd = m.setState(StateStreaming, "")
			}

		case agent.EventThinkingDelta:
			m.thinking.AppendDelta(msg.event.Delta)

		case agent.EventToolCallStarted:
			// Render the tool-call row here so project- and session-allowed
			// calls (which never reach CanUseTool) still appear in the
			// thread. Always forward to the chat — AppendToolCallWithOrigin
			// dedups by id (existing row → no-op) and promotes an empty-id
			// approval-path row by stamping this event's id onto it. Skipping
			// the call on signature match would block that promotion and
			// leave the row unkeyed, which causes parallel-dispatch results
			// to misattribute via the unfilled-row fallback in findToolCallRow.
			m.chat.FinishStreaming()
			m.thinking.MarkInactive()
			m.chat.AppendToolCallWithOrigin(msg.event.ToolCallID, msg.event.ToolName, msg.event.ToolArgs, msg.event.ToolDisp, msg.event.ToolSubagent)
			m.lastToolCallSig = toolCallSig(msg.event.ToolName, msg.event.ToolArgs)
			if msg.event.ToolName == "todowrite" {
				m.pendingTodoArgs = msg.event.ToolArgs
			}
			// Only enter StateToolRunning when no approval prompt is
			// outstanding for this call. m.pending != nil means a
			// ToolApprovalRequestMsg already won the race against this
			// EventToolCallStarted (the events channel and p.Send reach
			// the tea loop through different paths) — the indicator is
			// currently StateAwaitingApproval and must stay there until
			// the user resolves the prompt. The approval-grant path
			// transitions into StateToolRunning when [y/a/p/f] is pressed.
			if m.pending == nil {
				detail := describeRunningTool(msg.event.ToolName, msg.event.ToolArgs)
				stateCmd = m.setState(StateToolRunning, detail)
				m.activeToolCallID = msg.event.ToolCallID
				m.activeToolSubagent = msg.event.ToolSubagent
			}
			m.streamStarted = false // next turn's first delta re-triggers Streaming

		case agent.EventToolStatus:
			// Per-tool heartbeat from inside Tool.Execute. Only refresh
			// the indicator when the status belongs to the call we're
			// currently rendering — under parallel dispatch a status
			// from a finished call could otherwise overwrite the live
			// one. Falls through silently when no match: the status
			// just doesn't appear, no error.
			if m.workingState == StateToolRunning && msg.event.ToolCallID != "" && msg.event.ToolCallID == m.activeToolCallID {
				if status := strings.TrimSpace(msg.event.ToolDisp); status != "" {
					m.workingDetail = status
				}
			}

		case agent.EventSubagentStep:
			// Subagent end-of-step heartbeat. Renders a dim one-liner
			// in the chat so the user can watch the child make
			// progress instead of staring at a static indicator.
			m.chat.AppendSubagentHeartbeat(msg.event.ToolSubagent, msg.event.ToolDisp)

		case agent.EventToolCallDone:
			m.chat.AppendToolResult(msg.event.ToolCallID, msg.event.ToolName, msg.event.ToolResult, msg.event.ToolError, msg.event.ToolDisp)
			if msg.event.ToolName == "todowrite" {
				if !msg.event.ToolError {
					m.applyTodoUpdate(m.pendingTodoArgs)
					m.todoPanel.SetTodos(m.todos)
					m.relayout() // panel height may have changed
				}
				m.pendingTodoArgs = ""
			}
			stateCmd = m.setState(StateThinking, "")

		case agent.EventCompactStart:
			m.preCompactState = m.workingState
			m.preCompactDetail = m.workingDetail
			stateCmd = m.setState(StateCompacting, "")

		case agent.EventCompactDone:
			restored := m.preCompactState
			if restored == StateIdle {
				restored = StateThinking
			}
			stateCmd = m.setState(restored, m.preCompactDetail)

		case agent.EventUsageReport:
			if msg.event.Usage != nil {
				m.status.inputTokens += msg.event.Usage.InputTokens
				m.status.outputTokens += msg.event.Usage.OutputTokens
				if msg.event.Usage.InputTokens > 0 {
					m.status.contextTokens = msg.event.Usage.InputTokens
				}
				// Refresh the panel's token chip immediately so the
				// header doesn't lag the next 1 Hz elapsed tick.
				m.todoPanel.SetMeter(m.streaming, m.thinkingElapsed(time.Now()), m.status.inputTokens)
			}

		case agent.EventError:
			// Errors render through AppendSystemMessage rather than
			// AppendDelta. AppendDelta funnels text through the
			// glamour markdown renderer, which treats ANSI escape
			// codes as literal characters — pre-styled error text
			// then appears in chat as raw "[91m[m..." sequences.
			// AppendSystemMessage emits the text directly through
			// systemStyle, no markdown pass.
			m.chat.AppendSystemMessage("error: " + msg.event.Error.Error())

		case agent.EventTurnComplete:
			m.chat.FinishStreaming()
			m.thinking.MarkInactive()
			// streaming flag is cleared on agentDoneMsg so the Done sentinel
			// is captured before re-enabling input.

		case agent.EventCompactionApplied:
			// Pre-call compaction or reactive-retry summarization fired.
			// Display a short dim line so the user knows the conversation
			// was trimmed. ToolDisp carries the reason (e.g., "summarized
			// 34 messages").
			reason := msg.event.ToolDisp
			if reason == "" {
				reason = "compaction applied"
			}
			m.chat.AppendSystemMessage("[" + reason + "]")

		case agent.EventPlanApproved:
			// plan_exit succeeded. Stash the plan path on
			// AppModel so the agentDoneMsg handler — which fires after
			// the plan agent's run terminates with ErrEndTurn — can
			// flip to the build agent and kick off a fresh run with
			// the synthesised "execute it" user message. Acting here
			// inside the events loop would race the still-running run.
			m.pendingPlanApproval = msg.event.ToolDisp
		}

		// Re-arm the events listener for the next event — UNLESS this
		// was the terminal event (channel is already closing, the next
		// read would just be ok=false and the goroutine would exit).
		// agentDoneMsg fires separately via waitForAgentDone.
		if msg.event.Type != agent.EventTurnComplete {
			cmds = append(cmds, waitForAgentEvent(m.events))
		}
		if stateCmd != nil {
			cmds = append(cmds, stateCmd)
		}
		return m, tea.Batch(cmds...)

	case agentDoneMsg:
		m.streaming = false
		m.status.streaming = false
		m.status.elapsedSec = 0
		m.thinkingAccum = 0
		m.thinkingResumedAt = time.Time{}
		m.input.SetDisabled(false)
		m.lastToolCallSig = "" // next turn's tool calls always render fresh
		// Drop references to the (now-closed) channels and the (now-
		// cancelled) ctx cancel func so a post-turn Ctrl+C, /tab agent
		// cycle, or any new turn doesn't act on stale state. The next
		// handleEnterKey populates these fresh.
		m.cancelFn = nil
		m.events = nil
		m.done = nil
		m.setState(StateIdle, "")
		// Drop the panel's streaming meter so the header line vanishes
		// while idle. Token total persists for the next turn's resume.
		m.todoPanel.SetMeter(false, 0, m.status.inputTokens)
		m.relayout()
		if msg.reason != nil && !errors.Is(msg.reason, agent.ErrEndTurn) && !errors.Is(msg.reason, agent.ErrUserDenied) {
			m.chat.AppendSystemMessage("turn ended: " + msg.reason.Error())
		}

		// A successful plan_exit during the just-ended run
		// staged a flip. Now that the run has fully wound down (the
		// run-loop returned and the events channel closed), perform
		// the agent change, queue the BUILD_SWITCH reminder, and
		// kick off a fresh build-agent run with the synthesised
		// "execute the plan" user message.
		if m.pendingPlanApproval != "" {
			planPath := m.pendingPlanApproval
			m.pendingPlanApproval = ""
			cmd := m.completePlanApproval(planPath)
			return m, cmd
		}
		return m, nil
	}

	if !m.streaming {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		cmds = append(cmds, cmd)
	}

	prevSuggHeight := m.suggestions.Height()
	if m.streaming || m.helpVisible || m.thinkingVisible || m.modelPickerVisible || m.planApprovalVisible || m.pending != nil {
		m.suggestions.Hide()
	} else {
		m.suggestions.Update(m.input.Value())
	}
	if prevSuggHeight != m.suggestions.Height() {
		// The popup occupies a variable number of rows (one per match,
		// capped at suggestionsMaxVisible). Without a relayout the chat
		// viewport stays sized for the previous match count, so when
		// typing narrows the matches the popup shrinks but the viewport
		// doesn't reclaim those rows — the input bar visibly drifts up
		// the terminal as the user types.
		m.relayout()
	}

	switch msg.(type) {
	case tea.WindowSizeMsg, tea.MouseWheelMsg, tea.MouseMotionMsg:
		var cmd tea.Cmd
		m.chat, cmd = m.chat.Update(msg)
		cmds = append(cmds, cmd)
		if m.thinkingVisible {
			var tcmd tea.Cmd
			m.thinking, tcmd = m.thinking.Update(msg)
			cmds = append(cmds, tcmd)
		}
	}

	return m, tea.Batch(cmds...)
}

// inputHeight returns the row count the input panel currently occupies
// (textarea height + manual border rows). The textarea is currently
// fixed at 1 visible row, but pulling from the model lets a future
// auto-grow input work without re-doing layout math.
func (m AppModel) inputHeight() int {
	return 1 + inputBorderRows
}

// relayout recomputes viewport size and pushes width to children. Call
// whenever window size changes or whenever banner / todo-panel
// visibility flips so the chat viewport snaps to the right dimension.
func (m *AppModel) relayout() {
	if m.width <= 0 || m.height <= 0 {
		return
	}
	banner := 0
	if !m.hasUserMsg {
		banner = welcomeHeight(m.width)
	}
	m.todoPanel.SetWidth(m.width)
	todoHeight := 0
	if !m.thinkingVisible {
		// The thinking overlay (Ctrl+O) is a full-screen read view; the
		// panel hides so the reasoning text gets every available row.
		todoHeight = m.todoPanel.Height()
	}
	m.suggestions.SetWidth(m.width)
	suggestionsHeight := m.suggestions.Height()
	viewportHeight := m.height - banner - indicatorHeight - todoHeight - suggestionsHeight - m.inputHeight() - statusHeight - gapHeight
	if viewportHeight < 1 {
		viewportHeight = 1
	}
	m.chat.SetSize(m.width, viewportHeight)
	m.thinking.SetSize(m.width, viewportHeight)
	m.input.SetWidth(m.width)
	m.status.width = m.width
}

// View renders the full TUI.
func (m AppModel) View() tea.View {
	if m.quitting {
		return tea.NewView("")
	}
	parts := []string{}
	if !m.hasUserMsg {
		if banner := renderWelcome(m.width, m.welcome); banner != "" {
			parts = append(parts, banner)
		}
	}
	chatRegion := m.chat.View()
	if m.helpVisible && m.cmdRegistry != nil {
		chatRegion = renderHelpOverlay(m.chat.width, m.chat.height, m.cmdRegistry)
	}
	if m.thinkingVisible {
		chatRegion = m.thinking.View()
	}
	if m.modelPickerVisible {
		// Mirror chat dimensions onto the picker each frame so it
		// re-centres on terminal resize without needing a separate
		// Resize handler.
		m.modelPicker.SetSize(m.chat.width, m.chat.height)
		chatRegion = m.modelPicker.View()
	}
	if m.planApprovalVisible {
		// Same pattern as modelPicker — push current chat dims into
		// the overlay each frame so resize is automatic.
		m.planApproval.SetSize(m.chat.width, m.chat.height)
		chatRegion = m.planApproval.View()
	}
	parts = append(parts,
		chatRegion,
		m.renderIndicatorRow(),
	)
	// The thinking overlay (Ctrl+O) is a focused read view; the
	// sticky panel hides so the reasoning text gets every available
	// row. relayout() applies the symmetric height adjustment.
	if !m.thinkingVisible {
		if todoView := m.todoPanel.View(m.spinner); todoView != "" {
			parts = append(parts, todoView)
		}
	}
	if sv := m.suggestions.View(); sv != "" {
		parts = append(parts, sv)
	}
	parts = append(parts,
		m.input.View(),
		m.status.View(),
	)
	v := tea.NewView(lipgloss.JoinVertical(lipgloss.Left, parts...))
	// Mouse capture is a trade-off: cell-motion lets the chat
	// viewport scroll on mouse wheel, but it also intercepts
	// click-drag — which means the terminal's native text selection
	// stops working while the program is running. Workarounds:
	//
	//   - Per-selection: hold Shift (most terminals) or Option (iTerm2)
	//     while dragging. The terminal bypasses mouse reporting for
	//     the duration of the drag.
	//   - Per-session: press Ctrl+T to flip m.mouseCapture, which
	//     toggles MouseMode here and lets you select freely until
	//     toggled back. PgUp/PgDn still scroll the chat region in
	//     either mode, so we don't lose paging by going off.
	//
	// Bubbletea framework-level "wheel only" mouse mode does not
	// exist (see charmbracelet/bubbletea#162) — these are the only
	// options we have.
	if m.mouseCapture {
		v.MouseMode = tea.MouseModeCellMotion
	}
	return v
}

// renderIndicatorRow returns the indicator block. Always returns
// exactly two rendered rows (a leading blank spacer + the label row,
// blank when idle) so the layout remains stable across state
// transitions and the sticky label never sits flush against the chat
// transcript above it.
func (m AppModel) renderIndicatorRow() string {
	frame := ""
	if m.workingState.IsActive() {
		frame = m.spinner.View()
	}
	elapsed := ""
	if m.workingState == StateToolRunning && !m.toolStartedAt.IsZero() {
		if d := time.Since(m.toolStartedAt); d >= time.Second {
			elapsed = formatElapsed(int(d.Seconds()))
		}
	}
	detail := m.workingDetail
	if m.workingState == StateToolRunning && m.activeToolSubagent != "" {
		detail = taskDisplayName(m.activeToolSubagent) + " · " + detail
	}
	rendered := renderIndicator(m.workingState, detail, elapsed, frame, m.thinkingWord, m.shimmerFrame)
	if rendered == "" {
		rendered = " "
	}
	return " \n" + rendered
}
