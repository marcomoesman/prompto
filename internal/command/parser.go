package command

import "strings"

// Parse extracts a command name and its argument tokens from a raw input
// string. Leading whitespace and the leading '/' are stripped. Args are
// split on whitespace; quoting is not interpreted (matches Claude Code's
// behavior: $ARGS substitution and shell tools handle their own quoting).
//
// Returns ("", nil) when input has no leading slash or is empty.
func Parse(input string) (name string, args []string) {
	s := strings.TrimSpace(input)
	if !strings.HasPrefix(s, "/") {
		return "", nil
	}
	s = strings.TrimPrefix(s, "/")
	if s == "" {
		return "", nil
	}
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return "", nil
	}
	if len(fields) == 1 {
		return fields[0], nil
	}
	return fields[0], fields[1:]
}

// ArgString returns the verbatim argument substring (everything after the
// first whitespace-delimited token) for use as $ARGS in custom commands.
// Trims leading whitespace; preserves internal whitespace so a custom
// command can substitute argument text exactly as the user typed it.
// Returns "" when input has no leading slash.
func ArgString(input string) string {
	s := strings.TrimSpace(input)
	if !strings.HasPrefix(s, "/") {
		return ""
	}
	s = strings.TrimPrefix(s, "/")
	idx := strings.IndexAny(s, " \t")
	if idx < 0 {
		return ""
	}
	return strings.TrimLeft(s[idx:], " \t")
}
