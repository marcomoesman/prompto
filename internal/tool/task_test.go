package tool

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/marcomoesman/prompto/internal/agent"
)

func TestTaskTool_FormatForDisplay(t *testing.T) {
	tt := NewTaskTool()
	in, _ := json.Marshal(TaskInput{
		Description:  "find auth files",
		SubagentType: "explore",
		Prompt:       "...",
	})
	got := tt.FormatForDisplay(in)
	want := `Explore("find auth files")`
	if got != want {
		t.Errorf("FormatForDisplay = %q, want %q", got, want)
	}
}

func TestTaskTool_FormatForDisplayResearch(t *testing.T) {
	tt := NewTaskTool()
	in, _ := json.Marshal(TaskInput{
		Description:  "Research Pi coding agent system prompts",
		SubagentType: "research",
		Prompt:       "...",
	})
	got := tt.FormatForDisplay(in)
	want := `Research("Research Pi coding agent system prompts")`
	if got != want {
		t.Errorf("FormatForDisplay = %q, want %q", got, want)
	}
}

func TestTaskTool_PermissionKey(t *testing.T) {
	tt := NewTaskTool()
	in, _ := json.Marshal(TaskInput{SubagentType: "explore", Prompt: "x", Description: "y"})
	if got := tt.PermissionKey(in); got != "explore" {
		t.Errorf("PermissionKey = %q", got)
	}
}

func TestTaskTool_FlagsAndDefinition(t *testing.T) {
	tt := NewTaskTool()
	if tt.Name() != "task" {
		t.Errorf("Name = %q", tt.Name())
	}
	if !tt.IsConcurrencySafe() {
		t.Error("task should be concurrency-safe (gate handles real cap)")
	}
	if tt.IsReadOnly() {
		t.Error("task is not read-only — children may write")
	}
	if tt.Definition().Name != "task" {
		t.Errorf("Definition.Name = %q", tt.Definition().Name)
	}
}

func TestTaskTool_RequiresSpawnTask(t *testing.T) {
	tt := NewTaskTool()
	in, _ := json.Marshal(TaskInput{SubagentType: "explore", Prompt: "x", Description: "y"})
	tc := agent.ToolContext{} // SpawnTask is nil
	_, err := tt.Execute(t.Context(), tc, in)
	if err == nil {
		t.Fatal("expected error when SpawnTask is nil")
	}
	if !strings.Contains(err.Error(), "subagent") {
		t.Errorf("err = %q, expected mention of subagent", err.Error())
	}
}

func TestTaskTool_DispatchesToSpawner(t *testing.T) {
	tt := NewTaskTool()
	var called int
	var got agent.TaskSpawnInput
	tc := agent.ToolContext{
		SessionID: "parent-sess",
		SpawnTask: func(_ context.Context, in agent.TaskSpawnInput) (agent.TaskSpawnResult, error) {
			called++
			got = in
			return agent.TaskSpawnResult{TaskID: "child-id", Result: "found 3 files"}, nil
		},
	}
	args, _ := json.Marshal(TaskInput{
		SubagentType: "explore",
		Description:  "find auth",
		Prompt:       "look for auth files",
	})
	res, err := tt.Execute(t.Context(), tc, args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if called != 1 {
		t.Errorf("SpawnTask called %d times, want 1", called)
	}
	if got.SubagentType != "explore" || got.Prompt != "look for auth files" {
		t.Errorf("forwarded args wrong: %+v", got)
	}
	if !strings.Contains(res.Content, "task_id: child-id") {
		t.Errorf("Result missing task_id: %q", res.Content)
	}
	if !strings.Contains(res.Content, "found 3 files") {
		t.Errorf("Result missing payload: %q", res.Content)
	}
}

func TestTaskTool_SpawnerErrorPropagates(t *testing.T) {
	tt := NewTaskTool()
	tc := agent.ToolContext{
		SpawnTask: func(_ context.Context, _ agent.TaskSpawnInput) (agent.TaskSpawnResult, error) {
			return agent.TaskSpawnResult{}, errors.New("boom")
		},
	}
	args, _ := json.Marshal(TaskInput{SubagentType: "explore", Description: "d", Prompt: "p"})
	_, err := tt.Execute(t.Context(), tc, args)
	if err == nil {
		t.Fatal("expected error from SpawnTask to propagate")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("err = %q, expected wrapped boom", err.Error())
	}
}

func TestTaskTool_RequiresSubagentTypeAndPrompt(t *testing.T) {
	tt := NewTaskTool()
	tc := agent.ToolContext{
		SpawnTask: func(_ context.Context, _ agent.TaskSpawnInput) (agent.TaskSpawnResult, error) {
			t.Fatal("SpawnTask should not be called for invalid input")
			return agent.TaskSpawnResult{}, nil
		},
	}

	missingType, _ := json.Marshal(TaskInput{Prompt: "x", Description: "y"})
	if _, err := tt.Execute(t.Context(), tc, missingType); err == nil {
		t.Error("expected error when subagent_type empty")
	}
	missingPrompt, _ := json.Marshal(TaskInput{SubagentType: "explore", Description: "y"})
	if _, err := tt.Execute(t.Context(), tc, missingPrompt); err == nil {
		t.Error("expected error when prompt empty")
	}
}

// TestTaskTool_ConcurrentInvocations ensures the tool itself has no shared
// mutable state between calls.
func TestTaskTool_ConcurrentInvocations(t *testing.T) {
	tt := NewTaskTool()
	tc := agent.ToolContext{
		SpawnTask: func(_ context.Context, in agent.TaskSpawnInput) (agent.TaskSpawnResult, error) {
			return agent.TaskSpawnResult{TaskID: in.SubagentType + "-tid", Result: in.Description}, nil
		},
	}
	args, _ := json.Marshal(TaskInput{SubagentType: "explore", Description: "x", Prompt: "p"})

	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			if _, err := tt.Execute(t.Context(), tc, args); err != nil {
				t.Errorf("Execute: %v", err)
			}
		})
	}
	wg.Wait()
}
