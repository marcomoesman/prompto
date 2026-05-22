package tool

import (
	"runtime"
	"testing"
)

func TestNormalizeMSYSPath(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("MSYS path normalization only applies on Windows")
	}
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"git-bash drive root", "/g", `g:\`},
		{"git-bash full path", "/g/Go Workspace/prompto", `g:\Go Workspace\prompto`},
		{"git-bash users", "/c/Users/marco", `c:\Users\marco`},
		{"uppercase letter passes through unchanged form", "/G/foo", `G:\foo`},
		{"native windows path untouched", `G:\Go Workspace\prompto`, `G:\Go Workspace\prompto`},
		{"forward-slash drive untouched", "G:/Go Workspace/prompto", "G:/Go Workspace/prompto"},
		{"relative path untouched", "internal/tool", "internal/tool"},
		{"two-letter posix prefix not rewritten", "/go/src/foo", "/go/src/foo"},
		{"empty string", "", ""},
		{"single slash", "/", "/"},
		{"slash digit", "/1/foo", "/1/foo"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := normalizeMSYSPath(c.in)
			if got != c.want {
				t.Fatalf("normalizeMSYSPath(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestNormalizeMSYSPathNoOpOnPOSIX(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("only meaningful on POSIX hosts where /g/... is a real path")
	}
	in := "/g/Go Workspace/prompto"
	if got := normalizeMSYSPath(in); got != in {
		t.Fatalf("normalizeMSYSPath(%q) = %q, want unchanged", in, got)
	}
}
