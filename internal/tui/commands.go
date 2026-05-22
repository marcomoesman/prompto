package tui

import (
	"os"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/marcomoesman/prompto/internal/agent"
)

// agentEventMsg wraps an agent.Event for Bubbletea dispatch.
type agentEventMsg struct {
	event agent.Event
}

// agentDoneMsg signals the Done channel yielded. reason is the termination
// sentinel (or a wrapped transport error).
type agentDoneMsg struct {
	reason error
}

// elapsedTickMsg fires every second while a turn is running so the status
// bar's elapsed counter updates without blocking the input loop.
type elapsedTickMsg struct{}

// waitForAgentEvent returns a Cmd that blocks on the agent event channel.
// When the channel closes, nothing fires — agentDoneMsg is produced by
// waitForAgentDone separately.
func waitForAgentEvent(ch <-chan agent.Event) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-ch
		if !ok {
			// Event channel closed; suppress the message. Done handler will
			// re-enable input.
			return nil
		}
		return agentEventMsg{event: event}
	}
}

// waitForAgentDone blocks until the Done channel yields its terminal reason.
// Fires agentDoneMsg once, then never again.
func waitForAgentDone(ch <-chan error) tea.Cmd {
	return func() tea.Msg {
		reason := <-ch
		return agentDoneMsg{reason: reason}
	}
}

// elapsedTickCmd schedules an elapsedTickMsg one second from now. The
// AppModel re-arms it on each receipt while a turn is active, and stops
// re-arming on EventTurnComplete / Done.
func elapsedTickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg {
		return elapsedTickMsg{}
	})
}

// terminalPingCmd emits a terminal bell when prompto needs user input.
// Terminals decide whether that becomes an audible sound, visual flash, or
// nothing; there is no platform-specific audio dependency here.
func terminalPingCmd() tea.Cmd {
	return func() tea.Msg {
		_, _ = os.Stdout.WriteString("\a")
		return nil
	}
}
