package agent

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"

	"github.com/marcomoesman/prompto/internal/api"
)

// EventType identifies the kind of streaming event emitted by Run.
type EventType int

const (
	// EventTextDelta carries a streamed text chunk in the Delta field.
	EventTextDelta EventType = iota
	// EventToolCallStarted fires before a tool executes. CanUseTool has
	// already been consulted — the event is informational.
	EventToolCallStarted
	// EventToolCallDone fires after a tool executes. Carries ToolResult,
	// ToolError.
	EventToolCallDone
	// EventUsageReport carries an api.Usage pointer.
	EventUsageReport
	// EventError carries a non-terminal error. Fatal errors go on Done.
	EventError
	// EventTurnComplete is emitted as the last event before the channels close.
	EventTurnComplete
	// EventCompactionApplied fires after MaybeCompact or ForceSummarize
	// actually modifies the conversation. Carries a short user-facing
	// reason in ToolDisp (reused field; simpler than a new one).
	EventCompactionApplied
	// EventCompactStart fires immediately before the compactor inspects
	// the conversation. Pairs with EventCompactDone. The TUI uses these
	// to drive a "Compacting context…" indicator during multi-second
	// summarization API calls. Noop compactions emit the pair too — the
	// gap is microseconds and visually invisible.
	EventCompactStart
	// EventCompactDone fires after MaybeCompact / ForceSummarize returns,
	// regardless of outcome. Always paired with a prior EventCompactStart.
	EventCompactDone
	// EventThinkingDelta carries a streamed chunk of extended-thinking
	// reasoning text in Delta. Fires zero-or-more times per turn before
	// any EventTextDelta. The TUI uses these to populate the Ctrl+O
	// overlay; the run loop also accumulates them for round-trip on
	// signature-bearing assistant messages.
	EventThinkingDelta
	// EventPlanApproved fires after a successful plan_exit tool call.
	// The TUI consumes it to flip the active agent from plan to build,
	// queue the BUILD_SWITCH reminder, and synthesise a "plan
	// approved — execute it" user message. Carries the absolute plan
	// file path in ToolDisp (reused field; the existing `BuildSwitch
	// ReminderBody` consumer already takes a path string).
	EventPlanApproved
	// EventToolStatus is an optional per-tool heartbeat published from
	// inside Tool.Execute via tc.Status / tc.Publish. ToolCallID identifies
	// the in-flight call; ToolDisp carries the human-readable status
	// string ("fetching", "rendering with headless browser"). The TUI
	// uses it to refresh the StateToolRunning indicator label so the user
	// can see what stage a long-running tool is at. Fire-and-forget:
	// non-blocking sends, so updates may be dropped under buffer pressure.
	EventToolStatus
	// EventSubagentStep is the end-of-step heartbeat emitted by a
	// subagent's run loop. ToolSubagent carries the subagent's name;
	// ToolDisp carries a formatted summary line ("3 calls so far, last:
	// WebFetch(host)"). The parent forwards it through the task tool so
	// the user can see the child's progress without waiting for the
	// final summary.
	EventSubagentStep
)

// Event is one item on RunResult.Events.
type Event struct {
	Type     EventType
	Delta    string     // EventTextDelta
	Usage    *api.Usage // EventUsageReport
	Error    error      // EventError
	ToolName string     // EventToolCallStarted / EventToolCallDone
	ToolArgs string     // EventToolCallStarted: raw JSON args
	// ToolCallID is the provider-assigned tool_use_id that ties an
	// EventToolCallStarted to its matching EventToolCallDone. The TUI
	// uses it to attach the result summary to the correct call row,
	// which matters when the agent dispatches concurrency-safe calls
	// in parallel: Started events fire in dispatch order, Done events
	// fire in completion order, and signature-based pairing fails as
	// soon as two calls share the same (name, args).
	ToolCallID string
	// ToolDisp serves two roles depending on Type:
	//   - EventToolCallStarted: FormatForDisplay text (the call header).
	//   - EventToolCallDone: the optional success summary populated from
	//     Result.DisplaySummary (e.g. "Received 223.3KB (200 OK)"); empty
	//     on tools that don't produce one.
	//   - EventCompactionApplied: short reason string.
	ToolDisp     string
	ToolSubagent string // EventToolCallStarted: spawning subagent name (e.g. "explore"); empty for primary
	ToolResult   string // EventToolCallDone
	ToolError    bool   // EventToolCallDone
}

// RunInput is declared before Run per CLAUDE.md (function input struct lives
// next to its consumer).
type RunInput struct {
	Conversation *Conversation
	Model        string // override Agent.model; empty means use Agent.model
	MaxSteps     int    // 0 defaults to 25
	CanUseTool   CanUseTool
	ExtraSystem  string // AGENTS.md content

	// Persistence (optional): when Store is nil, the loop skips all DB
	// writes. When SessionID is empty but Store is non-nil, the loop
	// still runs but without persistence (used by tests).
	SessionID   string
	Store       Store
	FileChanges FileChangeSink

	// Agent identity + lineage. AgentName resolves an AgentDefinition
	// from the registry; empty means "build". ParentSessionID is empty
	// for primary runs and set to the parent's session id for subagents
	// (the task tool propagates this). SpawnTask is non-nil only for
	// primary runs; subagents get nil because AllAgentDisallowedTools
	// strips the task tool from their resolver anyway.
	AgentName       string
	ParentSessionID string
	SpawnTask       TaskSpawner

	// Nested AGENTS.md walk-up bound: tools that walk up from a file
	// path (today: read) stop at this directory exclusive — the eager
	// pass already covered it and above. Empty disables the lazy walk.
	AgentsMDLoadRoot string
}

// RunResult is what Run returns. The caller reads Events until it closes,
// then reads the single value on Done. Agent closes both channels.
type RunResult struct {
	Events <-chan Event
	Done   <-chan error
}

// Run executes the agentic loop. See package docs for the contract.
func (a *Agent) Run(ctx context.Context, in RunInput) RunResult {
	events := make(chan Event, 64)
	done := make(chan error, 1)

	maxSteps := in.MaxSteps
	if maxSteps <= 0 {
		// Internal fallback for callers that didn't thread the value
		// through (subagents, tests). Production callers from main.go
		// pass cfg.Agent.MaxSteps which has already been defaulted via
		// config.ApplyDefaults — they will never hit this branch.
		maxSteps = 100
	}
	model := in.Model
	if model == "" {
		model = a.model
	}

	// Resolve the agent definition. Unknown / empty names fall back to
	// "build" — which always exists in DefaultRegistry. Allows callers to
	// pass RunInput{} and still get a working primary.
	agentName := in.AgentName
	if agentName == "" {
		agentName = "build"
	}
	def, ok := a.registry.Resolve(agentName)
	if !ok {
		def, _ = a.registry.Resolve("build")
	}

	// Filter the resolver per-agent. AllAgentDisallowedTools (task,
	// todowrite) is subtracted only for subagents — primaries (build, plan)
	// keep both. The build agent's empty allowlist passes through.
	isSubagent := def.Mode == ModeSubagent
	tools := a.tools
	if !def.AllowsAllTools() || isSubagent {
		tools = NewFilteredResolver(a.tools, def.Tools, isSubagent)
	}

	go func() {
		var reason error
		// Recover from panics in runLoop (provider bugs, nil deref, etc.)
		// and turn them into an EventError + done error before closing
		// channels. Without this the goroutine dies mid-stream and leaves
		// events/done open forever — the TUI's select on those channels
		// hangs indefinitely with no message to the user.
		defer func() {
			if r := recover(); r != nil {
				reason = fmt.Errorf("agent: panic: %v", r)
				events <- Event{Type: EventError, Error: reason}
			}
			events <- Event{Type: EventTurnComplete}
			done <- reason
			close(events)
			close(done)
		}()
		reason = a.runLoop(ctx, runLoopParams{
			events:           events,
			conv:             in.Conversation,
			model:            model,
			maxSteps:         maxSteps,
			canUseTool:       in.CanUseTool,
			extraSystem:      in.ExtraSystem,
			sessionID:        in.SessionID,
			parentSessionID:  in.ParentSessionID,
			agentDef:         def,
			isSubagent:       isSubagent,
			tools:            tools,
			spawnTask:        in.SpawnTask,
			store:            in.Store,
			fileChanges:      in.FileChanges,
			agentsMDLoadRoot: in.AgentsMDLoadRoot,
		})
	}()

	return RunResult{Events: events, Done: done}
}

