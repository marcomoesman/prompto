package permission

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// Traversal sentinel errors.
var (
	ErrNullByte      = errors.New("permission: path contains a null byte")
	ErrURLEncoded    = errors.New("permission: path contains URL-encoded traversal sequences")
	ErrFullwidth     = errors.New("permission: path contains fullwidth dot/slash characters")
	ErrOutsideRoots  = errors.New("permission: path resolves outside allowed roots")
	ErrRelativeEmpty = errors.New("permission: empty path")
)

// NormalizePathInput bundles the inputs to NormalizePath.
type NormalizePathInput struct {
	Input        string
	AllowedRoots []string // absolute paths; resolved path must be within one
}

// NormalizePath resolves Input into a canonical absolute path that is
// guaranteed to live within one of AllowedRoots. Steps:
//  1. Reject null bytes outright.
//  2. Reject URL-encoded traversal sequences (%2e/%2f, any case).
//  3. NFC-normalize Unicode; reject fullwidth dot/slash characters.
//  4. filepath.Abs + filepath.Clean.
//  5. EvalSymlinks on the deepest existing ancestor (so not-yet-created
//     paths still resolve sensibly).
//  6. Verify the resolved path is within one of AllowedRoots.
func NormalizePath(in NormalizePathInput) (string, error) {
	if in.Input == "" {
		return "", ErrRelativeEmpty
	}

	// 1. null byte
	if strings.IndexByte(in.Input, 0) >= 0 {
		return "", ErrNullByte
	}

	// 2. URL-encoded traversal
	lower := strings.ToLower(in.Input)
	if strings.Contains(lower, "%2e") || strings.Contains(lower, "%2f") {
		return "", ErrURLEncoded
	}

	// 3. Unicode normalization + fullwidth reject
	normalized := norm.NFC.String(in.Input)
	if containsFullwidthPathChars(normalized) {
		return "", ErrFullwidth
	}

	// 4. Absolute + clean
	abs, err := filepath.Abs(normalized)
	if err != nil {
		return "", fmt.Errorf("permission: abs %q: %w", in.Input, err)
	}

	// 5. Resolve symlinks on the deepest existing ancestor. If the path
	//    itself doesn't exist yet, walk up until we find one that does.
	resolved, err := resolveDeepestExisting(abs)
	if err != nil {
		return "", fmt.Errorf("permission: resolving symlinks: %w", err)
	}

	// 6. Confine to allowed roots.
	if len(in.AllowedRoots) > 0 {
		inside := false
		for _, root := range in.AllowedRoots {
			rootResolved, err := resolveDeepestExisting(filepath.Clean(root))
			if err != nil {
				continue
			}
			if pathWithin(resolved, rootResolved) {
				inside = true
				break
			}
		}
		if !inside {
			return "", fmt.Errorf("%w: %s", ErrOutsideRoots, resolved)
		}
	}

	return resolved, nil
}

// containsFullwidthPathChars checks for fullwidth dot (U+FF0E) and slash
// (U+FF0F). Either indicates someone is trying to disguise ../ or /.
func containsFullwidthPathChars(s string) bool {
	return strings.ContainsAny(s, "\uFF0E\uFF0F")
}

// resolveDeepestExisting returns EvalSymlinks of the deepest ancestor that
// exists, then re-joins the missing suffix. This lets NormalizePath handle
// paths to not-yet-created files (Write on a new path).
func resolveDeepestExisting(p string) (string, error) {
	dir := p
	var suffix []string
	for {
		if _, err := os.Stat(dir); err == nil {
			resolved, err := filepath.EvalSymlinks(dir)
			if err != nil {
				return "", err
			}
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return resolved, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached filesystem root without finding an existing ancestor.
			return p, nil
		}
		suffix = append(suffix, filepath.Base(dir))
		dir = parent
	}
}

// pathWithin reports whether child is lexically inside root (after both
// have been cleaned). Uses a byte-prefix check with a separator guard so
// "/proj" doesn't falsely match "/projector".
func pathWithin(child, root string) bool {
	child = filepath.Clean(child)
	root = filepath.Clean(root)
	if child == root {
		return true
	}
	if root == string(filepath.Separator) {
		return filepath.IsAbs(child)
	}
	rootWithSep := root + string(filepath.Separator)
	return bytes.HasPrefix([]byte(child), []byte(rootWithSep))
}
