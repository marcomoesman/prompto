package store

import (
	"testing"
	"time"
)

func TestSessions_ListChildren_OnlyDirectChildren(t *testing.T) {
	s := openMem(t)
	ctx := t.Context()

	parent, err := s.CreateSession(ctx, CreateSessionInput{Model: "m", AgentName: "build"})
	if err != nil {
		t.Fatalf("parent: %v", err)
	}
	other, err := s.CreateSession(ctx, CreateSessionInput{Model: "m", AgentName: "build"})
	if err != nil {
		t.Fatalf("other: %v", err)
	}

	c1, err := s.CreateSession(ctx, CreateSessionInput{Model: "m", AgentName: "explore", ParentID: parent.ID})
	if err != nil {
		t.Fatalf("c1: %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	c2, err := s.CreateSession(ctx, CreateSessionInput{Model: "m", AgentName: "explore", ParentID: parent.ID})
	if err != nil {
		t.Fatalf("c2: %v", err)
	}
	// Grandchild: must NOT appear in ListChildren(parent).
	if _, err := s.CreateSession(ctx, CreateSessionInput{Model: "m", AgentName: "explore", ParentID: c1.ID}); err != nil {
		t.Fatalf("grandchild: %v", err)
	}
	// Sibling-of-parent's child: must NOT appear.
	if _, err := s.CreateSession(ctx, CreateSessionInput{Model: "m", AgentName: "explore", ParentID: other.ID}); err != nil {
		t.Fatalf("other-child: %v", err)
	}

	got, err := s.ListChildren(ctx, parent.ID)
	if err != nil {
		t.Fatalf("ListChildren: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (got %v)", len(got), got)
	}
	if got[0].ID != c1.ID || got[1].ID != c2.ID {
		t.Errorf("order = [%s,%s], want [%s,%s] (created_at ASC)",
			got[0].ID, got[1].ID, c1.ID, c2.ID)
	}
}

func TestSessions_ListChildren_NoneReturnsEmpty(t *testing.T) {
	s := openMem(t)
	parent, _ := s.CreateSession(t.Context(), CreateSessionInput{Model: "m"})
	got, err := s.ListChildren(t.Context(), parent.ID)
	if err != nil {
		t.Fatalf("ListChildren: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
}

func TestSessions_ListChildren_RequiresParentID(t *testing.T) {
	s := openMem(t)
	_, err := s.ListChildren(t.Context(), "")
	if err == nil {
		t.Fatal("expected error for empty parentID")
	}
}

func TestSessions_SetAgentName_RoundTrip(t *testing.T) {
	s := openMem(t)
	ctx := t.Context()
	sess, _ := s.CreateSession(ctx, CreateSessionInput{Model: "m"})
	if sess.AgentName != "build" {
		t.Fatalf("initial agent = %q, want build", sess.AgentName)
	}
	if err := s.SetAgentName(ctx, sess.ID, "plan"); err != nil {
		t.Fatalf("SetAgentName: %v", err)
	}
	got, _ := s.GetSession(ctx, sess.ID)
	if got.AgentName != "plan" {
		t.Errorf("after update: agent = %q, want plan", got.AgentName)
	}
}

func TestSessions_SetAgentName_Empty(t *testing.T) {
	s := openMem(t)
	sess, _ := s.CreateSession(t.Context(), CreateSessionInput{Model: "m"})
	if err := s.SetAgentName(t.Context(), sess.ID, ""); err == nil {
		t.Fatal("expected error for empty agentName")
	}
}

func TestSessions_SetAgentName_UnknownSession(t *testing.T) {
	s := openMem(t)
	if err := s.SetAgentName(t.Context(), "nope", "build"); err == nil {
		t.Fatal("expected ErrSessionNotFound for unknown session")
	}
}
