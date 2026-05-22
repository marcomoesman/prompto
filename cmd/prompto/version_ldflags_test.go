package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestVersionCanBeInjectedWithLdflags(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	exe := filepath.Join(t.TempDir(), "prompto-test")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}

	cmd := exec.Command("go", "build", "-o", exe, "-ldflags", "-X github.com/marcomoesman/prompto/internal/version.Version=9.9.9", "./cmd/prompto")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build with version ldflags failed: %v\n%s", err, out)
	}

	cmd = exec.Command(exe, "--version")
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("prompto --version failed: %v\n%s", err, out)
	}
	if got, want := strings.TrimSpace(string(out)), "prompto v9.9.9"; got != want {
		t.Fatalf("--version = %q, want %q", got, want)
	}
}