type runLoopParams struct {
	events           chan<- Event
	conv             *Conversation
	model            string
	maxSteps         int
	canUseTool       CanUseTool
	extraSystem      string
	sessionID        string
	parentSessionID  string
	agentDef         AgentDefinition
	isSubagent       bool
	tools            ToolResolver
	spawnTask        TaskSpawner
	store            Store          // may be nil
	fileChanges      FileChangeSink // may be nil
	agentsMDLoadRoot string
}

type toolCallAccumulator struct {
	id   string
	name string
	args strings.Builder
}

// thinkingAccumulator collects the streamed pieces of one extended-
// thinking content block. text/signature populate from thinking_delta /
// signature_delta events; redacted/data populate up-front from
// content_block_start when the server returns a redacted block. blocks
// are keyed by content-block index so out-of-order deltas across
// concurrent blocks don't cross-contaminate.
type thinkingAccumulator struct {
	index     int
	text      strings.Builder
	signature string
	redacted  bool
	data      string
}

// runLoop drives the conversation. Returns the termination reason
// (ErrEndTurn, ErrMaxSteps, ErrUserDenied, ErrContextLimit, or a
// wrapped provider/persistence error).
//
// Each iteration of the outer for-loop is one "turn" against the
// provider, capped by p.maxSteps. Inside the turn the inner for-loop
// is a retry-within-step: a context-limit error reactively triggers
// ForceSummarize and re-enters the call (bounded by
// reactiveCompactAttempted), and an empty-assistant turn from a
// confused open-weights model gets one nudge before giving up
// (bounded by emptyTurnNudgeUsed). The success path falls through
// to dispatch + tool_result and breaks out of the inner loop so the
// outer loop advances to the next step.
//
// Per-turn phases (inner loop body):
//
//  1. pre-call compaction (threshold-based summarize / clear)
//  2. load persisted todos for the volatile system-prompt section
//  3. build the system prompt + inject one-shot reminders
//  4. assemble api.CompleteParams (system, messages, tools)
//  5. provider.Complete: stream events, accumulate thinking / text /
//     tool-call deltas, capture usage + stopReason
//  6. write a request-log entry
//  7. error recovery — context-limit triggers ForceSummarize + retry
//  8. build the assistant message (thinking blocks, text, tool_use)
//     and persist it
//  9. empty-turn check — clean end-of-turn or one-shot nudge + retry
//  10. plan phase: evaluate every tool call against the permission
//     policy, ask the user when needed
//  11. dispatch + emission: run tools (concurrent batches under an
//     errgroup for concurrency-safe ones), build the tool_result
//     message, append + persist
func (a *Agent) runLoop(ctx context.Context, p runLoopParams) error {
	cwd, _ := os.Getwd()
	platform := runtime.GOOS + "/" + runtime.GOARCH
	var workspaceSummary WorkspaceSummary
	var verificationHint VerificationHint
	if a.workspaceHintsEnabled() {
		workspaceSummary = DetectWorkspace(cwd)
		verificationHint = DetectVerification(cwd)
	}

	tools := p.tools
	if tools == nil {
		tools = a.tools
	}
	if a.compactToolSchemasEnabled() {
		tools = NewCompactToolSchemaResolver(tools)
	}

	fileState := NewFileState()
	// Subagents do not own a session-level todo list; their child session
	// has its own slice (always empty for now). Stamp SaveTodos only for
	// primaries so the closure short-circuits when subagents bypass the
	// resolver filter (defensive — todowrite is already stripped).
	var saveTodos TodoSaver
	if !p.isSubagent && a.todos != nil {
		store := a.todos
		saveTodos = func(ctx context.Context, sessionID string, todos []Todo) error {
			return store.SaveTodos(ctx, sessionID, todos)
		}
	}

	// Per-run nested-AGENTS.md tracking. The map is mutex-guarded because
	// concurrency-safe tools dispatch under errgroup; subagents leave
	// the closures nil — their reminder surface is owned by the primary.
	//
	// The map is bounded — long runs that touch many AGENTS.md paths
	// (tool-heavy explorations of deep directory trees) shouldn't grow
	// it without limit. Once the cap is reached, the oldest entry by
	// insertion order evicts on the next mark; the practical effect of
	// re-emitting a reminder for an evicted path is harmless (one
	// extra system message), and the cap is large enough that any
	// realistic workspace stays under it.
	const seenAgentsMDCap = 256
	var (
		seenAgentsMD    map[string]struct{}
		seenAgentsOrder []string
		seenMu          sync.Mutex
		queueReminderFn func(string)
		hasSeenFn       func(string) bool
		markSeenFn      func(string)
	)
	if !p.isSubagent {
		seenAgentsMD = make(map[string]struct{})
		hasSeenFn = func(path string) bool {
			seenMu.Lock()
			defer seenMu.Unlock()
			_, ok := seenAgentsMD[path]
			return ok
		}
		markSeenFn = func(path string) {
			seenMu.Lock()
			defer seenMu.Unlock()
			if _, ok := seenAgentsMD[path]; ok {
				return
			}
			if len(seenAgentsOrder) >= seenAgentsMDCap {
				oldest := seenAgentsOrder[0]
				seenAgentsOrder = seenAgentsOrder[1:]
				delete(seenAgentsMD, oldest)
			}
			seenAgentsMD[path] = struct{}{}
			seenAgentsOrder = append(seenAgentsOrder, path)
		}
		if a.notifier != nil {
			notifier := a.notifier
			queueReminderFn = func(text string) {
				if text == "" {
					return
				}
				// Queue the raw body — InjectReminders wraps every body
				// in <system-reminder> at injection time, so wrapping
				// here would produce nested tags and burn tokens.
				notifier.QueueOneShot(text)
			}
		}
	}

	// Non-blocking publisher. Drops on a full events buffer so a slow
	// TUI consumer can never wedge a tool goroutine. Used for heartbeat
	// events (EventToolStatus, EventSubagentStep forwarded through
	// factory) where dropping is preferable to stalling work.
	publish := func(ev Event) {
		select {
		case p.events <- ev:
		default:
		}
	}

	tc := ToolContext{
		Cwd:              cwd,
		AllowedRoots:     []string{cwd},
		FileState:        fileState,
		RequestLogger:    a.logger,
		FileChanges:      p.fileChanges,
		SessionID:        p.sessionID,
		ParentSessionID:  p.parentSessionID,
		AgentName:        p.agentDef.Name,
		SpawnTask:        p.spawnTask,
		SaveTodos:        saveTodos,
		AgentsMDLoadRoot: p.agentsMDLoadRoot,
		QueueReminder:    queueReminderFn,
		HasSeenAgentsMD:  hasSeenFn,
		MarkSeenAgentsMD: markSeenFn,
		Publish:          publish,
	}
	aggregator := NewTurnAggregator(0)
	var loopGuard *LoopGuard
	if !p.isSubagent && a.loopGuardsEnabled() {
		loopGuard = NewLoopGuard()
	}

	// Resolve the plan-file path. The model picks a slug at first
	// plan write; the chosen path is persisted on the session row.
	// Resolution order:
	//   1. Persisted `sessions.plan_path` column.
	//   2. Legacy `<cwd>/.prompto/plans/<sessionID>.md` if it exists
	//      on disk (backward compat for sessions written before the
	//      model-chosen-slug path landed).
	//   3. Empty — signals "in plan mode, no file yet"; the
	//      PlanModeChecker reminder asks the model to pick a slug.
	planFilePath := ""
	if p.agentDef.PlanMode {
		persisted := ""
		if p.store != nil && p.sessionID != "" {
			loaded, err := p.store.LoadPlanPath(ctx, p.sessionID)
			if err != nil {
				p.events <- Event{Type: EventError, Error: fmt.Errorf("agent: load plan_path: %w", err)}
			} else {
				persisted = loaded
			}
		}
		resolved := ResolvePlanPath(ResolvePlanPathInput{
			Cwd:           cwd,
			SessionID:     p.sessionID,
			PersistedPath: persisted,
		})
		if persisted != "" {
			planFilePath = resolved
		} else if resolved != "" {
			// Persisted is empty: only adopt the legacy fallback path
			// when the file actually exists on disk. Otherwise leave
			// planFilePath empty so the checker prompts for a slug.
			if _, err := os.Stat(resolved); err == nil {
				planFilePath = resolved
			}
		}
	}

	// Cumulative tool-call counter for the subagent end-of-step heartbeat.
	// Counts only non-denied calls — denied calls never run, so they
	// shouldn't credit towards "calls so far". Primary agents skip the
	// heartbeat entirely; the counter stays at 0 for them.
	totalToolCalls := 0

	for step := 0; step < p.maxSteps; step++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("agent: %w", err)
		}

		// Fresh aggregate budget per turn.
		aggregator.Reset()

		// Per-turn reactive-retry guard: each turn may force-summarize at
		// most once in response to an ErrContextLimit-shaped provider
		// error. Prevents runaway compaction loops.
		reactiveCompactAttempted := false

		// emptyTurnNudgeUsed bounds the empty-assistant retry to one
		// nudge per turn so a genuinely-stuck model can't loop forever.
		emptyTurnNudgeUsed := false

		// Inner retry-within-step loop: `continue` re-enters the call
		// (used by reactive compaction and the empty-turn nudge),
		// `break` advances to the next outer step, `return` ends the
		// run.
		for {
			// Pre-call compaction: threshold-based summarize or tool-result
			// clearing. Mutates p.conv in place when it acts. Failures are
			// logged as EventError but don't abort the loop — the best-effort
			// un-compacted conversation continues to the provider.
			if a.compactor != nil {
				p.events <- Event{Type: EventCompactStart}
				result := a.compactor.MaybeCompact(ctx, p.conv, p.model, tools)
				p.events <- Event{Type: EventCompactDone}
				if result.Outcome != CompactOutcomeNoop {
					p.events <- Event{Type: EventCompactionApplied, ToolDisp: result.Reason}
					if result.Outcome == CompactOutcomeSummarized && result.SummaryMessage != nil {
						if p.store != nil && p.sessionID != "" {
							if result.ReplacedThroughMessageID != "" {
								if err := p.store.AppendSummaryMessage(ctx, p.sessionID, *result.SummaryMessage, result.ReplacedThroughMessageID); err != nil {
									p.events <- Event{Type: EventError, Error: fmt.Errorf("agent: persist compaction summary: %w", err)}
								}
							} else {
								_ = p.store.AppendMessage(ctx, p.sessionID, *result.SummaryMessage, nil)
							}
						}
					}
				}
			}

			// Load this turn's persisted todos so they render into the volatile
			// section of the system prompt. Failure is non-fatal (best-effort
			// surface); we drop them so the model still gets a turn.
			var todos []Todo
			if a.todos != nil && p.sessionID != "" && !p.isSubagent {
				loaded, err := a.todos.LoadTodos(ctx, p.sessionID)
				if err != nil {
					p.events <- Event{Type: EventError, Error: fmt.Errorf("agent: load todos: %w", err)}
				} else {
					todos = loaded
				}
			}

			// Rebuild the prompt per turn. The stable prefix is computed
			// fresh each loop (cheap), filtered by the agent's tool
			// allowlist and provider profile.
			promptIn := BuildSystemPromptInput{
				Cwd:                 cwd,
				Platform:            platform,
				Model:               p.model,
				Date:                time.Now().Format("2006-01-02"),
				ProjectInstructions: p.extraSystem,
				Todos:               todos,
				Tools:               p.agentDef.EffectiveTools(),
				LocalProvider:       a.localProvider,
				WorkspaceSummary:    workspaceSummary,
				VerificationHint:    verificationHint,
			}
			var system []api.SystemBlock
			if p.agentDef.SystemPrompt != nil {
				system = p.agentDef.SystemPrompt(promptIn)
			} else {
				system = BuildSystemPrompt(promptIn)
			}
			systemHash := sha256HexBlocks(system)

			// System-reminder injection: transient, applied only to the
			// outgoing message slice. The persisted conversation is untouched.
			// Subagents skip the notifier — their checkers (verify, stale
			// todos, plan-mode) don't apply, and one-shots are owned by the
			// primary's TUI.
			messages := p.conv.Messages()
			if a.notifier != nil && !p.isSubagent {
				rc := PreTurnContext{
					Conversation: p.conv,
					SessionID:    p.sessionID,
					AgentName:    p.agentDef.Name,
					InPlanMode:   p.agentDef.PlanMode,
					PlanFilePath: planFilePath,
					Todos:        todos,
					Verification: verificationHint,
				}
				bodies := a.notifier.PreTurn(rc)
				bodies = append(bodies, a.notifier.ConsumeOneShot()...)
				messages = InjectReminders(messages, bodies)
			}

			params := api.CompleteParams{
				Model:     p.model,
				System:    system,
				Messages:  messages,
				MaxTokens: a.maxTokens,
			}
			params.Temperature = a.temperature
			params.PresencePenalty = a.presencePenalty
			if tools != nil {
				params.Tools = tools.Definitions()
			}

			// Provider concurrency cap. nil gate is a no-op.
			if a.gate != nil {
				if err := a.gate.Acquire(ctx); err != nil {
					return fmt.Errorf("agent: %w", err)
				}
			}
			gateAcquired := a.gate != nil

			start := time.Now()
			assistantMsg := api.NewAssistantMessage()
			// textBuilder accumulates the streamed text for this turn.
			// strings.Builder avoids the O(n²) string-concat cost a
			// `textContent +=` loop incurs on long assistant outputs.
			var textBuilder strings.Builder
			accumulators := make(map[int]*toolCallAccumulator)
			thinkings := make(map[int]*thinkingAccumulator)
			var streamErr error
			var usage *api.Usage
			var stopReason string

			func() {
				if gateAcquired {
					defer a.gate.Release()
				}
				for event := range a.provider.Complete(ctx, params) {
					switch event.Type {
					case api.EventDelta:
						textBuilder.WriteString(event.Delta)
						p.events <- Event{Type: EventTextDelta, Delta: event.Delta}

					case api.EventToolCallStart:
						accumulators[event.ToolCallIndex] = &toolCallAccumulator{
							id:   event.ToolCallID,
							name: event.ToolCallName,
						}

					case api.EventToolCallDelta:
						if acc, ok := accumulators[event.ToolCallIndex]; ok {
							acc.args.WriteString(event.ToolCallArgs)
						}

					case api.EventThinkingStart:
						th := &thinkingAccumulator{
							index:    event.ThinkingIndex,
							redacted: event.ThinkingRedacted,
							data:     event.ThinkingRedactedData,
						}
						thinkings[event.ThinkingIndex] = th

					case api.EventThinkingDelta:
						th, ok := thinkings[event.ThinkingIndex]
						if !ok {
							// Defensive: spec guarantees a Start event first, but
							// don't drop a chunk if we somehow miss it.
							th = &thinkingAccumulator{index: event.ThinkingIndex}
							thinkings[event.ThinkingIndex] = th
						}
						if event.Delta != "" {
							th.text.WriteString(event.Delta)
							p.events <- Event{Type: EventThinkingDelta, Delta: event.Delta}
						}
						if event.ThinkingSignature != "" {
							th.signature = event.ThinkingSignature
						}

					case api.EventUsage:
						usage = event.Usage
						p.events <- Event{Type: EventUsageReport, Usage: event.Usage}

					case api.EventError:
						// Redact obvious credential substrings before
						// either persisting or surfacing to the TUI.
						// Major providers don't echo Authorization /
						// x-api-key in error bodies, but local /
						// self-hosted endpoints sometimes do. Preserve
						// the error chain when redaction is a no-op so
						// sentinels (api.ErrContextLimit, etc.) keep
						// matching via errors.Is.
						orig := event.Error.Error()
						redacted := RedactSecrets(orig)
						if redacted == orig {
							streamErr = event.Error
						} else {
							streamErr = errors.New(redacted)
						}
						p.events <- Event{Type: EventError, Error: streamErr}

					case api.EventDone:
						// Stream finished. Capture the provider's finish
						// flag for the request log so empty-turn diagnoses
						// don't require re-running.
						stopReason = event.StopReason
					}
				}
			}()
			textContent := textBuilder.String()
			recoveredToolCallCount := 0
			if len(accumulators) == 0 && textContent != "" && a.toolCallRecoveryEnabled() {
				recovered := recoverTextualToolCalls(textContent, tools)
				for i, call := range recovered.Calls {
					acc := &toolCallAccumulator{id: call.ID, name: call.Name}
					acc.args.WriteString(call.Args)
					accumulators[i] = acc
				}
				if len(recovered.Calls) > 0 {
					textContent = recovered.Text
				}
				recoveredToolCallCount = len(recovered.Calls)
			}

			// Log this request.
			var toolNames []string
			if tools != nil {
				for _, def := range tools.Definitions() {
					toolNames = append(toolNames, def.Name)
				}
			}
			logErr := ""
			if streamErr != nil {
				// streamErr is already redacted at the EventError branch
				// above, but pass through RedactSecrets defensively in
				// case a future caller surfaces an error from a path
				// that bypasses that branch.
				logErr = RedactSecrets(streamErr.Error())
			}
			emptyPreview := ""
			if textContent == "" && len(accumulators) == 0 && streamErr == nil {
				// Degenerate empty turn — capture whatever scrap of
				// content might have leaked into the visible channel
				// (often nothing) so the log shows something. Cap at
				// 500 bytes to keep log volume sane.
				emptyPreview = textContent
				if len(emptyPreview) > 500 {
					emptyPreview = emptyPreview[:500] + "…"
				}
			}
			_ = a.logger.Write(RequestLogEntry{
				Timestamp:               start,
				Model:                   p.model,
				MsgCount:                len(params.Messages),
				SystemSHA256:            systemHash,
				ToolNames:               toolNames,
				Usage:                   usage,
				DurationMs:              time.Since(start).Milliseconds(),
				Error:                   logErr,
				TextLen:                 len(textContent),
				ToolCallCount:           len(accumulators),
				RecoveredToolCallCount:  recoveredToolCallCount,
				StopReason:              stopReason,
				EmptyAssistantPreview:   emptyPreview,
				WorkspaceHintPresent:    workspaceSummary.Present(),
				VerificationHintPresent: verificationHint.Present(),
				LoopGuardActions:        loopGuard.TakeActions(),
			})

			if err := ctx.Err(); err != nil {
				return fmt.Errorf("agent: %w", err)
			}
			if streamErr != nil {
				if isContextLimit(streamErr) && !reactiveCompactAttempted && a.compactor != nil {
					reactiveCompactAttempted = true
					p.events <- Event{Type: EventCompactStart}
					msg, boundaryID, ferr := a.compactor.ForceSummarize(ctx, p.conv, p.model)
					p.events <- Event{Type: EventCompactDone}
					if ferr == nil {
						p.events <- Event{Type: EventCompactionApplied, ToolDisp: "forced summarize after context-limit error"}
						if msg != nil && p.store != nil && p.sessionID != "" {
							if boundaryID != "" {
								if err := p.store.AppendSummaryMessage(ctx, p.sessionID, *msg, boundaryID); err != nil {
									p.events <- Event{Type: EventError, Error: fmt.Errorf("agent: persist forced summary: %w", err)}
								}
							} else {
								_ = p.store.AppendMessage(ctx, p.sessionID, *msg, nil)
							}
						}
						// Retry the same turn with the compacted conversation.
						continue
					}
					// Force summarization itself failed; surface ErrContextLimit.
				}
				if isContextLimit(streamErr) {
					return ErrContextLimit
				}
				return fmt.Errorf("agent: %w", streamErr)
			}

			// Build the assistant message. Thinking blocks come first (Anthropic
			// emits them at the lowest content-block indices); their order
			// across multiple blocks is preserved so the server-issued
			// signatures verify on a subsequent turn that includes tool_use.
			if len(thinkings) > 0 {
				ordered := make([]*thinkingAccumulator, 0, len(thinkings))
				for _, th := range thinkings {
					ordered = append(ordered, th)
				}
				slices.SortFunc(ordered, func(a, b *thinkingAccumulator) int {
					return cmp.Compare(a.index, b.index)
				})
				for _, th := range ordered {
					assistantMsg.Content = append(assistantMsg.Content, api.ContentBlock{
						Type: api.BlockThinking,
						Thinking: &api.ThinkingBlock{
							Text:      th.text.String(),
							Signature: th.signature,
							Redacted:  th.redacted,
							Data:      th.data,
						},
					})
				}
			}
			if textContent != "" {
				assistantMsg.Content = append(assistantMsg.Content, api.ContentBlock{
					Type: api.BlockText,
					Text: textContent,
				})
			}

			// Deterministic order for tool calls by index.
			type indexedAcc struct {
				index int
				acc   *toolCallAccumulator
			}
			sorted := make([]indexedAcc, 0, len(accumulators))
			for idx, acc := range accumulators {
				sorted = append(sorted, indexedAcc{idx, acc})
			}
			slices.SortFunc(sorted, func(a, b indexedAcc) int {
				return cmp.Compare(a.index, b.index)
			})

			// Validate every tool-call's accumulated arguments as JSON before
			// committing them to the conversation. A malformed argument string
			// will fail json.RawMessage marshaling at persistence time AND, more
			// importantly, will be rejected by some servers (e.g. llama.cpp) on
			// the next turn when the assistant message is echoed back. Empty
			// args are normalized to "{}" so the wire format stays valid.
			//
			// On invalid JSON we used to abort the whole turn, which forced
			// the user to retype "continue". Now we substitute "{}" in the
			// persisted call (so the wire format is valid), and remember
			// the original raw bytes in invalidArgsByID so the dispatcher
			// can synthesize a tool_result error. The model sees that
			// error in the next turn's input and typically self-corrects.
			invalidArgsByID := map[string]string{}
			for _, item := range sorted {
				raw := item.acc.args.String()
				if raw == "" {
					raw = "{}"
				}
				storedArgs := raw
				if !json.Valid([]byte(raw)) {
					invalidArgsByID[item.acc.id] = raw
					storedArgs = "{}"
					p.events <- Event{
						Type:  EventError,
						Error: fmt.Errorf("agent: tool call %q (id=%s) returned invalid JSON arguments; substituting empty input and reporting to model", item.acc.name, item.acc.id),
					}
				}
				assistantMsg.Content = append(assistantMsg.Content, api.ContentBlock{
					Type: api.BlockToolUse,
					ToolCall: &api.ToolCall{
						ID:    item.acc.id,
						Name:  item.acc.name,
						Input: json.RawMessage(storedArgs),
					},
				})
			}
			// Empty assistant turn — no text and no structured tool
			// calls. Some open-weights tool-using models (kimi, glm,
			// hermes-style) emit this when they confuse the textual
			// <tool_call>…</tool_call> convention with the structured
			// tool-calling API. We detect it BEFORE appending so the
			// next provider call doesn't send messages ending with
			// `assistant`, which llama.cpp's Qwen3-thinking chat
			// template (and other prefill-aware servers) reject as
			// "Assistant response prefill is incompatible with
			// enable_thinking." Discarding the empty message also
			// keeps the prompt cache extending cleanly across the
			// retry — the conversation tail is unchanged from the
			// pre-call state.
			if len(sorted) == 0 && textContent == "" {
				if a.notifier != nil && !emptyTurnNudgeUsed {
					emptyTurnNudgeUsed = true
					a.notifier.QueueOneShot(emptyTurnNudgeBody)
					continue
				}
				return ErrEndTurn
			}
			if len(sorted) == 0 && textContent != "" && loopGuard != nil && loopGuard.RecordFinalText(textContent, queueReminderFn) {
				continue
			}

			p.conv.Append(assistantMsg)

			// Persist the assistant message immediately so --resume picks it up
			// on the next process. Failure is non-fatal; log via request logger.
			if p.store != nil && p.sessionID != "" {
				if err := p.store.AppendMessage(ctx, p.sessionID, assistantMsg, usage); err != nil {
					p.events <- Event{Type: EventError, Error: fmt.Errorf("agent: persist assistant message: %w", err)}
				}
			}

			// No tool calls but text present → clean end-of-turn.
			if len(sorted) == 0 {
				return ErrEndTurn
			}

			// Execute tool calls with approval + policy check.
			toolResultMsg := api.Message{
				ID:        uuid.New().String(),
				Role:      api.RoleTool,
				CreatedAt: time.Now(),
			}

			// Plan phase: evaluate every call (asking the user for Ask
			// decisions) and classify each for dispatch. Emit
			// EventToolCallStarted events in order here so the UI sees the
			// pipeline at the natural moment.
			tc.MessageID = assistantMsg.ID
			plans := make([]*toolCallPlan, len(sorted))
			for idx, item := range sorted {
				plan := &toolCallPlan{acc: item.acc, argsStr: item.acc.args.String()}
				plans[idx] = plan

				// Per-turn tool-call cap. Defence-in-depth: max_tokens
				// already bounds output, but a misbehaving model can
				// still emit a flurry of tiny tool_use blocks. Mark
				// overflow plans denied so they emit a recoverable
				// tool error and the model self-corrects on the next
				// turn. Skip the cap for subagents: their MaxSteps
				// bound is the right place to enforce shape, and a
				// cascade failure inside a child run is unhelpful.
				if !p.isSubagent && idx >= maxToolCallsPerTurn {
					plan.denied = fmt.Sprintf("denied: tool call cap exceeded (max %d per turn). Issue fewer tool calls.", maxToolCallsPerTurn)
				}

				// Calls whose args failed JSON validation are pre-marked
				// for synthetic error reply. The dispatcher skips them
				// (denied != "" branches the same way as policy denials)
				// and the emission phase writes the error into the
				// tool_result block — so the model sees its mistake on
				// the next turn and can retry with valid input.
				if plan.denied == "" {
					if rawBad, ok := invalidArgsByID[plan.acc.id]; ok {
						plan.denied = "error: invalid JSON arguments — got: " + rawBad +
							" (the structured input must be a complete JSON object; common causes: hit max_tokens mid-stream, or used an unsupported textual tool-call format. Retry with valid JSON.)"
					}
				}

				if t, ok := tools.Resolve(plan.acc.name); ok {
					plan.tool = t
					plan.perToolMax = t.MaxResultBytes()
					if ctxDisp, ok := t.(ContextualDisplay); ok {
						plan.disp = ctxDisp.FormatForDisplayWithContext([]byte(plan.argsStr), tc)
					} else {
						plan.disp = t.FormatForDisplay([]byte(plan.argsStr))
					}
					if keyed, ok := t.(ContextualPermissionKey); ok {
						key, err := keyed.PermissionKeyWithContext([]byte(plan.argsStr), tc)
						if err != nil {
							plan.denied = "permission key refused: " + err.Error()
						}
						plan.permKey = key
					} else {
						plan.permKey = t.PermissionKey([]byte(plan.argsStr))
					}
					plan.isReadOnly = t.IsReadOnly()
					plan.isConcurrent = t.IsConcurrencySafe()
				}

				// plan_exit pre-flight. Refuse the call before
				// the user sees the approval overlay when no plan file is
				// recorded for this session OR the plan markdown doesn't
				// satisfy the schema. Sets `denied` so the existing
				// invalid-args branch below routes the failure as a
				// recoverable tool error (the run continues; the model
				// fixes the plan and tries again on the same turn or the
				// next).
				if plan.denied == "" && plan.acc.name == "plan_exit" && p.agentDef.PlanMode {
					if err := preflightPlanExit(ctx, p, cwd); err != nil {
						plan.denied = "plan_exit refused: " + err.Error()
					}
				}

				subagentName := ""
				if p.isSubagent {
					subagentName = p.agentDef.Name
				}
				p.events <- Event{
					Type:         EventToolCallStarted,
					ToolCallID:   plan.acc.id,
					ToolName:     plan.acc.name,
					ToolArgs:     plan.argsStr,
					ToolDisp:     plan.disp,
					ToolSubagent: subagentName,
				}

				// Skip permission evaluation entirely for invalid-args
				// plans: there's nothing to ask about, the call won't
				// execute. Emit the synthesised error result here so the
				// UI shows the failure inline with the call row.
				if plan.denied != "" {
					p.events <- Event{
						Type:       EventToolCallDone,
						ToolCallID: plan.acc.id,
						ToolName:   plan.acc.name,
						ToolResult: plan.denied,
						ToolError:  true,
					}
					continue
				}

				// Evaluate. Ask routes through CanUseTool synchronously.
				evalResult := EvaluateResult{Decision: DecisionAsk}
				if a.evaluator != nil {
					evalResult = a.evaluator.Evaluate(EvaluateInput{
						Tool:       plan.acc.name,
						Key:        plan.permKey,
						IsReadOnly: plan.isReadOnly,
					})
				}
				switch evalResult.Decision {
				case DecisionDeny:
					reason := evalResult.Reason
					if reason == "" {
						reason = "denied by policy"
					}
					plan.denied = "denied: " + reason
				case DecisionAsk:
					decision, err := p.canUseTool(ctx, plan.acc.name, plan.permKey, []byte(plan.argsStr))
					if err != nil {
						// A genuine ctx cancellation should still terminate
						// the run; everything else (TUI timeout, channel
						// closed, callback panic recovered upstream) gets
						// folded into a per-call denial so the model sees
						// one tool error and the run continues.
						if ctxErr := ctx.Err(); ctxErr != nil {
							return fmt.Errorf("agent: %w", ctxErr)
						}
						plan.denied = "permission gate error: " + err.Error()
						p.events <- Event{Type: EventError, Error: fmt.Errorf("agent: canUseTool: %w", err)}
						break
					}
					if decision != DecisionAllow && decision != DecisionAllowForSubagent {
						plan.acc = item.acc // keep
						p.events <- Event{
							Type:       EventToolCallDone,
							ToolCallID: plan.acc.id,
							ToolName:   plan.acc.name,
							ToolResult: "denied by user",
							ToolError:  true,
						}
						return ErrUserDenied
					}
				case DecisionAllow, DecisionAllowForSubagent:
					// no action
				}

				if plan.denied == "" && loopGuard != nil {
					loopGuard.MaybeUseCached(plan)
				}
			}

			// Pre-dispatch backup hook. Plan-mode `write` /
			// `edit` calls targeting an existing plan path get the
			// previous version copied to `.prompto/plans/.history/`
			// first so `/plan diff` has something to compare against.
			// Best-effort: backup failures emit EventError but never
			// block the run.
			if p.agentDef.PlanMode {
				maybeBackupPlanFiles(plans, cwd, p.events)
			}

			// Dispatch phase: execute plans that aren't pre-denied. Concurrency-
			// safe plans in a contiguous run dispatch in parallel under an
			// errgroup; non-safe plans run inline; a non-safe plan ends the
			// current parallel batch. On ctx cancellation dispatchPlans
			// returns nil after stamping synthetic "cancelled" results on
			// any plan that didn't finish, so the emission phase below
			// still produces a balanced tool_result message; the caller
			// surfaces ctx.Err() after persistence.
			if err := dispatchPlans(ctx, tc, plans); err != nil {
				return fmt.Errorf("agent: %w", err)
			}

			// Emission phase: walk plans in original order, apply the per-turn
			// aggregator, emit EventToolCallDone, append tool_result blocks.
			for _, plan := range plans {
				content := plan.resultContent
				isError := plan.resultIsError
				if plan.denied != "" {
					content = plan.denied
					isError = true
				}
				content = aggregator.Apply(content, plan.perToolMax)

				// First plan-file write captures the model-
				// chosen path so the build-switch reminder, /resume,
				// and the per-turn plan-mode reminder all reference
				// the right file. Idempotent — only persists when no
				// path is recorded yet.
				if !isError && p.agentDef.PlanMode && plan.acc.name == "write" {
					maybeRecordFirstPlanWrite(ctx, p.store, p.sessionID, cwd, plan.argsStr, p.events)
				}

				// A successful plan_exit signals the model
				// believes the plan is complete. The pre-permission
				// validation already ran (plan.denied path); reaching
				// this branch means the validator passed and the user
				// approved via the plan-approval overlay. Emit an
				// EventPlanApproved so the TUI can flip the agent to
				// build, queue the BUILD_SWITCH reminder, and
				// synthesise an "execute it" user message.
				if !isError && p.agentDef.PlanMode && plan.acc.name == "plan_exit" {
					emitPlanApprovedEvent(ctx, p, cwd, p.events)
				}

				if loopGuard != nil {
					loopGuard.RecordPlanResult(plan, queueReminderFn)
				}
				if loopGuard != nil && queueReminderFn != nil {
					if feedback := failureFeedbackForPlan(plan, content, isError); feedback != "" {
						queueReminderFn(feedback)
					}
				}

				toolResultMsg.Content = append(toolResultMsg.Content, api.ContentBlock{
					Type: api.BlockToolResult,
					ToolResult: &api.ToolResult{
						ToolCallID: plan.acc.id,
						Content:    content,
						IsError:    isError,
					},
				})

				summary := ""
				if !isError {
					summary = plan.resultSummary
				}
				p.events <- Event{
					Type:       EventToolCallDone,
					ToolCallID: plan.acc.id,
					ToolName:   plan.acc.name,
					ToolResult: content,
					ToolError:  isError,
					ToolDisp:   summary,
				}
			}

			p.conv.Append(toolResultMsg)
			if p.store != nil && p.sessionID != "" {
				// Persist tool_result with a Background context: if ctx
				// was cancelled mid-dispatch we still want the balanced
				// tool_result message on disk so --resume picks up a
				// usable conversation. Without this, the assistant
				// message has tool_use blocks with no matching results.
				persistCtx := ctx
				if ctx.Err() != nil {
					persistCtx = context.Background()
				}
				if err := p.store.AppendMessage(persistCtx, p.sessionID, toolResultMsg, nil); err != nil {
					p.events <- Event{Type: EventError, Error: fmt.Errorf("agent: persist tool_result message: %w", err)}
				}
			}

			// If the dispatch phase observed cancellation, surface it now
			// — the tool_result is on disk and in the conversation, so
			// resume sees a balanced turn.
			if err := ctx.Err(); err != nil {
				return fmt.Errorf("agent: %w", err)
			}

			// A successful plan_exit terminates the plan
			// agent's run. The TUI's EventPlanApproved handler will
			// flip to the build agent and kick off a fresh run with
			// the synthesised "execute it" user message — looping
			// the plan agent for another provider call would just
			// emit a redundant final-text turn.
			for _, plan := range plans {
				if plan.acc.name == "plan_exit" && plan.denied == "" && !plan.resultIsError && p.agentDef.PlanMode {
					return ErrEndTurn
				}
			}

			// Subagent end-of-step heartbeat. Emit one line per step
			// with cumulative call count and the last non-denied call's
			// display string so the parent's TUI can show the user the
			// child is making progress without waiting for the final
			// summary. Primary runs skip — the parent already sees its
			// own tool calls in the chat.
			if p.isSubagent {
				stepCalls := 0
				lastDisp := ""
				for _, plan := range plans {
					if plan.denied != "" {
						continue
					}
					stepCalls++
					if plan.disp != "" {
						lastDisp = plan.disp
					} else {
						lastDisp = plan.acc.name
					}
				}
				if stepCalls > 0 {
					totalToolCalls += stepCalls
					body := fmt.Sprintf("%d calls so far · last: %s", totalToolCalls, lastDisp)
					p.events <- Event{
						Type:         EventSubagentStep,
						ToolSubagent: p.agentDef.Name,
						ToolDisp:     body,
					}
				}
			}

			break // success path: advance to the next outer step
		} // end inner retry loop
	}

	return ErrMaxSteps
}

