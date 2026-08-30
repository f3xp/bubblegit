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
//
// ponytail: one timeout for every call, and commit is the one that will reach
// it first — a pre-commit hook running a test suite passes 30s routinely, and
// the commit git actually completed then reports as a deadline error. Give
// commit its own budget when that shows up, not before.
const gitTimeout = 30 * time.Second

// filesPaneWidth and logPaneWidth are the fraction of the terminal given to
// the left pane in each view. The log takes more: a row there carries a graph,
// a SHA, a date and a subject where a file row carries a status code and a
// path.
const (
	filesPaneWidth = 0.34
	logPaneWidth   = 0.5
)

var errEmptyMessage = errors.New("empty commit message")

// focus names the half of the screen taking keys, not a particular pane: each
// view puts a different pair of panes into the same two slots.
type focus int

const (
	focusLeft focus = iota
	focusRight
)

// view is which pair of panes is on screen.
//
// A new pair replaces the old rather than squeezing in beside it: layout()
// splits the terminal in two, and on an 80-column terminal a third
// simultaneous pane is three unreadable slivers rather than three panes.
type view int

const (
	viewStatus view = iota
	viewLog
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

// logMsg carries a page of the log. more distinguishes a continuation, which
// extends the list, from a first page, which replaces it.
type logMsg struct {
	gen     uint64
	commits []git.Commit
	more    bool
	err     error
}

// detailMsg carries one commit's patch, generation-tagged for the same reason
// diffMsg is.
type detailMsg struct {
	gen    uint64
	detail git.Detail
	err    error
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
	log    pane.Log
	detail pane.Detail

	focus focus
	view  view

	// diffGen is bumped on every diff request. A diffMsg whose gen is not the
	// current one is discarded.
	//
	// Without this, holding j in the file list issues a diff request per
	// keystroke and they return out of order, so the pane intermittently
	// settles on the wrong file's diff. It is cheap here and miserable to
	// retrofit once several panes load independently.
	diffGen uint64

	// detailGen is diffGen's counterpart in the log view, and exists for the
	// same reason: holding j in the log list issues a request per keystroke,
	// and without a generation the pane settles on whichever answer happens to
	// arrive last rather than on the commit under the cursor.
	detailGen uint64

	// logGen is bumped only when the log is reloaded from the top. A page that
	// belongs to a superseded list would otherwise be appended to the new one.
	logGen uint64

	// logPaging suppresses a second page request while one is outstanding.
	// Scrolling inside the margin fires on every keystroke otherwise, and the
	// pages arrive duplicated.
	logPaging bool

	// logLoaded is false until the log view has been opened. The log is not
	// read at startup: nothing shows it yet and it costs a process spawn.
	logLoaded bool

	width, height int
	// Pane dimensions computed by layout. A lipgloss border sizes itself to
	// its content, so a pane whose body is short ("loading…") would otherwise
	// draw a box narrower than its column.
	leftW, rightW, bodyH int
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
		detail: pane.NewDetail(),
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

// loadLog reads the first page, replacing whatever the pane holds.
func (m *Model) loadLog() tea.Cmd {
	m.logGen++
	m.logPaging = true
	gen, repo := m.logGen, m.repo
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
		defer cancel()
		commits, err := git.Log(ctx, repo, nil, git.LogPageSize)
		return logMsg{gen: gen, commits: commits, err: err}
	}
}

// loadMoreLog reads the page below what is already loaded.
//
// It resumes from a set of SHAs, never an offset: `--skip=N` makes git walk
// and discard N commits, so paging by offset gets slower the further the user
// scrolls. See TestPaginationStrategy, and git.Frontier for why the resumption
// point is a set.
func (m *Model) loadMoreLog() tea.Cmd {
	from := m.log.Frontier()
	if m.logPaging || m.log.AtEnd() || len(from) == 0 {
		return nil
	}
	m.logPaging = true
	gen, repo := m.logGen, m.repo
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
		defer cancel()
		commits, err := git.Log(ctx, repo, from, git.LogPageSize)
		return logMsg{gen: gen, commits: commits, more: true, err: err}
	}
}

