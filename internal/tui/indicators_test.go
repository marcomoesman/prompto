package tui

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestWorkingState_IsActive(t *testing.T) {
	cases := []struct {
		state WorkingState
		want  bool
	}{
		{StateIdle, false},
		{StateThinking, true},
		{StateStreaming, true},
		{StateToolRunning, true},
		{StateCompacting, true},
		{StateAwaitingApproval, true},
	}
	for _, c := range cases {
		if got := c.state.IsActive(); got != c.want {
			t.Errorf("state %d IsActive = %v, want %v", c.state, got, c.want)
		}
	}
}

func TestWorkingState_Label(t *testing.T) {
	cases := []struct {
		state        WorkingState
		detail       string
		thinkingWord string
		want         string
	}{
		{StateThinking, "", "", "Thinking…"},
		{StateThinking, "", "Pondering", "Pondering…"},
		{StateStreaming, "", "", "Streaming response…"},
		{StateToolRunning, "Read foo.go", "", "Read foo.go…"},
		{StateToolRunning, "", "", "Running tool…"},
		{StateCompacting, "", "", "Compacting context…"},
		{StateAwaitingApproval, "edit foo.go", "", "Approval needed: edit foo.go"},
		{StateAwaitingApproval, "", "", "Approval needed"},
	}
	for _, c := range cases {
		if got := c.state.label(c.detail, c.thinkingWord); got != c.want {
			t.Errorf("state %d label(%q, %q) = %q, want %q", c.state, c.detail, c.thinkingWord, got, c.want)
		}
	}
}

func TestRenderIndicator_IdleEmpty(t *testing.T) {
	if got := renderIndicator(StateIdle, "", "", "⠋", "", 0); got != "" {
		t.Errorf("idle indicator should be empty, got: %q", got)
	}
}

func TestRenderIndicator_ApprovalUsesAlertGlyph(t *testing.T) {
	got := renderIndicator(StateAwaitingApproval, "edit foo.go", "", "⠋", "", 0)
	if !strings.Contains(got, "⚠") {
		t.Errorf("approval indicator should use ⚠, got: %q", got)
	}
	if strings.Contains(got, "⠋") {
		t.Errorf("approval indicator should NOT show spinner, got: %q", got)
	}
}

func TestRenderIndicator_ActiveContainsLabel(t *testing.T) {
	got := renderIndicator(StateStreaming, "", "", "⠋", "", 0)
	if !strings.Contains(got, "Streaming response") {
		t.Errorf("expected Streaming label, got: %q", got)
	}
}

// TestRenderIndicator_ThinkingShowsPromptism asserts the rotating
// promptism word replaces the bare "Thinking" label when one was
// picked. The word arrives via the new thinkingWord parameter; no
// pick → fallback to "Thinking…". The shimmer renderer wraps each
// rune in its own ANSI escape, so the assertions strip ANSI before
// substring-checking the visible text.
func TestRenderIndicator_ThinkingShowsPromptism(t *testing.T) {
	got := renderIndicator(StateThinking, "", "", "⠋", "Bamboozeling", 0)
	plain := stripANSI(got)
	if !strings.Contains(plain, "Bamboozeling…") {
		t.Errorf("expected promptism in indicator, got plain=%q raw=%q", plain, got)
	}
	if strings.Contains(plain, "Thinking…") {
		t.Errorf("default Thinking label should be overridden by the promptism, got: %q", plain)
	}
}

// TestRenderShimmerLabel_PaintsAllRunes asserts every rune in the
// label is preserved (and present) regardless of the frame. The
// shimmer is purely a coloring effect; the visible text must not
// be altered, truncated, or reordered.
func TestRenderShimmerLabel_PaintsAllRunes(t *testing.T) {
	const label = "Pondering…"
	for _, frame := range []int{0, 1, 5, 10, 100, -3} {
		got := renderShimmerLabel(label, frame)
		if plain := stripANSI(got); plain != label {
			t.Errorf("frame=%d: visible text mutated by shimmer: got %q, want %q", frame, plain, label)
		}
	}
}

// TestRenderShimmerLabel_FrameAdvanceMovesHighlight is the regression
// for the animation invariant: frames within the same sweep must
// produce different ANSI output (the bright spot has moved). Without
// this the shimmer would render statically.
func TestRenderShimmerLabel_FrameAdvanceMovesHighlight(t *testing.T) {
	const label = "Pondering…"
	a := renderShimmerLabel(label, 0)
	b := renderShimmerLabel(label, 1)
	if a == b {
		t.Errorf("shimmer did not advance: frame 0 and 1 produced identical output\n  %q", a)
	}
}

