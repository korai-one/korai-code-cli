package tui

import "github.com/charmbracelet/lipgloss"

// styles holds the lipgloss styles for transcript rendering. Kept in one place
// so the look is consistent and View stays pure.
type styles struct {
	user       lipgloss.Style
	assistant  lipgloss.Style
	tool       lipgloss.Style
	toolResult lipgloss.Style
	errorText  lipgloss.Style
	info       lipgloss.Style
	status     lipgloss.Style
	menuItem   lipgloss.Style
	menuSel    lipgloss.Style
	dialog     lipgloss.Style
}

func newStyles() styles {
	return styles{
		user:       lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12")),
		assistant:  lipgloss.NewStyle().Foreground(lipgloss.Color("15")),
		tool:       lipgloss.NewStyle().Foreground(lipgloss.Color("8")),
		toolResult: lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Faint(true),
		errorText:  lipgloss.NewStyle().Foreground(lipgloss.Color("9")),
		info:       lipgloss.NewStyle().Foreground(lipgloss.Color("11")),
		status:     lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Faint(true),
		menuItem:   lipgloss.NewStyle().Foreground(lipgloss.Color("7")),
		menuSel:    lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true),
		dialog: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("11")).
			Padding(0, 1),
	}
}