// toolCallPlan is the per-call work item produced during the plan phase
// and consumed during dispatch + emission. Each plan is touched three
// times: plan-phase populates classification + denial, dispatch-phase
// populates resultContent/resultIsError for non-denied plans, emission-
// phase reads the result to build the tool_result block.
type toolCallPlan struct {
	acc               *toolCallAccumulator
	argsStr           string
	tool              Tool // nil when Resolve failed
	disp              string
	permKey           string
	isReadOnly        bool
	isConcurrent      bool
	perToolMax        int
	guardSkipDispatch bool

	// Populated during plan phase when the evaluator returns Deny:
	denied string

	// Populated during dispatch phase:
	resultContent string
	resultIsError bool
	resultSummary string // mirrors Result.DisplaySummary; surfaced on EventToolCallDone.
}

// preflightPlanExit validates the plan agent's plan_exit
// invocation BEFORE the user is shown the approval overlay.
// Refuses when no plan file is recorded / discoverable for this
// session, or when the plan markdown is missing required schema
// sections.
//
// On error the caller sets `plan.denied` and routes through the
// existing tool-error path: the model sees the failure as a
// tool_result on this turn and fixes the plan without the run
// terminating. That's the difference vs. a user denial in the
// overlay (DecisionDeny → ErrUserDenied → run ends → user
// provides feedback).
func preflightPlanExit(ctx context.Context, p runLoopParams, cwd string) error {
	persisted := ""
	if p.store != nil && p.sessionID != "" {
		loaded, err := p.store.LoadPlanPath(ctx, p.sessionID)
		if err != nil {
			return fmt.Errorf("load plan_path: %w", err)
		}
		persisted = loaded
	}
	planPath := ResolvePlanPath(ResolvePlanPathInput{
		Cwd:           cwd,
		SessionID:     p.sessionID,
		PersistedPath: persisted,
	})
	if planPath == "" {
		return errors.New("no plan file path is available; write the plan first to .prompto/plans/YYYY-MM-DD-<slug>.md")
	}
	if persisted == "" {
		// No DB recording yet — only valid if the legacy filename
		// already exists on disk. Otherwise the model called
		// plan_exit before writing anything.
		if _, err := os.Stat(planPath); err != nil {
			return errors.New("no plan file recorded for this session; write the plan first to .prompto/plans/YYYY-MM-DD-<slug>.md")
		}
	}
	body, err := os.ReadFile(planPath)
	if err != nil {
		return fmt.Errorf("read plan file %s: %w", planPath, err)
	}
	if err := ValidatePlanMarkdown(body); err != nil {
		return err
	}
	return nil
}

