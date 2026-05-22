package tui

import (
	"fmt"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/marcomoesman/prompto/internal/command"
)

// renderHelpOverlay returns a centered help panel sized to fit width × height.
// The panel lists every registered slash command grouped by Kind plus a
// short keybinding cheatsheet. ESC dismisses; the caller draws this in
// place of the chat region while m.helpVisible is true.
func renderHelpOverlay(width, height int, reg *command.Registry) string {
	if width <= 0 || height <= 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString(helpHeaderStyle.Render("prompto · help"))
	b.WriteString("\n\n")

	cmds := reg.All()
	local, expanding := splitByKind(cmds)
	if len(local) > 0 {
		b.WriteString(helpHeaderStyle.Render("commands"))
		b.WriteByte('\n')
		writeCmdRows(&b, local)
	}
	if len(expanding) > 0 {
		b.WriteByte('\n')
		b.WriteString(helpHeaderStyle.Render("expanding (sent to model)"))
		b.WriteByte('\n')
		writeCmdRows(&b, expanding)
	}

	b.WriteByte('\n')
	b.WriteString(helpHeaderStyle.Render("keys"))
	b.WriteByte('\n')
	for _, row := range keyRows() {
		fmt.Fprintf(&b, "  %s  %s\n",
			helpKeyStyle.Render(padRight(row.key, 12)),
			helpDescStyle.Render(row.desc))
	}
	b.WriteByte('\n')
	b.WriteString(helpDescStyle.Render("ESC closes this panel"))

	panel := helpPanelStyle.Render(b.String())
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, panel)
}

// splitByKind partitions registered commands into (local, expanding) buckets,
// each sorted by name. The /help command itself is dropped from the listing
// — it's already implicit by virtue of the panel being open.
func splitByKind(cmds []command.Command) (local, expanding []command.Command) {
	for _, c := range cmds {
		if c.Name() == "help" {
			continue
		}
		switch c.Kind() {
		case command.KindLocal:
			local = append(local, c)
		case command.KindExpanding:
			expanding = append(expanding, c)
		}
	}
	sort.Slice(local, func(i, j int) bool { return local[i].Name() < local[j].Name() })
	sort.Slice(expanding, func(i, j int) bool { return expanding[i].Name() < expanding[j].Name() })
	return local, expanding
}

func writeCmdRows(b *strings.Builder, cmds []command.Command) {
	for _, c := range cmds {
		name := "/" + c.Name()
		fmt.Fprintf(b, "  %s  %s\n",
			helpKeyStyle.Render(padRight(name, 12)),
			helpDescStyle.Render(c.Help()))
	}
}

type keyRow struct {
	key  string
	desc string
}

func keyRows() []keyRow {
	return []keyRow{
		{"Tab", "cycle agent (build ↔ plan)"},
		{"Ctrl+Y", "cycle permission mode"},
		{"Ctrl+O", "toggle extended-thinking overlay"},
		{"Ctrl+T", "toggle mouse capture (off = native text select)"},
		{"PgUp/PgDn", "scroll chat history"},
		{"Shift+drag", "select text (Option+drag in iTerm2) without toggling"},
		{"ESC", "cancel current turn / close overlay"},
		{"Ctrl+C", "interrupt or quit (twice)"},
		{"Ctrl+D", "quit"},
		{"Shift+Enter", "newline in input"},
	}
}

func padRight(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(s))
}
