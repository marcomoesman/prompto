package agent

import (
	"context"

	"github.com/marcomoesman/prompto/internal/api"
)

// Decision is the outcome of asking whether a tool call may proceed.
// DecisionAsk means "no rule resolved this; consult the user"; the agent
// loop routes Ask through CanUseTool while Allow/Deny short-circuit.
type Decision int

const (
	DecisionDeny             Decision = 0
	DecisionAllow            Decision = 1
	DecisionAsk              Decision = 2
	DecisionAllowForSubagent Decision = 3
)

// CanUseTool is the approval callback passed into Run. The agent loop calls
// it synchronously for every tool invocation and blocks until it returns.
// The TUI implements this by displaying the pending call and blocking on a
// keypress. Tests and headless modes supply fakes.
type CanUseTool func(ctx context.Context, name, key string, input []byte) (Decision, error)

// ToolContext carries cross-cutting state every tool needs. Passed by value;
// treat as immutable per call except for FileState, which is internally
// synchronized, and RequestLogger, which may be nil (no-op).
//
// MessageID, ToolCallID, and FileChanges are populated by the agent loop
// before each tool Execute. Edit and Write copy MessageID/ToolCallID into
// FileChangeEvents so the sink receives fully-attributed records.
// FileChanges falls back to DiscardFileChanges when a zero-valued context
// is constructed (tests); tools call Record without nil checks.
type ToolContext struct {
	Cwd           string
	AllowedRoots  []string
	FileState     *FileState
	RequestLogger *RequestLogger

	MessageID   string
	ToolCallID  string
	FileChanges FileChangeSink

	// Agent-aware fields. SessionID and ParentSessionID let tools
	// (notably task) thread session lineage through the run loop.
	// AgentName is the resolved AgentDefinition.Name for the current run;
	// subagents see their own name here. SpawnTask is non-nil only for
	// primary runs — it lets the task tool spawn a child run without
	// importing internal/agent (the closure is wired in main.go via
	// agent.NewSpawner). Subagents have AllAgentDisallowedTools applied,
	// so the task tool is never resolved for them; SpawnTask is left nil
	// to make the contract explicit.
	SessionID       string
	ParentSessionID string
	AgentName       string
	SpawnTask       TaskSpawner

	// SaveTodos is the closure the TodoWrite tool calls to persist a new
	// list atomically. nil means "no persistence" (tests, or runs
	// without a TodoStore configured).
	SaveTodos TodoSaver

	// Lazy nested AGENTS.md surfacing. AgentsMDLoadRoot is the directory
	// above which the eager startup pass already loaded AGENTS.md
	// content; tools should not re-emit anything at or above that
	// directory. The closures are nil-safe: tools call them
	// unconditionally; the run loop populates them for primaries and
	// leaves them nil for subagents.
	AgentsMDLoadRoot string
	QueueReminder    func(text string)
	HasSeenAgentsMD  func(path string) bool
	MarkSeenAgentsMD func(path string)

	// Publish is the optional fire-and-forget event sink. The run loop
	// wires it to a non-blocking send on the run's events channel so a
	// slow consumer can never wedge a tool goroutine. Tools call it via
	// the Status helper to surface long-running progress; nil-safe.
	Publish func(Event)
}

// Status publishes a per-tool heartbeat for the in-flight call. No-op when
// Publish is nil or ToolCallID is empty (Status is meaningless without a
// call to attribute it to). Drops on full buffer.
func (tc ToolContext) Status(s string) {
	if tc.Publish == nil || tc.ToolCallID == "" {
		return
	}
	tc.Publish(Event{
		Type:       EventToolStatus,
		ToolCallID: tc.ToolCallID,
		ToolDisp:   s,
	})
}

// TaskSpawner is the closure that lets the task tool launch a subagent run
// without importing internal/agent. Wired in cmd/prompto/main.go via
// NewSpawner. nil means "no subagents available from this run."
type TaskSpawner func(ctx context.Context, in TaskSpawnInput) (TaskSpawnResult, error)

// TaskSpawnInput is the parameter struct for TaskSpawner.
type TaskSpawnInput struct {
	SubagentType string // resolved against the registry
	Prompt       string // initial user message for the child
	TaskID       string // empty = new session; non-empty resumes the child
	Description  string // surfaced in the indicator and child session row

	// ParentAgentName names the agent invoking the spawn. The spawner
	// uses it to enforce read-only-parent → read-only-child: a parent
	// whose AgentDefinition.ReadOnly is true may only spawn subagents
	// whose ReadOnly is also true. Empty disables the check (legacy
	// callers / tests).
	ParentAgentName string

	// EventSink, when non-nil, receives a filtered subset of the
	// child's events so the parent's TUI can surface child tool calls,
	// per-tool status, and end-of-step heartbeats live. Forwarded types:
	// EventToolCallStarted, EventToolCallDone, EventToolStatus,
	// EventSubagentStep. Other events (text/thinking deltas, usage,
	// turn-complete, compaction) stay internal to the child's run.
	EventSink func(Event)
}

// TaskSpawnResult is what TaskSpawner returns to the caller.
type TaskSpawnResult struct {
	TaskID string
	Result string
}

// Sink returns tc.FileChanges or a Discard sink when the field is nil. Tools
// should call this rather than reading tc.FileChanges directly.
func (tc ToolContext) Sink() FileChangeSink {
	if tc.FileChanges == nil {
		return DiscardFileChanges
	}
	return tc.FileChanges
}