// emitPlanApprovedEvent fires EventPlanApproved on
// successful plan_exit. The TUI consumes the event to flip the
// agent + queue the BUILD_SWITCH reminder + synthesise the
// "execute it" user message. Best-effort: a missing plan path
// here would only happen post-validation if the file vanished
// between preflight and Execute; surfacing a log event keeps the
// flip from happening but doesn't bring the run down.
func emitPlanApprovedEvent(ctx context.Context, p runLoopParams, cwd string, events chan<- Event) {
	persisted := ""
	if p.store != nil && p.sessionID != "" {
		loaded, err := p.store.LoadPlanPath(ctx, p.sessionID)
		if err != nil {
			events <- Event{Type: EventError, Error: fmt.Errorf("agent: load plan_path: %w", err)}
		} else {
			persisted = loaded
		}
	}
	planPath := ResolvePlanPath(ResolvePlanPathInput{
		Cwd:           cwd,
		SessionID:     p.sessionID,
		PersistedPath: persisted,
	})
	if planPath == "" {
		events <- Event{Type: EventError, Error: errors.New("agent: plan_exit succeeded but plan path could not be resolved")}
		return
	}
	events <- Event{
		Type:     EventPlanApproved,
		ToolDisp: planPath,
	}
}

// maybeRecordFirstPlanWrite implements the first-write
// hook. When a plan-mode `write` tool call succeeds and targets a
// path under `<cwd>/.prompto/plans/`, the chosen path is persisted
// on the session row so subsequent turns (and `/resume`) reference
// the right file. The helper is idempotent: it consults
// `LoadPlanPath` first and skips when a path is already recorded,
// so the model writing the same file repeatedly never overwrites
// the original recording.
//
// All failures are non-fatal (best-effort persistence): a logging
// event is emitted but the run loop continues. nil store / empty
// sessionID short-circuit silently — they signal headless mode.
func maybeRecordFirstPlanWrite(ctx context.Context, store Store, sessionID, cwd, argsJSON string, events chan<- Event) {
	if store == nil || sessionID == "" {
		return
	}
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return
	}
	if !IsPlanFilePath(cwd, args.Path) {
		return
	}
	existing, err := store.LoadPlanPath(ctx, sessionID)
	if err != nil {
		events <- Event{Type: EventError, Error: fmt.Errorf("agent: load plan_path: %w", err)}
		return
	}
	if existing != "" {
		return
	}
	if err := store.SetPlanPath(ctx, sessionID, args.Path); err != nil {
		events <- Event{Type: EventError, Error: fmt.Errorf("agent: persist plan_path: %w", err)}
	}
}

