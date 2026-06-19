package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// koraiBanner is the "KORAI" wordmark in the ANSI Shadow block style, shown on
// the empty start-up screen. One string per row so each can be tinted to make a
// vertical blue→purple gradient.
var koraiBanner = []string{
	`██╗  ██╗ ██████╗ ██████╗  █████╗ ██╗`,
	`██║ ██╔╝██╔═══██╗██╔══██╗██╔══██╗██║`,
	`█████╔╝ ██║   ██║██████╔╝███████║██║`,
	`██╔═██╗ ██║   ██║██╔══██╗██╔══██║██║`,
	`██║  ██╗╚██████╔╝██║  ██║██║  ██║██║`,
	`╚═╝  ╚═╝ ╚═════╝ ╚═╝  ╚═╝╚═╝  ╚═╝╚═╝`,
}

// bannerGradient tints the banner rows from blue at the top to purple at the
// bottom (one color per row).
var bannerGradient = []lipgloss.Color{
	"#6AA0FF", "#7E94FF", "#9288FB", "#A37CF7", "#B06BF3", "#BB9AF7",
}

// welcomeView renders the start-up screen: the gradient KORAI wordmark, a
// version/tagline line, and a short hint. It is shown while the transcript is
// empty and scrolls away once the conversation begins.
func (m Model) welcomeView() string {
	var b strings.Builder
	b.WriteByte('\n')
	for i, row := range koraiBanner {
		color := bannerGradient[i%len(bannerGradient)]
		b.WriteString("  " + lipgloss.NewStyle().Foreground(color).Bold(true).Render(row))
		b.WriteByte('\n')
	}
	b.WriteByte('\n')

	version := m.version
	if version == "" {
		version = "dev"
	}
	tagline := fmt.Sprintf("Korai Code CLI %s — AI coding agent on the Korai P2P network", version)
	hint := "Type a prompt to begin · / for commands · shift+tab for modes · ctrl+c to quit"

	b.WriteString("  " + m.styles.welcomeTitle.Render(tagline) + "\n")
	b.WriteString("  " + m.styles.welcomeHint.Render(hint))
	return b.String()
}
