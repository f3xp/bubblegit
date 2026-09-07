// Package ui holds the Bubble Tea layer. It must never call exec directly —
// every git operation goes through internal/git, wrapped in a tea.Cmd, so
// nothing blocks Update.
package ui

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/f3xp/bubblegit/internal/git"
	"github.com/f3xp/bubblegit/internal/ui/keys"
	"github.com/f3xp/bubblegit/internal/ui/pane"
	"github.com/f3xp/bubblegit/internal/ui/theme"
	"github.com/f3xp/bubblegit/internal/watch"
)

// gitTimeout bounds any single git invocation so a wedged process (a hung
// credential helper, a network remote) cannot leave a pane loading forever.
//
// ponytail: one timeout for every call, and commit is the one that will reach
// it first — a pre-commit hook running a test suite passes 30s routinely, and
// the commit git actually completed then reports as a deadline error. Give
// commit its own budget when that shows up, not before.
const gitTimeout = 30 * time.Second

// refreshInterval is how often the visible view is re-read unprompted, so a
// commit made in another terminal shows up without a keypress. lazygit's
// default; a file the watcher already covers shows up sooner.
const refreshInterval = 10 * time.Second

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
	viewStash:    0.5,
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
	viewStash
)

// tickMsg is the periodic refresh firing. Its handler is the one place the
// timer is re-armed, so there is never more than one outstanding.
type tickMsg struct{}

// watchMsg reports that a watched worktree file changed. Like tickMsg it
// carries nothing: the answer to "something changed" is a status re-read.
type watchMsg struct{}

type headMsg struct {
	head git.Head
	err  error
}

type statusMsg struct {
	// stats is the per-path line count of every uncommitted change, for the
	// file rows and, summed, the files pane title.
	stats map[string]git.LineStat
	files []git.FileStatus
	err   error
}

// stagedMsg reports that a write to the index, the working tree or the stash
// finished. It carries no payload: something moved, so every derived view has
// to be re-read from git rather than patched locally.
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

type stashesMsg struct {
	stashes []git.Stash
	err     error
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

	// watch covers the files the status list names, so an editor save is seen
	// at once rather than at the next tick. nil when the watcher could not be
	// started, in which case the tick alone carries the refresh.
	watch *watch.Watcher

	head git.Head
	err  error

	files pane.Files
	// added and deleted are the line totals behind the files pane title.
	added, deleted int
	diff           pane.Diff
	commit         pane.Commit
	log            pane.Log
	detail         pane.Detail
	branches       pane.Branches
	stashes        pane.Stashes

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

	// logRef is the ref the log view walks from, empty for HEAD. It is how a
	// branch's history is reached without a third pane: the log view is
	// parameterised by a revision, the way tig's main view is, rather than
	// duplicated per branch. Paging is unaffected — a resumed walk names the
	// SHAs it stopped at, which already say which history they belong to.
	//
	// Set by `enter` in the branch view and cleared by the log view's own key,
	// which is the only way back to HEAD: there is no view stack to pop, since
	// `q` quits. That is safe only because listTitle names the ref — a log
	// that silently changed which history it showed would read as a bug.
	logRef string

	// branchesLoaded is logLoaded's counterpart, and is cleared for the same
	// reasons: a commit moves the current branch's tip and its ahead count, so
	// the list on screen describes a branch that has moved on.
	branchesLoaded bool

	// stashesLoaded is the same flag for the stash list, cleared by every
	// write: a stash op is the obvious one, but a stage or a discard changes
	// what the next stash would hold too.
	stashesLoaded bool

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

	// confirm is the question a destructive key has asked and not yet had
	// answered. While it is set the key bar shows the question and the next
	// key is the answer: y goes ahead, anything else is no.
	confirm confirm
}

// confirm is a pending yes/no. do runs on yes; a nil do is no question.
//
// A line in the key bar rather than a dialog: the bar is where the eye already
// goes to find out what a key does, and a question there is one more row of
// keys with two answers.
type confirm struct {
	prompt string
	do     func(*Model) tea.Cmd
}

