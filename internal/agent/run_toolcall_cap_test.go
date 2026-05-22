package agent

import (
	"strings"
	"testing"

	"github.com/marcomoesman/prompto/internal/api"
)

// TestRun_ToolCallCapEnforced covers the per-turn cap. A pathological
// provider response that emits more than maxToolCallsPerTurn tool calls
// in one turn should see the first maxToolCallsPerTurn execute and the
// rest come back as recoverable tool errors. The model self-corrects
// on the next turn — we don't drive that loop here; MaxSteps=1 stops
// the run after the single response is processed.
func TestRun_ToolCallCapEnforced(t *testing.T) {
	const overshoot = 10
	total := maxToolCallsPerTurn + overshoot

	// Build a streamed response with `total` tool_use blocks.
	events := make([]api.StreamEvent, 0, total*2+1)
	for i := range total {
		events = append(events,
			api.StreamEvent{
				Type:          api.EventToolCallStart,
				ToolCallIndex: i,
				ToolCallID:    "tc_" + itoa(i),
				ToolCallName:  "echo",
			},
			api.StreamEvent{
				Type:          api.EventToolCallDelta,
				ToolCallIndex: i,
				ToolCallArgs:  `{}`,
			},
		)
	}
	events = append(events, api.StreamEvent{Type: api.EventDone, StopReason: "tool_use"})

	prov := &fakeProvider{responses: [][]api.StreamEvent{events}}
	agnt := New(NewAgentInput{
		Provider: prov,
		Model:    "test",
		Tools:    newFakeResolver(&echoTool{name: "echo", result: "ok"}),
	})
	conv := NewConversation()
	conv.Append(api.NewUserMessage("hi"))

	rr := agnt.Run(t.Context(), RunInput{
		Conversation: conv,
		MaxSteps:     1,
		CanUseTool:   allowAll,
	})

	// Denied plans emit EventToolCallDone twice — once in the plan
	// phase to surface the denial inline and once in the emission
	// phase walking the full plans slice. Dedupe by ToolCallID and
	// keep the LAST seen status per call (emission phase wins,
	// matching what the persisted tool_result message records).
	last := map[string]Event{}
	var ids []string
	for ev := range rr.Events {
		if ev.Type != EventToolCallDone {
			continue
		}
		if _, seen := last[ev.ToolCallID]; !seen {
			ids = append(ids, ev.ToolCallID)
		}
		last[ev.ToolCallID] = ev
	}
	<-rr.Done

	var okDone, capDone int
	for _, id := range ids {
		ev := last[id]
		switch {
		case ev.ToolError && strings.Contains(ev.ToolResult, "tool call cap exceeded"):
			capDone++
		case !ev.ToolError:
			okDone++
		}
	}

	if capDone != overshoot {
		t.Errorf("cap-denied tool calls = %d, want %d", capDone, overshoot)
	}
	if okDone != maxToolCallsPerTurn {
		t.Errorf("executed tool calls = %d, want %d", okDone, maxToolCallsPerTurn)
	}
}

// itoa is a tiny strconv-free int-to-string for test IDs so this file
// doesn't pull in strconv just for tc_NN labels.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [12]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
