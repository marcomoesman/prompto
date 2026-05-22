// Package permission implements rule-based tool authorization, permission
// modes, path traversal defense, and a hardcoded protected-file guard for
// prompto's agent loop.
package permission

import (
	"path"
	"path/filepath"
	"strings"
)

// MatchGlob reports whether relPath matches pattern. Supports:
//   - `*` matching a single path segment (like path.Match)
//   - `**` matching zero or more path segments anywhere in the pattern
//   - literal strings with normal path.Match semantics
//
// Patterns are case-sensitive. Both inputs are normalized to forward
// slashes before matching, so `**/.env` correctly matches against
// `C:\Users\foo\.env` on Windows — without that normalization the
// `**/` prefix logic split on `filepath.Separator` (a backslash on
// Windows, a no-op on the forward-slash path) and the path stayed a
// single element, missing every nested target. Mirrors the behavior
// of internal/tool/glob.go:matchGlob so file globs and permission
// rules use the same language.
func MatchGlob(pattern, relPath string) bool {
	pattern = filepath.ToSlash(pattern)
	relPath = filepath.ToSlash(relPath)

	// Handle patterns starting with **/
	if strings.HasPrefix(pattern, "**/") {
		suffix := pattern[3:]
		parts := strings.Split(relPath, "/")
		for i := range parts {
			tail := strings.Join(parts[i:], "/")
			if matched, _ := path.Match(suffix, tail); matched {
				return true
			}
		}
		return false
	}

	// Handle patterns with /**/ in the middle
	if idx := strings.Index(pattern, "/**/"); idx >= 0 {
		prefix := pattern[:idx]
		suffix := pattern[idx+4:]
		parts := strings.Split(relPath, "/")
		for i := 1; i < len(parts); i++ {
			left := strings.Join(parts[:i], "/")
			right := strings.Join(parts[i:], "/")
			prefixMatch, _ := path.Match(prefix, left)
			if !prefixMatch {
				continue
			}
			if MatchGlob("**/"+suffix, right) {
				return true
			}
		}
		return false
	}

	// Trailing /**
	if strings.HasSuffix(pattern, "/**") {
		prefix := pattern[:len(pattern)-3]
		matched, _ := path.Match(prefix, strings.Split(relPath, "/")[0])
		if matched {
			return true
		}
		dir := path.Dir(relPath)
		for dir != "." && dir != "/" {
			if matched, _ := path.Match(prefix, dir); matched {
				return true
			}
			dir = path.Dir(dir)
		}
		return false
	}

	// No ** — use path.Match directly (slash-separated semantics).
	matched, _ := path.Match(pattern, relPath)
	return matched
}
