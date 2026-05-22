package tool

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/marcomoesman/prompto/internal/agent"
	"github.com/marcomoesman/prompto/internal/permission"
)

type toolPathOp int

const (
	toolPathRead toolPathOp = iota
	toolPathSearch
	toolPathWrite
	toolPathEdit
)

func resolveToolPath(raw string, tc agent.ToolContext, op toolPathOp) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("path is required")
	}
	if i := strings.IndexAny(raw, "\n\r\t\x00"); i >= 0 {
		// Control characters in a path almost always mean the model
		// emitted a JSON tool-arguments string with under-escaped
		// backslashes — `\n`, `\t`, etc. decoded as the actual
		// control byte instead of a literal `\` + letter. Surface a
		// recoverable error that names the failure mode so the model
		// retries with `\\` rather than getting an opaque
		// "system cannot find the file" from the OS.
		return "", fmt.Errorf("path contains a control character at index %d — Windows backslashes must be doubled in JSON (`\\\\`), or use forward slashes (`/`) which work on every OS", i)
	}
	wd := tc.Cwd
	if wd == "" {
		var err error
		wd, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("getting working directory: %w", err)
		}
	}
	input := normalizeMSYSPath(raw)
	if !filepath.IsAbs(input) {
		input = filepath.Join(wd, input)
	}

	roots := tc.AllowedRoots
	if len(roots) == 0 {
		roots = []string{wd}
	}
	var allowed []string
	switch op {
	case toolPathWrite, toolPathEdit:
		allowed = roots
	case toolPathRead, toolPathSearch:
		// Reads/searches may target outside the workspace after approval,
		// but the approval key must still be canonical.
		allowed = nil
	}

	resolved, err := permission.NormalizePath(permission.NormalizePathInput{
		Input:        input,
		AllowedRoots: allowed,
	})
	if err != nil {
		return "", err
	}
	if op == toolPathWrite || op == toolPathEdit {
		if err := validateResolvedWritePath(resolved); err != nil {
			return "", err
		}
	}
	return resolved, nil
}

// normalizeMSYSPath rewrites Git-Bash / MSYS / Cygwin style absolute paths
// (`/g/Go Workspace/prompto`, `/c/Users/foo`) into native Windows paths
// (`g:\Go Workspace\prompto`, `c:\Users\foo`). LLMs trained heavily on
// POSIX shells frequently emit this form on Windows when they see the
// working directory rendered with forward slashes, even though Windows
// treats `/g/...` as a relative path on the current drive. Without this
// rewrite, the resolver joins it onto cwd and produces nonsense like
// `G:\Go Workspace\prompto\g\Go Workspace\prompto`.
//
// On non-Windows hosts this is a no-op — `/g/...` is a real path there.
func normalizeMSYSPath(p string) string {
	if runtime.GOOS != "windows" {
		return p
	}
	if len(p) < 2 || p[0] != '/' {
		return p
	}
	letter := p[1]
	if (letter < 'a' || letter > 'z') && (letter < 'A' || letter > 'Z') {
		return p
	}
	// Match `/x` or `/x/...` exactly — avoid touching `/xy/...`, which
	// is a real POSIX-style path the user might be passing through.
	if len(p) == 2 {
		return string(letter) + `:\`
	}
	if p[2] != '/' {
		return p
	}
	return string(letter) + `:\` + filepath.FromSlash(p[3:])
}

func searchPermissionKey(path string) string {
	if path == "" {
		return "search:"
	}
	return "search:" + path
}
