package tui

import (
	"strings"
	"testing"
)

// TestChat_CollapseConsecutiveReadsOfSameFile is the regression for the
// "wall of identical Read rows" complaint: a model paging through a
// long source file via offset/limit emits N tool_calls, all targeting
// the same file_path. The renderer should fold them into one row with
// a "× N reads · lines start-end of total · size" merged summary.
func TestChat_CollapseConsecutiveReadsOfSameFile(t *testing.T) {
	c := NewChatModel()
	c.SetSize(200, 100)

	for i, rng := range [][3]int{
		// {idx, offset, end} — total file is 1566 lines, 57.4KB
		{1, 1, 100},
		{2, 101, 200},
		{3, 201, 300},
	} {
		id := []string{"id-1", "id-2", "id-3"}[i]
		c.AppendToolCall(id, "read", `{"file_path":"internal/agent/run.go"}`,
			`Read(file_path: "internal/agent/run.go")`)
		summary := "lines " + itoa(rng[1]) + "–" + itoa(rng[2]) + " of 1566 · 57.4KB"
		c.AppendToolResult(id, "read", "...", false, summary)
	}

	rendered := c.viewport.View()

	// Expect a single merged "× 3" badge in the header.
	if !strings.Contains(rendered, "× 3") {
		t.Errorf("expected '× 3' count badge on merged header, got:\n%s", rendered)
	}
	// And one combined range line that spans the full run.
	if !strings.Contains(rendered, "3 reads · lines 1–300 of 1566 · 57.4KB") {
		t.Errorf("expected merged summary '3 reads · lines 1–300 of 1566 · 57.4KB' in:\n%s", rendered)
	}
	// The original per-row range lines must NOT appear individually.
	for _, individual := range []string{"lines 1–100 of 1566", "lines 101–200 of 1566", "lines 201–300 of 1566"} {
		if strings.Contains(rendered, individual) {
			t.Errorf("merged run should suppress per-row summary %q, but it appeared:\n%s", individual, rendered)
		}
	}
	// Only one `⚡ Read(...)` row visible.
	if got := strings.Count(rendered, `Read(file_path: "internal/agent/run.go")`); got != 1 {
		t.Errorf("expected exactly 1 Read header (merged), got %d:\n%s", got, rendered)
	}
}

// TestChat_DifferentFilesDoNotCollapse: collapse must key on file_path,
// not just on tool name. Three reads of three different files render
// as three separate rows.
func TestChat_DifferentFilesDoNotCollapse(t *testing.T) {
	c := NewChatModel()
	c.SetSize(200, 100)

	c.AppendToolCall("id-1", "read", `{"file_path":"a.go"}`, `Read(file_path: "a.go")`)
	c.AppendToolResult("id-1", "read", "...", false, "lines 1–50 of 100 · 1.0KB")
	c.AppendToolCall("id-2", "read", `{"file_path":"b.go"}`, `Read(file_path: "b.go")`)
	c.AppendToolResult("id-2", "read", "...", false, "lines 1–50 of 100 · 1.0KB")
	c.AppendToolCall("id-3", "read", `{"file_path":"c.go"}`, `Read(file_path: "c.go")`)
	c.AppendToolResult("id-3", "read", "...", false, "lines 1–50 of 100 · 1.0KB")

	rendered := c.viewport.View()

	if strings.Contains(rendered, "× ") {
		t.Errorf("no collapse expected across different files, but × count appeared:\n%s", rendered)
	}
	for _, name := range []string{"a.go", "b.go", "c.go"} {
		if !strings.Contains(rendered, name) {
			t.Errorf("missing per-file row %q in:\n%s", name, rendered)
		}
	}
}

// TestChat_ErroredCallBreaksCollapseRun: when one call in a sequence
// errored, the run must break around it (Q1=b). The errored call
// renders standalone with its full error body so the user can see
// what failed; calls before/after still collapse if eligible.
func TestChat_ErroredCallBreaksCollapseRun(t *testing.T) {
	c := NewChatModel()
	c.SetSize(200, 100)

	args := `{"file_path":"run.go"}`
	header := `Read(file_path: "run.go")`

	c.AppendToolCall("id-1", "read", args, header)
	c.AppendToolResult("id-1", "read", "...", false, "lines 1–100 of 1566 · 57.4KB")
	c.AppendToolCall("id-2", "read", args, header)
	c.AppendToolResult("id-2", "read", "lines 9999 out of range", true, "")
	c.AppendToolCall("id-3", "read", args, header)
	c.AppendToolResult("id-3", "read", "...", false, "lines 101–200 of 1566 · 57.4KB")
	c.AppendToolCall("id-4", "read", args, header)
	c.AppendToolResult("id-4", "read", "...", false, "lines 201–300 of 1566 · 57.4KB")

	rendered := c.viewport.View()

	// The errored call's failure body must be visible.
	if !strings.Contains(rendered, "lines 9999 out of range") {
		t.Errorf("errored call's body must remain visible, got:\n%s", rendered)
	}
	// The two successful reads AFTER the error should collapse to "× 2".
	if !strings.Contains(rendered, "× 2") {
		t.Errorf("expected '× 2' collapse for the two reads after the error, got:\n%s", rendered)
	}
	// And the single successful read BEFORE the error renders as
	// itself, NOT collapsed.
	if !strings.Contains(rendered, "lines 1–100 of 1566 · 57.4KB") {
		t.Errorf("pre-error read should render standalone with its individual summary, got:\n%s", rendered)
	}
}

