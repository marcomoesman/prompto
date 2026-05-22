package tool

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/marcomoesman/prompto/internal/agent"
	"github.com/marcomoesman/prompto/internal/api"
)

// TodoStatus is the allowed enum for Todo.Status. Mirrors Claude Code:
// pending → in_progress → completed.
type TodoStatus string

const (
	TodoPending    TodoStatus = "pending"
	TodoInProgress TodoStatus = "in_progress"
	TodoCompleted  TodoStatus = "completed"
)

// Todo is the JSON shape exposed to the model. Convertible to agent.Todo
// (the persistence/render shape) at the seam.
type Todo struct {
	ID         string     `json:"id"          jsonschema:"required,description=Stable ID; reuse across turns to mark progress on the same item."`
	Content    string     `json:"content"     jsonschema:"required,description=Imperative task description (e.g. 'Add notifier checker for stale todos')."`
	Status     TodoStatus `json:"status"      jsonschema:"required,description=One of: pending, in_progress, completed."`
	ActiveForm string     `json:"active_form" jsonschema:"required,description=Present-continuous form shown while in_progress (e.g. 'Adding notifier checker for stale todos')."`
}

// TodoWriteInput is the JSON parameter struct. Atomic full-list replace —
// callers send the entire new list every time, never a diff.
type TodoWriteInput struct {
	Todos []Todo `json:"todos" jsonschema:"required,description=Full new todo list. Atomic replacement: send the entire list every time, not a diff."`
}

// TodoWriteTool persists a session-scoped todo list. The list is the
// model's working-memory anchor across compaction boundaries; the run
// loop re-renders it into the volatile system prompt each turn.
type TodoWriteTool struct {
	definition api.ToolDefinition
}

// NewTodoWriteTool builds a TodoWriteTool. The schema is pre-computed
// once; the tool is otherwise stateless. Persistence runs through
// agent.ToolContext.SaveTodos at execute time.
func NewTodoWriteTool() *TodoWriteTool {
	return &TodoWriteTool{
		definition: api.ToolDefinition{
			Name:        "todowrite",
			Description: "Persist your working todo list for this session. Atomic full-list replace: send the entire new list every time, not a diff. Each todo: {id, content, status: pending|in_progress|completed, active_form}. Exactly one todo may be in_progress at a time. Reuse stable IDs across turns to mark progress on the same item. The list re-renders into your system prompt every turn so it survives compaction.",
			InputSchema: GenerateSchema(TodoWriteInput{}),
		},
	}
}

func (t *TodoWriteTool) Name() string                   { return "todowrite" }
func (t *TodoWriteTool) Definition() api.ToolDefinition { return t.definition }

// MaxResultBytes: confirmation strings are tiny; 256 is generous.
func (t *TodoWriteTool) MaxResultBytes() int { return 256 }

// IsReadOnly: false — writes the session sidecar.
func (t *TodoWriteTool) IsReadOnly() bool { return false }

// IsConcurrencySafe: false — atomic full-list replace must be the only
// writer in the turn.
func (t *TodoWriteTool) IsConcurrencySafe() bool { return false }

// PermissionKey returns "" — todowrite is allowed unconditionally for
// primaries (no per-call gate). Subagents never see it because
// AllAgentDisallowedTools strips it from their resolver.
func (t *TodoWriteTool) PermissionKey(_ []byte) string { return "" }

// FormatForDisplay renders `TodoWrite(items: 5)`. The detailed
// pending/in-progress/done breakdown moves to the success summary
// line so the call header stays one row short.
func (t *TodoWriteTool) FormatForDisplay(input []byte) string {
	var in TodoWriteInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "TodoWrite(?)"
	}
	return FormatCall("TodoWrite", "items", fmt.Sprintf("%d", len(in.Todos)))
}

// Execute validates the input list, persists it via tc.SaveTodos, and
// returns a one-line confirmation. Validation errors come back as a tool
// error; the model should re-issue a corrected list.
func (t *TodoWriteTool) Execute(ctx context.Context, tc agent.ToolContext, input []byte) (agent.Result, error) {
	var in TodoWriteInput
	if err := json.Unmarshal(input, &in); err != nil {
		return agent.Result{}, fmt.Errorf("todowrite: parse input: %w", err)
	}

	if err := validateTodos(in.Todos); err != nil {
		return agent.Result{}, fmt.Errorf("todowrite: %w", err)
	}

	// Persist via the closure stamped by the run loop. nil saver = no
	// persistence (tests / runs without a TodoStore); the call still
	// reports success so model behavior is consistent across modes.
	if tc.SaveTodos != nil && tc.SessionID != "" {
		if err := tc.SaveTodos(ctx, tc.SessionID, toAgentTodos(in.Todos)); err != nil {
			return agent.Result{}, fmt.Errorf("todowrite: persist: %w", err)
		}
	}

	pending, inProgress, done := tallyTodos(in.Todos)
	out := fmt.Sprintf("TodoWrite: %d pending, %d in progress, %d done", pending, inProgress, done)
	return agent.Result{
		Content:        out,
		Bytes:          len(out),
		DisplaySummary: fmt.Sprintf("%d pending · %d in progress · %d done", pending, inProgress, done),
	}, nil
}

// validateTodos enforces: unique IDs, valid status, at most one in_progress.
func validateTodos(todos []Todo) error {
	seen := make(map[string]struct{}, len(todos))
	inProgressCount := 0
	for i, td := range todos {
		if td.ID == "" {
			return fmt.Errorf("todo[%d]: id is required", i)
		}
		if _, dup := seen[td.ID]; dup {
			return fmt.Errorf("todo[%d]: duplicate id %q", i, td.ID)
		}
		seen[td.ID] = struct{}{}

		switch td.Status {
		case TodoPending, TodoCompleted:
			// ok
		case TodoInProgress:
			inProgressCount++
		default:
			return fmt.Errorf("todo[%d]: invalid status %q (want pending|in_progress|completed)", i, td.Status)
		}
	}
	if inProgressCount > 1 {
		return fmt.Errorf("at most one todo may be in_progress; got %d", inProgressCount)
	}
	return nil
}

func tallyTodos(todos []Todo) (pending, inProgress, done int) {
	for _, td := range todos {
		switch td.Status {
		case TodoInProgress:
			inProgress++
		case TodoCompleted:
			done++
		default:
			pending++
		}
	}
	return
}

func toAgentTodos(todos []Todo) []agent.Todo {
	out := make([]agent.Todo, len(todos))
	for i, td := range todos {
		out[i] = agent.Todo{
			ID:         td.ID,
			Content:    td.Content,
			Status:     string(td.Status),
			ActiveForm: td.ActiveForm,
		}
	}
	return out
}
