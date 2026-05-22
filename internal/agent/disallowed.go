package agent

// AllAgentDisallowedTools is the set of tools that NO subagent may invoke.
// The map is queried by NewFilteredResolver only when wrapping the
// resolver for a subagent; primaries (build, plan) are unaffected and
// receive any tool present in their allowlist.
//
// Today's entries:
//   - task: subagents cannot spawn grandchildren.
//   - todowrite: only primaries own a session-level todo list. Subagents
//     run in their own child session whose todos slice is always empty;
//     hiding the tool from them keeps the contract one-way.
var AllAgentDisallowedTools = map[string]bool{
	"task":      true,
	"todowrite": true,
}

// IsAgentDisallowed reports whether name is in the global disallow list.
// Read-only; safe for concurrent callers.
func IsAgentDisallowed(name string) bool {
	return AllAgentDisallowedTools[name]
}