// loadDetail requests the patch for the commit under the log cursor.
func (m *Model) loadDetail() tea.Cmd {
	c, ok := m.log.Selected()
	if !ok {
		m.detail.SetEmpty("no commit selected")
		return nil
	}

	m.detailGen++
	gen, repo, sha := m.detailGen, m.repo, c.SHA
	m.detail.SetLoading(sha)

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
		defer cancel()
		d, err := git.Show(ctx, repo, sha)
		return detailMsg{gen: gen, detail: d, err: err}
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
		// HEAD moved, so the log is stale. It is not re-read here: the log
		// view is not on screen (committing from it is refused), and reading
		// it now would spend a process on a pane nobody is looking at.
		m.logLoaded = false
		return m, tea.Batch(m.loadHead(), m.loadStatus())

	case amendMsg:
		cmd := m.commit.Open(msg.msg, true)
		if msg.err != nil {
			m.commit.SetError(msg.err)
		}
		m.layout()
		return m, cmd

	case logMsg:
		// Discard pages belonging to a superseded list, and leave the paging
		// flag alone: it belongs to the request that is still outstanding.
		if msg.gen != m.logGen {
			return m, nil
		}
		m.logPaging = false
		if msg.err != nil {
			// Shown in the detail pane rather than as the app-wide error,
			// which replaces the whole frame: a failed log read would then
			// leave no way back to the working tree, since the key that
			// switches views cannot clear it.
			m.detail.SetError(msg.err)
			// Stop asking. A page that keeps failing would otherwise respawn
			// git on every keystroke at the bottom of the list — the error
			// storm the applying flag exists to prevent on the write side.
			// Clearing logLoaded makes leaving the view and coming back retry.
			m.log.SetEnd(true)
			m.logLoaded = false
			return m, nil
		}
		if msg.more {
			m.log.Append(msg.commits)
		} else {
			m.logLoaded = true
			m.log.SetCommits(msg.commits)
		}
		// The history is exhausted once no loaded commit has an unloaded
		// parent left. A page that came back empty ends it too — without that
		// a walk which somehow returns nothing would be re-requested on every
		// keystroke, forever.
		m.log.SetEnd(len(msg.commits) == 0 || len(m.log.Frontier()) == 0)
		if msg.more {
			return m, nil
		}
		return m, m.loadDetail()

	case detailMsg:
		if msg.gen != m.detailGen {
			return m, nil
		}
		if msg.err != nil {
			m.detail.SetError(msg.err)
			return m, nil
		}
		m.detail.SetDetail(msg.detail)

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

	case keys.Matches(m.keys.StatusView, k):
		return m.setView(viewStatus)

	case keys.Matches(m.keys.LogView, k):
		return m.setView(viewLog)

	// Everything from here to the pane switch acts on the index or on HEAD,
	// which only means something while the status view is the one on screen.
	// Without the guard `space` in the log pane stages whatever the invisible
	// files pane happens to have selected — a write to the index with nothing
	// on screen to explain it — and `C` amends HEAD while the user is looking
	// at some entirely different commit.
	case keys.Matches(m.keys.ToggleStaged, k):
		if m.view != viewStatus {
			return m, nil
		}
		m.showStaged = !m.showStaged
		return m, m.loadDiff()

	// Both also refuse while a stage is in flight. applying is one flag for
	// every write, so opening the editor mid-apply lets a commit reach
	// doCommit() while the flag is still held by the apply — the confirm would
	// then do nothing, with nothing on screen to say why. Refusing at the door
	// also keeps the two arrival handlers from clearing each other's flag,
	// since only one write can ever be outstanding.
	case keys.Matches(m.keys.Commit, k):
		if m.view != viewStatus || m.applying {
			return m, nil
		}
		cmd := m.commit.Open("", false)
		m.layout()
		return m, cmd

	case keys.Matches(m.keys.Amend, k):
		if m.view != viewStatus || m.applying {
			return m, nil
		}
		return m, m.loadHeadMessage()

	case keys.Matches(m.keys.NextPane, k), keys.Matches(m.keys.PrevPane, k):
		if m.focus == focusLeft {
			m.focus = focusRight
		} else {
			m.focus = focusLeft
		}
		return m, nil
	}

	if m.view == viewLog {
		return m.handleLogKey(k)
	}
	if m.focus == focusRight {
		return m.handleDiffKey(k)
	}
	return m.handleFilesKey(k)
}

// setView switches which pair of panes is on screen, reading the log the first
// time it is asked for.
//
// The panes are re-sized because the two views split the terminal differently,
// and focus returns to the left pane: the right one holds a different document
// after the switch, and leaving focus on it would scroll a pane the user has
// not looked at yet.
func (m Model) setView(v view) (tea.Model, tea.Cmd) {
	if m.view == v {
		return m, nil
	}
	m.view, m.focus = v, focusLeft
	m.layout()
	if v == viewLog && !m.logLoaded {
		return m, m.loadLog()
	}
	return m, nil
}