func (c confirm) pending() bool { return c.do != nil }

func New(root string) Model {
	// A failed watcher is not fatal: the periodic refresh still runs, only
	// slower to notice a save.
	w, _ := watch.New()
	return Model{
		root:   root,
		repo:   git.New(root),
		keys:   keys.Default(),
		watch:  w,
		diff:   pane.NewDiff(),
		commit: pane.NewCommit(),
		detail: pane.NewDetail(),
		splits: splitDefaults,
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.loadHead(), m.loadStatus(), tick(), m.waitWatch())
}

func tick() tea.Cmd {
	return tea.Tick(refreshInterval, func(time.Time) tea.Msg { return tickMsg{} })
}

// waitWatch blocks a command goroutine on the watcher's next signal. It is
// re-issued from the watchMsg handler, the way tick is from tickMsg.
func (m Model) waitWatch() tea.Cmd {
	if m.watch == nil {
		return nil
	}
	ch := m.watch.Events()
	return func() tea.Msg {
		<-ch
		return watchMsg{}
	}
}

// refresh re-reads HEAD and the list on screen. The other lists are marked
// stale rather than re-read, as after a commit: a process spent on a pane
// nobody is looking at is waste. lazygit re-reads every panel, but every one of
// its panels is on screen at once.
func (m *Model) refresh() tea.Cmd {
	if m.applying {
		// The write's own arrival handler re-reads when it lands.
		return nil
	}
	// The visible view's arrival handler sets its own flag back.
	m.logLoaded, m.branchesLoaded, m.stashesLoaded = false, false, false
	cmds := []tea.Cmd{m.loadHead()}
	switch m.view {
	case viewStatus:
		cmds = append(cmds, m.loadStatus())
	case viewLog:
		cmds = append(cmds, m.loadLog())
	case viewBranches:
		cmds = append(cmds, m.loadBranches())
	case viewStash:
		cmds = append(cmds, m.loadStashes())
	}
	return tea.Batch(cmds...)
}

// watchPaths is what the watcher should cover after a status read: every path
// git named, absolute. Worktree files only, never anything under .git —
// `status` writes the index back (see git.Runner), so watching it would make
// every refresh trigger the next.
func (m Model) watchPaths(files []git.FileStatus) []string {
	paths := make([]string, 0, len(files))
	for _, f := range files {
		if f.Kind == git.KindIgnored {
			continue
		}
		paths = append(paths, filepath.Join(m.root, f.Path))
	}
	return paths
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
		if err != nil {
			return statusMsg{err: err}
		}
		// A second process per status read. The status pane is the only one
		// that shows the counts, and reading them alongside the list keeps
		// the two from disagreeing on screen.
		stats, err := git.Numstat(ctx, repo)
		return statusMsg{files: files, stats: stats, err: err}
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

	m.diff.SetLoading(sel.Path, staged)

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

	// nil rather than an empty string when unscoped: git.Log reads no tips as
	// HEAD, and an empty argument is a revision git cannot resolve.
	var from []string
	if m.logRef != "" {
		from = []string{m.logRef}
	}

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
		defer cancel()
		commits, err := git.Log(ctx, repo, from, git.LogPageSize)
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

// loadStashes reads the whole stash list.
func (m *Model) loadStashes() tea.Cmd {
	repo := m.repo
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
		defer cancel()
		s, err := git.Stashes(ctx, repo)
		return stashesMsg{stashes: s, err: err}
	}
}

