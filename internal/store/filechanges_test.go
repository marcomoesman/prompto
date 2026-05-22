package store

import (
	"bytes"
	"testing"

	"github.com/marcomoesman/prompto/internal/api"
)

func TestFileChanges_RecordAndList(t *testing.T) {
	s := openMem(t)
	ctx := t.Context()
	sid := newSessionFor(t, s)

	for i, op := range []string{"create", "modify", "modify"} {
		if err := s.RecordFileChange(ctx, RecordFileChangeInput{
			SessionID:    sid,
			Path:         "/tmp/file.txt",
			Op:           op,
			ContentAfter: []byte{byte(i), 'a', 'b'},
		}); err != nil {
			t.Fatalf("RecordFileChange: %v", err)
		}
	}

	changes, err := s.ListFileChangesBySession(ctx, sid)
	if err != nil {
		t.Fatalf("ListFileChangesBySession: %v", err)
	}
	if len(changes) != 3 {
		t.Fatalf("got %d changes, want 3", len(changes))
	}
	// Ordering: most recent first. IDs descend.
	if changes[0].ID <= changes[1].ID || changes[1].ID <= changes[2].ID {
		t.Errorf("order wrong: IDs %d %d %d", changes[0].ID, changes[1].ID, changes[2].ID)
	}
}

func TestFileChanges_SizeCapBothUnderStoresContent(t *testing.T) {
	s := openMem(t)
	ctx := t.Context()
	sid := newSessionFor(t, s)

	before := bytes.Repeat([]byte("a"), 100*1024) // 100 KB
	after := bytes.Repeat([]byte("b"), 100*1024)

	if err := s.RecordFileChange(ctx, RecordFileChangeInput{
		SessionID:     sid,
		Path:          "/tmp/f",
		Op:            "modify",
		ContentBefore: before,
		ContentAfter:  after,
	}); err != nil {
		t.Fatalf("RecordFileChange: %v", err)
	}

	changes, _ := s.ListFileChangesBySession(ctx, sid)
	if len(changes) != 1 {
		t.Fatalf("got %d, want 1", len(changes))
	}
	fc := changes[0]
	if fc.Truncated {
		t.Error("Truncated = true, want false")
	}
	if !bytes.Equal(fc.ContentBefore, before) {
		t.Error("ContentBefore mismatch")
	}
	if !bytes.Equal(fc.ContentAfter, after) {
		t.Error("ContentAfter mismatch")
	}
}

func TestFileChanges_SizeCapOneSideExceedsTruncatesBoth(t *testing.T) {
	s := openMem(t)
	ctx := t.Context()
	sid := newSessionFor(t, s)

	before := bytes.Repeat([]byte("a"), 500*1024)
	after := bytes.Repeat([]byte("b"), 2*1024*1024) // 2 MB — over cap

	if err := s.RecordFileChange(ctx, RecordFileChangeInput{
		SessionID:     sid,
		Path:          "/tmp/f",
		Op:            "modify",
		ContentBefore: before,
		ContentAfter:  after,
	}); err != nil {
		t.Fatalf("RecordFileChange: %v", err)
	}

	changes, _ := s.ListFileChangesBySession(ctx, sid)
	if len(changes) != 1 {
		t.Fatalf("got %d, want 1", len(changes))
	}
	fc := changes[0]
	if !fc.Truncated {
		t.Error("Truncated = false, want true")
	}
	if fc.ContentBefore != nil {
		t.Errorf("ContentBefore should be nil when truncated, got %d bytes", len(fc.ContentBefore))
	}
	if fc.ContentAfter != nil {
		t.Errorf("ContentAfter should be nil when truncated, got %d bytes", len(fc.ContentAfter))
	}
	if fc.Path != "/tmp/f" || fc.Op != "modify" {
		t.Errorf("path/op lost: %q %q", fc.Path, fc.Op)
	}
}

func TestFileChanges_RequiresFields(t *testing.T) {
	s := openMem(t)
	ctx := t.Context()
	sid := newSessionFor(t, s)

	err := s.RecordFileChange(ctx, RecordFileChangeInput{SessionID: sid, Path: "/x"})
	if err == nil {
		t.Error("expected error when Op empty")
	}
	err = s.RecordFileChange(ctx, RecordFileChangeInput{SessionID: sid, Op: "modify"})
	if err == nil {
		t.Error("expected error when Path empty")
	}
	err = s.RecordFileChange(ctx, RecordFileChangeInput{Path: "/x", Op: "modify"})
	if err == nil {
		t.Error("expected error when SessionID empty")
	}
}

func TestFileChanges_WithMessageAttribution(t *testing.T) {
	s := openMem(t)
	ctx := t.Context()
	sid := newSessionFor(t, s)

	// Seed a message to reference.
	msg := api.NewAssistantMessage()
	msg.Content = []api.ContentBlock{{Type: api.BlockText, Text: "x"}}
	_ = s.AppendMessage(ctx, sid, msg, nil)

	err := s.RecordFileChange(ctx, RecordFileChangeInput{
		SessionID:    sid,
		MessageID:    msg.ID,
		ToolCallID:   "tc_1",
		Path:         "/tmp/y",
		Op:           "create",
		ContentAfter: []byte("data"),
	})
	if err != nil {
		t.Fatalf("RecordFileChange: %v", err)
	}

	changes, _ := s.ListFileChangesBySession(ctx, sid)
	if len(changes) != 1 {
		t.Fatalf("got %d, want 1", len(changes))
	}
	if changes[0].MessageID != msg.ID {
		t.Errorf("MessageID = %q, want %q", changes[0].MessageID, msg.ID)
	}
	if changes[0].ToolCallID != "tc_1" {
		t.Errorf("ToolCallID = %q", changes[0].ToolCallID)
	}
}
