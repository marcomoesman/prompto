package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/marcomoesman/prompto/internal/api"
)

// SpawnerInput bundles the per-Agent parameters NewSpawner needs to
// construct a SpawnTask closure. The closure runs against the same
// provider, model, evaluator, gate, registry, and tool resolver as the
// primary so that per-model concurrency caps and per-agent tool
// allowlists apply identically to children.
type SpawnerInput struct {
	Agent       *Agent
	Store       SpawnerStore   // optional; nil disables persistence for children
	FileChanges FileChangeSink // optional; nil falls back to DiscardFileChanges
	CanUseTool  CanUseTool     // forwarded so children inherit the parent's prompt callback
}

// NewSpawner returns a closure that satisfies TaskSpawner. The closure is
// installed on the primary's ToolContext.SpawnTask field; the task tool
// invokes it. Subagents are filtered to drop the task tool, so this is
// only ever called from a primary.
//
// Behavior:
//  1. Resolve the subagent name in the registry. Unknown names return a
//     tool-shaped error.
//  2. If TaskID is set, load existing messages and treat as a resume; else
//     create a new child session with parent_id = primary's session id.
//  3. Run agent.Run with the child's Conversation, AgentName, etc. Drain
//     events; capture the last assistant text.
//  4. Mark the child session "ended" unless this was a resume.
//  5. Return TaskID + collected text.
func NewSpawner(in SpawnerInput) TaskSpawner {
	a := in.Agent
	store := in.Store
	fileChanges := in.FileChanges
	canUseTool := in.CanUseTool

	return func(ctx context.Context, args TaskSpawnInput) (TaskSpawnResult, error) {
		def, ok := a.registry.Resolve(args.SubagentType)
		if !ok {
			return TaskSpawnResult{}, fmt.Errorf("unknown subagent %q", args.SubagentType)
		}
		if def.Mode != ModeSubagent && def.Mode != ModeBoth {
			return TaskSpawnResult{}, fmt.Errorf("agent %q is not invokable as a subagent", args.SubagentType)
		}
		// Read-only parents may only spawn read-only subagents. Stops the
		// plan agent from bypassing its own constraints by spawning a
		// build-class child. Empty ParentAgentName disables the check
		// (tests, legacy callers).
		if args.ParentAgentName != "" {
			if parentDef, ok := a.registry.Resolve(args.ParentAgentName); ok && parentDef.ReadOnly && !def.ReadOnly {
				return TaskSpawnResult{}, fmt.Errorf("agent %q is read-only and may only spawn read-only subagents; %q is not read-only", args.ParentAgentName, args.SubagentType)
			}
		}

		conv := NewConversation()
		taskID := args.TaskID
		isResume := taskID != ""

		if isResume {
			if store == nil {
				return TaskSpawnResult{}, fmt.Errorf("cannot resume task %s: store not configured", taskID)
			}
			msgs, err := store.LoadMessages(ctx, taskID)
			if err != nil {
				return TaskSpawnResult{}, fmt.Errorf("loading task %s: %w", taskID, err)
			}
			for _, m := range msgs {
				conv.Append(m)
			}
			// Append the new prompt as a fresh user turn.
			conv.Append(api.Message{
				Role:    api.RoleUser,
				Content: []api.ContentBlock{{Type: api.BlockText, Text: args.Prompt}},
			})
		} else {
			// New child session.
			if store != nil {
				newID, err := store.CreateChildSession(ctx, CreateChildSessionInput{
					ParentID:  parentSessionFromCtx(ctx),
					AgentName: args.SubagentType,
					Model:     a.model,
					Title:     args.Description,
				})
				if err != nil {
					return TaskSpawnResult{}, fmt.Errorf("creating child session: %w", err)
				}
				taskID = newID
			}
			conv.Append(api.Message{
				Role:    api.RoleUser,
				Content: []api.ContentBlock{{Type: api.BlockText, Text: args.Prompt}},
			})
			// Persist the seed user message so resume picks it up.
			if store != nil && taskID != "" {
				_ = store.AppendMessage(ctx, taskID, conv.Messages()[len(conv.Messages())-1], nil)
			}
		}

		approvalGate := newSubagentApprovalGate(canUseTool)
		runCtx := WithSubagentApprovalScope(ctx, args.SubagentType, taskID)

		// Children share the primary's gate, evaluator, compactor, and
		// registry; only the resolver and AgentName change.
		result := a.Run(runCtx, RunInput{
			Conversation:    conv,
			Model:           a.model,
			MaxSteps:        def.MaxSteps,
			CanUseTool:      approvalGate.CanUseTool,
			SessionID:       taskID,
			ParentSessionID: parentSessionFromCtx(ctx),
			Store:           store,
			FileChanges:     fileChanges,
			AgentName:       args.SubagentType,
			// SpawnTask intentionally omitted: AllAgentDisallowedTools
			// strips "task" from the child's resolver, so the child can
			// never invoke a grandchild even if the field were set.
		})

		// Drain events and capture the final assistant text. We keep
		// draining past EventTurnComplete until the channel closes so the
		// child goroutine isn't blocked on a full Events buffer.
		// Selected event types are forwarded to args.EventSink so the
		// parent's TUI can surface the child's progress live (tool
		// calls, per-tool status, step heartbeats). Other event types
		// stay internal to the child run.
		// strings.Builder avoids the O(n²) cost of concatenating one
		// EventTextDelta per assignment — verbose subagents (Explore
		// returning a 100KB+ summary) can emit thousands of deltas
		// per turn, and `+=` would copy the entire prefix on each.
		// Mirrors the main loop's textBuilder usage in run.go.
		var textBuilder strings.Builder
		for ev := range result.Events {
			switch ev.Type {
			case EventTextDelta:
				textBuilder.WriteString(ev.Delta)
			case EventToolCallStarted, EventToolCallDone, EventToolStatus, EventSubagentStep:
				if args.EventSink != nil {
					args.EventSink(ev)
				}
			}
		}
		lastAssistantText := textBuilder.String()
		runErr := <-result.Done

		// Mark child ended unless this was a resume — resumes leave the
		// status untouched so future resumes can keep going.
		if !isResume && store != nil && taskID != "" {
			_ = store.SetSessionStatus(ctx, taskID, "ended")
		}

		if runErr != nil && runErr != ErrEndTurn {
			return TaskSpawnResult{TaskID: taskID, Result: lastAssistantText},
				fmt.Errorf("subagent run: %w (task_id=%s)", runErr, taskID)
		}

		return TaskSpawnResult{TaskID: taskID, Result: lastAssistantText}, nil
	}
}

