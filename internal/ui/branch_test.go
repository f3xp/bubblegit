package ui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/teatest/v2"
)

// enterBranches switches to the branch view and runs the reads it starts — the
// branch list, then the tip commit of whatever the cursor lands on.
func (h *harness) enterBranches() {
	h.t.Helper()
	h.maybeRun(h.key("3"))
	if h.m.view != viewBranches {
		h.t.Fatal("the branch view did not open")
	}
}

// selectBranch moves the branch cursor onto the named branch.
//
// It walks up to the first row before searching down: the view opens on the
// current branch rather than on row zero, so a target above it is not
// reachable with j alone.
func (h *harness) selectBranch(name string) {
	h.t.Helper()
	for range h.m.branches.Len() {
		h.maybeRun(h.key("k"))
	}
	for range h.m.branches.Len() {
		if h.m.branches.SelectedName() == name {
			return
		}
		h.maybeRun(h.key("j"))
	}
	h.t.Fatalf("no branch named %q in the list", name)
}

// TestBranchViewShowsTrackingStates walks the five states a row can be in.
// They are the whole point of the pane: a list of names is something `git
// branch` already prints.
func TestBranchViewShowsTrackingStates(t *testing.T) {
	h := newHarness(t)
	h.enterBranches()

	if h.m.branches.Len() != 5 {
		t.Fatalf("the list holds %d branches, want the fixture's 5", h.m.branches.Len())
	}

	if body := ansi.Strip(h.m.Body()); !strings.Contains(body, "Branches (5)") {
		t.Errorf("the branch pane is not on screen:\n%s", body)
	}

	// Asserted on the rows up to the date rather than anywhere in the frame:
	// a row is truncated to its column, so a bare Contains for "↑2" passes on
	// whichever row happens to still have room for it, and a bare "=" matches
	// the commit pane beside it.
	rows := ansi.Strip(h.m.branches.View())
	for _, want := range []string{
		"  behind ↓1 2020-",          // one commit back from origin/main
		"  feature 2020-",            // tracking nothing: no cell at all
		"  gone-upstream gone 2020-", // its upstream ref was deleted
		"* main ↑2 2020-",            // checked out, and ahead of origin/main
		"  synced = 2020-",           // matching its upstream
	} {
		if !strings.Contains(rows, want) {
			t.Errorf("no row reads %q:\n%s", want, rows)
		}
	}
}

// TestBranchDetailShowsSelectedTip: the right pane is the commit pane, showing
// the tip of the branch under the cursor rather than HEAD.
func TestBranchDetailShowsSelectedTip(t *testing.T) {
	h := newHarness(t)
	h.enterBranches()

	h.selectBranch("behind")
	sel, ok := h.m.branches.Selected()
	if !ok {
		t.Fatal("the branch pane has no selection")
	}
	if got := h.m.detail.Title(); got != sel.Short {
		t.Errorf("the detail pane shows %q, want the tip %q of the selected branch",
			got, sel.Short)
	}

	// `behind` sits on the initial commit, whose patch is the whole tree.
	body := ansi.Strip(h.m.Body())
	if !strings.Contains(body, "func main()") {
		t.Errorf("the tip commit's patch is missing:\n%s", body)
	}
}

// TestSharedDetailFollowsTheViewItIsIn is the bug the two panes sharing one
// commit pane would otherwise have.
//
// The branch view leaves the detail pane holding a branch tip. Coming back to
// the log has to re-read the commit under the log cursor: the log list is
// still loaded, so nothing else would issue that read, and the pane would sit
// there describing a commit the log cursor is not on.
func TestSharedDetailFollowsTheViewItIsIn(t *testing.T) {
	h := newHarness(t)
	h.enterLog()
	h.selectCommit("edit plain.txt")
	wantCommit := h.m.log.SelectedSHA()

	h.enterBranches()
	h.selectBranch("feature")
	wantTip := h.m.branches.SelectedName()
	if h.m.detail.Title() == wantCommit[:7] {
		t.Fatalf("the branch view is still showing the log's commit %q", wantCommit[:7])
	}

	h.maybeRun(h.key("2"))
	if got := h.m.detail.Title(); got != wantCommit[:7] {
		t.Errorf("back in the log the detail pane shows %q, want the log cursor's %q",
			got, wantCommit[:7])
	}

	h.enterBranches()
	if h.m.branches.SelectedName() != wantTip {
		t.Errorf("the branch selection became %q, want %q",
			h.m.branches.SelectedName(), wantTip)
	}
	if h.m.detail.Title() == wantCommit[:7] {
		t.Error("back in the branch view the detail pane still shows the log's commit")
	}
}

