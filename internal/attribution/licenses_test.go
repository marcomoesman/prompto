package attribution

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestThirdPartyModulesCoversGoModRequires(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	f, err := os.Open(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatalf("open go.mod: %v", err)
	}
	defer func() { _ = f.Close() }()

	got := make(map[string]ModuleNotice, len(ThirdPartyModules))
	for _, m := range ThirdPartyModules {
		got[m.Path] = m
	}

	inRequire := false
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch {
		case line == "require (":
			inRequire = true
			continue
		case inRequire && line == ")":
			inRequire = false
			continue
		case line == "" || strings.HasPrefix(line, "//"):
			continue
		}

		if !inRequire && !strings.HasPrefix(line, "require ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		modPath := fields[0]
		if modPath == "require" {
			modPath = fields[1]
		}
		notice, ok := got[modPath]
		if !ok {
			t.Errorf("missing license notice for required module %s", modPath)
			continue
		}
		if notice.Version == "" || notice.License == "" {
			t.Errorf("incomplete license notice for %s: %+v", modPath, notice)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan go.mod: %v", err)
	}
}

func TestRenderLicenseReportIncludesSpecialNotices(t *testing.T) {
	report := RenderLicenseReport()
	for _, want := range []string{
		"prompto ",
		"License: Apache-2.0",
		"**MIT**\n\n",
		"\n**BSD-4-Clause**\n\n",
		"github.com/bogdanfinn/fhttp",
		"does not include a top-level LICENSE file",
		"This product includes software developed by Bogdan Finn.",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("license report missing %q", want)
		}
	}
}

func TestThirdPartyNoticesFileMatchesRuntimeReport(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	got, err := os.ReadFile(filepath.Join(root, "THIRD_PARTY_NOTICES.md"))
	if err != nil {
		t.Fatalf("read THIRD_PARTY_NOTICES.md: %v", err)
	}
	want := RenderLicenseReport()
	if strings.TrimSpace(string(got)) != strings.TrimSpace(want) {
		t.Fatalf("THIRD_PARTY_NOTICES.md is out of sync with RenderLicenseReport%s", firstMismatch(string(got), want))
	}
}

func TestGoReleaserPackagesThirdPartyNotices(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	cfg, err := os.ReadFile(filepath.Join(root, ".goreleaser.yaml"))
	if err != nil {
		t.Fatalf("read .goreleaser.yaml: %v", err)
	}
	if !strings.Contains(string(cfg), "THIRD_PARTY_NOTICES.md") {
		t.Fatal(".goreleaser.yaml does not package THIRD_PARTY_NOTICES.md")
	}
}

func firstMismatch(got, want string) string {
	gotLines := strings.Split(strings.TrimSpace(got), "\n")
	wantLines := strings.Split(strings.TrimSpace(want), "\n")
	n := len(gotLines)
	if len(wantLines) < n {
		n = len(wantLines)
	}
	for i := 0; i < n; i++ {
		if gotLines[i] != wantLines[i] {
			return fmt.Sprintf(" at line %d:\n got: %q\nwant: %q", i+1, gotLines[i], wantLines[i])
		}
	}
	return fmt.Sprintf(": got %d lines, want %d lines", len(gotLines), len(wantLines))
}