type subagentApprovalGate struct {
	parent CanUseTool

	mu      sync.RWMutex
	allowed map[string]bool
}

func newSubagentApprovalGate(parent CanUseTool) *subagentApprovalGate {
	return &subagentApprovalGate{
		parent:  parent,
		allowed: make(map[string]bool),
	}
}

func (g *subagentApprovalGate) CanUseTool(ctx context.Context, name, key string, input []byte) (Decision, error) {
	if g == nil || g.parent == nil {
		return DecisionAsk, nil
	}
	if _, ok := SubagentApprovalScopeFromContext(ctx); !ok {
		return g.parent(ctx, name, key, input)
	}
	if g.isAllowed(name) {
		return DecisionAllow, nil
	}
	decision, err := g.parent(ctx, name, key, input)
	if err != nil {
		return decision, err
	}
	if decision == DecisionAllowForSubagent {
		g.allow(name)
		return DecisionAllow, nil
	}
	return decision, nil
}

func (g *subagentApprovalGate) isAllowed(name string) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.allowed[name]
}

func (g *subagentApprovalGate) allow(name string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.allowed[name] = true
}

// parentCtxKey is the type used to thread the parent's session id through
// the context when SpawnTask is invoked from inside the run loop. The
// run loop populates ToolContext.SessionID; the task tool reads it and
// must pass it back into the closure. We use the closure-creation-time
// agent's session, not the per-call value, because each Run binds to one
// session — but children may themselves spawn (in the future), so threading
// through ctx keeps the lineage right.
type parentCtxKey struct{}

// WithParentSession returns ctx with sessionID stamped as the parent for
// any SpawnTask invocations that bubble up from this scope. The task
// tool's Execute is the only intended caller.
func WithParentSession(ctx context.Context, sessionID string) context.Context {
	if sessionID == "" {
		return ctx
	}
	return context.WithValue(ctx, parentCtxKey{}, sessionID)
}

func parentSessionFromCtx(ctx context.Context) string {
	if v, ok := ctx.Value(parentCtxKey{}).(string); ok {
		return v
	}
	return ""
}
