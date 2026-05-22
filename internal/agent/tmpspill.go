package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/marcomoesman/prompto/internal/privatefs"
)

// spillDirName is the per-project subdirectory under Cwd where oversized
// tool output is written. The user is responsible for cleaning it between
// sessions; /clear handles it for the active session.
const spillDirName = ".prompto/tmp"

// SpillInput bundles the inputs to Spill. Declared before the function per
// CLAUDE.md.
type SpillInput struct {
	Cwd     string
	Content string
}

// Spill writes Content to Cwd/.prompto/tmp/<sha256>.txt and returns the path.
// The filename is content-addressed: identical content writes to the same
// path idempotently, distinct content to distinct paths. Creates the spill
// directory if missing.
func Spill(in SpillInput) (string, error) {
	if in.Cwd == "" {
		return "", fmt.Errorf("spill: Cwd is required")
	}
	dir := filepath.Join(in.Cwd, spillDirName)
	if err := privatefs.EnsureDir(dir); err != nil {
		return "", fmt.Errorf("spill: creating %s: %w", dir, err)
	}
	sum := sha256.Sum256([]byte(in.Content))
	name := hex.EncodeToString(sum[:]) + ".txt"
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(in.Content), privatefs.FileMode); err != nil {
		return "", fmt.Errorf("spill: writing %s: %w", path, err)
	}
	_ = os.Chmod(path, privatefs.FileMode)
	return path, nil
}