// TestStagingKeysAreInertInBranchView is the same guard the log view has.
// Every write key is dispatched before focus is routed, so without a view
// check `space` here stages whatever the off-screen files pane has selected.
func TestStagingKeysAreInertInBranchView(t *testing.T) {
	h := newHarness(t)
	before := h.indexTree()

	h.enterBranches()
	staged := h.m.showStaged
	for _, k := range []string{" ", "a", "t", "c", "C"} {
		t.Run(fmt.Sprintf("key %q", k), func(t *testing.T) {
			h.maybeRun(h.key(k))
			if h.m.commit.Active() {
				t.Errorf("%q opened the commit editor from the branch view", k)
			}
			if got := h.indexTree(); got != before {
				t.Errorf("%q changed the index from the branch view:\n%s", k, got)
			}
			if h.m.showStaged != staged {
				t.Errorf("%q switched the hidden diff pane's side", k)
			}
		})
	}
	if h.m.view != viewBranches {
		t.Error("a staging key switched the view")
	}
}

// TestBranchesAreNotReadUntilAsked: startup reads HEAD and the status, and
// nothing else.
func TestBranchesAreNotReadUntilAsked(t *testing.T) {
	h := newHarness(t)
	if h.m.branchesLoaded || h.m.branches.Len() != 0 {
		t.Errorf("the branch list was read before the view was opened (%d branches)",
			h.m.branches.Len())
	}
	h.enterBranches()
	if !h.m.branchesLoaded {
		t.Error("the branch view opened without reading the branches")
	}
}

// TestCommitInvalidatesBranches: a commit moves the current branch's tip and
// puts it one further ahead of its upstream, so the list from before it
// describes a branch that has moved.
func TestCommitInvalidatesBranches(t *testing.T) {
	h := newHarness(t)
	h.enterBranches()
	h.selectBranch("main")
	before, _ := h.m.branches.Selected()

	h.maybeRun(h.key("1"))
	h.press("c")
	h.typeText("a new commit")
	h.run(h.confirm())

	if h.m.branchesLoaded {
		t.Error("the branch list was left marked as loaded after HEAD moved")
	}

	h.enterBranches()
	h.selectBranch("main")
	after, _ := h.m.branches.Selected()
	if after.SHA == before.SHA {
		t.Error("main's tip did not move across the commit; the list was not re-read")
	}
	if after.Ahead != before.Ahead+1 {
		t.Errorf("main reads %d ahead of its upstream, want %d",
			after.Ahead, before.Ahead+1)
	}
}

// TestNarrowBranchViewSwapsPanes: below two minimum-width panes this view has
// to fall back to one pane like the others, rather than drawing two slivers.
func TestNarrowBranchViewSwapsPanes(t *testing.T) {
	h := newHarness(t)
	h.enterBranches()
	h.send(tea.WindowSizeMsg{Width: 40, Height: 20})

	if !h.m.narrow {
		t.Fatal("a 40-column terminal is not narrow enough to drop a pane")
	}
	body := ansi.Strip(h.m.Body())
	if !strings.Contains(body, "Branches (") {
		t.Errorf("the branch pane is not the visible one:\n%s", body)
	}

	h.send(tea.KeyPressMsg{Code: tea.KeyTab})
	body = ansi.Strip(h.m.Body())
	if !strings.Contains(body, "Commit — ") {
		t.Errorf("tab did not swap to the commit pane:\n%s", body)
	}
}

// TestBranchViewRenders pins the whole frame: the marker, the tracking cell,
// the date and subject, and the commit pane beside it.
func TestBranchViewRenders(t *testing.T) {
	h := newHarness(t)
	h.enterBranches()
	teatest.RequireEqualOutput(t, []byte(h.m.Body()))
}

