package agent

import (
	"context"

	"github.com/marcomoesman/prompto/internal/api"
)

// Store is the narrow persistence interface the agent loop uses. The
// concrete implementation lives in internal/store; a nil Store means
// persistence is disabled (tests, headless mode).
//
// Kept minimal on purpose so the agent package has no dependency on
// internal/store. Every method the loop needs is declared here; main.go
// passes its internal/store.*Store which satisfies this interface
// structurally.
type Store interface {
	AppendMessage(ctx context.Context, sessionID string, msg api.Message, usage *api.Usage) error
	// AppendSummaryMessage persists a compaction's summary message together
	// with a marker recording which prior message it replaces (inclusive).
	// Implementations must do both inserts atomically; on resume,
	// LoadMessages should return only messages past the most recent marker.
	AppendSummaryMessage(ctx context.Context, sessionID string, msg api.Message, replacedThroughMessageID string) error
	// LoadPlanPath returns the persisted `sessions.plan_path` for a
	// session, or "" when not set. The plan agent picks a
	// slug at first plan write; the chosen path is recorded so resume
	// finds it.
	LoadPlanPath(ctx context.Context, sessionID string) (string, error)
	// SetPlanPath records the model-chosen plan path on a session
	// row. Idempotent caller responsibility: the run loop only calls
	// this on the first plan-file write per session.
	SetPlanPath(ctx context.Context, sessionID, path string) error
}

// SpawnerStore is the persistence interface NewSpawner needs in addition
// to Store. Lives here (not internal/store) so the agent package never
// imports the concrete store. internal/store.*Store structurally satisfies
// this interface.
type SpawnerStore interface {
	Store
	// CreateChildSession inserts a new session row with the given
	// parent / agent / model, and returns its id. Title is optional.
	CreateChildSession(ctx context.Context, in CreateChildSessionInput) (string, error)
	// SetSessionStatus updates a session row's status field.
	SetSessionStatus(ctx context.Context, id, status string) error
	// LoadMessages returns the persisted messages for a child run, in
	// insertion order. Used to resume an existing child by task_id.
	LoadMessages(ctx context.Context, sessionID string) ([]api.Message, error)
}

// CreateChildSessionInput is the parameter struct for SpawnerStore.
// CreateChildSession.
type CreateChildSessionInput struct {
	ParentID  string
	AgentName string
	Model     string
	Title     string
}
