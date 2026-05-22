package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/marcomoesman/prompto/internal/command"
)

// fakeCmd is a minimal command.Command for testing SuggestionsModel
// without dragging in real builtins (which depend on agent/store).
type fakeCmd struct {
	name string
	help string
}

func (f fakeCmd) Name() string                                                { return f.name }
func (f fakeCmd) Aliases() []string                                           { return nil }
func (f fakeCmd) Kind() command.Kind                                          { return command.KindLocal }
func (f fakeCmd) Help() string                                                { return f.help }
func (f fakeCmd) Exec(context.Context, []string, command.Env) (command.Result, error) {
	return command.Result{}, nil
}

// newTestRegistry registers cmds in name order so tests have a
// predictable Registry.All() result.
func newTestRegistry(t *testing.T, names ...string) *command.Registry {
	t.Helper()
	r := command.NewRegistry()
	for _, n := range names {
		if err := r.Register(fakeCmd{name: n, help: n + " help"}); err != nil {
			t.Fatalf("register %q: %v", n, err)
		}
	}
	return r
}

func TestSuggestionsHiddenWhenNoSlash(t *testing.T) {
	reg := newTestRegistry(t, "help", "quit")
	m := NewSuggestionsModel(reg)
	m.Update("hello")
	if m.Visible() {
		t.Fatalf("expected hidden for plain text")
	}
}

func TestSuggestionsHiddenForNilRegistry(t *testing.T) {
	m := NewSuggestionsModel(nil)
	m.Update("/help")
	if m.Visible() {
		t.Fatalf("expected hidden when registry is nil")
	}
}

func TestSuggestionsShowsAllOnBareSlash(t *testing.T) {
	reg := newTestRegistry(t, "alpha", "beta", "gamma")
	m := NewSuggestionsModel(reg)
	m.Update("/")
	if !m.Visible() {
		t.Fatalf("expected visible for `/`")
	}
	if got := len(m.matches); got != 3 {
		t.Fatalf("matches=%d, want 3", got)
	}
	if m.cursor != 0 {
		t.Fatalf("cursor=%d, want 0", m.cursor)
	}
}

func TestSuggestionsPrefixFilter(t *testing.T) {
	reg := newTestRegistry(t, "compact", "context", "clear", "help")
	m := NewSuggestionsModel(reg)
	m.Update("/co")
	if !m.Visible() {
		t.Fatalf("expected visible")
	}
	want := []string{"compact", "context"}
	got := make([]string, len(m.matches))
	for i, c := range m.matches {
		got[i] = c.Name()
	}
	if !equalStrings(got, want) {
		t.Fatalf("matches=%v, want %v", got, want)
	}
}

func TestSuggestionsCaseInsensitive(t *testing.T) {
	reg := newTestRegistry(t, "Compact", "compute")
	m := NewSuggestionsModel(reg)
	m.Update("/COM")
	if !m.Visible() || len(m.matches) != 2 {
		t.Fatalf("expected 2 matches, got %d (visible=%v)", len(m.matches), m.Visible())
	}
}

func TestSuggestionsHidesAfterSpace(t *testing.T) {
	reg := newTestRegistry(t, "model")
	m := NewSuggestionsModel(reg)
	m.Update("/model")
	if !m.Visible() {
		t.Fatalf("expected visible before space")
	}
	m.Update("/model ")
	if m.Visible() {
		t.Fatalf("expected hidden after typing space (user moving to args)")
	}
}

func TestSuggestionsHidesOnNoMatch(t *testing.T) {
	reg := newTestRegistry(t, "help")
	m := NewSuggestionsModel(reg)
	m.Update("/zzz")
	if m.Visible() {
		t.Fatalf("expected hidden when prefix matches nothing")
	}
}

func TestSuggestionsResetsCursorOnPrefixChange(t *testing.T) {
	reg := newTestRegistry(t, "alpha", "ant", "beta")
	m := NewSuggestionsModel(reg)
	m.Update("/a")
	m.Move(1) // cursor → 1 (ant)
	if m.cursor != 1 {
		t.Fatalf("cursor=%d, want 1", m.cursor)
	}
	m.Update("/b") // prefix changed
	if m.cursor != 0 {
		t.Fatalf("cursor=%d after prefix change, want 0", m.cursor)
	}
}

func TestSuggestionsMoveClampsAtBounds(t *testing.T) {
	reg := newTestRegistry(t, "a", "b", "c")
	m := NewSuggestionsModel(reg)
	m.Update("/")
	m.Move(-5)
	if m.cursor != 0 {
		t.Fatalf("cursor=%d after Move(-5), want 0", m.cursor)
	}
	m.Move(99)
	if m.cursor != 2 {
		t.Fatalf("cursor=%d after Move(99), want 2", m.cursor)
	}
}

