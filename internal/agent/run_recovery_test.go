package agent

import (
	"errors"
	"strings"
	"testing"

	"github.com/marcomoesman/prompto/internal/api"
)

// emptyResponse is a stream that yields no text and no tool calls —
// the degenerate "empty assistant turn" some open-weights models
// emit when they confuse the textual <tool_call> convention with
// the structured tool-calling API.
func emptyResponse() []api.StreamEvent {
	return []api.StreamEvent{
		{Type: api.EventDone, StopReason: "stop"},
	}
}

// invalidArgsResponse builds a stream where a tool call's accumulated
// arguments are not valid JSON (just `{` — common when a max_tokens
// cap truncates mid-stream).
func invalidArgsResponse(id, name string) []api.StreamEvent {
	return []api.StreamEvent{
		{Type: api.EventToolCallStart, ToolCallIndex: 0, ToolCallID: id, ToolCallName: name},
		{Type: api.EventToolCallDelta, ToolCallIndex: 0, ToolCallArgs: "{"},
		{Type: api.EventDone, StopReason: "tool_use"},
	}
}

// TestRun_EmptyTurnNudgeAllowsRecovery covers the open-weights
// empty-assistant case. First provider call yields no text and no
// tool calls; the agent must inject a one-shot reminder and call
// the provider a second time. The second call returns clean text,
// which closes the turn normally.
func TestRun_EmptyTurnNudgeAllowsRecovery(t *testing.T) {
	prov := &fakeProvider{responses: [][]api.StreamEvent{
		emptyResponse(),
		textResponse("ok now I'll respond properly"),
	}}
	agnt := New(NewAgentInput{
		Provider: prov,
		Model:    "test",
		Tools:    newFakeResolver(),
		Notifier: NewNotifier(),
	})
	conv := NewConversation()
	conv.Append(api.NewUserMessage("hi"))
	rr := agnt.Run(t.Context(), RunInput{
		Conversation: conv,
		MaxSteps:     5,
		CanUseTool:   allowAll,
	})
	if err := drain(t, rr); !errors.Is(err, ErrEndTurn) {
		t.Fatalf("run returned %v; want ErrEndTurn (clean recovery)", err)
	}
	if prov.calls != 2 {
		t.Errorf("expected 2 provider calls (empty turn + retry), got %d", prov.calls)
	}
	if len(prov.params) < 2 || !paramsContainText(prov.params[1], singleNextActionInstruction) {
		t.Fatalf("retry params did not include single-next-action reminder: %#v", prov.params)
	}
}

// TestRun_EmptyTurnNudgeBoundedToOnce regresses the safety bound:
// a model that keeps returning empty turns must not loop forever.
// Second empty turn ends with ErrEndTurn rather than another nudge.
func TestRun_EmptyTurnNudgeBoundedToOnce(t *testing.T) {
	prov := &fakeProvider{responses: [][]api.StreamEvent{
		emptyResponse(),
		emptyResponse(), // second empty turn — must NOT trigger another nudge
	}}
	agnt := New(NewAgentInput{
		Provider: prov,
		Model:    "test",
		Tools:    newFakeResolver(),
		Notifier: NewNotifier(),
	})
	conv := NewConversation()
	conv.Append(api.NewUserMessage("hi"))
	rr := agnt.Run(t.Context(), RunInput{
		Conversation: conv,
		MaxSteps:     5,
		CanUseTool:   allowAll,
	})
	if err := drain(t, rr); !errors.Is(err, ErrEndTurn) {
		t.Fatalf("expected ErrEndTurn after second empty turn, got %v", err)
	}
	if prov.calls != 2 {
		t.Errorf("expected exactly 2 provider calls (one nudge max), got %d", prov.calls)
	}
}

// TestRun_InvalidJSONArgsDoesNotKillTurn covers the recovery path
// for malformed tool-call arguments. Previously, invalid JSON
// returned a fatal error and ended the turn. Now the agent
// substitutes `{}` in the persisted call, synthesizes a tool_result
// error explaining the failure, and continues the loop so the
// model can retry on the next turn.
func TestRun_InvalidJSONArgsDoesNotKillTurn(t *testing.T) {
	prov := &fakeProvider{responses: [][]api.StreamEvent{
		invalidArgsResponse("tc_1", "echo"),
		textResponse("understood, retrying with valid args"),
	}}
	agnt := New(NewAgentInput{
		Provider: prov,
		Model:    "test",
		Tools:    newFakeResolver(&echoTool{name: "echo", result: "ok"}),
	})
	conv := NewConversation()
	conv.Append(api.NewUserMessage("hi"))
	rr := agnt.Run(t.Context(), RunInput{
		Conversation: conv,
		MaxSteps:     5,
		CanUseTool:   allowAll,
	})

	var sawSynthesizedError bool
	for evt := range rr.Events {
		if evt.Type == EventToolCallDone && evt.ToolError && strings.Contains(evt.ToolResult, "invalid JSON arguments") {
			sawSynthesizedError = true
		}
	}
	if err := <-rr.Done; !errors.Is(err, ErrEndTurn) {
		t.Fatalf("run returned %v; want ErrEndTurn (turn should have recovered)", err)
	}
	if !sawSynthesizedError {
		t.Error("expected EventToolCallDone with synthesized invalid-JSON error")
	}
	if prov.calls != 2 {
		t.Errorf("expected 2 provider calls (initial + retry), got %d", prov.calls)
	}

	// The persisted assistant message must carry valid wire JSON
	// (substituted `{}`) so the next provider call doesn't reject
	// it. Inspect the conversation for the tool_use block.
	msgs := conv.All()
	var foundSubstituted bool
	for _, m := range msgs {
		if m.Role != api.RoleAssistant {
			continue
		}
		for _, b := range m.Content {
			if b.Type == api.BlockToolUse && b.ToolCall != nil && b.ToolCall.ID == "tc_1" {
				if string(b.ToolCall.Input) == "{}" {
					foundSubstituted = true
				}
			}
		}
	}
	if !foundSubstituted {
		t.Errorf("expected invalid-args call to be persisted with `{}` substitution; got messages: %+v", msgs)
	}
}