// maybeBackupPlanFiles snapshots existing plan files before the run
// loop dispatches `write` / `edit` calls that would overwrite them.
// Gives `/plan diff` something to compare against. Called
// only in plan mode, after permission resolution but before dispatch.
//
// Each non-denied write/edit/replace_lines plan whose path argument resolves to a
// plan file under cwd triggers a `BackupPlan` call. Errors are sent
// as EventError but never block the run — backups are best-effort.
func maybeBackupPlanFiles(plans []*toolCallPlan, cwd string, events chan<- Event) {
	if cwd == "" {
		return
	}
	for _, plan := range plans {
		if plan == nil || plan.denied != "" {
			continue
		}
		if plan.acc.name != "write" && plan.acc.name != "edit" && plan.acc.name != "replace_lines" {
			continue
		}
		var args struct {
			Path     string `json:"path"`
			FilePath string `json:"file_path"`
		}
		if err := json.Unmarshal([]byte(plan.argsStr), &args); err != nil {
			continue
		}
		path := args.Path
		if path == "" {
			path = args.FilePath
		}
		if !IsPlanFilePath(cwd, path) {
			continue
		}
		if err := BackupPlan(path); err != nil {
			events <- Event{Type: EventError, Error: fmt.Errorf("agent: backup plan %s: %w", path, err)}
		}
	}
}

