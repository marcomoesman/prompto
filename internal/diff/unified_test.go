package diff

import (
	"strings"
	"testing"
)

func TestUnified_IdenticalReturnsEmpty(t *testing.T) {
	if got := Unified("foo\nbar\n", "foo\nbar\n"); got != "" {
		t.Errorf("identical = %q, want empty", got)
	}
	if got := Unified("", ""); got != "" {
		t.Errorf("both empty = %q, want empty", got)
	}
}

func TestUnified_PureAddition(t *testing.T) {
	got := Unified("", "added\n")
	if !strings.Contains(got, "+added") {
		t.Errorf("pure addition missing +added: %q", got)
	}
	if strings.Contains(got, "-") {
		// Hunk header has a `-` so substring contains check is too
		// loose; instead make sure no body line starts with `-`.
		for _, line := range strings.Split(got, "\n") {
			if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "@@") {
				t.Errorf("pure addition produced a remove line: %q", line)
			}
		}
	}
}

func TestUnified_PureRemoval(t *testing.T) {
	got := Unified("removed\n", "")
	if !strings.Contains(got, "-removed") {
		t.Errorf("pure removal missing -removed: %q", got)
	}
	for _, line := range strings.Split(got, "\n") {
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "@@") {
			t.Errorf("pure removal produced an add line: %q", line)
		}
	}
}

func TestUnified_MiddleInsertion(t *testing.T) {
	old := "a\nb\nc\n"
	newer := "a\nB\nc\n"
	got := Unified(old, newer)
	// Body should mark `b` removed and `B` added with `a` and `c`
	// kept as equal context.
	if !strings.Contains(got, "-b") {
		t.Errorf("expected `-b` line in:\n%s", got)
	}
	if !strings.Contains(got, "+B") {
		t.Errorf("expected `+B` line in:\n%s", got)
	}
	if !strings.Contains(got, " a") {
		t.Errorf("expected ` a` (equal) line in:\n%s", got)
	}
	if !strings.Contains(got, " c") {
		t.Errorf("expected ` c` (equal) line in:\n%s", got)
	}
}

func TestUnified_CRLFNormalised(t *testing.T) {
	a := "foo\r\nbar\r\n"
	b := "foo\nbar\n"
	if got := Unified(a, b); got != "" {
		t.Errorf("CRLF vs LF should diff to empty, got:\n%s", got)
	}
}

func TestUnified_HunkHeaderShape(t *testing.T) {
	got := Unified("a\n", "a\nb\n")
	if !strings.HasPrefix(got, "@@ -1,1 +1,2 @@\n") {
		t.Errorf("expected hunk header @@ -1,1 +1,2 @@, got first line: %q",
			strings.SplitN(got, "\n", 2)[0])
	}
}

func TestUnified_LargeBodySingleLineEdit(t *testing.T) {
	// A 200-line input with a single-line edit shouldn't blow up;
	// guards against accidental quadratic blowups in the writer.
	var oldLines, newLines []string
	for i := range 200 {
		line := "line " + string(rune('A'+i%26)) + "\n"
		oldLines = append(oldLines, line)
		newLines = append(newLines, line)
	}
	newLines[100] = "MUTATED\n"
	old := strings.Join(oldLines, "")
	newer := strings.Join(newLines, "")
	got := Unified(old, newer)
	if !strings.Contains(got, "+MUTATED") {
		t.Errorf("missing +MUTATED in 200-line diff")
	}
}
