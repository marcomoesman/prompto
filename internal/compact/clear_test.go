package compact

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/marcomoesman/prompto/internal/agent"
	"github.com/marcomoesman/prompto/internal/api"
)

// fakeTool lets tests control IsReadOnly per tool name.
type fakeTool struct {
	name     string
	readOnly bool
}

func (t *fakeTool) Name() string                   { return t.name }
func (t *fakeTool) Definition() api.ToolDefinition { return api.ToolDefinition{} }
func (t *fakeTool) FormatForDisplay([]byte) string { return "" }
func (t *fakeTool) MaxResultBytes() int            { return 0 }
func (t *fakeTool) IsReadOnly() bool               { return t.readOnly }
func (t *fakeTool) IsConcurrencySafe() bool        { return t.readOnly }
func (t *fakeTool) PermissionKey([]byte) string    { return "" }
func (t *fakeTool) Execute(context.Context, agent.ToolContext, []byte) (agent.Result, error) {
	return agent.Result{}, nil
}

// fakeResolver implements agent.ToolResolver.
type fakeResolver struct {
	tools map[string]agent.Tool
}

func newFakeResolver(tools ...agent.Tool) *fakeResolver {
	m := make(map[string]agent.Tool, len(tools))
	for _, t := range tools {
		m[t.Name()] = t
	}
	return &fakeResolver{tools: m}
}

func (r *fakeResolver) Resolve(name string) (agent.Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

func (r *fakeResolver) Definitions() []api.ToolDefinition { return nil }

// buildConv builds a conversation with alternating assistant(tool_use) /
// tool(tool_result) pairs. Each pair uses toolName for its name and
// tool_<i> for the id.
func buildConv(t *testing.T, toolName string, pairs int) *agent.Conversation {
	t.Helper()
	conv := agent.NewConversation()
	conv.Append(api.NewUserMessage("start"))
	for i := 0; i < pairs; i++ {
		assistant := api.NewAssistantMessage()
		assistant.Content = []api.ContentBlock{{
			Type: api.BlockToolUse,
			ToolCall: &api.ToolCall{
				ID:    "tc_" + itoa(i),
				Name:  toolName,
				Input: json.RawMessage(`{}`),
			},
		}}
		conv.Append(assistant)
		conv.Append(api.Message{
			Role: api.RoleTool,
			Content: []api.ContentBlock{{
				Type: api.BlockToolResult,
				ToolResult: &api.ToolResult{
					ToolCallID: "tc_" + itoa(i),
					Content:    "result " + itoa(i),
				},
			}},
		})
	}
	return conv
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

func TestClear_ReplacesOldReadOnlyResults(t *testing.T) {
	conv := buildConv(t, "read", 5) // 1 user + 5*(assistant+tool) = 11 messages
	resolver := newFakeResolver(&fakeTool{name: "read", readOnly: true})

	cleared := ClearOldToolResults(conv, ClearOpts{KeepRecent: 4, Resolver: resolver})
	if cleared == 0 {
		t.Fatal("expected some tool_results cleared, got 0")
	}

	// Last 4 messages should be untouched; earlier tool_results cleared.
	msgs := conv.All()
	cutoff := len(msgs) - 4
	for i, m := range msgs {
		for _, b := range m.Content {
			if b.Type != api.BlockToolResult || b.ToolResult == nil {
				continue
			}
			if i < cutoff {
				if b.ToolResult.Content != ClearedPlaceholder {
					t.Errorf("msg[%d] tool_result not cleared: %q", i, b.ToolResult.Content)
				}
			} else {
				if b.ToolResult.Content == ClearedPlaceholder {
					t.Errorf("msg[%d] in recent window was cleared", i)
				}
			}
		}
	}
}

func TestClear_PreservesNonReadOnlyResults(t *testing.T) {
	conv := buildConv(t, "edit", 5)
	resolver := newFakeResolver(&fakeTool{name: "edit", readOnly: false})

	cleared := ClearOldToolResults(conv, ClearOpts{KeepRecent: 2, Resolver: resolver})
	if cleared != 0 {
		t.Errorf("edit is non-read-only; expected 0 cleared, got %d", cleared)
	}
	// Verify nothing was modified.
	for _, m := range conv.All() {
		for _, b := range m.Content {
			if b.Type == api.BlockToolResult && b.ToolResult != nil && b.ToolResult.Content == ClearedPlaceholder {
				t.Errorf("non-read-only tool_result was cleared")
			}
		}
	}
}

func TestClear_NilResolverConservativelyClears(t *testing.T) {
	conv := buildConv(t, "bash", 5)
	cleared := ClearOldToolResults(conv, ClearOpts{KeepRecent: 2, Resolver: nil})
	if cleared == 0 {
		t.Error("nil resolver should clear all old tool_results; got 0")
	}
}

func TestClear_RecentResultsUntouched(t *testing.T) {
	conv := buildConv(t, "read", 3) // 1 + 6 = 7 messages
	resolver := newFakeResolver(&fakeTool{name: "read", readOnly: true})

	cleared := ClearOldToolResults(conv, ClearOpts{KeepRecent: 100, Resolver: resolver})
	if cleared != 0 {
		t.Errorf("KeepRecent larger than conv should clear 0; got %d", cleared)
	}
}

func TestClear_PairingIntact(t *testing.T) {
	conv := buildConv(t, "read", 4)
	resolver := newFakeResolver(&fakeTool{name: "read", readOnly: true})
	ClearOldToolResults(conv, ClearOpts{KeepRecent: 2, Resolver: resolver})

	// Count tool_use and tool_result blocks; should match.
	var useCount, resultCount int
	for _, m := range conv.All() {
		for _, b := range m.Content {
			switch b.Type {
			case api.BlockToolUse:
				useCount++
			case api.BlockToolResult:
				resultCount++
			}
		}
	}
	if useCount != resultCount {
		t.Errorf("pairing lost: %d tool_use vs %d tool_result", useCount, resultCount)
	}
}

func TestClear_IdempotentOnClearedResults(t *testing.T) {
	conv := buildConv(t, "read", 5)
	resolver := newFakeResolver(&fakeTool{name: "read", readOnly: true})

	first := ClearOldToolResults(conv, ClearOpts{KeepRecent: 2, Resolver: resolver})
	second := ClearOldToolResults(conv, ClearOpts{KeepRecent: 2, Resolver: resolver})
	if second != 0 {
		t.Errorf("second Clear should be 0 (already cleared); got %d (first=%d)", second, first)
	}
}