// emptyTurnNudgeBody is injected as a one-shot system reminder when
// an open-weights tool-using model produces an empty assistant turn
// (no text, no structured tool_calls). This commonly happens when
// the model emits its tool call as a textual <tool_call>…</tool_call>
// envelope inside reasoning instead of through the structured API,
// then expects the inference engine to "execute" it. The reminder
// is bounded to one fire per turn by emptyTurnNudgeUsed so a
// genuinely stuck model can't loop.
const emptyTurnNudgeBody = "Your previous response was empty. If you intended to call a tool, use the structured tool-calling API; textual `<tool_call>…</tool_call>` blocks are NOT executed. Choose exactly one next action: read, grep/glob, edit/replace_lines, bash verification, or final answer."

// maxToolCallsPerTurn bounds the number of tool calls a single primary-
// agent turn may issue. max_tokens already caps output volume, but a
// misbehaving model can still emit a flurry of tiny tool_use blocks
// inside one assistant message. Calls past the cap are pre-marked
// denied so the model sees a recoverable tool error and self-corrects.
// Subagents are exempt — their MaxSteps bound is the right place to
// shape their behaviour.
const maxToolCallsPerTurn = 50

// concurrentBatchEnd finds the end index (exclusive) of a contiguous
// run of concurrency-safe plans starting at start. Denied plans
// inside the run are NOT boundaries — they never execute, so they
// shouldn't fragment a batch of concurrent neighbors. Only an
// unsafe / unknown-tool plan ends the run.
func concurrentBatchEnd(plans []*toolCallPlan, start int) int {
	end := start
	for end < len(plans) {
		b := plans[end]
		if b.tool == nil || !b.isConcurrent {
			break
		}
		end++
	}
	return end
}

