package tui

import "charm.land/lipgloss/v2"

// The palette is a blue→purple scheme (Tokyo Night-ish): blue for the user and
// accents, purple for tool activity and the active selection, soft lavender for
// assistant text. Hex colors degrade gracefully on 256-color terminals.
var (
	colBlue     = lipgloss.Color("#7AA2F7") // primary accent (user, prompts)
	colPurple   = lipgloss.Color("#BB9AF7") // tool activity, selection
	colLavender = lipgloss.Color("#C0CAF5") // assistant body text
	colCyan     = lipgloss.Color("#7DCFFF") // info / status accents
	colMuted    = lipgloss.Color("#565F89") // dim metadata
	colRed      = lipgloss.Color("#F7768E") // errors
)

// styles holds the lipgloss styles for transcript rendering. Kept in one place
// so the look is consistent and View stays pure.
type styles struct {
	user         lipgloss.Style
	assistant    lipgloss.Style
	tool         lipgloss.Style
	toolResult   lipgloss.Style
	errorText    lipgloss.Style
	info         lipgloss.Style
	status       lipgloss.Style
	menuItem     lipgloss.Style
	menuSel      lipgloss.Style
	welcomeTitle lipgloss.Style
	welcomeHint  lipgloss.Style
	dialog       lipgloss.Style
}

func newStyles() styles {
	return styles{
		user:         lipgloss.NewStyle().Bold(true).Foreground(colBlue),
		assistant:    lipgloss.NewStyle().Foreground(colLavender),
		tool:         lipgloss.NewStyle().Foreground(colPurple),
		toolResult:   lipgloss.NewStyle().Foreground(colMuted),
		errorText:    lipgloss.NewStyle().Foreground(colRed),
		info:         lipgloss.NewStyle().Foreground(colCyan),
		status:       lipgloss.NewStyle().Foreground(colMuted).Faint(true),
		menuItem:     lipgloss.NewStyle().Foreground(colLavender),
		menuSel:      lipgloss.NewStyle().Foreground(colPurple).Bold(true),
		welcomeTitle: lipgloss.NewStyle().Foreground(colBlue).Bold(true),
		welcomeHint:  lipgloss.NewStyle().Foreground(colMuted),
		dialog: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colPurple).
			Padding(0, 1),
	}
}
