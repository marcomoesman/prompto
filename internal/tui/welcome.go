package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// promptoArt is the big-block ASCII banner shown on launch. Width: 47
// columns. Below 50 cols we fall back to a compact text-only banner.
const promptoArt = ` ____  ____   ___  __  __ ____ _____ ___
|  _ \|  _ \ / _ \|  \/  |  _ \_   _/ _ \
| |_) | |_) | | | | |\/| | |_) || || | | |
|  __/|  _ <| |_| | |  | |  __/ | || |_| |
|_|   |_| \_\\___/|_|  |_|_|    |_| \___/ `

// minBannerWidth is the threshold below which we fall back to a plain
// text banner. Equal to the ASCII art width (47) plus a small breathing
// margin.
const minBannerWidth = 50

// WelcomeData carries the banner's runtime fields.
type WelcomeData struct {
	Version string // e.g. "v0.5"
	Agent   string // "build" / "plan"
	Model   string // e.g. "qwopus-36-27b"
}

// renderWelcome builds the welcome banner rendered when the conversation
// is empty. Returns an empty string when the caller should hide it.
func renderWelcome(width int, data WelcomeData) string {
	if width <= 0 {
		return ""
	}
	if width < minBannerWidth {
		return renderWelcomeCompact(width, data)
	}

	// ASCII art block, centered horizontally.
	art := bannerArtStyle.Render(promptoArt)
	artBlock := lipgloss.PlaceHorizontal(width, lipgloss.Center, art)

	tips := []string{
		formatTipLine([]string{
			"prompto " + data.Version,
			"agent: " + data.Agent,
			"model: " + data.Model,
		}),
		formatTipLine([]string{
			"/help for commands",
			"? for keybinds",
			"ESC cancels turn",
		}),
		formatTipLine([]string{
			"tab to switch input",
			"ctrl+y mode",
			"ctrl+c quits",
		}),
	}
	var tipBlock []string
	for _, t := range tips {
		tipBlock = append(tipBlock, lipgloss.PlaceHorizontal(width, lipgloss.Center, bannerTipStyle.Render(t)))
	}

	return strings.Join(append([]string{"", artBlock, ""}, append(tipBlock, "")...), "\n")
}

// renderWelcomeCompact is the narrow-terminal fallback. No ASCII art —
// just two short lines so the user still gets oriented at < 50 cols.
func renderWelcomeCompact(width int, data WelcomeData) string {
	header := bannerArtStyle.Render("prompto " + data.Version)
	sub := bannerTipStyle.Render(data.Agent + " · " + data.Model)
	return strings.Join([]string{
		"",
		lipgloss.PlaceHorizontal(width, lipgloss.Center, header),
		lipgloss.PlaceHorizontal(width, lipgloss.Center, sub),
		lipgloss.PlaceHorizontal(width, lipgloss.Center, bannerTipStyle.Render("/help · ESC cancel · ctrl+c quit")),
		"",
	}, "\n")
}

// welcomeHeight returns the row count the welcome banner occupies. Used
// by the layout math in app.go to shrink the chat viewport accordingly.
func welcomeHeight(width int) int {
	if width <= 0 {
		return 0
	}
	if width < minBannerWidth {
		// blank, header, sub, tips, blank → 5
		return 5
	}
	// blank, art (5 lines), blank, 3 tips, blank → 11
	return 11
}

// formatTipLine joins tokens with ` · ` separators in dim style.
func formatTipLine(tokens []string) string {
	return strings.Join(tokens, "  ·  ")
}
