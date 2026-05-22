package store

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/marcomoesman/prompto/internal/api"
)

// TestResume_RoundTrip covers the end-to-end story: create session, append a
// realistic three-turn exchange, close the store, reopen from disk, load the
// conversation, and verify the round-trip is byte-faithful.
func TestResume_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "db.sqlite")

	s1, err := Open(OpenInput{Path: path})
	if err != nil {
		t.Fatalf("open 1: %v", err)
	}

	sess, err := s1.CreateSession(t.Context(), CreateSessionInput{Model: "test-model"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	user := api.NewUserMessage("read go.mod please")

	assistant := api.NewAssistantMessage()
	assistant.Content = []api.ContentBlock{
		{Type: api.BlockText, Text: "I'll read it."},
		{Type: api.BlockToolUse, ToolCall: &api.ToolCall{
			ID:    "tc_1",
			Name:  "read",
			Input: json.RawMessage(`{"file_path":"go.mod"}`),
		}},
	}

	tool := api.Message{
		ID:   "msg_tool_1",
		Role: api.RoleTool,
		Content: []api.ContentBlock{
			{Type: api.BlockToolResult, ToolResult: &api.ToolResult{
				ToolCallID: "tc_1",
				Content:    "module github.com/marcomoesman/prompto\n",
				IsError:    false,
			}},
		},
	}

	if err := s1.AppendMessage(t.Context(), sess.ID, user, nil); err != nil {
		t.Fatal(err)
	}
	if err := s1.AppendMessage(t.Context(), sess.ID, assistant, &api.Usage{
		InputTokens: 20, OutputTokens: 5, CacheRead: 12,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s1.AppendMessage(t.Context(), sess.ID, tool, nil); err != nil {
		t.Fatal(err)
	}

	if err := s1.RecordFileChange(t.Context(), RecordFileChangeInput{
		SessionID:     sess.ID,
		MessageID:     assistant.ID,
		ToolCallID:    "tc_1",
		Path:          "go.mod",
		Op:            "modify",
		ContentBefore: []byte("old\n"),
		ContentAfter:  []byte("new\n"),
	}); err != nil {
		t.Fatal(err)
	}

	_ = s1.Close()

	// Re-open and verify round-trip.
	s2, err := Open(OpenInput{Path: path})
	if err != nil {
		t.Fatalf("open 2: %v", err)
	}
	t.Cleanup(func() { _ = s2.Close() })

	got, err := s2.LoadMessages(t.Context(), sess.ID)
	if err != nil {
		t.Fatalf("LoadMessages: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d messages, want 3", len(got))
	}

	if got[0].ID != user.ID || got[0].Text() != "read go.mod please" {
		t.Errorf("user message mismatch: id=%q text=%q", got[0].ID, got[0].Text())
	}

	if len(got[1].Content) != 2 {
		t.Fatalf("assistant content blocks = %d, want 2", len(got[1].Content))
	}
	tu := got[1].Content[1]
	if tu.ToolCall == nil || tu.ToolCall.Name != "read" {
		t.Errorf("tool_use mismatch: %+v", tu)
	}
	if !bytes.Equal(tu.ToolCall.Input, []byte(`{"file_path":"go.mod"}`)) {
		t.Errorf("tool_use input = %s", tu.ToolCall.Input)
	}

	tr := got[2].Content[0].ToolResult
	if tr == nil || tr.ToolCallID != "tc_1" {
		t.Errorf("tool_result mismatch: %+v", tr)
	}

	changes, err := s2.ListFileChangesBySession(t.Context(), sess.ID)
	if err != nil {
		t.Fatalf("ListFileChanges: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("got %d file changes, want 1", len(changes))
	}
	if !bytes.Equal(changes[0].ContentBefore, []byte("old\n")) ||
		!bytes.Equal(changes[0].ContentAfter, []byte("new\n")) {
		t.Errorf("file-change content round-trip lost")
	}
}

// TestResume_AfterSummarize covers the post-Phase-9 invariant: when a
// session was compacted in a previous process, reopening the DB and
// LoadMessages-ing must return exactly the post-boundary tail (the summary
// included), matching what Conversation.ReplaceHead produced in memory.
// Without the compactions table this test would fail because LoadMessages
// would return the full original head plus the summary appended at the end.
func TestResume_AfterSummarize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "db.sqlite")

	s1, err := Open(OpenInput{Path: path})
	if err != nil {
		t.Fatalf("open 1: %v", err)
	}

	sess, err := s1.CreateSession(t.Context(), CreateSessionInput{Model: "test-model"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Five-message head plus a summary that subsumes the first four. After
	// resume the loaded conversation should be [m5, summary] in that order.
	msgs := []api.Message{
		api.NewUserMessage("turn 1 user"),
		api.NewAssistantMessage(),
		api.NewUserMessage("turn 2 user"),
		api.NewAssistantMessage(),
		api.NewUserMessage("turn 3 user"),
	}
	// Give the bare assistant messages content so JSON round-trip stays sane.
	msgs[1].Content = []api.ContentBlock{{Type: api.BlockText, Text: "turn 1 assistant"}}
	msgs[3].Content = []api.ContentBlock{{Type: api.BlockText, Text: "turn 2 assistant"}}

	for _, m := range msgs {
		if err := s1.AppendMessage(t.Context(), sess.ID, m, nil); err != nil {
			t.Fatal(err)
		}
	}

	summary := api.NewUserMessage("<compact_summary>turns 1-2 condensed</compact_summary>")
	// Boundary: msgs[3] (the second assistant). msgs[4] (turn 3 user) survives.
	if err := s1.AppendSummaryMessage(t.Context(), sess.ID, summary, msgs[3].ID); err != nil {
		t.Fatalf("AppendSummaryMessage: %v", err)
	}

	_ = s1.Close()

	s2, err := Open(OpenInput{Path: path})
	if err != nil {
		t.Fatalf("open 2: %v", err)
	}
	t.Cleanup(func() { _ = s2.Close() })

	got, err := s2.LoadMessages(t.Context(), sess.ID)
	if err != nil {
		t.Fatalf("LoadMessages: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d messages, want 2 ([turn 3 user, summary]); ids=%v", len(got), idsOf(got))
	}
	if got[0].ID != msgs[4].ID {
		t.Errorf("got[0] = %q, want msgs[4] (%q)", got[0].ID, msgs[4].ID)
	}
	if got[1].ID != summary.ID {
		t.Errorf("got[1] = %q, want summary (%q)", got[1].ID, summary.ID)
	}
}