// handleLogKey routes the log view.
//
// The staging bindings are simply absent here rather than guarded one by one:
// nothing in this view is stageable, so there is no key for it to mean.
func (m Model) handleLogKey(k string) (tea.Model, tea.Cmd) {
	if m.focus == focusRight {
		switch {
		case keys.Matches(m.keys.Down, k):
			m.detail.MoveBy(1)
		case keys.Matches(m.keys.Up, k):
			m.detail.MoveBy(-1)
		case keys.Matches(m.keys.PageDown, k):
			m.detail.HalfPageDown()
		case keys.Matches(m.keys.PageUp, k):
			m.detail.HalfPageUp()
		case keys.Matches(m.keys.Top, k):
			m.detail.Top()
		case keys.Matches(m.keys.Bottom, k):
			m.detail.Bottom()
		}
		return m, nil
	}

	before := m.log.SelectedSHA()
	switch {
	case keys.Matches(m.keys.Down, k):
		m.log.MoveBy(1)
	case keys.Matches(m.keys.Up, k):
		m.log.MoveBy(-1)
	case keys.Matches(m.keys.Bottom, k):
		m.log.Bottom()
	case keys.Matches(m.keys.Top, k):
		m.log.Top()
	case keys.Matches(m.keys.PageDown, k):
		m.log.HalfPageDown()
	case keys.Matches(m.keys.PageUp, k):
		m.log.HalfPageUp()
	default:
		return m, nil
	}

	var cmds []tea.Cmd
	// Fetch the next page a screenful before the cursor reaches the bottom, so
	// scrolling does not stall on a process spawn at the last row.
	if m.log.NearEnd() {
		if cmd := m.loadMoreLog(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	// Only when the selection actually moved: repeated j at the end of the
	// list must not spawn a git process per keystroke.
	if m.log.SelectedSHA() != before {
		cmds = append(cmds, m.loadDetail())
	}
	return m, tea.Batch(cmds...)
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
		return commitMsg{err: git.CreateCommit(ctx, repo, msg, amend)}
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
		m.leftW, m.rightW = m.width, m.width
	} else {
		m.leftW = int(float64(m.width) * m.paneSplit())
		if m.leftW < minPaneWidth {
			m.leftW = minPaneWidth
		}
		m.rightW = m.width - m.leftW
		if m.rightW < minPaneWidth {
			m.rightW = minPaneWidth
			m.leftW = m.width - m.rightW
		}
	}

	// Every pane is sized, not only the pair on screen: a view switch calls
	// layout() but a window resize while one view is hidden does not reach it
	// again, so a hidden pane sized once at zero would render as one column
	// the moment it appeared.
	m.files.SetSize(m.contentW(m.leftW), m.contentH())
	m.diff.SetSize(m.contentW(m.rightW), m.contentH())
	m.log.SetSize(m.contentW(m.leftW), m.contentH())
	m.detail.SetSize(m.contentW(m.rightW), m.contentH())
	// The editor replaces both panes rather than sitting beside them, so it is
	// the one thing here sized against the full width.
	m.commit.SetSize(m.contentW(m.width), m.contentH())
}

// paneSplit is the fraction of the terminal the left pane gets in this view.
func (m Model) paneSplit() float64 {
	if m.view == viewLog {
		return logPaneWidth
	}
	return filesPaneWidth
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

	var panes string
	switch {
	case m.commit.Active():
		panes = m.framed(m.commit.Title(), m.commit.View(), m.width, true)
	case m.narrow:
		// One pane at a time; tab swaps which one is visible.
		if m.focus == focusRight {
			panes = m.framed(m.rightTitle(), m.rightView(), m.rightW, true)
		} else {
			panes = m.framed(m.leftTitle(), m.leftView(), m.leftW, true)
		}
	default:
		panes = lipgloss.JoinHorizontal(lipgloss.Top,
			m.framed(m.leftTitle(), m.leftView(), m.leftW, m.focus == focusLeft),
			m.framed(m.rightTitle(), m.rightView(), m.rightW, m.focus == focusRight),
		)
	}

	frame := lipgloss.JoinVertical(lipgloss.Left,
		ansi.Truncate(m.header(), m.width, ""),
		panes,
	)
	// Last line of defence: never hand the terminal more rows than it has.
	return lipgloss.NewStyle().MaxHeight(m.height).MaxWidth(m.width).Render(frame)
}

// leftTitle and rightTitle name whichever pane the current view puts in each
// slot, so body() does not have to know which view it is drawing.
func (m Model) leftTitle() string {
	if m.view == viewLog {
		if n := m.log.Len(); n > 0 {
			// A trailing + means the count is what has been read so far, not
			// how many commits the repository has.
			more := ""
			if !m.log.AtEnd() {
				more = "+"
			}
			return "Log (" + strconv.Itoa(n) + more + ")"
		}
		return "Log"
	}
	if n := m.files.Len(); n > 0 {
		return "Files (" + strconv.Itoa(n) + ")"
	}
	return "Files"
}

func (m Model) rightTitle() string {
	if m.view == viewLog {
		if sha := m.detail.Title(); sha != "" {
			return "Commit — " + sha
		}
		return "Commit"
	}
	return diffTitle(m.diff.Title(), m.stagedSide())
}

func (m Model) leftView() string {
	if m.view == viewLog {
		return m.log.View()
	}
	return m.files.View()
}

func (m Model) rightView() string {
	if m.view == viewLog {
		return m.detail.View()
	}
	return m.diff.View()
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