// dispatchPlans executes all plans with a result still pending (i.e. not
// denied). Contiguous runs of IsConcurrencySafe plans are dispatched under
// an errgroup; a non-safe plan (or an unknown tool) runs inline and ends
// the current batch. Per-plan execution errors become tool_result errors,
// not group errors — only ctx cancellation aborts the group.
//
// On cancellation, dispatchPlans returns nil rather than the ctx error so
// the caller still walks the emission phase and produces tool_result
// blocks for every plan. Plans whose execution didn't finish get filled
// with a synthetic "cancelled" error so the assistant message's tool_use
// blocks always have a matching tool_result — leaving the conversation
// resumable. The caller is expected to consult ctx.Err() afterwards if it
// needs to surface the cancellation as the turn's outcome.
func dispatchPlans(ctx context.Context, tc ToolContext, plans []*toolCallPlan) error {
	i := 0
	for i < len(plans) {
		plan := plans[i]
		if plan.denied != "" || plan.guardSkipDispatch {
			i++
			continue
		}

		// Unsafe or unknown-tool: run inline.
		if plan.tool == nil || !plan.isConcurrent {
			tcCall := tc
			tcCall.ToolCallID = plan.acc.id
			executePlan(ctx, tcCall, plan)
			i++
			if ctx.Err() != nil {
				fillCancelledPlans(plans)
				return nil
			}
			continue
		}

		batchEnd := concurrentBatchEnd(plans, i)

		// Dispatch batch under an errgroup. Denied plans are skipped
		// here (their content is filled later from plan.denied during
		// the tool_result block emit). Each surviving goroutine holds
		// its own ToolContext copy so ToolCallID doesn't race across
		// plans.
		group, gctx := errgroup.WithContext(ctx)
		for _, plan := range plans[i:batchEnd] {
			if plan.denied != "" || plan.guardSkipDispatch {
				continue
			}
			p := plan
			tcCall := tc
			tcCall.ToolCallID = p.acc.id
			group.Go(func() error {
				executePlan(gctx, tcCall, p)
				return gctx.Err() // only ctx errors bubble; tool errors stay on the plan
			})
		}
		if err := group.Wait(); err != nil {
			if ctx.Err() != nil {
				fillCancelledPlans(plans)
				return nil
			}
			return err
		}
		i = batchEnd
	}
	return nil
}