// loadStashTip requests the patch for the selected stash. A stash is a commit
// whose first parent is the HEAD it was taken on, so git.Show renders it as
// the change it holds; the untracked files of a `-u` stash sit on a third
// parent and are not part of that patch.
func (m *Model) loadStashTip() tea.Cmd {
	st, ok := m.stashes.Selected()
	if !ok {
		m.detail.SetEmpty("no stashes")
		return nil
	}
	return m.loadDetailFor(st.SHA)
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

	case tickMsg:
		return m, tea.Batch(m.refresh(), tick())

	case watchMsg:
		// Re-armed even when refresh declined: a write is in flight, and its
		// own reload will read the change.
		return m, tea.Batch(m.refresh(), m.waitWatch())

	case headMsg:
		m.head, m.err = msg.head, msg.err

	// ponytail: no generation counter, unlike diffMsg. A refresh can land
	// beside a stage's own reload, two reads of the same repo milliseconds
	// apart, and the later one wins — which is almost always also the newer.
	// Copy logGen into a statusGen the day an out-of-order pair shows up.
	case statusMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		if m.watch != nil {
			m.watch.Set(m.watchPaths(msg.files))
		}
		m.files.SetFiles(msg.files)
		m.files.SetStats(msg.stats)
		m.added, m.deleted = git.Total(msg.stats)
		return m, m.loadDiff()

	case stagedMsg:
		m.applying = false
		if msg.err != nil {
			// index.lock contention and a patch that will not apply both land
			// here, and both are things the user has to see: silently doing
			// nothing on a stage key is indistinguishable from a broken key.
			m.docError(msg.err)
			return m, nil
		}
		// A stash op changes both lists; the status one is re-read now and
		// the stash one when its view is next on screen.
		m.stashesLoaded = false
		if m.view == viewStash {
			return m, tea.Batch(m.loadStatus(), m.loadStashes())
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

	case stashesMsg:
		if msg.err != nil {
			m.detail.SetError(msg.err)
			return m, nil
		}
		m.stashesLoaded = true
		m.stashes.SetStashes(msg.stashes)
		if m.view != viewStash {
			return m, nil
		}
		return m, m.loadStashTip()

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

	// A pending question takes the next key as its answer, whatever the key.
	// Swallowing a no rather than acting on it keeps `j` from moving a cursor
	// the user was looking at the question over. ctrl+c stays quit, as it does
	// under the popup and in the editor.
	if m.confirm.pending() {
		do := m.confirm.do
		m.confirm = confirm{}
		switch k {
		case "ctrl+c":
			return m, tea.Quit
		case "y", "Y":
			return m, do(&m)
		}
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

	// The log key is also the way back to HEAD, since there is no view stack
	// to pop out of a branch-scoped log. Cleared here rather than in setView,
	// which the branch path goes through too — and unconditionally, so it
	// works while the log view is already the one on screen and setView would
	// return early.
	case keys.Matches(m.keys.LogView, k):
		if m.logRef != "" {
			m.logRef, m.logLoaded = "", false
			if m.view == viewLog {
				return m, m.loadLog()
			}
		}
		return m.setView(viewLog)

	case keys.Matches(m.keys.BranchView, k):
		return m.setView(viewBranches)

	case keys.Matches(m.keys.StashView, k):
		return m.setView(viewStash)

	// Works in every view: it re-reads whichever list is on screen.
	case keys.Matches(m.keys.Refresh, k):
		return m, m.refresh()

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
	case viewStash:
		return m.handleStashKey(k)
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
	case viewStash:
		if !m.stashesLoaded {
			return m, m.loadStashes()
		}
		return m, m.loadStashTip()
	}
	return m, nil
}

// handleLogKey routes the log view.
//
// The staging bindings are simply absent here rather than guarded one by one:
// nothing in this view is stageable, so there is no key for it to mean.
func (m Model) handleLogKey(k string) (tea.Model, tea.Cmd) {
	// Escape backs out of a branch-scoped log to the list it was opened from,
	// which is the other half of `enter`. It is guarded on the scope rather
	// than offered always: a log reached with `2` was not opened from
	// anywhere, and sending that one to the branch view would be a jump, not a
	// return.
	//
	// The scope is deliberately left set. Clearing it here would strand the
	// stale commits: logLoaded stays true, so the next `2` would skip the
	// re-read and title one branch's history as HEAD's.
	//
	// Before the focus routing, so it works from either pane — backing out is
	// about the view, the way the view keys are, not about what has focus.
	if keys.Matches(m.keys.Cancel, k) && m.logRef != "" {
		return m.setView(viewBranches)
	}

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

	// The scope is set before setView, not after: setView takes a value
	// receiver, so it copies the model and an assignment made afterwards
	// would be dropped on the copy it returns.
	case keys.Matches(m.keys.BranchLog, k):
		sel, ok := m.branches.Selected()
		if !ok {
			return m, nil
		}
		// Scoping to the branch you are on is the same history HEAD walks, so
		// it is left unscoped rather than titled with a name that adds
		// nothing.
		m.logRef = ""
		if !sel.Current {
			m.logRef = sel.Name
		}
		m.logLoaded = false
		return m.setView(viewLog)
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

// handleStashKey routes the stash view. Pop and apply put a stash back on the
// working tree; only drop loses it, so only drop asks.
func (m Model) handleStashKey(k string) (tea.Model, tea.Cmd) {
	if m.focus == focusDoc {
		return m.handleDetailKey(k)
	}

	before := m.stashes.SelectedSHA()
	switch {
	case keys.Matches(m.keys.StashPop, k), keys.Matches(m.keys.StashApply, k), keys.Matches(m.keys.StashDrop, k):
		st, ok := m.stashes.Selected()
		if !ok {
			return m, nil
		}
		ref := st.Ref
		switch {
		case keys.Matches(m.keys.StashPop, k):
			return m, m.write(func(ctx context.Context, r *git.Runner) error { return git.StashPop(ctx, r, ref) })
		case keys.Matches(m.keys.StashApply, k):
			return m, m.write(func(ctx context.Context, r *git.Runner) error { return git.StashApply(ctx, r, ref) })
		}
		m.ask("Drop "+ref+"?", func(ctx context.Context, r *git.Runner) error { return git.StashDrop(ctx, r, ref) })
		return m, nil
	case keys.Matches(m.keys.Down, k):
		m.stashes.MoveBy(1)
	case keys.Matches(m.keys.Up, k):
		m.stashes.MoveBy(-1)
	case keys.Matches(m.keys.Bottom, k):
		m.stashes.Bottom()
	case keys.Matches(m.keys.Top, k):
		m.stashes.Top()
	case keys.Matches(m.keys.PageDown, k):
		m.stashes.HalfPageDown()
	case keys.Matches(m.keys.PageUp, k):
		m.stashes.HalfPageUp()
	default:
		return m, nil
	}
	return m, m.afterStashMove(before)
}

// afterStashMove issues the re-read a moved stash cursor implies.
func (m *Model) afterStashMove(before string) tea.Cmd {
	if m.stashes.SelectedSHA() == before {
		return nil
	}
	return m.loadStashTip()
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
		m.files.HalfPageDown()
	case keys.Matches(m.keys.PageUp, k):
		m.files.HalfPageUp()
	case keys.Matches(m.keys.Stage, k), keys.Matches(m.keys.StageHunk, k):
		// In the file list both keys mean the whole file: there is no hunk to
		// single out from here.
		return m, m.stageFile()

	case keys.Matches(m.keys.StageAll, k):
		return m, m.write(git.StageAll)
	case keys.Matches(m.keys.UnstageAll, k):
		return m, m.write(git.UnstageAll)
	case keys.Matches(m.keys.Discard, k):
		m.ask("Discard every unstaged change to tracked files?", git.DiscardWorktree)
		return m, nil
	case keys.Matches(m.keys.Clean, k):
		n := 0
		for i := range m.files.Len() {
			if m.files.At(i).IsUntracked() {
				n++
			}
		}
		if n == 0 {
			return m, nil
		}
		m.ask("Delete "+strconv.Itoa(n)+" untracked file(s)?", git.CleanUntracked)
		return m, nil
	case keys.Matches(m.keys.Stash, k):
		return m, m.write(func(ctx context.Context, r *git.Runner) error { return git.StashPush(ctx, r, true) })
	case keys.Matches(m.keys.StashTracked, k):
		return m, m.write(func(ctx context.Context, r *git.Runner) error { return git.StashPush(ctx, r, false) })
	case keys.Matches(m.keys.StashPop, k):
		return m, m.write(func(ctx context.Context, r *git.Runner) error { return git.StashPop(ctx, r, "") })
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
	if !ok {
		return nil
	}
	path := sel.Path
	if m.stagedSide() {
		return m.write(func(ctx context.Context, r *git.Runner) error { return git.UnstageFile(ctx, r, path) })
	}
	return m.write(func(ctx context.Context, r *git.Runner) error { return git.StageFile(ctx, r, path) })
}

// write runs one git write behind the applying flag and reports it as a
// stagedMsg. Every whole-tree and stash key goes through here, so the guard,
// the timeout and the message live in one place.
func (m *Model) write(fn func(context.Context, *git.Runner) error) tea.Cmd {
	if m.applying {
		return nil
	}
	m.applying = true
	repo := m.repo
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
		defer cancel()
		return stagedMsg{err: fn(ctx, repo)}
	}
}

// ask parks a write behind a yes/no in the key bar.
func (m *Model) ask(prompt string, fn func(context.Context, *git.Runner) error) {
	if m.applying {
		return
	}
	m.confirm = confirm{prompt: prompt, do: func(m *Model) tea.Cmd { return m.write(fn) }}
}

// docError shows a failed write in whichever document pane is on screen: the
// diff in the status view, the commit everywhere else. An error nobody can
// see is a key that silently does nothing.
func (m *Model) docError(err error) {
	if m.view == viewStatus {
		m.diff.SetError(err)
		return
	}
	m.detail.SetError(err)
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
	if m.commit.Active() || m.showHelp || m.confirm.pending() {
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
	case viewStash:
		before := m.stashes.SelectedSHA()
		move(&m.stashes)
		return m, m.afterStashMove(before)
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
// header, the key bar and for anything outside the frame.
//
// This is layout() read backwards, and the two have to agree: framed() draws a
// pane as a border row, a title row, contentH() body rows and a closing border
// row, and the left pane owns columns 0..leftW-1 with the right one taking the
// rest. The left pane is the document and the right one the list, in every
// view. Every branch layout() has, this one has too — no border on a terminal
// too short for one, a single full-width pane when there is no room for two —
// which is why it is pinned by a table test rather than trusted.
func (m Model) hitTest(x, y int) (mouseHit, bool) {
	if !m.ready || x < 0 || x >= m.width || y < headerH || y >= m.height-footerH {
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
		top += chromeH - 1 // the top border, the title and the rule under it
	}
	if y < top || y >= top+m.contentH() {
		return hit, true
	}
	hit.row = y - top
	return hit, true
}

// minPaneWidth keeps both panes legible rather than letting one collapse.
const minPaneWidth = 24

// Pane chrome: a border row above and below, plus a title row. headerH and
// footerH are the app's own rows around the panes: the repository line above,
// the key bar below.
const (
	chromeH = 4 // top border, title, rule, bottom border
	chromeW = 2
	headerH = 1
	footerH = 1
)

// layout splits the terminal between the panes.
//
// Every clamp here has to agree with framed(), or the rendered frame comes
// out larger than the terminal and the display tears on resize. The rule is
// that filesW+diffW is exactly m.width, and header plus pane plus footer is
// exactly m.height.
func (m *Model) layout() {
	m.bodyH = m.height - headerH - footerH
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
	m.stashes.SetSize(m.contentW(m.rightW), m.contentH())
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
		panes = m.framed(title{name: m.commit.Title()}, m.commit.View(), m.width, true)
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
		m.footer(),
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

// title is a pane heading in three weights: the pane's name, what it is
// showing, and a count or a side. Which weight each part gets is framed()'s
// business — a focused pane reads name bold and count dim, an unfocused pane
// is dim throughout — so the parts stay apart until then.
type title struct {
	name    string // "Files", "Log", "Diff"
	subject string // a path, a ref, a sha
	meta    string // "(10)", "(worktree)", "+8 −10"; may already carry colour
}

// String is the heading as plain text, which is what tests compare and what
// an unfocused pane dims as a whole.
func (t title) String() string {
	s := t.name
	if t.subject != "" {
		s += " — " + t.subject
	}
	if t.meta != "" {
		s += " " + ansi.Strip(t.meta)
	}
	return s
}

func (t title) render(focused bool) string {
	if !focused {
		return theme.TitleDim.Render(t.String())
	}
	s := theme.Title.Render(t.name)
	if t.subject != "" {
		s += theme.TitleDim.Render(" — ") + theme.Context.Render(t.subject)
	}
	if t.meta != "" {
		s += " " + t.meta
	}
	return s
}

// count is the dim "(n)" a list title ends in.
func count(n int, more string) string {
	return theme.TitleDim.Render("(" + strconv.Itoa(n) + more + ")")
}

// listTitle and docTitle name whichever pane the current view puts in each
// role, so body() does not have to know which view it is drawing.
func (m Model) listTitle() title {
	if m.view == viewStash {
		t := title{name: "Stashes"}
		if n := m.stashes.Len(); n > 0 {
			t.meta = count(n, "")
		}
		return t
	}
	if m.view == viewBranches {
		t := title{name: "Branches"}
		if n := m.branches.Len(); n > 0 {
			t.meta = count(n, "")
		}
		return t
	}
	if m.view == viewLog {
		// The ref comes before the count so a narrow pane truncates the
		// number rather than the name: which history this is matters more
		// than how much of it has been read.
		t := title{name: "Log", subject: m.logRef}
		if n := m.log.Len(); n > 0 {
			// A trailing + means the count is what has been read so far, not
			// how many commits the repository has.
			more := ""
			if !m.log.AtEnd() {
				more = "+"
			}
			t.meta = count(n, more)
		}
		return t
	}
	t := title{name: "Files"}
	if n := m.files.Len(); n > 0 {
		t.meta = count(n, "")
	}
	if m.added+m.deleted > 0 {
		t.meta += " " + theme.Add.Render("+"+strconv.Itoa(m.added)) + " " + theme.Del.Render("−"+strconv.Itoa(m.deleted))
	}
	return t
}

func (m Model) docTitle() title {
	// The log, branch and stash views share the commit pane. The stash view
	// names its entry by ref rather than by the sha the pane holds: stash@{0}
	// is how the user knows it, and how the keys address it.
	if m.view == viewStash {
		st, _ := m.stashes.Selected()
		return title{name: "Stash", subject: st.Ref}
	}
	if m.view != viewStatus {
		return title{name: "Commit", subject: m.detail.Title()}
	}
	return diffTitle(m.diff.Title(), m.stagedSide())
}

func (m Model) listView() string {
	switch m.view {
	case viewLog:
		return m.log.View()
	case viewBranches:
		return m.branches.View()
	case viewStash:
		return m.stashes.View()
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
	return theme.Title.Render(filepath.Base(m.root)) + "  " + theme.Branch.Render("⎇ "+ref)
}

// framed draws a pane at an exact size.
//
// The width and height are set on the inner content rather than left to the
// border: lipgloss sizes a border to whatever it wraps, so a pane showing
// "loading…" would draw a box a few cells wide next to a full-height
// neighbour.
//
// The title is set in one cell from the border and ruled off from the body,
// so a list whose first row is itself a heading does not read as two.
func (m Model) framed(t title, body string, width int, focused bool) string {
	inner := m.contentW(width)
	height := m.contentH()

	sized := lipgloss.NewStyle().Width(inner).Height(height).MaxHeight(height).Render(body)
	if !m.bordered() {
		return sized
	}

	head := lipgloss.NewStyle().Width(inner).Render(" " + ansi.Truncate(t.render(focused), max(inner-1, 0), "…"))
	rule := theme.Rule.Render(strings.Repeat("─", inner))

	return theme.Border(focused).Render(lipgloss.JoinVertical(lipgloss.Left, head, rule, sized))
}

func diffTitle(path string, staged bool) title {
	if path == "" {
		return title{name: "Diff"}
	}
	side := "worktree"
	if staged {
		side = "staged"
	}
	return title{name: "Diff", subject: path, meta: theme.TitleDim.Render("(" + side + ")")}
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}