func TestRun_InvalidJSONQueuesSingleNextActionFeedback(t *testing.T) {
	notifier := NewNotifier()
	prov := &fakeProvider{responses: [][]api.StreamEvent{
		invalidArgsResponse("tc_1", "echo"),
	}}
	agnt := New(NewAgentInput{
		Provider: prov,
		Model:    "test",
		Tools:    newFakeResolver(&echoTool{name: "echo", result: "ok"}),
		Notifier: notifier,
	})
	conv := NewConversation()
	conv.Append(api.NewUserMessage("hi"))
	if err := drain(t, agnt.Run(t.Context(), RunInput{
		Conversation: conv,
		MaxSteps:     1,
		CanUseTool:   allowAll,
	})); !errors.Is(err, ErrMaxSteps) {
		t.Fatalf("run returned %v, want ErrMaxSteps", err)
	}
	queued := notifier.ConsumeOneShot()
	if len(queued) != 1 || !strings.Contains(queued[0], "invalid JSON") || !strings.Contains(queued[0], singleNextActionInstruction) {
		t.Fatalf("queued = %#v, want invalid JSON single-next-action feedback", queued)
	}
}

func TestRun_EditFailureQueuesSingleNextActionFeedback(t *testing.T) {
	notifier := NewNotifier()
	prov := &fakeProvider{responses: [][]api.StreamEvent{
		toolUseResponse("tc_1", "edit", `{"file_path":"x","old_string":"a","new_string":"b"}`),
	}}
	agnt := New(NewAgentInput{
		Provider: prov,
		Model:    "test",
		Tools:    newFakeResolver(&echoTool{name: "edit", err: errors.New("edit x: edits[0]: old_string not found")}),
		Notifier: notifier,
	})
	conv := NewConversation()
	conv.Append(api.NewUserMessage("edit"))
	if err := drain(t, agnt.Run(t.Context(), RunInput{
		Conversation: conv,
		MaxSteps:     1,
		CanUseTool:   allowAll,
	})); !errors.Is(err, ErrMaxSteps) {
		t.Fatalf("run returned %v, want ErrMaxSteps", err)
	}
	queued := notifier.ConsumeOneShot()
	if len(queued) != 1 || !strings.Contains(queued[0], "old_string") || !strings.Contains(queued[0], "replace_lines") {
		t.Fatalf("queued = %#v, want edit failure feedback", queued)
	}
}

func TestRun_ReplaceLinesFailureQueuesSingleNextActionFeedback(t *testing.T) {
	notifier := NewNotifier()
	prov := &fakeProvider{responses: [][]api.StreamEvent{
		toolUseResponse("tc_1", "replace_lines", `{"file_path":"x","start_line":9,"end_line":9,"replacement":"x"}`),
	}}
	agnt := New(NewAgentInput{
		Provider: prov,
		Model:    "test",
		Tools:    newFakeResolver(&echoTool{name: "replace_lines", err: errors.New("replace_lines x: range 9-9 is beyond EOF; file has 2 lines")}),
		Notifier: notifier,
	})
	conv := NewConversation()
	conv.Append(api.NewUserMessage("replace"))
	if err := drain(t, agnt.Run(t.Context(), RunInput{
		Conversation: conv,
		MaxSteps:     1,
		CanUseTool:   allowAll,
	})); !errors.Is(err, ErrMaxSteps) {
		t.Fatalf("run returned %v, want ErrMaxSteps", err)
	}
	queued := notifier.ConsumeOneShot()
	if len(queued) != 1 || !strings.Contains(queued[0], "line range") || !strings.Contains(queued[0], singleNextActionInstruction) {
		t.Fatalf("queued = %#v, want replace_lines range feedback", queued)
	}
}

// TestSystemPromptDiscouragesTextualToolCalls is a content guard on
// the system prompt — the no-textual-tool-calls sentence is
// load-bearing for the empty-turn fix on Kimi/GLM/Hermes-style
// models. The block is conditional on LocalProvider
// because cloud models never need it; this test exercises the local
// path so a regression in either the constant or the assembly logic
// fails loudly.
func TestSystemPromptDiscouragesTextualToolCalls(t *testing.T) {
	got := buildToolUseRules(BuildSystemPromptInput{LocalProvider: true})
	for _, want := range []string{
		"structured tool-calling API",
		"<tool_call>",
		"NOT executed",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("local-provider tool_use_rules missing required guidance %q:\n%s", want, got)
		}
	}

	// Cloud-provider variant must NOT include the warning — that's
	// the whole point of the conditional. If this guard fires, the
	// LocalProvider gate has been removed.
	cloud := buildToolUseRules(BuildSystemPromptInput{LocalProvider: false})
	if strings.Contains(cloud, "<tool_call>") {
		t.Errorf("cloud-provider tool_use_rules should NOT include the textual-tool-call warning; got:\n%s", cloud)
	}
}

func paramsContainText(params api.CompleteParams, needle string) bool {
	for _, msg := range params.Messages {
		for _, block := range msg.Content {
			if block.Type == api.BlockText && strings.Contains(block.Text, needle) {
				return true
			}
		}
	}
	return false
}
