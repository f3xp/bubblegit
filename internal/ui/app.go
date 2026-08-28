// Package ui holds the Bubble Tea layer. It must never call exec directly —
// every git operation goes through internal/git, wrapped in a tea.Cmd, so
// nothing blocks Update.
package ui

import (
	"context"
	"path/filepath"
	"strconv"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/f3xp/bubblegit/internal/git"
	"github.com/f3xp/bubblegit/internal/ui/keys"
	"github.com/f3xp/bubblegit/internal/ui/pane"
	"github.com/f3xp/bubblegit/internal/ui/theme"
)

// gitTimeout bounds any single git invocation so a wedged process (a hung
// credential helper, a network remote) cannot leave a pane loading forever.
const gitTimeout = 30 * time.Second

// filesPaneWidth is the fraction of the terminal given to the file list.
const filesPaneWidth = 0.34

type focus int

const (
	focusFiles focus = iota
	focusDiff
)

type headMsg struct {
	head git.Head
	err  error
}

type statusMsg struct {
	files []git.FileStatus
	err   error
}

// diffMsg carries the generation of the request that produced it. Anything
// older than the pane's current generation is a stale answer to a question
// the user has already moved on from.
type diffMsg struct {
	gen  uint64
	diff git.FileDiff
	err  error
}

type Model struct {
	root string
	repo *git.Runner
	keys keys.Map

	head git.Head
	err  error

	files pane.Files
	diff  pane.Diff
	focus focus

	// diffGen is bumped on every diff request. A diffMsg whose gen is not the
	// current one is discarded.
	//
	// Without this, holding j in the file list issues a diff request per
	// keystroke and they return out of order, so the pane intermittently
	// settles on the wrong file's diff. It is cheap here and miserable to
	// retrofit once several panes load independently.
	diffGen uint64

	width, height int
	// Pane dimensions computed by layout. A lipgloss border sizes itself to
	// its content, so a pane whose body is short ("loading…") would otherwise
	// draw a box narrower than its column.
	filesW, diffW, bodyH int
	ready                bool
}

func New(root string) Model {
	return Model{
		root: root,
		repo: git.New(root),
		keys: keys.Default(),
		diff: pane.NewDiff(),
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.loadHead(), m.loadStatus())
}

func (m Model) loadHead() tea.Cmd {
	repo := m.repo
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
		defer cancel()
		h, err := git.ReadHead(ctx, repo)
		return headMsg{head: h, err: err}
	}
}

func (m Model) loadStatus() tea.Cmd {
	repo := m.repo
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
		defer cancel()
		files, err := git.Status(ctx, repo)
		return statusMsg{files: files, err: err}
	}
}

// loadDiff requests the diff for the current selection, tagged with the
// generation it belongs to.
func (m *Model) loadDiff() tea.Cmd {
	sel, ok := m.files.Selected()
	if !ok {
		m.diff.SetEmpty("no file selected")
		return nil
	}

	m.diffGen++
	gen := m.diffGen
	repo := m.repo

	// A file may be changed in the index, in the worktree, or both. Showing
	// the worktree change is the more useful default: it is what the user is
	// about to stage.
	staged := sel.IsStaged() && !sel.IsUnstaged()
	untracked := sel.IsUntracked()

	m.diff.SetLoading(sel.Path)

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
		defer cancel()
		d, err := git.DiffFile(ctx, repo, sel.Path, staged, untracked)
		return diffMsg{gen: gen, diff: d, err: err}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		m.ready = true

	case headMsg:
		m.head, m.err = msg.head, msg.err

	case statusMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.files.SetFiles(msg.files)
		return m, m.loadDiff()

	case diffMsg:
		// Discard answers to superseded questions.
		if msg.gen != m.diffGen {
			return m, nil
		}
		if msg.err != nil {
			m.diff.SetError(msg.err)
			return m, nil
		}
		m.diff.SetDiff(msg.diff)

	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := msg.String()

	switch {
	case keys.Matches(m.keys.Quit, k):
		return m, tea.Quit

	case keys.Matches(m.keys.NextPane, k), keys.Matches(m.keys.PrevPane, k):
		if m.focus == focusFiles {
			m.focus = focusDiff
		} else {
			m.focus = focusFiles
		}
		return m, nil
	}

	if m.focus == focusDiff {
		return m.handleDiffKey(k)
	}
	return m.handleFilesKey(k)
}

