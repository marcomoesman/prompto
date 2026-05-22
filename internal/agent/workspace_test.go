package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTestFile(t *testing.T, dir, name, body string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDetectWorkspaceAndVerification_Go(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/x\n")
	writeTestFile(t, dir, "cmd/prompto/main.go", "package main\n")

	ws := DetectWorkspace(dir)
	if ws.Runtime != "Go" || ws.Entry != filepath.Join("cmd", "prompto") || ws.Test != "go test ./..." {
		t.Fatalf("workspace = %+v", ws)
	}
	got := DetectVerification(dir).Commands
	want := []string{"go test ./...", "go vet ./..."}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("commands = %#v, want %#v", got, want)
	}
}

func TestDetectWorkspaceAndVerification_Node(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "package.json", `{"main":"src/index.js","scripts":{"test":"vitest","build":"vite build","dev":"vite"}}`)
	writeTestFile(t, dir, "pnpm-lock.yaml", "")

	ws := DetectWorkspace(dir)
	if ws.Runtime != "Node" || ws.PackageManager != "pnpm" || ws.Build != "pnpm build" || ws.Test != "pnpm test" {
		t.Fatalf("workspace = %+v", ws)
	}
	got := DetectVerification(dir).Commands
	if strings.Join(got, ",") != "pnpm test,pnpm build" {
		t.Fatalf("commands = %#v", got)
	}
}

func TestDetectWorkspaceAndVerification_Python(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "requirements.txt", "pytest==8.0.0\n")

	ws := DetectWorkspace(dir)
	if ws.Runtime != "Python" || ws.Test != "python -m pytest" {
		t.Fatalf("workspace = %+v", ws)
	}
	got := DetectVerification(dir).Commands
	if len(got) != 1 || got[0] != "python -m pytest" {
		t.Fatalf("commands = %#v", got)
	}
}

func TestDetectWorkspaceAndVerification_Rust(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "Cargo.toml", "[package]\nname = \"x\"\n")

	ws := DetectWorkspace(dir)
	if ws.Runtime != "Rust" || ws.Build != "cargo build" || ws.Test != "cargo test" {
		t.Fatalf("workspace = %+v", ws)
	}
	got := DetectVerification(dir).Commands
	if len(got) != 1 || got[0] != "cargo test" {
		t.Fatalf("commands = %#v", got)
	}
}

func TestDetectWorkspace_InvalidOrMissingManifests(t *testing.T) {
	dir := t.TempDir()
	if DetectWorkspace(dir).Present() {
		t.Fatal("empty dir should not produce workspace hint")
	}
	if DetectVerification(dir).Present() {
		t.Fatal("empty dir should not produce verification hint")
	}
	writeTestFile(t, dir, "package.json", "{")
	if DetectWorkspace(dir).Present() {
		t.Fatal("invalid package.json should not produce workspace hint")
	}
}

func TestPromptIncludesWorkspaceHintsAsVolatile(t *testing.T) {
	blocks := BuildSystemPrompt(BuildSystemPromptInput{
		Cwd:      "/repo",
		Platform: "darwin/arm64",
		Model:    "m",
		Date:     "2026-05-22",
		WorkspaceSummary: WorkspaceSummary{
			Runtime: "Go",
			Entry:   "cmd/prompto",
			Test:    "go test ./...",
		},
		VerificationHint: VerificationHint{Commands: []string{"go test ./...", "go vet ./..."}},
	})
	var cacheIdx int
	var combined string
	for i, b := range blocks {
		if b.Cache {
			cacheIdx = i
		}
		combined += b.Text + "\n"
	}
	if cacheIdx != len(stableBlocks(BuildSystemPromptInput{}))-1 {
		t.Fatalf("cache boundary index = %d, stable prompt shape changed", cacheIdx)
	}
	if !strings.Contains(combined, "# Workspace summary") || !strings.Contains(combined, "# Verification") {
		t.Fatalf("missing workspace/verification hints:\n%s", combined)
	}
}