// TestChat_NonReadToolsNeverCollapse: collapseKey is computed only for
// "read". Three consecutive Edit calls — even on the same file — must
// render as three rows, since Edit summaries / diffs are per-call
// information the user shouldn't lose.
func TestChat_NonReadToolsNeverCollapse(t *testing.T) {
	c := NewChatModel()
	c.SetSize(200, 100)

	c.AppendToolCall("id-1", "edit", `{"file_path":"x.go"}`, `Edit(file_path: "x.go")`)
	c.AppendToolResult("id-1", "edit", "...", false, "1 change")
	c.AppendToolCall("id-2", "edit", `{"file_path":"x.go"}`, `Edit(file_path: "x.go")`)
	c.AppendToolResult("id-2", "edit", "...", false, "1 change")

	rendered := c.viewport.View()
	if strings.Contains(rendered, "× ") {
		t.Errorf("Edit must not collapse; got × badge:\n%s", rendered)
	}
}

// TestChat_InFlightCallDoesNotCollapse: a run where the last call
// hasn't received its Done event yet must not partial-merge — the
// final row stays separate until its summary lands. This avoids
// jitter as Done events trickle in for parallel-dispatch batches.
func TestChat_InFlightCallDoesNotCollapse(t *testing.T) {
	c := NewChatModel()
	c.SetSize(200, 100)

	c.AppendToolCall("id-1", "read", `{"file_path":"run.go"}`, `Read(file_path: "run.go")`)
	c.AppendToolResult("id-1", "read", "...", false, "lines 1–100 of 1566 · 57.4KB")
	c.AppendToolCall("id-2", "read", `{"file_path":"run.go"}`, `Read(file_path: "run.go")`)
	c.AppendToolResult("id-2", "read", "...", false, "lines 101–200 of 1566 · 57.4KB")
	// id-3 in-flight: started but no result yet.
	c.AppendToolCall("id-3", "read", `{"file_path":"run.go"}`, `Read(file_path: "run.go")`)

	rendered := c.viewport.View()
	// Only the resolved pair (id-1 + id-2) merges. The in-flight id-3
	// renders as a third standalone header row.
	if !strings.Contains(rendered, "× 2") {
		t.Errorf("expected '× 2' for the two resolved reads, got:\n%s", rendered)
	}
	if got := strings.Count(rendered, `Read(file_path: "run.go")`); got != 2 {
		t.Errorf("expected 2 Read headers (merged pair + in-flight one), got %d:\n%s", got, rendered)
	}
}

// TestParseReadRangeSummary covers the parser used by the collapse
// detector. The only acceptable shape is the en-dash ranged form;
// other Read summary variants must be rejected so they don't merge.
func TestParseReadRangeSummary(t *testing.T) {
	cases := []struct {
		name string
		in   string
		ok   bool
	}{
		{"ranged en-dash", "lines 1–100 of 1566 · 57.4KB", true},
		{"hyphen not en-dash", "lines 1-100 of 1566 · 57.4KB", false},
		{"whole-file format", "124 lines · 4.9KB", false},
		{"spilled format", "lines 1–500 · 23.0MB", false},
		{"garbage", "wat", false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, ok := parseReadRangeSummary(tc.in)
			if ok != tc.ok {
				t.Errorf("parseReadRangeSummary(%q) ok = %v, want %v", tc.in, ok, tc.ok)
			}
		})
	}
}

func TestParseReadRangeSummary_ExtractsFields(t *testing.T) {
	got, ok := parseReadRangeSummary("lines 101–200 of 1566 · 57.4KB")
	if !ok {
		t.Fatal("parse failed")
	}
	if got.start != 101 || got.end != 200 || got.total != 1566 || got.size != "57.4KB" {
		t.Errorf("parsed = %+v, want {start:101 end:200 total:1566 size:57.4KB}", got)
	}
}