// TestRenderShimmerLabel_CycleWraps asserts the bright-spot
// position wraps at the cycle boundary. Frame 0 and frame=cycle
// produce identical output — the sweep restarts.
func TestRenderShimmerLabel_CycleWraps(t *testing.T) {
	const label = "Pondering"
	cycle := len([]rune(label)) + shimmerCycleGap
	a := renderShimmerLabel(label, 0)
	b := renderShimmerLabel(label, cycle)
	if a != b {
		t.Errorf("shimmer cycle did not wrap at frame=%d: outputs differ", cycle)
	}
}

// TestRenderShimmerLabel_EmptyText is a defensive check: an empty
// label must not panic and must return empty output.
func TestRenderShimmerLabel_EmptyText(t *testing.T) {
	if got := renderShimmerLabel("", 0); got != "" {
		t.Errorf("empty label produced output: %q", got)
	}
}

// TestPickPromptism_AlwaysReturnsListMember asserts pickPromptism
// never returns a value outside the curated promptisms list. Run
// enough iterations to exercise the RNG distribution.
func TestPickPromptism_AlwaysReturnsListMember(t *testing.T) {
	allowed := make(map[string]struct{}, len(promptisms))
	for _, w := range promptisms {
		allowed[w] = struct{}{}
	}
	for range 1000 {
		got := pickPromptism()
		if _, ok := allowed[got]; !ok {
			t.Fatalf("pickPromptism returned %q, not in promptisms list", got)
		}
	}
}

func TestSummarizeToolCall_KnownTools(t *testing.T) {
	cases := []struct {
		name string
		args string
		want string
	}{
		{"read", `{"file_path":"foo.go"}`, "Read foo.go"},
		{"edit", `{"file_path":"cmd/main.go"}`, "Edit cmd/main.go"},
		{"write", `{"file_path":"x.txt"}`, "Write x.txt"},
		{"bash", `{"command":"go test ./..."}`, "Bash go test ./..."},
		{"grep", `{"pattern":"TODO"}`, "Grep TODO"},
		{"glob", `{"pattern":"**/*.go"}`, "Glob **/*.go"},
		{"list", `{"path":"."}`, "List ."},
		{"webfetch", `{"url":"https://example.com"}`, "Webfetch https://example.com"},
	}
	for _, c := range cases {
		if got := summarizeToolCall(c.name, c.args); got != c.want {
			t.Errorf("summarizeToolCall(%q, %q) = %q, want %q", c.name, c.args, got, c.want)
		}
	}
}