// Result is what a tool's Execute returns. Bytes is the pre-truncation
// size of Content (informational; the agent-loop aggregator is what
// actually caps tool output).
//
// DisplaySummary is the optional one-liner the TUI shows beneath the
// tool-call row on success — e.g. "Received 223.3KB (200 OK)" or
// "exit 0 · 1.2s". Empty string means no summary line is rendered.
type Result struct {
	Content        string
	Bytes          int
	DisplaySummary string
}

// Tool is the interface every executable tool must implement. Lives in the
// agent package next to its consumer; concrete implementations live in
// internal/tool and depend on this interface.
type Tool interface {
	Name() string
	Definition() api.ToolDefinition
	FormatForDisplay(input []byte) string
	// MaxResultBytes returns the per-tool ceiling applied by the agent loop
	// to the content this tool returns. 0 means use DefaultMaxResultBytes.
	// Tools that stream large content via an alternative channel (e.g. Read's
	// spill-to-disk) may return a very large or effectively unlimited value.
	MaxResultBytes() int
	Execute(ctx context.Context, tc ToolContext, input []byte) (Result, error)

	// IsReadOnly reports whether this tool ever modifies filesystem or
	// program state. Read/Grep/Glob/List/WebFetch → true. Edit/Write/Bash
	// → false. Used by the evaluator (AcceptEdits mode) and by the
	// protected-file guard (only writes to protected paths are auto-denied).
	IsReadOnly() bool

	// IsConcurrencySafe reports whether this tool may run in parallel with
	// siblings in the same tool-call batch. Read/Grep/Glob/List/WebFetch
	// are safe; Edit/Write race on FileState; Bash has arbitrary side
	// effects. Only safe tools are candidates for errgroup dispatch.
	IsConcurrencySafe() bool

	// PermissionKey returns the string the evaluator matches against rule
	// patterns for this specific invocation. Read/Edit/Write → the
	// absolute file path. Bash → the command. WebFetch → "domain:<host>".
	// Tools without a meaningful key can return "".
	PermissionKey(input []byte) string
}

// ContextualPermissionKey is implemented by filesystem tools whose permission
// key depends on ToolContext, primarily Cwd and AllowedRoots. The agent loop
// prefers this over Tool.PermissionKey when present so policy evaluation sees
// canonical paths rather than raw model input.
type ContextualPermissionKey interface {
	PermissionKeyWithContext(input []byte, tc ToolContext) (string, error)
}

// ContextualDisplay is implemented by tools whose display rendering benefits
// from the surrounding ToolContext — most often to relativize an absolute
// file path against tc.Cwd. Optional; the agent loop falls back to
// FormatForDisplay for tools that don't implement it.
type ContextualDisplay interface {
	FormatForDisplayWithContext(input []byte, tc ToolContext) string
}

// ToolResolver lets the agent look up tools by name without importing the
// tool package directly. internal/tool.Registry satisfies this interface.
type ToolResolver interface {
	Resolve(name string) (Tool, bool)
	Definitions() []api.ToolDefinition
}

// NewFilteredResolver wraps inner so only the tools whose Name appears in
// allow are resolvable. AllAgentDisallowedTools is subtracted only when
// isSubagent is true — primaries can still resolve "task" / "todowrite"
// if their allowlist includes them. Pass an empty allow slice to mean
// "all tools (minus subagent disallows when applicable)". The returned
// resolver is read-only; mutations to the underlying registry are visible
// immediately.
func NewFilteredResolver(inner ToolResolver, allow []string, isSubagent bool) ToolResolver {
	allowSet := make(map[string]struct{}, len(allow))
	for _, name := range allow {
		if isSubagent && AllAgentDisallowedTools[name] {
			continue
		}
		allowSet[name] = struct{}{}
	}
	return &filteredResolver{inner: inner, allow: allowSet, isSubagent: isSubagent}
}

type filteredResolver struct {
	inner ToolResolver
	// allow is the explicit set; nil/empty means "any tool (minus
	// subagent disallows when isSubagent is true)".
	allow      map[string]struct{}
	isSubagent bool
}

func (f *filteredResolver) Resolve(name string) (Tool, bool) {
	if f.isSubagent && AllAgentDisallowedTools[name] {
		return nil, false
	}
	if len(f.allow) > 0 {
		if _, ok := f.allow[name]; !ok {
			return nil, false
		}
	}
	return f.inner.Resolve(name)
}

func (f *filteredResolver) Definitions() []api.ToolDefinition {
	all := f.inner.Definitions()
	out := make([]api.ToolDefinition, 0, len(all))
	for _, d := range all {
		if f.isSubagent && AllAgentDisallowedTools[d.Name] {
			continue
		}
		if len(f.allow) > 0 {
			if _, ok := f.allow[d.Name]; !ok {
				continue
			}
		}
		out = append(out, d)
	}
	return out
}

// emptyToolResolver is the zero-value resolver used when an Agent is
// constructed with Tools: nil. It resolves nothing and returns no
// definitions, so the run loop skips dispatch cleanly instead of
// dereferencing a nil interface.
type emptyToolResolver struct{}

func (emptyToolResolver) Resolve(string) (Tool, bool)       { return nil, false }
func (emptyToolResolver) Definitions() []api.ToolDefinition { return nil }
