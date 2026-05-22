package agent

import "context"

// FileChangeEvent carries a file edit out of the tool layer into whatever
// listener main.go wires up. Tools populate Path/Op/ContentBefore/
// ContentAfter; MessageID and ToolCallID are pulled from ToolContext by the
// tool before Record is called so the sink receives a complete, attributed
// event.
type FileChangeEvent struct {
	MessageID     string
	ToolCallID    string
	Path          string
	Op            string // "create" | "modify" | "delete"
	ContentBefore []byte
	ContentAfter  []byte
	// Mode is the filesystem mode bits the file had at change-record
	// time (post-modify for modify ops, pre-delete for delete ops).
	// /undo restores this on revert so a 0755 script doesn't lose its
	// executable bit. Zero means "not captured" — the sink falls back
	// to a default (0o644) on restore.
	Mode uint32
}

// FileChangeSink accepts file-change events emitted by Edit and Write. A nil
// sink is a no-op — tools use the NilFileChangeSink helper so they don't
// have to nil-check a bare interface field on ToolContext.
//
// ctx is the executing tool's context; sinks that hit shared state (e.g.
// the SQLite store) propagate it so a Ctrl+C mid-tool aborts the persist
// instead of letting the cancellation escape the tool boundary.
type FileChangeSink interface {
	Record(ctx context.Context, ev FileChangeEvent) error
}

// NilFileChangeSink returns a sink that discards every event. Used by tests
// and by the default ToolContext when persistence is disabled.
type nilFileChangeSink struct{}

func (nilFileChangeSink) Record(context.Context, FileChangeEvent) error { return nil }

// DiscardFileChanges is a sink singleton that drops all events. Safe for
// tools to call without nil checks.
var DiscardFileChanges FileChangeSink = nilFileChangeSink{}

// SessionScopedSink is implemented by FileChangeSink instances that bind to
// a single session at construction time. The TUI uses this to retarget the
// shared sink when /clear or /new rotates the active session, rather than
// rebuilding every dependent wiring.
type SessionScopedSink interface {
	SetSessionID(id string)
}