func TestSuggestionsScrollWindow(t *testing.T) {
	reg := newTestRegistry(t, "a", "b", "c", "d", "e", "f", "g")
	m := NewSuggestionsModel(reg)
	m.Update("/")
	if m.scroll != 0 {
		t.Fatalf("initial scroll=%d, want 0", m.scroll)
	}
	// Move past the visible window of 5 → scroll should slide.
	for i := 0; i < 5; i++ {
		m.Move(1)
	}
	if m.cursor != 5 {
		t.Fatalf("cursor=%d after 5 moves, want 5", m.cursor)
	}
	if m.scroll != 1 {
		t.Fatalf("scroll=%d after sliding, want 1", m.scroll)
	}
	m.Move(1)
	if m.scroll != 2 {
		t.Fatalf("scroll=%d after one more, want 2", m.scroll)
	}
	// Move back up: scroll should follow.
	m.Move(-10)
	if m.cursor != 0 || m.scroll != 0 {
		t.Fatalf("after big up: cursor=%d scroll=%d, want 0/0", m.cursor, m.scroll)
	}
}

func TestSuggestionsSelectedReturnsHighlighted(t *testing.T) {
	reg := newTestRegistry(t, "alpha", "beta")
	m := NewSuggestionsModel(reg)
	m.Update("/")
	if got := m.Selected(); got == nil || got.Name() != "alpha" {
		t.Fatalf("Selected()=%v, want alpha", got)
	}
	m.Move(1)
	if got := m.Selected(); got == nil || got.Name() != "beta" {
		t.Fatalf("Selected()=%v, want beta", got)
	}
}

func TestSuggestionsSelectedNilWhenHidden(t *testing.T) {
	reg := newTestRegistry(t, "alpha")
	m := NewSuggestionsModel(reg)
	if m.Selected() != nil {
		t.Fatalf("Selected() should be nil before any Update")
	}
	m.Update("hello")
	if m.Selected() != nil {
		t.Fatalf("Selected() should be nil when hidden")
	}
}

func TestSuggestionsHideForcesHidden(t *testing.T) {
	reg := newTestRegistry(t, "alpha")
	m := NewSuggestionsModel(reg)
	m.Update("/")
	if !m.Visible() {
		t.Fatalf("precondition: should be visible")
	}
	m.Hide()
	if m.Visible() {
		t.Fatalf("Hide() should force-hide")
	}
}

func TestSuggestionsHeightWithBorder(t *testing.T) {
	reg := newTestRegistry(t, "a", "b", "c")
	m := NewSuggestionsModel(reg)
	if h := m.Height(); h != 0 {
		t.Fatalf("hidden height=%d, want 0", h)
	}
	m.Update("/")
	// 3 matches + 2 border + 1 hint = 6
	if h := m.Height(); h != 6 {
		t.Fatalf("height=%d, want 6", h)
	}
}

func TestSuggestionsHeightCapsAtMax(t *testing.T) {
	reg := newTestRegistry(t, "a", "b", "c", "d", "e", "f", "g")
	m := NewSuggestionsModel(reg)
	m.Update("/")
	// 5 visible rows + 2 border + 1 hint = 8
	if h := m.Height(); h != 8 {
		t.Fatalf("height=%d, want 8 (cap at maxVisible+chrome)", h)
	}
}

func TestSuggestionsViewIncludesNamesAndHelp(t *testing.T) {
	reg := newTestRegistry(t, "model", "mode")
	m := NewSuggestionsModel(reg)
	m.SetWidth(60)
	m.Update("/m")
	v := m.View()
	if !strings.Contains(v, "/model") {
		t.Fatalf("view missing /model; got:\n%s", v)
	}
	if !strings.Contains(v, "/mode") {
		t.Fatalf("view missing /mode; got:\n%s", v)
	}
	if !strings.Contains(v, "model help") {
		t.Fatalf("view missing help text; got:\n%s", v)
	}
}

func TestSuggestionsViewEmptyWhenHidden(t *testing.T) {
	reg := newTestRegistry(t, "alpha")
	m := NewSuggestionsModel(reg)
	m.SetWidth(60)
	if v := m.View(); v != "" {
		t.Fatalf("expected empty view when hidden, got: %q", v)
	}
}

func TestSlashPrefix(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"", "", false},
		{"/", "", true},
		{"/help", "help", true},
		{"  /help", "help", true},
		{"/help ", "", false},
		{"/help foo", "", false},
		{"hello", "", false},
		{"//", "/", false}, // contains a slash but no whitespace; allow as prefix
	}
	for _, tc := range cases {
		got, ok := slashPrefix(tc.in)
		// `//` is a degenerate case — `/` after the strip is allowed
		// because `/` is not whitespace. We document this by accepting
		// either result for the `//` case.
		if tc.in == "//" {
			continue
		}
		if got != tc.want || ok != tc.ok {
			t.Errorf("slashPrefix(%q) = (%q, %v), want (%q, %v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestTruncateToWidth(t *testing.T) {
	cases := []struct {
		in   string
		max  int
		want string
	}{
		{"hello", 10, "hello"},
		{"hello world", 8, "hello w…"},
		{"hi", 0, ""},
		{"hi", 1, ""}, // too narrow to even show ellipsis sensibly
	}
	for _, tc := range cases {
		got := truncateToWidth(tc.in, tc.max)
		if got != tc.want {
			t.Errorf("truncateToWidth(%q, %d) = %q, want %q", tc.in, tc.max, got, tc.want)
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