// fillCancelledPlans stamps a synthetic error on every plan that has no
// result and no denial. Used after ctx cancellation so the emission
// phase still produces a tool_result block for every tool_use the
// assistant message advertised.
func fillCancelledPlans(plans []*toolCallPlan) {
	for _, plan := range plans {
		if plan == nil || plan.denied != "" {
			continue
		}
		if plan.resultContent != "" || plan.resultIsError {
			continue
		}
		plan.resultContent = "error: tool call cancelled before completion"
		plan.resultIsError = true
	}
}

// executePlan runs one plan's tool and stores the result on the plan. Tool
// errors are recorded as tool_result errors; they never propagate further.
func executePlan(ctx context.Context, tc ToolContext, plan *toolCallPlan) {
	defer func() {
		if r := recover(); r != nil {
			plan.resultContent = fmt.Sprintf("error: tool %q panicked: %v", plan.acc.name, r)
			plan.resultIsError = true
		}
	}()
	if plan.tool == nil {
		plan.resultContent = fmt.Sprintf("error: unknown tool %q", plan.acc.name)
		plan.resultIsError = true
		return
	}
	res, err := plan.tool.Execute(ctx, tc, []byte(plan.argsStr))
	if err != nil {
		plan.resultContent = fmt.Sprintf("error: %s", err.Error())
		plan.resultIsError = true
		return
	}
	plan.resultContent = res.Content
	plan.resultSummary = res.DisplaySummary
}

// sha256HexBlocks hashes a sequence of system blocks for the request log.
// The hash is informational; it is not sent to the API and doesn't need to
// match Anthropic's cache-key derivation.
func sha256HexBlocks(blocks []api.SystemBlock) string {
	h := sha256.New()
	for _, b := range blocks {
		h.Write([]byte(b.Text))
		h.Write([]byte{0}) // separator
	}
	return hex.EncodeToString(h.Sum(nil))
}

// isContextLimit returns true when the streaming error indicates the
// prompt exceeded the provider's input context. Two-stage check:
//
//  1. errors.Is against api.ErrContextLimit — the canonical, allocation-
//     free path for providers that wrap their context-limit errors with
//     the sentinel. Survives server-side message rewording.
//  2. Substring fallback for providers that haven't opted into the
//     sentinel (custom OpenAI-compatible servers, older builds). The
//     known phrasings come from Anthropic ("prompt is too long"), OpenAI
//     ("context_length_exceeded"), and most OpenAI-compatible relays
//     ("context window"). New providers should prefer the sentinel —
//     this list is best-effort.
//
// The error.Error() allocation in the fallback loop is acceptable
// because this function runs at most twice per turn (initial check +
// post-compaction retry), not on the streaming hot path.
func isContextLimit(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, api.ErrContextLimit) {
		return true
	}
	for e := err; e != nil; e = errors.Unwrap(e) {
		msg := e.Error()
		if strings.Contains(msg, "prompt is too long") ||
			strings.Contains(msg, "context_length_exceeded") ||
			strings.Contains(msg, "context window") {
			return true
		}
	}
	return false
}