// TestCheckoutSwitchesBranch: `b` moves HEAD, and everything derived from it
// is re-read — the header, the branch list's current marker, and the log,
// which is marked stale rather than re-read behind the user's back.
func TestCheckoutSwitchesBranch(t *testing.T) {
	h := newHarness(t)
	h.enterLog() // so there is a loaded log for the switch to invalidate
	h.enterBranches()
	h.selectBranch("feature")

	h.run(h.key("b"))

	if h.m.head.Branch != "feature" {
		t.Errorf("the header reads %q, want feature", h.m.head.Branch)
	}
	sel, _ := h.m.branches.Selected()
	if sel.Name != "feature" || !sel.Current {
		t.Errorf("the list marks %+v, want feature as the current branch", sel)
	}
	if h.m.logLoaded {
		t.Error("the log was left marked as loaded after HEAD moved")
	}
	if h.m.applying {
		t.Error("the write flag was left held after the switch finished")
	}
	if !strings.Contains(ansi.Strip(h.m.Body()), "* feature") {
		t.Errorf("the marker did not move to feature:\n%s", ansi.Strip(h.m.Body()))
	}
}

// TestCheckoutShowsGitsRefusal: git refuses a switch that would throw away a
// local change, and the user has to see why. `behind` sits on a commit where
// the fixture's modified plain.txt has different content.
func TestCheckoutShowsGitsRefusal(t *testing.T) {
	h := newHarness(t)
	h.enterBranches()
	h.selectBranch("behind")

	h.run(h.key("b"))

	if h.m.head.Branch != "main" {
		t.Errorf("HEAD moved to %q despite the refusal", h.m.head.Branch)
	}
	if h.m.applying {
		t.Error("the write flag was left held after the switch failed")
	}
	body := ansi.Strip(h.m.Body())
	if !strings.Contains(body, "plain.txt") {
		t.Errorf("the pane does not say why the switch was refused:\n%s", body)
	}
}

// TestCheckoutIsInertOnTheCurrentBranch: git would exit 0 with "Already on
// ...", spending a process to redraw what the pane already says.
func TestCheckoutIsInertOnTheCurrentBranch(t *testing.T) {
	h := newHarness(t)
	h.enterBranches()
	h.selectBranch("main")

	if cmd := h.key("b"); cmd != nil {
		t.Error("checking out the branch already checked out spawned git")
	}
}

// TestCheckoutIsInertOutsideTheBranchView: `b` is the branch view's key. In
// the status and log views there is no branch under a cursor, and switching
// away from what is on screen there would be a write with nothing to explain
// it.
func TestCheckoutIsInertOutsideTheBranchView(t *testing.T) {
	h := newHarness(t)
	h.enterBranches()
	h.selectBranch("feature")

	h.maybeRun(h.key("1"))
	if cmd := h.key("b"); cmd != nil {
		t.Error("b checked out a branch from the status view")
	}
	h.enterLog()
	if cmd := h.key("b"); cmd != nil {
		t.Error("b checked out a branch from the log view")
	}
	if h.m.head.Branch != "main" {
		t.Errorf("HEAD became %q, want main", h.m.head.Branch)
	}
}

// TestCheckoutWithTheStagedSideShowing is the one cross-view interaction the
// switch touches. `t` leaves the diff pane on the index-vs-HEAD side, and a
// checkout moves both HEAD and the working tree under it: the reload has to
// come back with a diff rather than an error, on whatever the files pane
// selects when the path it was on is not in the new branch.
func TestCheckoutWithTheStagedSideShowing(t *testing.T) {
	h := newHarness(t)
	h.selectFile("staged.txt") // present on main, absent from feature
	h.maybeRun(h.key("t"))
	if !h.m.showStaged {
		t.Fatal("t did not switch the diff pane to the staged side")
	}

	h.enterBranches()
	h.selectBranch("feature")
	h.run(h.key("b"))

	h.maybeRun(h.key("1"))
	if h.m.err != nil {
		t.Fatalf("the frame is showing an app-wide error after the switch: %v", h.m.err)
	}
	if _, ok := h.m.files.Selected(); !ok {
		t.Error("the files pane has no selection after the switch")
	}
	if body := ansi.Strip(h.m.Body()); strings.Contains(body, "git error") {
		t.Errorf("the diff reload failed after the switch:\n%s", body)
	}
}

