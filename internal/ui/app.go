// Package ui holds the Bubble Tea layer. It must never call exec directly —
// every git operation goes through internal/git, wrapped in a tea.Cmd, so
// nothing blocks Update.
package ui

import (
	"context"
	"errors"
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

var errEmptyMessage = errors.New("empty commit message")

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

// stagedMsg reports that a stage or un-stage finished. It carries no payload:
// the index moved, so every derived view has to be re-read from git rather
// than patched locally.
type stagedMsg struct{ err error }

// commitMsg reports that a commit or amend finished. Like stagedMsg it carries
// no payload: HEAD moved and the index emptied, so everything derived from
// either has to be re-read.
type commitMsg struct{ err error }

// amendMsg carries HEAD's message back, to pre-fill a reword. Reading it is a
// git call like any other, so the editor opens on the answer rather than
// blocking Update on the question.
type amendMsg struct {
	msg string
	err error
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

	files  pane.Files
	diff   pane.Diff
	commit pane.Commit
	focus  focus

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

	// applying suppresses writes while one is in flight — a stage, a commit or
	// an amend. A commit is the one that needs it most: signing and a
	// pre-commit hook can take seconds, which is ample time to press the key
	// again and spawn a second `git commit`.
	//
	// diffGen does not cover this: it discards stale answers, and the problem
	// here is a stale question. Holding the stage key builds every patch from
	// the diff on screen, but the first apply has already moved the index out
	// from under the rest, so they fail one after another and a correct action
	// reads as an error storm.
	applying bool

	// narrow drops to a single pane when there is not room for two.
	narrow bool
	ready  bool
}

func New(root string) Model {
	return Model{
		root:   root,
		repo:   git.New(root),
		keys:   keys.Default(),
		diff:   pane.NewDiff(),
		commit: pane.NewCommit(),
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

	staged := m.stagedSide()
	untracked := sel.IsUntracked()

	m.diff.SetLoading(sel.Path)

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
		defer cancel()
		d, err := git.DiffFile(ctx, repo, sel.Path, staged, untracked)
		return diffMsg{gen: gen, diff: d, err: err}
	}
}

// stagedSide reports whether the diff pane is showing the index-vs-HEAD change
// rather than the worktree-vs-index one.
//
// A file may be changed in the index, in the worktree, or both. The worktree
// change is the more useful default — it is what the user is about to stage —
// but a file with no worktree change has only a staged one to show, and the
// toggle reaches the staged side of the rest.
//
// Every caller has to agree: this decides which diff is fetched, what the
// pane title claims, and — since staging the side already on screen is the
// only thing the key can sensibly mean — whether the stage key stages or
// un-stages.
func (m Model) stagedSide() bool {
	if m.showStaged {
		return true
	}
	sel, ok := m.files.Selected()
	return ok && sel.IsStaged() && !sel.IsUnstaged()
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

	case stagedMsg:
		m.applying = false
		if msg.err != nil {
			// index.lock contention and a patch that will not apply both land
			// here, and both are things the user has to see: silently doing
			// nothing on a stage key is indistinguishable from a broken key.
			m.diff.SetError(msg.err)
			return m, nil
		}
		return m, m.loadStatus()

	case commitMsg:
		m.applying = false
		if msg.err != nil {
			// The editor stays open and keeps the message. A rejected commit
			// is usually a pre-commit hook or a failed signature, and throwing
			// away what the user just wrote is not a reasonable response to
			// either.
			m.commit.SetError(msg.err)
			return m, nil
		}
		m.commit.Close()
		m.layout()
		return m, tea.Batch(m.loadHead(), m.loadStatus())

	case amendMsg:
		cmd := m.commit.Open(msg.msg, true)
		if msg.err != nil {
			m.commit.SetError(msg.err)
		}
		m.layout()
		return m, cmd

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

	// The message editor is a mode, and while it is open it owns every key:
	// `q` types a q, `space` types a space, `t` types a t. This is the one
	// place the flat key dispatch has to branch before it reaches the
	// bindings, because those bindings are single letters.
	if m.commit.Active() {
		return m.handleCommitKey(msg, k)
	}

	switch {
	case keys.Matches(m.keys.Quit, k):
		return m, tea.Quit

	case keys.Matches(m.keys.ToggleStaged, k):
		m.showStaged = !m.showStaged
		return m, m.loadDiff()

	case keys.Matches(m.keys.Commit, k):
		cmd := m.commit.Open("", false)
		m.layout()
		return m, cmd

	case keys.Matches(m.keys.Amend, k):
		return m, m.loadHeadMessage()

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

// handleCommitKey routes to the editor, letting only its own bindings through.
func (m Model) handleCommitKey(msg tea.KeyPressMsg, k string) (tea.Model, tea.Cmd) {
	switch {
	case keys.Matches(m.keys.Cancel, k):
		m.commit.Close()
		m.layout()
		return m, nil

	// ctrl+c by name rather than through the Quit binding, which also holds
	// `q`. A terminal program that swallows ctrl+c has trapped the user, and
	// discarding an unfinished commit message is what ctrl+c means anyway.
	case k == "ctrl+c":
		return m, tea.Quit

	case keys.Matches(m.keys.Confirm, k):
		return m, m.doCommit()
	}
	return m, m.commit.Update(msg)
}

// doCommit records the index, or replaces HEAD when the editor was opened to
// amend. The editor stays open until git accepts it.
func (m *Model) doCommit() tea.Cmd {
	if m.applying {
		return nil
	}
	if m.commit.Empty() {
		// git refuses an empty message too, but only after running the
		// pre-commit hook, which may take seconds and have side effects.
		m.commit.SetError(errEmptyMessage)
		return nil
	}

	m.applying = true
	repo, msg, amend := m.repo, m.commit.Value(), m.commit.Amending()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
		defer cancel()
		return commitMsg{err: git.Commit(ctx, repo, msg, amend)}
	}
}

// loadHeadMessage fetches HEAD's message and opens the editor on the answer.
func (m Model) loadHeadMessage() tea.Cmd {
	repo := m.repo
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
		defer cancel()
		msg, err := git.HeadMessage(ctx, repo)
		return amendMsg{msg: msg, err: err}
	}
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
	case keys.Matches(m.keys.Stage, k), keys.Matches(m.keys.StageHunk, k):
		// In the file list both keys mean the whole file: there is no hunk to
		// single out from here.
		return m, m.stageFile()
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
		m.diff.MoveBy(1)
	case keys.Matches(m.keys.Up, k):
		m.diff.MoveBy(-1)
	case keys.Matches(m.keys.PageDown, k):
		m.diff.HalfPageDown()
	case keys.Matches(m.keys.PageUp, k):
		m.diff.HalfPageUp()
	case keys.Matches(m.keys.Top, k):
		m.diff.Top()
	case keys.Matches(m.keys.Bottom, k):
		m.diff.Bottom()
	case keys.Matches(m.keys.Stage, k):
		return m, m.stageSelection(false)
	case keys.Matches(m.keys.StageHunk, k):
		return m, m.stageSelection(true)
	}
	return m, nil
}

