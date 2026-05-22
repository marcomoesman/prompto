package tui

import (
	"fmt"
	"strings"
	"testing"
)

func TestStatusBar_ContextPct(t *testing.T) {
	cases := []struct {
		ctx   int
		limit int
		want  int
	}{
		{0, 100_000, -1}, // no usage yet
		{50_000, 0, -1},  // limit unset
		{50_000, 100_000, 50},
		{99_999, 100_000, 99},
		{200_000, 100_000, 100}, // bounded
	}
	for _, c := range cases {
		m := StatusModel{contextTokens: c.ctx, contextLimit: c.limit}
		if got := m.contextPct(); got != c.want {
			t.Errorf("contextPct(ctx=%d limit=%d) = %d, want %d", c.ctx, c.limit, got, c.want)
		}
	}
}

func TestStatusBar_RendersContextPercentage(t *testing.T) {
	m := StatusModel{
		modelName:     "model-x",
		contextTokens: 50_000,
		contextLimit:  100_000,
		width:         120,
	}
	out := m.View()
	if !strings.Contains(out, "context: 50%") {
		t.Errorf("expected context: 50%% in status, got: %q", out)
	}
}

func TestStatusBar_NarrowDropsRight(t *testing.T) {
	m := StatusModel{
		modelName: "model-x",
		width:     70, // < 80 drops right
	}
	out := m.View()
	if strings.Contains(out, "? for help") {
		t.Errorf("expected right segment dropped at width 70, got: %q", out)
	}
	if !strings.Contains(out, "model-x") {
		t.Errorf("center segment should still render at width 70, got: %q", out)
	}
}

func TestStatusBar_VeryNarrowDropsCenter(t *testing.T) {
	m := StatusModel{
		modelName:     "model-x",
		contextTokens: 50_000,
		contextLimit:  100_000,
		width:         50, // < 60 drops center+right
	}
	out := m.View()
	if strings.Contains(out, "model-x") {
		t.Errorf("center segment should be dropped at width 50, got: %q", out)
	}
	if strings.Contains(out, "? for help") {
		t.Errorf("right segment should be dropped at width 50, got: %q", out)
	}
	if !strings.Contains(out, "context:") {
		t.Errorf("left segment must always render, got: %q", out)
	}
}

func TestStatusBar_ElapsedSeconds(t *testing.T) {
	m := StatusModel{elapsedSec: 7, width: 120, modelName: "m"}
	out := m.View()
	if !strings.Contains(out, "7s") {
		t.Errorf("expected ⏱ 7s in status, got: %q", out)
	}
}

func TestStatusBar_ElapsedRendersMinutesAndHours(t *testing.T) {
	cases := []struct {
		sec  int
		want string
	}{
		{635, "10m35s"},
		{3600, "1h00m"},
		{7325, "2h02m"},
	}
	for _, c := range cases {
		m := StatusModel{elapsedSec: c.sec, width: 120, modelName: "m"}
		out := m.View()
		if !strings.Contains(out, c.want) {
			t.Errorf("elapsedSec=%d: expected %q in status, got: %q", c.sec, c.want, out)
		}
		// Raw seconds should never leak through once we cross a minute.
		if strings.Contains(out, fmt.Sprintf("%ds", c.sec)) {
			t.Errorf("elapsedSec=%d: raw seconds leaked into status: %q", c.sec, out)
		}
	}
}

func TestFormatElapsed(t *testing.T) {
	cases := []struct {
		sec  int
		want string
	}{
		{0, "0s"},
		{1, "1s"},
		{59, "59s"},
		{60, "1m00s"},
		{61, "1m01s"},
		{635, "10m35s"},
		{3599, "59m59s"},
		{3600, "1h00m"},
		{3660, "1h01m"},
		{7325, "2h02m"},
		{86399, "23h59m"},
		{86400, "1d00h"},
		{90000, "1d01h"},
	}
	for _, c := range cases {
		if got := formatElapsed(c.sec); got != c.want {
			t.Errorf("formatElapsed(%d) = %q, want %q", c.sec, got, c.want)
		}
	}
}

// The status bar's todo segment was removed when the sticky todo
// panel landed; the panel above the input is now the single surface
// for todo state. A regression here would re-introduce the duplicate
// surface the sticky-panel work explicitly removed.
func TestStatusBar_DoesNotRenderTodoSegment(t *testing.T) {
	m := StatusModel{modelName: "m", width: 120}
	out := m.View()
	if strings.Contains(out, "todos") {
		t.Errorf("status bar must not render a todo segment after the sticky panel landed; got: %q", out)
	}
}

func TestFormatTokenCount(t *testing.T) {
	cases := []struct {
		n    int
		want string
	}{
		{0, "0"},
		{42, "42"},
		{1_000, "1.0k"},
		{1_500, "1.5k"},
		{12_345, "12.3k"},
	}
	for _, c := range cases {
		if got := formatTokenCount(c.n); got != c.want {
			t.Errorf("formatTokenCount(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}
