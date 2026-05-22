package agent

import "github.com/marcomoesman/prompto/internal/api"

// Conversation holds the ordered message history for a session.
type Conversation struct {
	messages []api.Message
}

// NewConversation creates an empty conversation.
func NewConversation() *Conversation {
	return &Conversation{}
}

// Append adds a message to the conversation.
func (c *Conversation) Append(msg api.Message) {
	c.messages = append(c.messages, msg)
}

// Messages returns messages suitable for the API (excludes system role
// messages). System messages are vanishingly rare in this codebase
// (the system prompt is built separately, never appended), so the
// common case has zero of them; the fast path returns the underlying
// slice directly. Callers must not mutate the returned slice — they
// don't today, and InjectReminders / similar helpers already make
// their own defensive copies before modifying.
func (c *Conversation) Messages() []api.Message {
	for _, msg := range c.messages {
		if msg.Role == api.RoleSystem {
			// Slow path: at least one system message present, build
			// the filtered slice.
			result := make([]api.Message, 0, len(c.messages)-1)
			for _, m := range c.messages {
				if m.Role != api.RoleSystem {
					result = append(result, m)
				}
			}
			return result
		}
	}
	return c.messages
}

// All returns all messages including system (for TUI display).
func (c *Conversation) All() []api.Message {
	return c.messages
}

// ReplaceHead replaces all but the last keepTail messages with replacement
// placed at the head of the retained tail. Result: [replacement, tail...].
//
// When keepTail >= len(messages), nothing is cut; replacement is simply
// prepended. When replacement has zero-value Content, it is not inserted —
// effectively just truncating to the tail.
//
// Used by the summarizer during compaction; never touched by ordinary
// append flows.
func (c *Conversation) ReplaceHead(keepTail int, replacement api.Message) {
	if keepTail < 0 {
		keepTail = 0
	}
	start := len(c.messages) - keepTail
	if start < 0 {
		start = 0
	}
	tail := c.messages[start:]
	next := make([]api.Message, 0, len(tail)+1)
	if len(replacement.Content) > 0 {
		next = append(next, replacement)
	}
	next = append(next, tail...)
	c.messages = next
}