// stageSelection stages the line or hunk under the diff cursor, or un-stages
// it when the pane is showing the staged side.
func (m *Model) stageSelection(wholeHunk bool) tea.Cmd {
	sel, ok := m.files.Selected()
	if !ok || m.applying || !m.diff.Ready() {
		// Nothing on screen is a diff of the selected file yet, so there is no
		// honest answer to what this key means. Doing nothing beats guessing.
		return nil
	}
	reverse := m.stagedSide()

	fd, hunk, line, ok := m.diff.Selection()
	// ok is false for a binary file, which has no cursor to read.
	if !ok || wholeFileOnly(sel, reverse) {
		return m.stageFile()
	}
	if wholeHunk {
		line = -1
	}

	patch, err := git.Patch(fd, hunk, line, reverse)
	switch {
	case err != nil:
		m.diff.SetError(err)
		return nil
	case patch == nil:
		// The cursor is on a context line. Nothing to stage, and not a
		// mistake, so no error either.
		return nil
	}

	m.applying = true
	repo := m.repo
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
		defer cancel()
		return stagedMsg{err: git.ApplyCached(ctx, repo, patch, reverse)}
	}
}

// stageFile stages or un-stages the selected path outright.
func (m *Model) stageFile() tea.Cmd {
	sel, ok := m.files.Selected()
	if !ok || m.applying {
		return nil
	}

	m.applying = true
	repo, path, reverse := m.repo, sel.Path, m.stagedSide()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
		defer cancel()
		if reverse {
			return stagedMsg{err: git.UnstageFile(ctx, repo, path)}
		}
		return stagedMsg{err: git.StageFile(ctx, repo, path)}
	}
}

// wholeFileOnly reports whether a change can only be staged in one piece.
//
// A deletion cannot: `git apply --cached` on an all-deletion patch writes the
// empty blob into the index and leaves the entry behind, so git never learns
// the file is gone.
//
// A conflict cannot either, for a blunter reason. An unmerged path diffs as a
// combined diff — "@@@" headers, two columns of +/- prefixes — which the
// parser reads as an ordinary one and turns into a patch describing lines that
// do not exist. `git add` on a conflicted path is the operation that means
// something there anyway: it resolves it with what is in the worktree.
func wholeFileOnly(f git.FileStatus, staged bool) bool {
	if f.IsUnmerged() {
		return true
	}
	if staged {
		return f.Staged == 'D'
	}
	return f.Unstaged == 'D'
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
	// The editor replaces both panes rather than sitting beside them, so it is
	// the one thing here sized against the full width.
	m.commit.SetSize(m.contentW(m.width), m.contentH())
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
	if m.commit.Active() {
		panes = m.framed(m.commit.Title(), m.commit.View(), m.width, true)
	} else if m.narrow {
		// One pane at a time; tab swaps which one is visible.
		if m.focus == focusDiff {
			panes = m.framed(diffTitle(m.diff.Title(), m.stagedSide()), m.diff.View(), m.diffW, true)
		} else {
			panes = m.framed(filesTitle, m.files.View(), m.filesW, true)
		}
	} else {
		panes = lipgloss.JoinHorizontal(lipgloss.Top,
			m.framed(filesTitle, m.files.View(), m.filesW, m.focus == focusFiles),
			m.framed(diffTitle(m.diff.Title(), m.stagedSide()), m.diff.View(), m.diffW, m.focus == focusDiff),
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
