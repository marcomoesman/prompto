package tui

import "charm.land/lipgloss/v2"

var (
	userStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true)
	assistantStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Bold(true)
	toolStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Bold(true)  // yellow (primary-agent ⚡ glyph + tool name)
	toolSubagentStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("75")).Bold(true)  // light sky blue (subagent ⚡ glyph + tool name); 12 looked purplish on many palettes
	systemHeaderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("14")).Bold(true) // bright cyan "System" block label
	statusStyle       = lipgloss.NewStyle().Background(lipgloss.Color("236")).Foreground(lipgloss.Color("252"))
	errorStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	dimStyle          = lipgloss.NewStyle().Foreground(lipgloss.Color("242"))
	systemStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("250"))           // brighter than dimStyle for readability
	approvalStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Bold(true) // bright white
	// Unified-diff row styling. Each row is three cells: line-number
	// gutter (right-aligned, dim), +/- gutter (single char), content
	// (full row width, background-filled for add/remove). Backgrounds
	// span to the viewport edge so the changed region reads as a
	// labeled band — no inline `+ ` prefix that collides with markdown
	// bullets in the content.
	diffLineNumStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("242")).Background(lipgloss.Color("236")).Width(5).Align(lipgloss.Right)
	diffGutterAddBG   = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Background(lipgloss.Color("22")).Bold(true)  // bright green +, dark green bg
	diffGutterRemBG   = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Background(lipgloss.Color("52")).Bold(true)   // bright red -, dark red bg
	diffGutterCtxBG   = lipgloss.NewStyle().Foreground(lipgloss.Color("242"))                                              // dim space for context
	diffAddRowBG      = lipgloss.NewStyle().Foreground(lipgloss.Color("254")).Background(lipgloss.Color("22"))             // near-white text on dark green
	diffRemRowBG      = lipgloss.NewStyle().Foreground(lipgloss.Color("254")).Background(lipgloss.Color("52"))             // near-white text on dark red
	diffCtxRowBG      = lipgloss.NewStyle().Foreground(lipgloss.Color("250"))                                              // brighter than dim for context-line readability

	// Working-state indicator (above input).
	indicatorStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("14")).Bold(true)  // spinner glyph (bright cyan)
	indicatorTextStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Bold(true) // label (bright + bold)
	indicatorAlertStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Bold(true)  // approval prompt

	// Sticky todo panel. Active-form header and todo body rows render in
	// a warm gold so the panel stands apart from the chat transcript and
	// the cyan/grey indicator row directly above it.
	todoActiveStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true) // header + ■ in-progress
	todoPendingStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))            // □ pending

	// Welcome banner.
	bannerArtStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true)
	bannerTipStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("242"))

	// Mode-aware input border colors. The input renders with a rounded
	// border whose foreground tracks the current permission mode so the
	// user has a constant peripheral cue when in elevated mode.
	modeDefaultBorderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("240")) // neutral grey
	modeAcceptBorderStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))  // yellow
	modeBypassBorderStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))   // red

	// Mode label rendered inline with the border-top characters.
	modeDefaultLabelStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	modeAcceptLabelStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Bold(true)
	modeBypassLabelStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true)

	// Help overlay panel.
	helpPanelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("14")).
			Padding(1, 2)
	helpHeaderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Bold(true)
	helpKeyStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("14"))
	helpDescStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))

	// Model picker overlay. Shares the help-overlay panel idiom but
	// uses a slightly different palette to signal a different mode
	// (interactive selection vs. read-only reference). Foregrounds
	// stay in the lighter end of the 256-color palette so the panel
	// is readable on any dark terminal — see the screenshot the user
	// flagged where colour 250 was too dim.
	pickerPanelStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("14")).
				Padding(1, 2)
	pickerHeaderStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Bold(true) // bright white
	pickerSelectedStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("14")).Bold(true) // bright cyan
	pickerSelectedBg      = lipgloss.NewStyle().Background(lipgloss.Color("237"))           // soft highlight band
	pickerNameStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("254"))           // near-white default
	pickerMetaStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))           // dim gray for provider/max_tokens
	pickerActiveMarkStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Bold(true) // green check on active model
	pickerHintStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("248"))           // footer hint text

	// Plan-approval overlay. Same rounded-border idiom
	// as the help / model-picker overlays; cyan border, bold white
	// header, dim footer, accent on the relative-path chip.
	planApprovalPanelStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("14")).
				Padding(1, 2)
	planApprovalHeaderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Bold(true)
	planApprovalPathStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Bold(true)
	planApprovalFooterStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("248"))

	// Slash-command suggestions popup. Shares the rounded-border idiom
	// of /help and the model picker; selected row uses the picker's
	// background band so navigation feels consistent.
	suggestionsPanelStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("14")).
				Padding(0, 1)
	suggestionsSelectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("14")).Bold(true)
	suggestionsHintStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("242")).Italic(true)

	// Thinking overlay.
	thinkingHeaderStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("13")).Bold(true) // magenta
	thinkingFooterStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("242"))
	thinkingDividerStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	thinkingEmptyStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("242")).Italic(true)
)