func TestSummarizeToolCall_TruncatesLong(t *testing.T) {
	long := strings.Repeat("a", 200)
	// Use grep here, not bash. Bash is intentionally never truncated
	// because the indicator is the user's last view of the command
	// before approval.
	got := summarizeToolCall("grep", `{"pattern":"`+long+`"}`)
	if n := utf8.RuneCountInString(got); n > 40 {
		t.Errorf("expected truncation to <=40 runes, got %d: %q", n, got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("expected ellipsis suffix on truncated label, got: %q", got)
	}
}

// TestSummarizeToolCall_BashNeverTruncates asserts the safety invariant:
// the approval indicator must show the full bash command, however long.
// If this test is changed to allow truncation, re-read the discussion in
// summarizeToolCall before doing so.
func TestSummarizeToolCall_BashNeverTruncates(t *testing.T) {
	long := strings.Repeat("a", 500)
	got := summarizeToolCall("bash", `{"command":"`+long+`"}`)
	want := "Bash " + long
	if got != want {
		t.Errorf("bash command was truncated or altered.\n got: %q\nwant: %q", got, want)
	}
	if strings.Contains(got, "…") {
		t.Errorf("bash indicator must not contain ellipsis, got: %q", got)
	}
}

func TestSummarizeToolCall_FallbackOnUnknown(t *testing.T) {
	if got := summarizeToolCall("custom", "{}"); got != "Custom" {
		t.Errorf("unknown tool fallback = %q, want %q", got, "Custom")
	}
}

func TestSummarizeToolCall_BadJSON(t *testing.T) {
	if got := summarizeToolCall("read", "not-json"); got != "Read" {
		t.Errorf("bad-JSON fallback = %q, want %q", got, "Read")
	}
}

func TestSummarizeToolCall_TodoWriteShowsTally(t *testing.T) {
	args := `{"todos":[
		{"id":"1","content":"a","status":"pending","active_form":"a"},
		{"id":"2","content":"b","status":"in_progress","active_form":"b"},
		{"id":"3","content":"c","status":"completed","active_form":"c"},
		{"id":"4","content":"d","status":"completed","active_form":"d"}
	]}`
	got := summarizeToolCall("todowrite", args)
	if got != "TodoWrite ☐1 ▶1 ✓2" {
		t.Errorf("todowrite summary = %q, want %q", got, "TodoWrite ☐1 ▶1 ✓2")
	}
}

func TestSummarizeToolCall_TodoWriteEmptyList(t *testing.T) {
	got := summarizeToolCall("todowrite", `{"todos":[]}`)
	if got != "TodoWrite ☐0 ▶0 ✓0" {
		t.Errorf("empty todowrite summary = %q, want %q", got, "TodoWrite ☐0 ▶0 ✓0")
	}
}

func TestSummarizeToolCall_TodoWriteUnknownStatusFallsToPending(t *testing.T) {
	args := `{"todos":[{"id":"1","status":"weird"}]}`
	got := summarizeToolCall("todowrite", args)
	if got != "TodoWrite ☐1 ▶0 ✓0" {
		t.Errorf("unknown-status fallback = %q, want %q", got, "TodoWrite ☐1 ▶0 ✓0")
	}
}

func TestDescribeRunningTool_KnownTools(t *testing.T) {
	cases := []struct {
		name string
		args string
		want string
	}{
		{"read", `{"file_path":"foo.go"}`, "Reading foo.go"},
		{"edit", `{"file_path":"cmd/main.go"}`, "Editing cmd/main.go"},
		{"write", `{"file_path":"x.txt"}`, "Writing x.txt"},
		{"bash", `{"command":"go test ./..."}`, "Running go test ./..."},
		{"grep", `{"pattern":"TODO"}`, `Searching for "TODO"`},
		{"glob", `{"pattern":"**/*.go"}`, `Finding files "**/*.go"`},
		{"list", `{"path":"."}`, "Listing ."},
		{"webfetch", `{"url":"https://example.com/path"}`, "Reading page content from example.com"},
		{"webfetch_headless", `{"url":"https://docs.foo.dev/x"}`, "Reading page content from docs.foo.dev"},
		{"task", `{"subagent_type":"explore","description":"investigate auth"}`, "Dispatching explore agent"},
		{"task", `{"subagent_type":"research","description":"Research Pi coding agent system prompts"}`, "Dispatching research agent"},
		{"todowrite", `{"todos":[{"status":"pending"},{"status":"completed"}]}`, "Updating todos ☐1 ▶0 ✓1"},
	}
	for _, c := range cases {
		if got := describeRunningTool(c.name, c.args); got != c.want {
			t.Errorf("describeRunningTool(%q, %q) = %q, want %q", c.name, c.args, got, c.want)
		}
	}
}

func TestDescribeRunningTool_FallsBackOnMissingArgs(t *testing.T) {
	cases := []struct {
		name string
		args string
		want string
	}{
		{"read", `{}`, "Reading file"},
		{"webfetch", `{}`, "Reading page content"},
		{"webfetch", `{"url":"::not a url::"}`, "Reading page content"},
		{"bash", `{}`, "Running shell command"},
		{"task", `{}`, "Dispatching agent"},
		{"someUnknownTool", `{}`, "Running someUnknownTool"},
		{"", `{}`, "Running tool"},
	}
	for _, c := range cases {
		if got := describeRunningTool(c.name, c.args); got != c.want {
			t.Errorf("describeRunningTool(%q, %q) = %q, want %q", c.name, c.args, got, c.want)
		}
	}
}

func TestDescribeRunningTool_TruncatesLongDetail(t *testing.T) {
	long := strings.Repeat("a", 200)
	got := describeRunningTool("bash", `{"command":"`+long+`"}`)
	if n := utf8.RuneCountInString(got); n > 40 {
		t.Errorf("expected truncation to <=40 runes for running indicator, got %d: %q", n, got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("expected ellipsis suffix on truncated running indicator, got: %q", got)
	}
}