// TestBranchLogScopesTheLogView covers the tig-style parameterised log: enter
// on a branch opens the log view on that branch's history rather than HEAD's.
// It is the only way to read a branch you are not on without checking it out,
// and it is what the layout gives instead of a third pane.
func TestBranchLogScopesTheLogView(t *testing.T) {
	h := newHarness(t)
	h.enterBranches()
	h.selectBranch("feature")
	h.run(h.enter())

	if !h.m.InLogView() {
		t.Fatal("enter on a branch did not open the log view")
	}
	// `feature` is the side of the merge, so its history is the two commits it
	// forked from plus its own — and not the merge that took it back onto
	// main, which is the commit HEAD's log leads with.
	if h.m.log.Len() != 3 {
		t.Fatalf("the log holds %d commits, want the 3 on `feature`", h.m.log.Len())
	}
	rows := ansi.Strip(h.m.log.View())
	if strings.Contains(rows, "merge feature into main") {
		t.Errorf("the log scoped to `feature` still shows main's merge:\n%s", rows)
	}
	// Looked up through the model rather than the rendered rows: with the ref
	// labels in front of it the subject does not survive a 38-column pane.
	h.selectCommit("add feature.txt")
	// The title is what licenses the scope existing at all: a log that changed
	// which history it walked without saying so reads as a bug.
	if title := h.m.listTitle(); title != "Log — feature (3)" {
		t.Errorf("the log pane is titled %q, want it to name the branch", title)
	}

	// The log key is the way back. There is no view stack to pop, so if this
	// regresses a scoped log becomes a one-way door.
	h.run(h.key("2"))
	if h.m.log.Len() != 4 {
		t.Fatalf("back on HEAD the log holds %d commits, want the fixture's 4", h.m.log.Len())
	}
	if title := h.m.listTitle(); title != "Log (4)" {
		t.Errorf("the log pane is titled %q, want the unscoped title", title)
	}
}

// TestBranchLogOnCurrentBranchStaysUnscoped keeps the title honest the other
// way: the branch you are on walks the same history HEAD does, so naming it
// would add a scope that is not one.
func TestBranchLogOnCurrentBranchStaysUnscoped(t *testing.T) {
	h := newHarness(t)
	h.enterBranches()
	h.selectBranch("main")
	h.run(h.enter())

	if title := h.m.listTitle(); title != "Log (4)" {
		t.Errorf("the log pane is titled %q, want the unscoped title", title)
	}
}

// TestBranchLogRecoversFromAMissingRef keeps the scope from becoming a one-way
// door. The branch list is a snapshot, so the ref it names can be gone by the
// time enter asks git to walk it — and the log key is the only way back, since
// `q` quits rather than popping a view.
func TestBranchLogRecoversFromAMissingRef(t *testing.T) {
	h := newHarness(t)
	h.enterBranches()
	h.selectBranch("feature")

	if _, err := h.r.Run(context.Background(), "update-ref", "-d", "refs/heads/feature"); err != nil {
		t.Fatal(err)
	}
	h.run(h.enter())

	if !h.m.InLogView() {
		t.Fatal("enter on a branch did not open the log view")
	}
	if !strings.Contains(ansi.Strip(h.m.detail.View()), "feature") {
		t.Errorf("the failed walk did not say which ref it could not resolve:\n%s", ansi.Strip(h.m.detail.View()))
	}

	h.run(h.key("2"))
	if h.m.log.Len() != 4 {
		t.Fatalf("the log key did not recover: it holds %d commits, want the fixture's 4", h.m.log.Len())
	}
	if title := h.m.listTitle(); title != "Log (4)" {
		t.Errorf("the log pane is titled %q, want the unscoped title", title)
	}
}

// TestBranchLogEscapeReturnsToBranches covers the other half of enter. It also
// pins the guard: escape in an unscoped log has nowhere to return to, so it
// must not jump to a view the user never came from.
func TestBranchLogEscapeReturnsToBranches(t *testing.T) {
	h := newHarness(t)
	h.enterBranches()
	h.selectBranch("feature")
	h.run(h.enter())

	h.maybeRun(h.esc())
	if h.m.view != viewBranches {
		t.Fatal("escape did not back out of the branch log")
	}
	if h.m.branches.SelectedName() != "feature" {
		t.Errorf("the branch cursor came back on %q, want the branch it drilled into", h.m.branches.SelectedName())
	}

	// The scope outlives the trip, so the log key still has something to
	// clear: without it logLoaded would keep the scoped commits on screen
	// under an unscoped title. Via the status view, which is the longest way
	// round — two view changes before the log key sees the scope at all.
	h.maybeRun(h.key("1"))
	h.run(h.key("2"))
	if title := h.m.listTitle(); title != "Log (4)" {
		t.Errorf("the log pane is titled %q, want the unscoped title", title)
	}

	// Unscoped now, so escape is inert rather than a jump.
	h.maybeRun(h.esc())
	if h.m.view != viewLog {
		t.Error("escape left an unscoped log view, which it was not opened from")
	}
}
