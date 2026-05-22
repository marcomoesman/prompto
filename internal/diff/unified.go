// Package diff produces a minimal unified-diff rendering for the
// `/plan diff` slash command. The output is line-oriented and meant
// for chat scroll-back, not for `patch -p1` consumption: there is
// one lumped hunk header and no surrounding-context trimming. That
// keeps the implementation small and the result readable.
package diff

import "strings"

// Unified returns a unified diff between two text bodies. Lines are
// split on `\n` with trailing `\r` stripped so CRLF and LF inputs
// produce the same diff. Identical inputs return "".
//
// The output begins with a `@@ ... @@` hunk header followed by lines
// prefixed with `-` (only in old), `+` (only in new), or ` ` (in
// both). The rendering is deliberately lossy on hunk boundaries —
// every line goes into a single hunk — but is sufficient for chat.
func Unified(oldText, newText string) string {
	oldLines := splitLines(oldText)
	newLines := splitLines(newText)

	ops := diffLines(oldLines, newLines)

	// All-equal body → no real diff. Returning "" lets callers treat
	// the result as a presence check ("did anything change?") without
	// re-scanning. Catches CRLF-only deltas that the line splitter
	// already normalised.
	allEqual := true
	for _, o := range ops {
		if o.kind != opEqual {
			allEqual = false
			break
		}
	}
	if allEqual {
		return ""
	}

	var b strings.Builder
	b.WriteString("@@ -1,")
	writeInt(&b, len(oldLines))
	b.WriteString(" +1,")
	writeInt(&b, len(newLines))
	b.WriteString(" @@\n")
	for _, op := range ops {
		switch op.kind {
		case opEqual:
			b.WriteByte(' ')
		case opRemove:
			b.WriteByte('-')
		case opAdd:
			b.WriteByte('+')
		}
		b.WriteString(op.line)
		b.WriteByte('\n')
	}
	return b.String()
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, "\n")
	for i, p := range parts {
		parts[i] = strings.TrimSuffix(p, "\r")
	}
	// Trailing empty element from a final `\n` — keep it as one
	// "blank line" so a file that ends with a newline diffs the same
	// as one that doesn't add or remove that trailing newline.
	if len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

type opKind int

const (
	opEqual opKind = iota
	opRemove
	opAdd
)

type op struct {
	kind opKind
	line string
}

// OpKind tags one entry in a structured diff. Mirrors the internal
// opKind so callers don't have to depend on the package's private
// type for line-by-line rendering.
type OpKind int

// OpKind values. OpEqual lines appear in both inputs, OpRemove only
// in the old input, OpAdd only in the new.
const (
	OpEqual OpKind = iota
	OpRemove
	OpAdd
)

// Op is one structured diff entry. OldLine and NewLine are 1-based
// line numbers in the respective inputs; whichever side the op
// doesn't appear on holds 0. Line is the literal text without the
// trailing newline (or `\r` from CRLF input — splitLines normalises
// that the same way Unified does).
type Op struct {
	Kind    OpKind
	OldLine int
	NewLine int
	Line    string
}

// Diff returns a structured line-oriented diff between two text
// bodies. Same input contract as Unified (LF/CRLF agnostic), but
// surfaces the per-line ops so callers like the TUI can render
// gutter+line-number layouts instead of consuming the flat unified
// string. Identical inputs return nil.
func Diff(oldText, newText string) []Op {
	oldLines := splitLines(oldText)
	newLines := splitLines(newText)
	ops := diffLines(oldLines, newLines)

	allEqual := true
	for _, o := range ops {
		if o.kind != opEqual {
			allEqual = false
			break
		}
	}
	if allEqual {
		return nil
	}

	out := make([]Op, 0, len(ops))
	oldNum, newNum := 1, 1
	for _, o := range ops {
		switch o.kind {
		case opEqual:
			out = append(out, Op{Kind: OpEqual, OldLine: oldNum, NewLine: newNum, Line: o.line})
			oldNum++
			newNum++
		case opRemove:
			out = append(out, Op{Kind: OpRemove, OldLine: oldNum, Line: o.line})
			oldNum++
		case opAdd:
			out = append(out, Op{Kind: OpAdd, NewLine: newNum, Line: o.line})
			newNum++
		}
	}
	return out
}

// diffLines runs the textbook longest-common-subsequence DP, then
// backtracks to produce a list of equal/remove/add operations in
// document order. O(len(a)*len(b)) time and space — fine for plan
// markdown which is tens of lines, not megabytes.
func diffLines(a, b []string) []op {
	n, m := len(a), len(b)
	// dp[i][j] = LCS length of a[i:] vs b[j:]. Bottom-up so
	// backtracking is forward (clean op order without reversing).
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}

	var ops []op
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, op{kind: opEqual, line: a[i]})
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			ops = append(ops, op{kind: opRemove, line: a[i]})
			i++
		default:
			ops = append(ops, op{kind: opAdd, line: b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, op{kind: opRemove, line: a[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, op{kind: opAdd, line: b[j]})
	}
	return ops
}

// writeInt appends a non-negative integer to b without an alloc-heavy
// strconv.Itoa hop. Plan files are small but this gets called from
// every diff render; cheap optimisation, no behaviour change.
func writeInt(b *strings.Builder, n int) {
	if n == 0 {
		b.WriteByte('0')
		return
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	b.Write(buf[i:])
}
