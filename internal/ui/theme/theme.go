// Package theme holds the shared lipgloss styles.
package theme

import "charm.land/lipgloss/v2"

var (
	Add     = lipgloss.NewStyle().Foreground(lipgloss.Color("#a6e3a1"))
	Del     = lipgloss.NewStyle().Foreground(lipgloss.Color("#f38ba8"))
	Context = lipgloss.NewStyle().Foreground(lipgloss.Color("#cdd6f4"))
	Meta    = lipgloss.NewStyle().Foreground(lipgloss.Color("#89b4fa"))
	Dim     = lipgloss.NewStyle().Foreground(lipgloss.Color("#6c7086"))
	Err     = lipgloss.NewStyle().Foreground(lipgloss.Color("#f38ba8")).Bold(true)

	// Staged and Unstaged colour the two halves of git's XY status code.
	Staged   = lipgloss.NewStyle().Foreground(lipgloss.Color("#a6e3a1"))
	Unstaged = lipgloss.NewStyle().Foreground(lipgloss.Color("#f9e2af"))

	Selected = lipgloss.NewStyle().Background(lipgloss.Color("#313244"))

	// Cursor marks the diff row that staging acts on. It colours a glyph in the
	// viewport's gutter rather than the row itself: a background cannot survive
	// the resets the syntax highlighter leaves inside the line, and it has to
	// out-read the file list's Selected, which stays visible beside it.
	Cursor   = lipgloss.NewStyle().Foreground(lipgloss.Color("#89b4fa"))
	Title    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#cdd6f4"))
	TitleDim = lipgloss.NewStyle().Foreground(lipgloss.Color("#6c7086"))
)

// Border returns the frame for a pane. The focused pane is the only one with
// a bright border, so focus is readable without colour vision.
func Border(focused bool) lipgloss.Style {
	s := lipgloss.NewStyle().Border(lipgloss.RoundedBorder())
	if focused {
		return s.BorderForeground(lipgloss.Color("#89b4fa"))
	}
	return s.BorderForeground(lipgloss.Color("#45475a"))
}
