package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/marcomoesman/prompto/internal/agent"
	"github.com/marcomoesman/prompto/internal/api"
)

// TaskInput is the JSON parameter struct for the task tool.
type TaskInput struct {
	Description  string `json:"description" jsonschema:"required,description=Short label for this task; surfaces in the indicator and the child session row."`
	Prompt       string `json:"prompt" jsonschema:"required,description=Full instruction handed to the subagent. Should be self-contained — the subagent does not see the parent's conversation."`
	SubagentType string `json:"subagent_type" jsonschema:"required,description=Name of the subagent to spawn. Available: 'explore' (read-only codebase investigator) or 'research' (online research with cited summary)."`
	TaskID       string `json:"task_id,omitzero" jsonschema:"description=Resume an existing child by id. Empty creates a new child session."`
}

// TaskTool spawns a subagent run via the SpawnTask closure on ToolContext
// and returns the child's final assistant text plus the child task_id so
// the parent can resume the same child later.
type TaskTool struct {
	definition api.ToolDefinition
}

// NewTaskTool builds a TaskTool. The schema is pre-computed once at startup
// so Definition() returns a stable []byte; the tool is otherwise stateless.
func NewTaskTool() *TaskTool {
	return &TaskTool{
		definition: api.ToolDefinition{
			Name:        "task",
			Description: "Spawn a subagent (explore for codebase, research for the open web) to investigate a focused question and return a tight summary. The subagent runs read-only; only its final assistant message reaches you. Run one research subagent at a time unless the questions are clearly independent.",
			InputSchema: GenerateSchema(TaskInput{}),
		},
	}
}

func (t *TaskTool) Name() string                   { return "task" }
func (t *TaskTool) Definition() api.ToolDefinition { return t.definition }

// MaxResultBytes: subagent summaries are typically a few KB. Allow up to
// 64KB before the per-turn aggregator clips. Keeps long investigation
// outputs visible without letting an unbounded child crowd out other work.
func (t *TaskTool) MaxResultBytes() int { return 64 * 1024 }

// IsReadOnly: false — subagents may write (build subagents, when added).
// Today's only subagent (explore) is read-only by allowlist, but the tool
// itself doesn't constrain children.
func (t *TaskTool) IsReadOnly() bool { return false }

// IsConcurrencySafe: true — the provider gate bounds actual concurrency.
// Returning true lets the dispatcher batch independent task calls under an
// errgroup; serialization happens naturally when MaxParallel=1.
func (t *TaskTool) IsConcurrencySafe() bool { return true }

// PermissionKey returns the subagent_type so users can write a project
// rule like `task:explore = allow` to skip the per-call prompt.
func (t *TaskTool) PermissionKey(input []byte) string {
	var in TaskInput
	if err := json.Unmarshal(input, &in); err != nil {
		return ""
	}
	return in.SubagentType
}

// FormatForDisplay renders the task call header as
// `Explore("investigate auth flow")`.
func (t *TaskTool) FormatForDisplay(input []byte) string {
	var in TaskInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "Task(?)"
	}
	name := taskDisplayName(in.SubagentType)
	desc := strings.TrimSpace(in.Description)
	if desc == "" {
		return name
	}
	return name + "(" + QuoteArg(desc, 80) + ")"
}

func taskDisplayName(subagentType string) string {
	subagentType = strings.TrimSpace(subagentType)
	if subagentType == "" {
		return "Task"
	}
	return strings.ToUpper(subagentType[:1]) + subagentType[1:]
}

// Execute dispatches via tc.SpawnTask. Returns the subagent's final text
// plus its task_id — the LLM can re-issue task with that task_id to
// continue the same child run.
func (t *TaskTool) Execute(ctx context.Context, tc agent.ToolContext, input []byte) (agent.Result, error) {
	var in TaskInput
	if err := json.Unmarshal(input, &in); err != nil {
		return agent.Result{}, fmt.Errorf("task: parse input: %w", err)
	}
	if strings.TrimSpace(in.SubagentType) == "" {
		return agent.Result{}, fmt.Errorf("task: subagent_type is required")
	}
	if strings.TrimSpace(in.Prompt) == "" {
		return agent.Result{}, fmt.Errorf("task: prompt is required")
	}
	if tc.SpawnTask == nil {
		return agent.Result{}, fmt.Errorf("task: subagent spawning is not available in this context (subagents cannot recurse)")
	}

	res, err := tc.SpawnTask(agent.WithParentSession(ctx, tc.SessionID), agent.TaskSpawnInput{
		SubagentType:    in.SubagentType,
		Prompt:          in.Prompt,
		TaskID:          in.TaskID,
		Description:     in.Description,
		ParentAgentName: tc.AgentName,
		// Forward selected subagent events (tool calls, tool status,
		// step heartbeats) up to the parent's run-stream so the user
		// sees the child's progress live.
		EventSink: tc.Publish,
	})
	if err != nil {
		return agent.Result{}, fmt.Errorf("task[%s]: %w", in.SubagentType, err)
	}

	body := strings.TrimSpace(res.Result)
	if body == "" {
		body = "(no assistant text returned)"
	}
	out := fmt.Sprintf("task_id: %s\n\n%s", res.TaskID, body)
	idPrefix := res.TaskID
	if len(idPrefix) > 8 {
		idPrefix = idPrefix[:8]
	}
	return agent.Result{
		Content:        out,
		Bytes:          len(out),
		DisplaySummary: fmt.Sprintf("task_id: %s · %s returned", idPrefix, HumanizeBytes(len(body))),
	}, nil
}
