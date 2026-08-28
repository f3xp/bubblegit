// Package ui holds the Bubble Tea layer. It must never call exec directly —
// every git operation goes through internal/git, wrapped in a tea.Cmd, so
// nothing blocks Update.
package ui

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/f3xp/bubblegit/internal/git"
	"github.com/f3xp/bubblegit/internal/ui/keys"
)

// gitTimeout bounds any single git invocation so a wedged process (a hung
// credential helper, a network remote) cannot leave a pane loading forever.
const gitTimeout = 30 * time.Second

type headMsg struct {
	head git.Head
	err  error
}

type Model struct {
	root string
	repo *git.Runner
	keys keys.Map

	head git.Head
	err  error

	width, height int
}

func New(root string) Model {
	return Model{
		root: root,
		repo: git.New(root),
		keys: keys.Default(),
	}
}

func (m Model) Init() tea.Cmd { return m.loadHead() }

func (m Model) loadHead() tea.Cmd {
	repo := m.repo
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
		defer cancel()
		h, err := git.ReadHead(ctx, repo)
		return headMsg{head: h, err: err}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height

	case headMsg:
		m.head, m.err = msg.head, msg.err

	case tea.KeyPressMsg:
		if keys.Matches(m.keys.Quit, msg.String()) {
			return m, tea.Quit
		}
	}
	return m, nil
}

var (
	styleBranch = lipgloss.NewStyle().Bold(true)
	styleDim    = lipgloss.NewStyle().Faint(true)
	styleErr    = lipgloss.NewStyle().Foreground(lipgloss.Red)
)

func (m Model) View() tea.View {
	v := tea.NewView(m.body())
	v.AltScreen = true
	v.MouseMode = tea.MouseModeAllMotion
	// Ask for progressive keyboard enhancement where the terminal supports it,
	// and fall back silently where it does not.
	v.KeyboardEnhancements.ReportEventTypes = true
	return v
}

// Body is the rendered frame without the tea.View wrapper, exported so tests
// can assert on it directly.
func (m Model) Body() string { return m.body() }

func (m Model) body() string {
	if m.err != nil {
		return styleErr.Render("git error: " + m.err.Error())
	}

	ref := m.head.Branch
	switch {
	case m.head.Detached:
		ref = "detached at " + short(m.head.Commit)
	case m.head.Commit == "":
		ref = m.head.Branch + " (no commits yet)"
	case ref == "":
		ref = "loading…"
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		styleBranch.Render(ref),
		// The repo's name, not its absolute path: it is what the user
		// recognises, it survives a move, and it keeps golden files stable.
		styleDim.Render(filepath.Base(m.root)),
		"",
		styleDim.Render(fmt.Sprintf("%dx%d — press q to quit", m.width, m.height)),
	)
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}
