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

// splitDefaults is the fraction of the terminal the left pane starts with in
// each view. The left pane is the document in every view, so this is the
// document's share. The status view gives its diff the most: a file row is a
// status code and a path and is content with a third. A log row carries a
// graph, a SHA, a date and a subject, and a branch row a name, a tracking
// count, a date and a subject, so those two lists need half.
//
// It is where each view starts, not where it stays: the splitter moves them.
var splitDefaults = [...]float64{
	viewStatus:   0.66,
	viewLog:      0.5,
	viewBranches: 0.5,
}

var errEmptyMessage = errors.New("empty commit message")

// focus names the role of the pane taking keys — a view's list, or the
// document beside it — not a particular pane. Each view puts a different pair
// of panes into the two roles, and every view draws them the same way round:
// the document on the left, where it gets read, and the list on the right.
// Everything that routes a key or a click reasons in roles, so the same key
// means the same thing in every view.
type focus int

const (
	focusList focus = iota
	focusDoc
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
	viewBranches
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
//
// What it carries is the rendered diff rather than the parsed one. Rendering a
// diff costs tens of microseconds a line — hundreds of milliseconds for a large
// file — and doing it where this message is handled would spend all of it
// inside Update, where nothing else can happen. It is the second half of the
// same read, so it belongs on the same goroutine.
type diffMsg struct {
	gen     uint64
	content pane.DiffContent
	err     error
}

// logMsg carries a page of the log. more distinguishes a continuation, which
// extends the list, from a first page, which replaces it.
type logMsg struct {
	gen     uint64
	commits []git.Commit
	more    bool
	err     error
}

// checkoutMsg reports that a switch finished. Like stagedMsg it carries no
// payload: HEAD moved and the working tree was rewritten under it, so
// everything derived from either has to be re-read.
type checkoutMsg struct{ err error }

// branchesMsg carries the whole branch list. There is no paging: the count is
// bounded by how many branches a person keeps, not by the size of the history,
// and one read answers ahead/behind for all of them.
type branchesMsg struct {
	branches []git.Branch
	err      error
}

// detailMsg carries one commit's patch, rendered and generation-tagged for the
// same reasons diffMsg is — and a commit carries every changed file's patch, so
// there is more of it to render.
type detailMsg struct {
	gen     uint64
	content pane.DetailContent
	err     error
}

type Model struct {
	root string
	repo *git.Runner
	keys keys.Map

	head git.Head
	err  error

	files    pane.Files
	diff     pane.Diff
	commit   pane.Commit
	log      pane.Log
	detail   pane.Detail
	branches pane.Branches

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

	// branchesLoaded is logLoaded's counterpart, and is cleared for the same
	// reasons: a commit moves the current branch's tip and its ahead count, so
	// the list on screen describes a branch that has moved on.
	branchesLoaded bool

	// splits is the fraction of the terminal the left pane gets, per view,
	// seeded from splitDefaults and moved by the splitter. Per view because
	// the three pairs of panes want different proportions of the same
	// terminal; an array rather than a map because Model is copied on every
	// Update and a map would be shared by every copy of it.
	splits [len(splitDefaults)]float64

	// dragging is set while the splitter is being dragged, and dragGrab is
	// where in it the pointer took hold — the splitter is two adjacent border
	// columns, and without the offset grabbing the right-hand one would jump
	// the boundary a column before it started to track.
	dragging bool
	dragGrab int

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

	// showHelp draws the keybinding popup over the panes. It is a mode like
	// the editor is a mode — while it is up it owns the keyboard — but a
	// read-only one, so any key dismisses it rather than only an escape.
	showHelp bool
}

func New(root string) Model {
	return Model{
		root:   root,
		repo:   git.New(root),
		keys:   keys.Default(),
		diff:   pane.NewDiff(),
		commit: pane.NewCommit(),
		detail: pane.NewDetail(),
		splits: splitDefaults,
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
		return diffMsg{gen: gen, content: pane.RenderDiff(d), err: err}
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
// The guards come before Frontier(), which walks every commit already loaded to
// find the tips to resume from. Every j at the bottom of the list asks for the
// next page, so with a request already in flight that walk was being redone per
// keystroke for an answer that was thrown away.
func (m *Model) loadMoreLog() tea.Cmd {
	if m.logPaging || m.log.AtEnd() {
		return nil
	}
	from := m.log.Frontier()
	if len(from) == 0 {
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

// loadBranches reads the whole branch list.
func (m *Model) loadBranches() tea.Cmd {
	repo := m.repo
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
		defer cancel()
		b, err := git.Branches(ctx, repo)
		return branchesMsg{branches: b, err: err}
	}
}

// loadDetail requests the patch for the commit under the log cursor.
func (m *Model) loadDetail() tea.Cmd {
	c, ok := m.log.Selected()
	if !ok {
		m.detail.SetEmpty("no commit selected")
		return nil
	}
	return m.loadDetailFor(c.SHA)
}

// loadBranchTip requests the patch for the tip of the selected branch. The
// detail pane is shared with the log view, which is why every entry into
// either view re-issues its own load — see setView.
func (m *Model) loadBranchTip() tea.Cmd {
	b, ok := m.branches.Selected()
	if !ok {
		m.detail.SetEmpty("no branch selected")
		return nil
	}
	return m.loadDetailFor(b.SHA)
}

func (m *Model) loadDetailFor(sha string) tea.Cmd {
	m.detailGen++
	gen, repo := m.detailGen, m.repo
	m.detail.SetLoading(sha)

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
		defer cancel()
		d, err := git.Show(ctx, repo, sha)
		return detailMsg{gen: gen, content: pane.RenderDetail(d), err: err}
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

	case checkoutMsg:
		m.applying = false
		if msg.err != nil {
			// Shown, not swallowed: git refuses a switch that would overwrite
			// a local change, and a key that silently does nothing is
			// indistinguishable from a broken one.
			m.detail.SetError(msg.err)
			return m, nil
		}
		// HEAD, the working tree and every tracking count moved at once. The
		// branch list is on screen so it is re-read now rather than marked
		// stale; the log is not, so it is only marked.
		m.logLoaded = false
		return m, tea.Batch(m.loadHead(), m.loadStatus(), m.loadBranches())

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
		// HEAD moved, so the log and the branch list are both stale — the
		// current branch has a new tip and one more commit ahead of its
		// upstream. Neither is re-read here: committing is only possible from
		// the status view, so neither pane is on screen, and reading now would
		// spend a process on a pane nobody is looking at.
		m.logLoaded, m.branchesLoaded = false, false
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

	case branchesMsg:
		if msg.err != nil {
			// Shown in the detail pane rather than as the app-wide error, for
			// the same reason a failed log read is: the app-wide error
			// replaces the whole frame, and no key clears it, so a failed
			// branch read would leave no way back to the working tree.
			m.detail.SetError(msg.err)
			// Leave branchesLoaded false so re-entering the view retries.
			return m, nil
		}
		m.branchesLoaded = true
		m.branches.SetBranches(msg.branches)
		if m.view != viewBranches {
			// The list arrived for a view the user has already left. Keep it —
			// it is current — but do not pull the detail pane out from under
			// whatever is on screen now.
			return m, nil
		}
		return m, m.loadBranchTip()

	case detailMsg:
		if msg.gen != m.detailGen {
			return m, nil
		}
		if msg.err != nil {
			m.detail.SetError(msg.err)
			return m, nil
		}
		m.detail.SetDetail(msg.content)

	case diffMsg:
		// Discard answers to superseded questions.
		if msg.gen != m.diffGen {
			return m, nil
		}
		if msg.err != nil {
			m.diff.SetError(msg.err)
			return m, nil
		}
		m.diff.SetDiff(msg.content)

	case tea.KeyPressMsg:
		return m.handleKey(msg)

	// One case for every mouse event, matched on the interface rather than on
	// each concrete type: a click, a release, a drag and a wheel notch all
	// carry the same Mouse payload, and handleMouse switches on the ones it
	// acts on. Listing the types here instead would mean a new one silently
	// falling through to the default.
	case tea.MouseMsg:
		return m.handleMouse(msg)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := msg.String()

	// The help popup is read-only, so it does not need its own bindings: the
	// next key puts it away, whatever it was. Swallowing that key rather than
	// acting on it too keeps `j` from scrolling a list the popup is covering.
	// ctrl+c is the exception, for the same reason the editor makes it one.
	if m.showHelp {
		if k == "ctrl+c" {
			return m, tea.Quit
		}
		m.showHelp = false
		return m, nil
	}

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

	// Below the editor guard, not above it: `?` is an ordinary character in a
	// commit message.
	case keys.Matches(m.keys.Help, k):
		m.showHelp = true
		return m, nil

	case keys.Matches(m.keys.StatusView, k):
		return m.setView(viewStatus)

	case keys.Matches(m.keys.LogView, k):
		return m.setView(viewLog)

	case keys.Matches(m.keys.BranchView, k):
		return m.setView(viewBranches)

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

	// The splitter, from the keyboard. Left and Right have been in the keymap
	// and bound to nothing since M0, and moving the boundary between the panes
	// is what a horizontal motion means in a two-pane layout.
	//
	// One column a press. The terminal repeats a held key, and a coarser step
	// would be unable to land on a particular column — which is the whole
	// reason to reach for the keyboard rather than the pointer.
	//
	// Unlike the drag, these work in the unbordered layout: a very short
	// terminal has no border column to take hold of, but its panes still split
	// the width. setSplit refuses when there is genuinely no boundary, which
	// is the narrow layout's single pane.
	//
	// ponytail: this takes the only natural binding for scrolling the diff
	// sideways, which the viewport supports and nothing has ever reached — the
	// pane turns wrapping off precisely so a long line scrolls rather than
	// reflows, and then no key scrolls it. Give it H and L if a clipped line
	// turns out to matter; widening the pane is the answer to it today, which
	// is what these keys now do.
	case keys.Matches(m.keys.Left, k):
		m.setSplit(m.leftW - 1)
		return m, nil

	case keys.Matches(m.keys.Right, k):
		m.setSplit(m.leftW + 1)
		return m, nil

	case keys.Matches(m.keys.NextPane, k), keys.Matches(m.keys.PrevPane, k):
		if m.focus == focusList {
			m.focus = focusDoc
		} else {
			m.focus = focusList
		}
		return m, nil
	}

	switch m.view {
	case viewLog:
		return m.handleLogKey(k)
	case viewBranches:
		return m.handleBranchKey(k)
	}
	if m.focus == focusDoc {
		return m.handleDiffKey(k)
	}
	return m.handleFilesKey(k)
}

// setView switches which pair of panes is on screen, reading the log the first
// time it is asked for.
//
// The panes are re-sized because the two views split the terminal differently,
// and focus returns to the list: the document pane holds a different document
// after the switch, and leaving focus on it would scroll a pane the user has
// not looked at yet.
func (m Model) setView(v view) (tea.Model, tea.Cmd) {
	if m.view == v {
		return m, nil
	}
	m.view, m.focus = v, focusList
	m.layout()

	// The log and branch views share one detail pane, so entering either has
	// to re-issue its own load even when the list is already there: without
	// it, coming back to the log shows the branch tip it was left holding
	// while the log cursor sits somewhere else entirely. It costs one process
	// on a keypress, not one per keystroke.
	switch v {
	case viewLog:
		if !m.logLoaded {
			return m, m.loadLog()
		}
		return m, m.loadDetail()
	case viewBranches:
		if !m.branchesLoaded {
			return m, m.loadBranches()
		}
		return m, m.loadBranchTip()
	}
	return m, nil
}

// handleLogKey routes the log view.
//
// The staging bindings are simply absent here rather than guarded one by one:
// nothing in this view is stageable, so there is no key for it to mean.
func (m Model) handleLogKey(k string) (tea.Model, tea.Cmd) {
	if m.focus == focusDoc {
		return m.handleDetailKey(k)
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

	return m, m.afterLogMove(before)
}

// afterLogMove issues the reads a moved log cursor implies. It is shared by
// the key path and the mouse path: a click and a wheel notch move the same
// cursor, and a copy of this bookkeeping per input device is a copy that will
// drift.
//
// before is the SHA the cursor was on. Passing it in rather than reading it
// here is what makes the check possible at all — by the time this runs the
// cursor has already moved.
func (m *Model) afterLogMove(before string) tea.Cmd {
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
	return tea.Batch(cmds...)
}

// handleDetailKey scrolls the commit pane. It is shared by the log and branch
// views, which put the same pane in the same slot.
func (m Model) handleDetailKey(k string) (tea.Model, tea.Cmd) {
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

// handleBranchKey routes the branch view.
//
// Checking out is the one write here. The staging and commit bindings are
// absent for the same reason they are absent from the log view: there is
// nothing in this view for them to mean.
func (m Model) handleBranchKey(k string) (tea.Model, tea.Cmd) {
	if m.focus == focusDoc {
		return m.handleDetailKey(k)
	}

	before := m.branches.SelectedName()
	switch {
	case keys.Matches(m.keys.Branch, k):
		return m, m.doCheckout()
	case keys.Matches(m.keys.Down, k):
		m.branches.MoveBy(1)
	case keys.Matches(m.keys.Up, k):
		m.branches.MoveBy(-1)
	case keys.Matches(m.keys.Bottom, k):
		m.branches.Bottom()
	case keys.Matches(m.keys.Top, k):
		m.branches.Top()
	case keys.Matches(m.keys.PageDown, k):
		m.branches.HalfPageDown()
	case keys.Matches(m.keys.PageUp, k):
		m.branches.HalfPageUp()
	default:
		return m, nil
	}

	return m, m.afterBranchMove(before)
}

// afterBranchMove issues the tip re-read a moved branch cursor implies. Shared
// with the mouse path, for the same reason afterLogMove is.
func (m *Model) afterBranchMove(before string) tea.Cmd {
	// Only when the selection actually moved: repeated j at the end of the
	// list must not spawn a git process per keystroke.
	if m.branches.SelectedName() == before {
		return nil
	}
	return m.loadBranchTip()
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

// doCheckout switches to the branch under the cursor.
//
// There is no confirmation step. A switch is reversible, and the one way it
// loses work — overwriting a local change — is the case git refuses on its
// own, reporting it as an error the pane shows.
func (m *Model) doCheckout() tea.Cmd {
	sel, ok := m.branches.Selected()
	if !ok || m.applying {
		return nil
	}
	if sel.Current {
		// git would exit 0 with "Already on ...", spending a process to
		// rewrite the pane with what it already says.
		return nil
	}

	m.applying = true
	repo, name := m.repo, sel.Name
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
		defer cancel()
		return checkoutMsg{err: git.Checkout(ctx, repo, name)}
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
	sel, _ := m.files.Selected()
	before := sel.Path

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

	return m, m.afterFilesMove(before)
}

// afterFilesMove issues the diff re-read a moved file cursor implies. Shared
// with the mouse path, for the same reason afterLogMove is.
func (m *Model) afterFilesMove(before string) tea.Cmd {
	// Only re-fetch when the selection actually moved; repeated j at the end
	// of the list must not spawn a git process per keystroke, and neither must
	// a click on the row that is already selected.
	if after, _ := m.files.Selected(); after.Path == before {
		return nil
	}
	return m.loadDiff()
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

// wheelStep is how many rows one wheel notch moves. Three is what a terminal
// scrollback does, and a notch that moved one row would need a spin per screen.
const wheelStep = 3

// handleMouse routes clicks, drags and the wheel.
//
// A click takes focus and puts the pane's cursor on the row under the pointer,
// or takes hold of the splitter when that is what it landed on; the wheel
// scrolls the pane under the pointer without taking focus, which is what a
// wheel does everywhere else.
func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	e := msg.Mouse()

	// Release and motion are answered ahead of every other guard, because a
	// drag has to end wherever it ends. A release outside the frame, or one
	// that arrives while the editor is open, would otherwise leave the flag
	// set — and under cell motion the terminal only reports motion while a
	// button is held, so the next press-and-move anywhere on screen would
	// resize the splitter.
	switch msg.(type) {
	case tea.MouseReleaseMsg:
		m.dragging = false
		return m, nil
	case tea.MouseMotionMsg:
		// Motion with no grab is a mouse being moved across a pane, which
		// means nothing here. The editor covers both panes, so a drag behind
		// it moves a boundary that is not on screen.
		if m.dragging && !m.commit.Active() {
			m.setSplit(e.X + m.dragGrab)
		}
		return m, nil
	}

	// The message editor replaces both panes rather than sitting beside them,
	// so while it is open there is nothing under the pointer to click. It owns
	// the mouse for the same reason it owns every key. The help popup covers
	// the middle of the frame, including the splitter, so it does the same.
	if m.commit.Active() || m.showHelp {
		return m, nil
	}

	hit, ok := m.hitTest(e.X, e.Y)
	if !ok {
		return m, nil
	}

	switch msg.(type) {
	case tea.MouseClickMsg:
		// Left only. The middle button pastes in most terminals and the right
		// one opens their own menu, and taking either would be taking a
		// gesture the user means for the terminal.
		if e.Button != tea.MouseLeft {
			return m, nil
		}
		if hit.splitter {
			// Where in the splitter the pointer took hold, so the boundary
			// tracks the pointer from the first reported motion rather than
			// snapping a column one way or the other.
			m.dragging, m.dragGrab = true, m.leftW-e.X
			return m, nil
		}
		// Focus moves even when the click landed on the pane's border or
		// title rather than on a row: the pane is what was clicked, and a
		// click that visibly does nothing reads as a dead frame.
		m.focus = hit.pane
		if hit.row < 0 {
			return m, nil
		}
		return m.mouseMove(hit.pane, hit.row, true)

	case tea.MouseWheelMsg:
		switch e.Button {
		case tea.MouseWheelUp:
			return m.mouseMove(hit.pane, -wheelStep, false)
		case tea.MouseWheelDown:
			return m.mouseMove(hit.pane, wheelStep, false)
		}
	}
	return m, nil
}

// mouseTarget is the shape of the panes a mouse can move: the three lists and
// the diff. The commit pane is deliberately absent — it has no cursor, so it
// has no row for a click to land on.
type mouseTarget interface {
	SelectRow(row int)
	MoveBy(n int)
}

// mouseMove moves the cursor of the pane in role p and issues whatever re-read
// the move implies. n is a body row when abs is set — a click — and a number
// of rows to travel when it is not — a wheel notch.
//
// One function rather than one per event, because the part that will drift is
// the mapping from a role to a pane: two views put the same commit pane in the
// document role and the third puts a diff there, and that belongs in one
// place. The re-reads go through the same after*Move helpers the key path
// uses, so a click pages the log and reloads the detail exactly as j does.
func (m Model) mouseMove(p focus, n int, abs bool) (tea.Model, tea.Cmd) {
	move := func(t mouseTarget) {
		if abs {
			t.SelectRow(n)
		} else {
			t.MoveBy(n)
		}
	}

	if p == focusDoc {
		if m.view != viewStatus {
			// The commit pane scrolls as a document and has no cursor, so a
			// click on one of its rows means nothing beyond the focus it has
			// already taken.
			if !abs {
				m.detail.MoveBy(n)
			}
			return m, nil
		}
		move(&m.diff)
		return m, nil
	}

	switch m.view {
	case viewLog:
		before := m.log.SelectedSHA()
		move(&m.log)
		return m, m.afterLogMove(before)
	case viewBranches:
		before := m.branches.SelectedName()
		move(&m.branches)
		return m, m.afterBranchMove(before)
	}
	sel, _ := m.files.Selected()
	move(&m.files)
	return m, m.afterFilesMove(sel.Path)
}

// mouseHit is where a mouse event landed: which pane, by role, and which row of
// that pane's body. row is -1 when the point is on the pane's chrome — its
// border or its title — rather than on a content row, and splitter marks the
// boundary between the two panes, which is a handle rather than either of them.
type mouseHit struct {
	pane     focus
	row      int
	splitter bool
}

// hitTest maps a terminal cell to the pane under it. ok is false for the app
// header and for anything outside the frame.
//
// This is layout() read backwards, and the two have to agree: framed() draws a
// pane as a border row, a title row, contentH() body rows and a closing border
// row, and the left pane owns columns 0..leftW-1 with the right one taking the
// rest. The left pane is the document and the right one the list, in every
// view. Every branch layout() has, this one has too — no border on a terminal
// too short for one, a single full-width pane when there is no room for two —
// which is why it is pinned by a table test rather than trusted.
func (m Model) hitTest(x, y int) (mouseHit, bool) {
	if !m.ready || x < 0 || x >= m.width || y < headerH || y >= m.height {
		return mouseHit{}, false
	}

	hit := mouseHit{pane: focusList, row: -1}

	// The splitter is the two adjacent border columns where the panes meet,
	// read from the absolute column before the right pane's offset comes off
	// x: afterwards both of them are indexed exactly like the frame's own
	// outer borders, and column 0 and the last column are not handles.
	//
	// There is nothing to grab in the unbordered layout, where the panes are
	// flush and no column belongs to the boundary, nor in the narrow one,
	// where there is only one pane. The keys have no such limit — see
	// setSplit's callers.
	hit.splitter = m.bordered() && !m.narrow && (x == m.leftW-1 || x == m.leftW)

	paneW := m.leftW
	switch {
	case m.narrow:
		// One pane fills the width, and the focused one is the one on screen.
		hit.pane = m.focus
	case x >= m.leftW:
		paneW = m.rightW
		x -= m.leftW
	default:
		hit.pane = focusDoc
	}

	top := headerH
	if m.bordered() {
		// A side border is the pane, not a row of it.
		if x == 0 || x == paneW-1 {
			return hit, true
		}
		top += 2 // the top border and the title
	}
	if y < top || y >= top+m.contentH() {
		return hit, true
	}
	hit.row = y - top
	return hit, true
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
		m.leftW = int(float64(m.width) * m.splits[m.view])
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
	// the moment it appeared. Lists go on the right, documents on the left.
	m.files.SetSize(m.contentW(m.rightW), m.contentH())
	m.log.SetSize(m.contentW(m.rightW), m.contentH())
	m.branches.SetSize(m.contentW(m.rightW), m.contentH())
	m.diff.SetSize(m.contentW(m.leftW), m.contentH())
	m.detail.SetSize(m.contentW(m.leftW), m.contentH())
	// The editor replaces both panes rather than sitting beside them, so it is
	// the one thing here sized against the full width.
	m.commit.SetSize(m.contentW(m.width), m.contentH())
}

// setSplit moves the boundary between the panes of the current view so the
// left one is leftW columns wide, and re-lays out on the answer.
//
// The split is stored as a fraction rather than a column count, so a later
// resize keeps the proportion the user chose. A stored column count would
// survive a resize as a proportion nobody asked for — a 40/40 split dragged on
// an 80-column terminal is half of it, not 40 columns of a 200-column one.
//
// The clamp is here as well as in layout() because what is stored is what a
// resize is later read against: layout() clamping a 2% fraction up to
// minPaneWidth would leave the pane at the minimum on this terminal and at two
// columns on a much wider one.
func (m *Model) setSplit(leftW int) {
	// Below two minimum-width panes there is one pane, and no boundary
	// between panes to move.
	if m.narrow || m.width <= 0 {
		return
	}
	if leftW < minPaneWidth {
		leftW = minPaneWidth
	}
	if maxW := m.width - minPaneWidth; leftW > maxW {
		leftW = maxW
	}
	// The middle of the column, not its left edge. layout() truncates the
	// fraction back into a column count, and float64(leftW)/float64(width)
	// does not always survive the round trip — 29 columns of 100 comes back as
	// 28 — so a stepped resize would stall on those columns with the key doing
	// nothing at all. Half a column of slack is below the resolution of
	// anything that reads this and puts the truncation in the middle of the
	// column rather than on its boundary.
	m.splits[m.view] = (float64(leftW) + 0.5) / float64(m.width)
	m.layout()
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
	// Cell motion, not all motion: the app acts on clicks, drags and the
	// wheel, and asking for pointer movement with no button held would deliver
	// an event per cell the mouse crosses — a full Update and re-render each,
	// for a message the app discards. It is also the better-supported of the
	// two modes.
	//
	// Either mode costs the terminal's own text selection, which the app can
	// neither read nor replace. Most terminals still select when shift is
	// held, which is the escape hatch this trades against.
	v.MouseMode = tea.MouseModeCellMotion
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
		if m.focus == focusDoc {
			panes = m.framed(m.docTitle(), m.docView(), m.width, true)
		} else {
			panes = m.framed(m.listTitle(), m.listView(), m.width, true)
		}
	default:
		panes = lipgloss.JoinHorizontal(lipgloss.Top,
			m.framed(m.docTitle(), m.docView(), m.leftW, m.focus == focusDoc),
			m.framed(m.listTitle(), m.listView(), m.rightW, m.focus == focusList),
		)
	}

	frame := lipgloss.JoinVertical(lipgloss.Left,
		ansi.Truncate(m.header(), m.width, ""),
		panes,
	)
	// Last line of defence: never hand the terminal more rows than it has.
	base := lipgloss.NewStyle().MaxHeight(m.height).MaxWidth(m.width).Render(frame)
	if m.showHelp {
		return m.withHelp(base)
	}
	return base
}

// withHelp composes the keybinding popup over an already-rendered frame.
//
// A canvas rather than a pane swap: the popup is a popup, and the point of one
// is that the panes stay visible around it. Note that Canvas.Render trims
// trailing whitespace, which is why this is reached only when the popup is up.
func (m Model) withHelp(base string) string {
	popup := helpView(m.keys, m.width-4, m.height-4)
	if popup == "" {
		return base
	}
	x := max((m.width-lipgloss.Width(popup))/2, 0)
	y := max((m.height-lipgloss.Height(popup))/2, 0)
	return lipgloss.NewCanvas(m.width, m.height).Compose(
		lipgloss.NewCompositor(
			lipgloss.NewLayer(base),
			lipgloss.NewLayer(popup).X(x).Y(y).Z(1),
		),
	).Render()
}

// listTitle and docTitle name whichever pane the current view puts in each
// role, so body() does not have to know which view it is drawing.
func (m Model) listTitle() string {
	if m.view == viewBranches {
		if n := m.branches.Len(); n > 0 {
			return "Branches (" + strconv.Itoa(n) + ")"
		}
		return "Branches"
	}
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

func (m Model) docTitle() string {
	// The log and branch views share the commit pane, so they share its title.
	if m.view != viewStatus {
		if sha := m.detail.Title(); sha != "" {
			return "Commit — " + sha
		}
		return "Commit"
	}
	return diffTitle(m.diff.Title(), m.stagedSide())
}

func (m Model) listView() string {
	switch m.view {
	case viewLog:
		return m.log.View()
	case viewBranches:
		return m.branches.View()
	}
	return m.files.View()
}

func (m Model) docView() string {
	if m.view != viewStatus {
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
	return theme.Title.Render(filepath.Base(m.root)) + theme.TitleDim.Render("  ⎇ "+ref)
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
