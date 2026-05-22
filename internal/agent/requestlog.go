package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/marcomoesman/prompto/internal/api"
	"github.com/marcomoesman/prompto/internal/privatefs"
)

// RequestLogEntry is one line in the JSONL request log.
type RequestLogEntry struct {
	Timestamp    time.Time  `json:"timestamp"`
	Model        string     `json:"model"`
	MsgCount     int        `json:"msg_count"`
	SystemSHA256 string     `json:"system_sha256,omitempty"`
	ToolNames    []string   `json:"tool_names,omitempty"`
	Usage        *api.Usage `json:"usage,omitempty"`
	DurationMs   int64      `json:"duration_ms"`
	Error        string     `json:"error,omitempty"`

	// Response shape diagnostics. Populated on every entry so
	// "why did the turn end?" investigations don't require
	// re-running the conversation.
	//
	// TextLen is the byte length of the visible (non-thinking) text
	// the assistant produced. Zero on empty turns.
	TextLen int `json:"text_len,omitempty"`
	// ToolCallCount is the number of structured tool calls the
	// model emitted on this turn (post-validation, pre-dispatch).
	ToolCallCount int `json:"tool_call_count"`
	// RecoveredToolCallCount is the subset of ToolCallCount produced by
	// deterministic textual-tool-call recovery.
	RecoveredToolCallCount  int      `json:"recovered_tool_call_count,omitempty"`
	WorkspaceHintPresent    bool     `json:"workspace_hint_present,omitempty"`
	VerificationHintPresent bool     `json:"verification_hint_present,omitempty"`
	LoopGuardActions        []string `json:"loop_guard_actions,omitempty"`
	// StopReason is the provider's finish flag from the last
	// EventDone — "stop", "tool_use"/"tool_calls", "length", etc.
	// Empty when no EventDone arrived (error paths).
	StopReason string `json:"stop_reason,omitempty"`
	// EmptyAssistantPreview captures the first ~500 bytes of
	// textContent ONLY on degenerate empty-assistant turns
	// (TextLen==0 && ToolCallCount==0). The preview is otherwise
	// omitted to keep log volume sane. Useful for spotting models
	// that emitted textual <tool_call> blocks the agent didn't
	// parse as structured calls.
	EmptyAssistantPreview string `json:"empty_assistant_preview,omitempty"`
}

// RequestLogger appends JSONL request records to a file when debug logging
// is enabled. A nil *RequestLogger is a no-op, so callers don't have to
// nil-check.
type RequestLogger struct {
	mu   sync.Mutex
	file *os.File
}

// NewRequestLoggerInput bundles the inputs to NewRequestLogger.
type NewRequestLoggerInput struct {
	Debug bool
	Dir   string // e.g. ".prompto/logs"
	Now   func() time.Time
}

// NewRequestLogger returns a RequestLogger when Debug is true, otherwise nil.
// The directory is created if missing. Today's file is opened lazily in
// append mode.
func NewRequestLogger(in NewRequestLoggerInput) (*RequestLogger, error) {
	if !in.Debug {
		return nil, nil
	}

	now := in.Now
	if now == nil {
		now = time.Now
	}

	if err := privatefs.EnsureDir(in.Dir); err != nil {
		return nil, fmt.Errorf("creating log dir %s: %w", in.Dir, err)
	}
	path := filepath.Join(in.Dir, fmt.Sprintf("requests-%s.jsonl", now().Format("2006-01-02")))

	// 0o600: request log lines may carry conversation snippets, system-prompt
	// hashes, and (with sufficiently verbose providers) error bodies that
	// echo request payloads. Owner-only.
	f, err := privatefs.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	return &RequestLogger{file: f}, nil
}

// Write appends one JSONL record. Safe on a nil receiver. Safe for concurrent
// callers — mutex serializes writes so lines don't interleave.
func (l *RequestLogger) Write(entry RequestLogEntry) error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	line, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshaling log entry: %w", err)
	}
	line = append(line, '\n')
	if _, err := l.file.Write(line); err != nil {
		return fmt.Errorf("writing log: %w", err)
	}
	return nil
}

// Close flushes and closes the underlying file. Safe on a nil receiver.
func (l *RequestLogger) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}
