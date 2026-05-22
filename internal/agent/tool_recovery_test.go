package agent

import (
	"errors"
	"strings"
	"testing"

	"github.com/marcomoesman/prompto/internal/api"
)

func TestRecoverTextualToolCalls_AcceptsSupportedFormats(t *testing.T) {
	resolver := newFakeResolver(&echoTool{name: "read", result: "ok"}, &echoTool{name: "grep", result: "ok"})
	cases := []struct {
		name string
		text string
		want string
	}{
		{
			name: "fenced object",
			text: "```json\n{\"name\":\"read\",\"arguments\":{\"path\":\"x.go\"}}\n```",
			want: "read",
		},
		{
			name: "tool call tag",
			text: "<tool_call>{\"tool\":\"grep\",\"input\":{\"pattern\":\"x\"}}</tool_call>",
			want: "grep",
		},
		{
			name: "function calls array",
			text: "<function_calls>[{\"name\":\"read\",\"arguments\":{\"path\":\"x.go\"}}]</function_calls>",
			want: "read",
		},
		{
			name: "single tool-keyed object",
			text: "{\"read\":{\"path\":\"x.go\"}}",
			want: "read",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := recoverTextualToolCalls(tc.text, resolver)
			if len(got.Calls) != 1 {
				t.Fatalf("recovered %d calls, want 1: %+v", len(got.Calls), got.Calls)
			}
			if got.Calls[0].Name != tc.want {
				t.Fatalf("name = %q, want %q", got.Calls[0].Name, tc.want)
			}
			if !strings.HasPrefix(got.Calls[0].ID, "recovered_") {
				t.Fatalf("id = %q, want recovered_*", got.Calls[0].ID)
			}
		})
	}
}

func TestRecoverTextualToolCalls_RejectsUnsafeOrAmbiguous(t *testing.T) {
	resolver := newFakeResolver(&echoTool{name: "read", result: "ok"}, &echoTool{name: "also_read", result: "ok"})
	cases := []string{
		`{"name":"unknown","arguments":{}}`,
		`{"name":"read","arguments":["not-object"]}`,
		`{"name":"read","arguments":`,
		"```json\n{\"name\":\"read\",\"arguments\":{}}\n```\n```json\n{\"name\":\"read\",\"arguments\":{}}\n```",
		`{"read":{},"also_read":{}}`,
	}
	for _, text := range cases {
		if got := recoverTextualToolCalls(text, resolver); len(got.Calls) != 0 {
			t.Fatalf("recoverTextualToolCalls(%q) = %+v, want none", text, got)
		}
	}
}

func TestRecoverTextualToolCalls_StripsRecoveredPayloadFromText(t *testing.T) {
	resolver := newFakeResolver(&echoTool{name: "read", result: "ok"})
	got := recoverTextualToolCalls("I'll inspect it.\n```json\n{\"name\":\"read\",\"arguments\":{\"path\":\"x.go\"}}\n```\nThen continue.", resolver)
	if len(got.Calls) != 1 {
		t.Fatalf("recovered %d calls, want 1", len(got.Calls))
	}
	if strings.Contains(got.Text, "```") || strings.Contains(got.Text, `"name":"read"`) {
		t.Fatalf("recovered payload left in text: %q", got.Text)
	}
	if !strings.Contains(got.Text, "I'll inspect it.") || !strings.Contains(got.Text, "Then continue.") {
		t.Fatalf("surrounding text not preserved: %q", got.Text)
	}
}

func TestRun_LocalTextualToolCallIsRecoveredAndExecutes(t *testing.T) {
	prov := &fakeProvider{responses: [][]api.StreamEvent{
		textResponse("```json\n{\"name\":\"echo\",\"arguments\":{\"message\":\"hi\"}}\n```"),
		textResponse("done"),
	}}
	agnt := New(NewAgentInput{
		Provider:      prov,
		Model:         "test",
		Tools:         newFakeResolver(&echoTool{name: "echo", result: "ok"}),
		LocalProvider: true,
	})
	conv := NewConversation()
	conv.Append(api.NewUserMessage("call echo"))

	rr := agnt.Run(t.Context(), RunInput{Conversation: conv, MaxSteps: 5, CanUseTool: allowAll})
	var sawTool bool
	for evt := range rr.Events {
		if evt.Type == EventToolCallDone && evt.ToolName == "echo" && evt.ToolResult == "ok" {
			sawTool = true
		}
	}
	if err := <-rr.Done; !errors.Is(err, ErrEndTurn) {
		t.Fatalf("run returned %v, want ErrEndTurn", err)
	}
	if !sawTool {
		t.Fatal("expected recovered echo tool to execute")
	}
	if prov.calls != 2 {
		t.Fatalf("provider calls = %d, want 2", prov.calls)
	}
	for _, msg := range conv.Messages() {
		if strings.Contains(msg.Text(), `"name":"echo"`) {
			t.Fatalf("recovered textual payload should not be persisted as assistant text: %q", msg.Text())
		}
	}
}

func TestRun_CloudTextualToolCallAutoIsNotRecovered(t *testing.T) {
	prov := &fakeProvider{responses: [][]api.StreamEvent{
		textResponse("{\"name\":\"echo\",\"arguments\":{\"message\":\"hi\"}}"),
	}}
	agnt := New(NewAgentInput{
		Provider: prov,
		Model:    "test",
		Tools:    newFakeResolver(&echoTool{name: "echo", result: "should-not-run"}),
	})
	conv := NewConversation()
	conv.Append(api.NewUserMessage("call echo"))

	rr := agnt.Run(t.Context(), RunInput{Conversation: conv, MaxSteps: 5, CanUseTool: allowAll})
	for evt := range rr.Events {
		if evt.Type == EventToolCallDone {
			t.Fatalf("unexpected tool execution: %+v", evt)
		}
	}
	if err := <-rr.Done; !errors.Is(err, ErrEndTurn) {
		t.Fatalf("run returned %v, want ErrEndTurn", err)
	}
}
