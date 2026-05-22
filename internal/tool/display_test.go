package tool

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestHumanizeBytes(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{0, "0B"},
		{1, "1B"},
		{1023, "1023B"},
		{1024, "1.0KB"},
		{1536, "1.5KB"},
		{223 * 1024, "223.0KB"},
		{223*1024 + 307, "223.3KB"},
		{1024 * 1024, "1.0MB"},
		{15*1024*1024 + 512*1024, "15.5MB"},
		{1024 * 1024 * 1024, "1.0GB"},
	}
	for _, c := range cases {
		if got := HumanizeBytes(c.in); got != c.want {
			t.Errorf("HumanizeBytes(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestQuoteArg(t *testing.T) {
	if got := QuoteArg("hello", 80); got != `"hello"` {
		t.Errorf("plain: got %q", got)
	}
	if got := QuoteArg(`a"b`, 80); got != `"a\"b"` {
		t.Errorf("inner-quote: got %q", got)
	}
	long := "abcdefghij"
	if got := QuoteArg(long, 5); got != `"abcd…"` {
		t.Errorf("trim: got %q", got)
	}
	if got := QuoteArg("a\nb", 80); got != `"a\nb"` {
		t.Errorf("newline: got %q", got)
	}
	// UTF-8 must not be Go-escaped.
	if got := QuoteArg("héllo", 80); got != `"héllo"` {
		t.Errorf("utf8: got %q", got)
	}
}

// TestQuoteArg_TruncatesAtRuneBoundary regresses a UTF-8-corruption
// bug where the byte-indexed truncation could split a multibyte
// codepoint (CJK, emoji, accented Latin), producing invalid UTF-8
// that rendered as garbled bytes in the terminal.
func TestQuoteArg_TruncatesAtRuneBoundary(t *testing.T) {
	cases := []struct {
		name   string
		input  string
		maxLen int
	}{
		// Each rune is 3 bytes; 5-rune limit forces truncation
		// after rune 4 + ellipsis.
		{"cjk", "你好世界你好世界", 5},
		// Each emoji is 4 bytes; truncation must not split a
		// surrogate-style sequence.
		{"emoji", "🐶🐱🐭🐹🐰🦊🐻🐼", 5},
		// Mixed ASCII + multibyte; truncation point lands in
		// middle of a multibyte rune in the buggy version.
		{"mixed", "ascii_前缀_suffix_more", 10},
		// Accented Latin: 2 bytes per accented rune.
		{"accents", "naïve résumé café crêpe", 8},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := QuoteArg(c.input, c.maxLen)
			if !utf8.ValidString(got) {
				t.Errorf("output is not valid UTF-8: % x", got)
			}
			if !strings.HasSuffix(got, `…"`) {
				t.Errorf("expected trailing ellipsis+quote on truncated value, got %q", got)
			}
		})
	}
}

func TestFormatCall(t *testing.T) {
	got := FormatCall("WebFetch", "url", "https://example.com", "prompt", "what is on this page")
	want := `WebFetch(url: "https://example.com", prompt: "what is on this page")`
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestFormatCallSingle(t *testing.T) {
	if got := FormatCall("Read", "file_path", "/x/y.go"); got != `Read(file_path: "/x/y.go")` {
		t.Errorf("got %q", got)
	}
}

func TestFormatCallNoArgs(t *testing.T) {
	if got := FormatCall("Bare"); got != "Bare()" {
		t.Errorf("got %q", got)
	}
}

func TestRelPathForDisplay_InsideCwd(t *testing.T) {
	// Use filepath.Join so the assertion works on both Unix and Windows.
	cwd := filepath.Join("home", "u", "proj")
	abs := filepath.Join(cwd, "internal", "main.go")
	got := RelPathForDisplay(cwd, abs)
	if got != "internal/main.go" {
		t.Errorf("relpath = %q, want %q", got, "internal/main.go")
	}
}

func TestRelPathForDisplay_NestedDirectory(t *testing.T) {
	cwd := filepath.Join("home", "u", "proj")
	abs := filepath.Join(cwd, "internal", "tui", "env.go")
	got := RelPathForDisplay(cwd, abs)
	if got != "internal/tui/env.go" {
		t.Errorf("relpath = %q, want %q", got, "internal/tui/env.go")
	}
}

func TestRelPathForDisplay_OutsideCwdFallsBackToAbsolute(t *testing.T) {
	// A path that is genuinely outside cwd must come back unchanged so the
	// user sees exactly what was touched. On Windows, /etc/hosts is on the
	// same volume as a relative cwd, so filepath.Rel would synthesize a
	// "..\..\etc\hosts" — the test guards against that.
	cwd := filepath.Join("home", "u", "proj")
	outside := filepath.Join("etc", "hosts")
	got := RelPathForDisplay(cwd, outside)
	if got != outside {
		t.Errorf("relpath = %q, want absolute fallback %q", got, outside)
	}
}

func TestRelPathForDisplay_URLUnchanged(t *testing.T) {
	for _, u := range []string{"https://example.com/a", "http://localhost:8080/api"} {
		if got := RelPathForDisplay("/x", u); got != u {
			t.Errorf("url %q got rewritten to %q", u, got)
		}
	}
}

func TestRelPathForDisplay_EmptyArgs(t *testing.T) {
	if got := RelPathForDisplay("", "/a/b"); got != "/a/b" {
		t.Errorf("empty cwd: got %q, want passthrough", got)
	}
	if got := RelPathForDisplay("/cwd", ""); got != "" {
		t.Errorf("empty path: got %q, want empty", got)
	}
}

func TestRelPathForDisplay_WindowsCrossVolume(t *testing.T) {
	// Windows-only: filepath.Rel returns an error for cross-volume paths.
	// We must fall back to the absolute form.
	if runtime.GOOS != "windows" {
		t.Skip("windows-specific cross-volume behaviour")
	}
	got := RelPathForDisplay(`G:\Go Workspace\prompto`, `C:\Windows\System32\drivers\etc\hosts`)
	if !strings.HasPrefix(got, "C:") {
		t.Errorf("cross-volume path = %q, want absolute fallback starting with C:", got)
	}
}
