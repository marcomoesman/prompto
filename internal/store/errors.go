package store

import "errors"

// Sentinel errors returned by the store.
var (
	// ErrSessionNotFound is returned when a session lookup finds nothing.
	ErrSessionNotFound = errors.New("store: session not found")
	// ErrSessionAmbiguous is returned when a prefix lookup matches multiple
	// sessions. Callers should prompt the user for a longer prefix.
	ErrSessionAmbiguous = errors.New("store: session prefix is ambiguous")
	// ErrPrefixTooShort is returned when a prefix lookup is below the
	// minimum length. Prevents accidentally resuming the wrong session.
	ErrPrefixTooShort = errors.New("store: session prefix too short (need at least 8 chars)")
	// ErrFileChangeNotFound is returned when a file_changes row lookup or
	// delete finds nothing. /undo uses it to detect a stale id.
	ErrFileChangeNotFound = errors.New("store: file change not found")
	// ErrUnknownMessage is returned when AppendSummaryMessage is called with
	// a replacedThroughMessageID that does not match any persisted row in
	// the session. Indicates the caller passed a stale or in-memory-only id.
	ErrUnknownMessage = errors.New("store: unknown message id")
)

// MinSessionPrefix is the minimum length accepted by FindSessionByPrefix.
const MinSessionPrefix = 8
