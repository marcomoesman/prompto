package tool

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// RelPathForDisplay renders an absolute path as a cwd-relative,
// forward-slash form suitable for tool-call headers. Used by path-aware
// tools' FormatForDisplayWithContext methods so the chat shows
// `internal/tui/env.go` instead of the noisy escaped absolute form
// `"G:\\Go Workspace\\prompto\\internal\\tui\\env.go"` that QuoteArg
// emits for raw Windows paths.
//
// Returns p unchanged when:
//   - either argument is empty,
//   - p is an http/https URL (no relativization makes sense),
//   - filepath.Rel fails (different volumes on Windows, etc.), or
//   - the relative form escapes cwd (starts with "..") — preserving the
//     absolute path lets the user see exactly which out-of-tree file
//     was touched.
//
// Forward slashes are deliberate: they survive QuoteArg's escaping cleanly
// on both platforms, and match how prompto already renders paths in the
// system prompt (internal/agent/prompt.go) and in grep/glob results.
func RelPathForDisplay(cwd, p string) string {
	if cwd == "" || p == "" {
		return p
	}
	if strings.HasPrefix(p, "http://") || strings.HasPrefix(p, "https://") {
		return p
	}
	rel, err := filepath.Rel(cwd, p)
	if err != nil {
		return p
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return p
	}
	return filepath.ToSlash(rel)
}

// HumanizeBytes formats a byte count as a Claude-Code-style short string.
// Below 1 KiB returns "<n>B"; thereafter switches to KB/MB/GB at 10^3
// boundaries with one decimal place. Uses 1024-based units (KiB) under
// the SI labels — same convention `du -h` uses on macOS / GNU.
func HumanizeBytes(n int) string {
	if n < 1024 {
		return fmt.Sprintf("%dB", n)
	}
	v := float64(n) / 1024.0
	if v < 1024 {
		return fmt.Sprintf("%.1fKB", v)
	}
	v /= 1024.0
	if v < 1024 {
		return fmt.Sprintf("%.1fMB", v)
	}
	v /= 1024.0
	return fmt.Sprintf("%.1fGB", v)
}

// NoTruncate disables the length cap in QuoteArg. Use it from
// FormatForDisplay implementations where truncating the value would be
// a safety problem (e.g. the bash command being approved must always
// be shown in full so the user can read what they're authorising).
const NoTruncate = -1

// QuoteArg renders a tool-call argument for the Claude-Code-style row
// header, e.g. `file_path: "/Users/.../foo.go"`. The value is trimmed
// to ~maxLen characters with an ellipsis suffix; embedded `"` are
// backslash-escaped so the line stays parseable for visual scan. We
// avoid `strconv.Quote` because it Go-escapes UTF-8 (`ก` etc.)
// which renders as noise in a terminal.
//
// maxLen == 0 falls back to the 80-char default. maxLen < 0
// (NoTruncate) disables truncation entirely.
func QuoteArg(s string, maxLen int) string {
	if maxLen == 0 {
		maxLen = 80
	}
	trimmed := s
	if maxLen > 0 && utf8.RuneCountInString(trimmed) > maxLen {
		// Truncate at a rune boundary so multi-byte codepoints (CJK,
		// emoji, accented Latin) aren't split mid-sequence — that
		// would render as "" in the terminal. Walk runes counting
		// up to maxLen-1, then append the ellipsis.
		runes := []rune(trimmed)
		trimmed = string(runes[:maxLen-1]) + "…"
	}
	// Minimal escape: only backslashes and double quotes.
	var b strings.Builder
	b.Grow(len(trimmed) + 2)
	b.WriteByte('"')
	for _, r := range trimmed {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// FormatCall renders a tool-call header in the
// `Name(arg: "val", arg: "val")` form. Values run through QuoteArg
// with the default 80-character cap. The kvs slice is interpreted as
// pairs (key, value, key, value, ...). Odd entries are silently
// dropped — keep the call site honest by always passing pairs.
func FormatCall(name string, kvs ...string) string {
	var b strings.Builder
	b.WriteString(name)
	b.WriteByte('(')
	first := true
	for i := 0; i+1 < len(kvs); i += 2 {
		if !first {
			b.WriteString(", ")
		}
		first = false
		b.WriteString(kvs[i])
		b.WriteString(": ")
		b.WriteString(QuoteArg(kvs[i+1], 80))
	}
	b.WriteByte(')')
	return b.String()
}