func (m Model) handleFilesKey(k string) (tea.Model, tea.Cmd) {
	before, _ := m.files.Selected()

	switch {
	case keys.Matches(m.keys.Down, k):
		m.files.MoveBy(1)
	case keys.Matches(m.keys.Up, k):
		m.files.MoveBy(-1)
	case keys.Matches(m.keys.Bottom, k):
		m.files.Bottom()
	case keys.Matches(m.keys.Top, k):
		m.files.Top()
	case keys.Matches(m.keys.PageDown, k):
		m.files.MoveBy(m.files.Len() / 2)
	case keys.Matches(m.keys.PageUp, k):
		m.files.MoveBy(-m.files.Len() / 2)
	default:
		return m, nil
	}

	// Only re-fetch when the selection actually moved; repeated j at the end
	// of the list must not spawn a git process per keystroke.
	if after, _ := m.files.Selected(); after.Path == before.Path {
		return m, nil
	}
	return m, m.loadDiff()
}

func (m Model) handleDiffKey(k string) (tea.Model, tea.Cmd) {
	switch {
	case keys.Matches(m.keys.Down, k):
		m.diff.ScrollBy(1)
	case keys.Matches(m.keys.Up, k):
		m.diff.ScrollBy(-1)
	case keys.Matches(m.keys.PageDown, k):
		m.diff.HalfPageDown()
	case keys.Matches(m.keys.PageUp, k):
		m.diff.HalfPageUp()
	case keys.Matches(m.keys.Top, k):
		m.diff.Top()
	case keys.Matches(m.keys.Bottom, k):
		m.diff.Bottom()
	}
	return m, nil
}

// minPaneWidth keeps both panes legible rather than letting one collapse.
const minPaneWidth = 20

// layout splits the terminal between the two panes. Border and title take
// three rows and two columns per pane.
func (m *Model) layout() {
	const (
		chromeH = 3 // top border, title, bottom border
		chromeW = 2 // left and right border
		headerH = 1 // the branch line above both panes
	)

	m.bodyH = m.height - headerH
	if m.bodyH < chromeH+1 {
		m.bodyH = chromeH + 1
	}

	m.filesW = int(float64(m.width) * filesPaneWidth)
	if m.filesW < minPaneWidth {
		m.filesW = minPaneWidth
	}
	m.diffW = m.width - m.filesW
	if m.diffW < minPaneWidth {
		m.diffW = minPaneWidth
	}

	m.files.SetSize(m.filesW-chromeW, m.bodyH-chromeH)
	m.diff.SetSize(m.diffW-chromeW, m.bodyH-chromeH)
}

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
		return theme.Err.Render("git error: " + m.err.Error())
	}
	if !m.ready {
		return theme.Dim.Render("loading…")
	}

	filesTitle := "Files"
	if n := m.files.Len(); n > 0 {
		filesTitle += " (" + strconv.Itoa(n) + ")"
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		m.header(),
		lipgloss.JoinHorizontal(lipgloss.Top,
			m.framed(filesTitle, m.files.View(), m.filesW, m.focus == focusFiles),
			m.framed(diffTitle(m.diff.Title()), m.diff.View(), m.diffW, m.focus == focusDiff),
		),
	)
}

func (m Model) header() string {
	ref := m.head.Branch
	switch {
	case m.head.Detached:
		ref = "detached at " + short(m.head.Commit)
	case m.head.Commit == "":
		ref = m.head.Branch + " (no commits yet)"
	case ref == "":
		ref = "loading…"
	}
	return theme.Title.Render(ref) + theme.TitleDim.Render("  "+filepath.Base(m.root))
}

// framed draws a pane at an exact size.
//
// The width and height are set on the inner content rather than left to the
// border: lipgloss sizes a border to whatever it wraps, so a pane showing
// "loading…" would draw a box a few cells wide next to a full-height
// neighbour.
func (m Model) framed(title, body string, width int, focused bool) string {
	inner := width - 2 // left and right border
	if inner < 1 {
		inner = 1
	}
	height := m.bodyH - 3 // top border, title row, bottom border
	if height < 1 {
		height = 1
	}

	head := theme.Title.Render(ansi.Truncate(title, inner, "…"))
	if !focused {
		head = theme.TitleDim.Render(ansi.Truncate(title, inner, "…"))
	}

	content := lipgloss.JoinVertical(lipgloss.Left,
		lipgloss.NewStyle().Width(inner).Render(head),
		lipgloss.NewStyle().Width(inner).Height(height).Render(body),
	)
	return theme.Border(focused).Render(content)
}

func diffTitle(path string) string {
	if path == "" {
		return "Diff"
	}
	return "Diff — " + path
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}
