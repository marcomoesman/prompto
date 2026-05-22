package store

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/marcomoesman/prompto/internal/api"
)

func newSessionFor(t *testing.T, s *Store) string {
	t.Helper()
	sess, err := s.CreateSession(t.Context(), CreateSessionInput{Model: "m"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	return sess.ID
}

func TestMessages_AppendPreservesOrdinal(t *testing.T) {
	s := openMem(t)
	ctx := t.Context()
	sid := newSessionFor(t, s)

	for range 5 {
		if err := s.AppendMessage(ctx, sid, api.NewUserMessage("hi"), nil); err != nil {
			t.Fatalf("AppendMessage: %v", err)
		}
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT ordinal FROM messages WHERE session_id = ? ORDER BY ordinal`, sid)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()

	var ords []int
	for rows.Next() {
		var o int
		_ = rows.Scan(&o)
		ords = append(ords, o)
	}
	if len(ords) != 5 {
		t.Fatalf("got %d rows, want 5", len(ords))
	}
	for i, o := range ords {
		if o != i {
			t.Errorf("ords[%d] = %d, want %d", i, o, i)
		}
	}
}

func TestMessages_LoadReturnsOrdered(t *testing.T) {
	s := openMem(t)
	ctx := t.Context()
	sid := newSessionFor(t, s)

	first := api.NewUserMessage("first")
	second := api.NewUserMessage("second")
	third := api.NewUserMessage("third")

	_ = s.AppendMessage(ctx, sid, first, nil)
	_ = s.AppendMessage(ctx, sid, second, nil)
	_ = s.AppendMessage(ctx, sid, third, nil)

	msgs, err := s.LoadMessages(ctx, sid)
	if err != nil {
		t.Fatalf("LoadMessages: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("got %d messages, want 3", len(msgs))
	}
	if msgs[0].ID != first.ID || msgs[1].ID != second.ID || msgs[2].ID != third.ID {
		t.Errorf("order wrong: %q %q %q", msgs[0].ID, msgs[1].ID, msgs[2].ID)
	}
}

func TestMessages_RoundTripText(t *testing.T) {
	s := openMem(t)
	ctx := t.Context()
	sid := newSessionFor(t, s)

	orig := api.NewUserMessage("hello world\nwith a newline")
	_ = s.AppendMessage(ctx, sid, orig, nil)

	msgs, err := s.LoadMessages(ctx, sid)
	if err != nil {
		t.Fatalf("LoadMessages: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	if msgs[0].Text() != "hello world\nwith a newline" {
		t.Errorf("text = %q", msgs[0].Text())
	}
}

func TestMessages_RoundTripToolUseAndResult(t *testing.T) {
	s := openMem(t)
	ctx := t.Context()
	sid := newSessionFor(t, s)

	assistant := api.NewAssistantMessage()
	assistant.Content = []api.ContentBlock{
		{Type: api.BlockText, Text: "calling tool"},
		{Type: api.BlockToolUse, ToolCall: &api.ToolCall{
			ID:    "tc_1",
			Name:  "read",
			Input: json.RawMessage(`{"path":"/tmp/x"}`),
		}},
	}
	toolResult := api.Message{
		ID:   "msg_tr",
		Role: api.RoleTool,
		Content: []api.ContentBlock{
			{Type: api.BlockToolResult, ToolResult: &api.ToolResult{
				ToolCallID: "tc_1",
				Content:    "file contents",
				IsError:    false,
			}},
		},
	}

	if err := s.AppendMessage(ctx, sid, assistant, &api.Usage{InputTokens: 10, OutputTokens: 3}); err != nil {
		t.Fatalf("append assistant: %v", err)
	}
	if err := s.AppendMessage(ctx, sid, toolResult, nil); err != nil {
		t.Fatalf("append tool: %v", err)
	}

	msgs, err := s.LoadMessages(ctx, sid)
	if err != nil {
		t.Fatalf("LoadMessages: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2", len(msgs))
	}
	// Assistant message round-trip.
	if len(msgs[0].Content) != 2 {
		t.Fatalf("assistant content blocks = %d, want 2", len(msgs[0].Content))
	}
	tu := msgs[0].Content[1]
	if tu.Type != api.BlockToolUse || tu.ToolCall == nil {
		t.Fatalf("block 1 = %+v, want tool_use", tu)
	}
	if tu.ToolCall.ID != "tc_1" || tu.ToolCall.Name != "read" {
		t.Errorf("tool_use payload mismatch: %+v", tu.ToolCall)
	}
	if string(tu.ToolCall.Input) != `{"path":"/tmp/x"}` {
		t.Errorf("tool_use input = %s", tu.ToolCall.Input)
	}
	// Tool result round-trip.
	tr := msgs[1].Content[0]
	if tr.Type != api.BlockToolResult || tr.ToolResult == nil {
		t.Fatalf("block = %+v, want tool_result", tr)
	}
	if tr.ToolResult.ToolCallID != "tc_1" || tr.ToolResult.Content != "file contents" {
		t.Errorf("tool_result payload mismatch: %+v", tr.ToolResult)
	}
}

func TestMessages_CountMessages(t *testing.T) {
	s := openMem(t)
	ctx := t.Context()
	sid := newSessionFor(t, s)

	for range 3 {
		_ = s.AppendMessage(ctx, sid, api.NewUserMessage("x"), nil)
	}
	n, err := s.CountMessages(ctx, sid)
	if err != nil {
		t.Fatalf("CountMessages: %v", err)
	}
	if n != 3 {
		t.Errorf("count = %d, want 3", n)
	}
}

func TestMessages_AppendSummary_TrimsHeadOnLoad(t *testing.T) {
	s := openMem(t)
	ctx := t.Context()
	sid := newSessionFor(t, s)

	first := api.NewUserMessage("first")
	second := api.NewUserMessage("second")
	third := api.NewUserMessage("third")
	if err := s.AppendMessage(ctx, sid, first, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendMessage(ctx, sid, second, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendMessage(ctx, sid, third, nil); err != nil {
		t.Fatal(err)
	}

	// Summary replaces "first" and "second" (boundary message id = second).
	summary := api.NewUserMessage("<compact_summary>1+2</compact_summary>")
	if err := s.AppendSummaryMessage(ctx, sid, summary, second.ID); err != nil {
		t.Fatalf("AppendSummaryMessage: %v", err)
	}

	got, err := s.LoadMessages(ctx, sid)
	if err != nil {
		t.Fatalf("LoadMessages: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d messages, want 2 (third + summary)", len(got))
	}
	if got[0].ID != third.ID {
		t.Errorf("got[0] = %q, want third (%q)", got[0].ID, third.ID)
	}
	if got[1].ID != summary.ID {
		t.Errorf("got[1] = %q, want summary (%q)", got[1].ID, summary.ID)
	}
}

func TestMessages_AppendSummary_SuccessiveCompactionsHonourLatestBoundary(t *testing.T) {
	s := openMem(t)
	ctx := t.Context()
	sid := newSessionFor(t, s)

	// Build a chain: m1 m2 m3 [summary1 replacing m1..m2] m4 m5 [summary2 replacing m3..m4]
	m1 := api.NewUserMessage("1")
	m2 := api.NewUserMessage("2")
	m3 := api.NewUserMessage("3")
	for _, m := range []api.Message{m1, m2, m3} {
		if err := s.AppendMessage(ctx, sid, m, nil); err != nil {
			t.Fatal(err)
		}
	}
	summary1 := api.NewUserMessage("<compact_summary>s1</compact_summary>")
	if err := s.AppendSummaryMessage(ctx, sid, summary1, m2.ID); err != nil {
		t.Fatalf("AppendSummaryMessage 1: %v", err)
	}

	m4 := api.NewUserMessage("4")
	m5 := api.NewUserMessage("5")
	for _, m := range []api.Message{m4, m5} {
		if err := s.AppendMessage(ctx, sid, m, nil); err != nil {
			t.Fatal(err)
		}
	}
	summary2 := api.NewUserMessage("<compact_summary>s2</compact_summary>")
	if err := s.AppendSummaryMessage(ctx, sid, summary2, m4.ID); err != nil {
		t.Fatalf("AppendSummaryMessage 2: %v", err)
	}

	got, err := s.LoadMessages(ctx, sid)
	if err != nil {
		t.Fatalf("LoadMessages: %v", err)
	}
	// Latest boundary is m4's ordinal (5). Past-boundary rows: m5 (ord 6), summary2 (ord 7).
	// summary1 (ord 3) and earlier are trimmed.
	if len(got) != 2 {
		t.Fatalf("got %d messages, want 2 (m5 + summary2), ids = %v", len(got), idsOf(got))
	}
	if got[0].ID != m5.ID {
		t.Errorf("got[0] = %q, want m5 (%q)", got[0].ID, m5.ID)
	}
	if got[1].ID != summary2.ID {
		t.Errorf("got[1] = %q, want summary2 (%q)", got[1].ID, summary2.ID)
	}
}

func TestMessages_AppendSummary_UnknownBoundaryRollsBack(t *testing.T) {
	s := openMem(t)
	ctx := t.Context()
	sid := newSessionFor(t, s)

	// Pre-existing non-boundary message so we have something to count.
	keep := api.NewUserMessage("keep")
	if err := s.AppendMessage(ctx, sid, keep, nil); err != nil {
		t.Fatal(err)
	}

	summary := api.NewUserMessage("<compact_summary>x</compact_summary>")
	err := s.AppendSummaryMessage(ctx, sid, summary, "msg-that-does-not-exist")
	if !errors.Is(err, ErrUnknownMessage) {
		t.Fatalf("err = %v, want ErrUnknownMessage", err)
	}

	// Neither the summary message nor a compactions row should have landed.
	got, err := s.LoadMessages(ctx, sid)
	if err != nil {
		t.Fatalf("LoadMessages: %v", err)
	}
	if len(got) != 1 || got[0].ID != keep.ID {
		t.Fatalf("after rollback got %v, want exactly the keep message", idsOf(got))
	}

	var compactionRows int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM compactions WHERE session_id = ?`, sid,
	).Scan(&compactionRows); err != nil {
		t.Fatal(err)
	}
	if compactionRows != 0 {
		t.Errorf("compactions rows after rollback = %d, want 0", compactionRows)
	}
}

func idsOf(msgs []api.Message) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.ID
	}
	return out
}

func TestMessages_AppendBumpsSessionUpdatedAt(t *testing.T) {
	s := openMem(t)
	ctx := t.Context()
	sess, _ := s.CreateSession(ctx, CreateSessionInput{Model: "m"})
	origMs := sess.UpdatedAt.UnixMilli()

	// Sleep so the UnixMilli tick advances (storage resolution is ms).
	time.Sleep(3 * time.Millisecond)
	if err := s.AppendMessage(ctx, sess.ID, api.NewUserMessage("x"), nil); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetSession(ctx, sess.ID)
	if got.UpdatedAt.UnixMilli() <= origMs {
		t.Errorf("updated_at did not advance at ms resolution: %d → %d", origMs, got.UpdatedAt.UnixMilli())
	}
}
