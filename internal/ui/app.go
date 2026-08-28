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
	// showStaged makes the diff pane show the index-vs-HEAD change instead of
	// the worktree-vs-index one. A partially staged file has both, and
	// without a toggle half its changes are unreachable.
	showStaged bool

	// narrow drops to a single pane when there is not room for two.
	narrow bool
	ready  bool
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

	// A file may be changed in the index, in the worktree, or both. The
	// worktree change is the more useful default — it is what the user is
	// about to stage — but a file with no worktree change has only a staged
	// one to show, and the toggle reaches the staged side of the rest.
	staged := m.showStaged || (sel.IsStaged() && !sel.IsUnstaged())
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

	case keys.Matches(m.keys.ToggleStaged, k):
		m.showStaged = !m.showStaged
		return m, m.loadDiff()

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
const minPaneWidth = 24

// Pane chrome: a border row above and below, plus a title row.
const (
	chromeH = 3
	chromeW = 2
	headerH = 1
)

// layout splits the terminal between the panes.
//
// Every clamp here has to agree with framed(), or the rendered frame comes
// out larger than the terminal and the display tears on resize. The rule is
// that filesW+diffW is exactly m.width, and header plus pane is exactly
// m.height.
func (m *Model) layout() {
	m.bodyH = m.height - headerH
	if m.bodyH < 1 {
		m.bodyH = 1
	}

	// Below two minimum-width panes, show one pane at full width rather than
	// two unreadable slivers.
	m.narrow = m.width < 2*minPaneWidth
	if m.narrow {
		m.filesW, m.diffW = m.width, m.width
	} else {
		m.filesW = int(float64(m.width) * filesPaneWidth)
		if m.filesW < minPaneWidth {
			m.filesW = minPaneWidth
		}
		m.diffW = m.width - m.filesW
		if m.diffW < minPaneWidth {
			m.diffW = minPaneWidth
			m.filesW = m.width - m.diffW
		}
	}

	m.files.SetSize(m.contentW(m.filesW), m.contentH())
	m.diff.SetSize(m.contentW(m.diffW), m.contentH())
}

// contentW is the usable width inside a pane's border.
func (m Model) contentW(paneW int) int {
	w := paneW - chromeW
	if !m.bordered() {
		w = paneW
	}
	if w < 1 {
		return 1
	}
	return w
}

// contentH is the usable height inside a pane's border.
func (m Model) contentH() int {
	h := m.bodyH - chromeH
	if !m.bordered() {
		h = m.bodyH
	}
	if h < 1 {
		return 1
	}
	return h
}

// bordered reports whether there is room to draw pane chrome at all. On a
// very short terminal the border would cost more rows than the content it
// frames, so it is dropped entirely.
func (m Model) bordered() bool { return m.bodyH >= chromeH+1 }

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

	var panes string
	if m.narrow {
		// One pane at a time; tab swaps which one is visible.
		if m.focus == focusDiff {
			panes = m.framed(diffTitle(m.diff.Title(), m.showStaged), m.diff.View(), m.diffW, true)
		} else {
			panes = m.framed(filesTitle, m.files.View(), m.filesW, true)
		}
	} else {
		panes = lipgloss.JoinHorizontal(lipgloss.Top,
			m.framed(filesTitle, m.files.View(), m.filesW, m.focus == focusFiles),
			m.framed(diffTitle(m.diff.Title(), m.showStaged), m.diff.View(), m.diffW, m.focus == focusDiff),
		)
	}

	frame := lipgloss.JoinVertical(lipgloss.Left,
		ansi.Truncate(m.header(), m.width, ""),
		panes,
	)
	// Last line of defence: never hand the terminal more rows than it has.
	return lipgloss.NewStyle().MaxHeight(m.height).MaxWidth(m.width).Render(frame)
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
	inner := m.contentW(width)
	height := m.contentH()

	sized := lipgloss.NewStyle().Width(inner).Height(height).MaxHeight(height).Render(body)
	if !m.bordered() {
		return sized
	}

	style := theme.Title
	if !focused {
		style = theme.TitleDim
	}
	head := lipgloss.NewStyle().Width(inner).Render(style.Render(ansi.Truncate(title, inner, "…")))

	return theme.Border(focused).Render(lipgloss.JoinVertical(lipgloss.Left, head, sized))
}

func diffTitle(path string, staged bool) string {
	if path == "" {
		return "Diff"
	}
	side := "worktree"
	if staged {
		side = "staged"
	}
	return "Diff — " + path + " (" + side + ")"
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}
