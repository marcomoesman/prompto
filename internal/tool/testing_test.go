package tool

import (
	"os"
	"testing"

	"github.com/marcomoesman/prompto/internal/agent"
)

// newTestCtx returns a ToolContext suitable for tool tests. Cwd is a fresh
// temp dir; FileState is empty; RequestLogger is nil.
func newTestCtx(t *testing.T) agent.ToolContext {
	t.Helper()
	return agent.ToolContext{
		Cwd:           t.TempDir(),
		AllowedRoots:  []string{string(os.PathSeparator)},
		FileState:     agent.NewFileState(),
		RequestLogger: nil,
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdirall %s: %v", path, err)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
