package ui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/f3xp/bubblegit/internal/git"
	"github.com/f3xp/bubblegit/internal/gittest"
	"github.com/f3xp/bubblegit/internal/ui/pane"
)

// enterLog switches to the log view and runs the reads it starts — the log
// page, then the detail for whatever the cursor lands on.
func (h *harness) enterLog() {
	h.t.Helper()
	h.maybeRun(h.key("2"))
	if h.m.view != viewLog {
		h.t.Fatal("the log view did not open")
	}
}

// maybeRun drives a command when the key produced one. Switching views only
// reads when the log has not been read yet, so the same key is a git call once
// and pure state every time after.
func (h *harness) maybeRun(cmd tea.Cmd) {
	h.t.Helper()
	if cmd != nil {
		h.run(cmd)
	}
}

// selectCommit moves the log cursor onto the commit with the given subject.
func (h *harness) selectCommit(subject string) {
	h.t.Helper()
	for range h.m.log.Len() {
		c, ok := h.m.log.Selected()
		if !ok {
			h.t.Fatal("the log pane has no selection")
		}
		if c.Subject == subject {
			return
		}
		h.maybeRun(h.key("j"))
	}
	h.t.Fatalf("no commit named %q in the log", subject)
}

func TestLogViewShowsCommitsAndGraph(t *testing.T) {
	h := newHarness(t)
	h.enterLog()

	if h.m.log.Len() != 4 {
		t.Fatalf("log holds %d commits, want the fixture's 4", h.m.log.Len())
	}

	body := ansi.Strip(h.m.Body())
	for _, want := range []string{"Log — all (4)", "merge feature into main", "◆", "FI"} {
		if !strings.Contains(body, want) {
			t.Errorf("the log view is missing %q:\n%s", want, body)
		}
	}

	// Asserted on the pane rather than the frame, and on the merge opening a
	// lane and the side branch joining back rather than on a bare "│": the
	// rounded pane border draws that same character, so a whole-frame check
	// for it passes with no graph at all.
	graph := ansi.Strip(h.m.log.View())
	if !strings.Contains(graph, "◆╮") || !strings.Contains(graph, "├●") {
		t.Errorf("the side branch has no lane of its own:\n%s", graph)
	}
}

// TestLogDefaultsToAllAndToggles: the log opens on every ref, the title says
// so, and the log view's own key flips it to HEAD's history and back.
func TestLogDefaultsToAllAndToggles(t *testing.T) {
	h := newHarness(t)
	h.enterLog()
	if title := h.m.listTitle().String(); title != "Log — all (4)" {
		t.Fatalf("the log opened titled %q, want every ref", title)
	}
	gen := h.m.logGen
	h.run(h.key("a"))
	if title := h.m.listTitle().String(); title != "Log — HEAD (4)" {
		t.Errorf("after a the log is titled %q, want HEAD", title)
	}
	if h.m.logGen == gen {
		t.Error("the toggle did not re-read the log")
	}
	h.run(h.key("a"))
	if title := h.m.listTitle().String(); title != "Log — all (4)" {
		t.Errorf("a second a titled the log %q, want every ref again", title)
	}
}

// TestLogSearchPagesUntilFound: a query with no match on the loaded pages
// asks for the next one and lands when it arrives. Along the way it pins the
// prompt as a mode — a j typed into it is a letter, not a motion.
func TestLogSearchPagesUntilFound(t *testing.T) {
	h := newHarness(t)
	h.enterLog()
	page1, page2 := commitsFor(200)[:100], commitsFor(200)[100:]
	h.send(logMsg{gen: h.m.logGen, commits: page1})

	h.key("/")
	if !h.m.logPrompt {
		t.Fatal("/ did not open the search prompt")
	}
	// The subjects are empty, so the short SHA is what a query can hit.
	want := page2[5]
	h.typeText("j")
	if h.m.log.SelectedSHA() != page1[0].SHA || h.m.logQuery != "j" {
		t.Errorf("a j typed into the prompt moved the cursor, or was not typed: query %q", h.m.logQuery)
	}
	h.setQuery(want.Short)

	cmd := h.enter()
	if h.m.logPrompt {
		t.Error("enter left the prompt open")
	}
	if cmd == nil || !h.m.logWant.search {
		t.Fatal("a query with no match on the loaded page did not ask for the next one")
	}
	h.send(logMsg{gen: h.m.logGen, more: true, commits: page2})
	if h.m.log.SelectedSHA() != want.SHA {
		t.Errorf("the search landed on %q, want %q from the second page", h.m.log.SelectedSHA(), want.Short)
	}
	if h.m.logWant.pending() {
		t.Error("the jump is still pending after landing")
	}
	// The prompt row stays while the query is in force, and shows which match
	// the cursor is on.
	if view := ansi.Strip(h.m.log.View()); !strings.Contains(view, "/ "+want.Short+"  1/1") {
		t.Errorf("the prompt row does not show the query and its count:\n%s", view)
	}
	h.maybeRun(h.esc())
	if h.m.logQuery != "" {
		t.Error("escape did not drop the query")
	}
	if h.m.view != viewLog {
		t.Error("escape with a query in force left the view")
	}
}

// setQuery replaces what the prompt holds, for a test that wants a known
// query without spelling every keystroke.
func (h *harness) setQuery(q string) {
	h.t.Helper()
	for h.m.logQuery != "" {
		h.send(tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	h.typeText(q)
	if h.m.logQuery != q {
		h.t.Fatalf("the prompt holds %q after typing %q", h.m.logQuery, q)
	}
}

// TestLogSearchWalksMatches: n and N move between the matches of a query the
// prompt has closed on, in both directions.
func TestLogSearchWalksMatches(t *testing.T) {
	h := newHarness(t)
	h.enterLog()
	h.key("/")
	h.typeText("FIXTURE") // every fixture commit is by "fixture"; case must not matter
	// maybeRun throughout: a match whose detail is cached or already being
	// read ahead of the cursor produces no command.
	h.maybeRun(h.enter())
	first := h.m.log.SelectedSHA()
	h.maybeRun(h.key("n"))
	second := h.m.log.SelectedSHA()
	if first == second {
		t.Fatal("n did not move to the next match")
	}
	h.maybeRun(h.key("N"))
	if h.m.log.SelectedSHA() != first {
		t.Error("N did not come back to the previous match")
	}
}

// TestLogParentAndChildJump: [ follows the first parent down, ] the nearest
// child back up; and a parent below the loaded pages is paged to.
func TestLogParentAndChildJump(t *testing.T) {
	h := newHarness(t)
	h.enterLog()
	h.selectCommit("merge feature into main")
	merge := h.m.log.SelectedSHA()

	// maybeRun: a neighbour's detail may already be cached from a prefetch.
	h.maybeRun(h.key("["))
	if c, _ := h.m.log.Selected(); c.Subject != "edit plain.txt" {
		t.Fatalf("[ landed on %q, want the merge's first parent", c.Subject)
	}
	// The edit has two children, the feature commit and the merge; the
	// nearest one above is the feature commit, and its own child is the merge.
	h.maybeRun(h.key("]"))
	if c, _ := h.m.log.Selected(); c.Subject != "add feature.txt" {
		t.Fatalf("] landed on %q, want the nearest child above", c.Subject)
	}
	h.maybeRun(h.key("]"))
	if h.m.log.SelectedSHA() != merge {
		t.Error("a second ] did not reach the merge")
	}

	// A first page whose last commit's parent is not loaded: [ on it asks for
	// the next page and lands when it arrives.
	pages := commitsFor(200)
	h.send(logMsg{gen: h.m.logGen, commits: pages[:100]})
	h.m.log.Bottom()
	if cmd := h.key("["); cmd == nil {
		t.Fatal("[ on a commit whose parent is not loaded did not ask for a page")
	}
	h.send(logMsg{gen: h.m.logGen, more: true, commits: pages[100:]})
	if h.m.log.SelectedSHA() != pages[100].SHA {
		t.Errorf("[ landed on %q, want the parent from the next page", h.m.log.SelectedSHA())
	}
}

// TestBranchJumpLandsOnTip: space in the branch view opens the log on every
// ref with the cursor on that branch's tip, so the branch is seen in context
// rather than alone.
func TestBranchJumpLandsOnTip(t *testing.T) {
	h := newHarness(t)
	h.enterBranches()
	h.selectBranch("behind")
	sel, _ := h.m.branches.Selected()

	h.run(h.key(" "))
	if h.m.view != viewLog {
		t.Fatal("space did not open the log view")
	}
	if title := h.m.listTitle().String(); title != "Log — all (4)" {
		t.Errorf("the log is titled %q, want every ref", title)
	}
	if h.m.log.SelectedSHA() != sel.SHA {
		t.Errorf("the cursor is on %q, want the tip of behind %q", h.m.log.SelectedSHA(), sel.Short)
	}
	if !strings.Contains(h.m.detail.Title(), sel.Short) {
		t.Errorf("the detail pane shows %q, want the tip", h.m.detail.Title())
	}
}

// TestLogDetailRendersSelectedCommit walks the two commits that render blank
// under the obvious git commands, through the whole UI rather than the git
// layer: the merge at the top and the root at the bottom.
func TestLogDetailRendersSelectedCommit(t *testing.T) {
	h := newHarness(t)
	h.enterLog()

	body := ansi.Strip(h.m.Body())
	if !strings.Contains(body, "feature.txt") {
		t.Errorf("the merge's detail pane shows no patch:\n%s", body)
	}

	h.selectCommit("base: files with spaces, unicode, and no trailing newline")
	body = ansi.Strip(h.m.Body())
	if !strings.Contains(body, "Author: fixture") {
		t.Errorf("the detail header is missing:\n%s", body)
	}
	if !strings.Contains(body, "func main()") {
		t.Errorf("the initial commit's patch is missing:\n%s", body)
	}
}

// TestStagingKeysAreInertInLogView is the guard on the view split.
//
// Every write key is dispatched before focus is routed, so without an explicit
// view check `space` in the log pane stages whatever the files pane — which is
// not on screen — happens to have selected. That is a write to the index with
// nothing on screen to explain it, and the user's next commit quietly carries
// a file they never chose.
func TestStagingKeysAreInertInLogView(t *testing.T) {
	h := newHarness(t)
	before := h.indexTree()

	h.enterLog()
	staged := h.m.showStaged
	for _, k := range []string{" ", "a", "t", "c", "C"} {
		t.Run(fmt.Sprintf("key %q", k), func(t *testing.T) {
			h.maybeRun(h.key(k))
			if h.m.commit.Active() {
				t.Errorf("%q opened the commit editor from the log view", k)
			}
			if got := h.indexTree(); got != before {
				t.Errorf("%q changed the index from the log view:\n%s", k, got)
			}
			// t writes nothing, so the two checks above pass for it whether or
			// not it is gated. What it does ungated is flip which side the
			// hidden diff pane shows and spend a process re-reading it.
			if h.m.showStaged != staged {
				t.Errorf("%q switched the hidden diff pane's side", k)
			}
		})
	}
	if h.m.view != viewLog {
		t.Error("a staging key switched the view")
	}
}

// indexTree is the index as a single comparable string, so a test can assert
// that a key changed nothing at all.
func (h *harness) indexTree() string {
	h.t.Helper()
	out, err := h.r.Run(context.Background(), "ls-files", "-s")
	if err != nil {
		h.t.Fatal(err)
	}
	return string(out)
}

// TestViewSwitchKeepsBothSides: each view owns its own selection, and coming
// back to one must not have reset it.
func TestViewSwitchKeepsBothSides(t *testing.T) {
	h := newHarness(t)
	h.press("j", "j")
	wantFile := h.m.SelectedPath()

	h.enterLog()
	h.selectCommit("edit plain.txt")
	wantCommit := h.m.log.SelectedSHA()

	h.maybeRun(h.key("1"))
	if h.m.view != viewStatus {
		t.Fatal("1 did not return to the status view")
	}
	if got := h.m.SelectedPath(); got != wantFile {
		t.Errorf("the file selection became %q, want %q", got, wantFile)
	}

	h.maybeRun(h.key("2"))
	if got := h.m.log.SelectedSHA(); got != wantCommit {
		t.Errorf("the commit selection became %q, want %q", got, wantCommit)
	}
}

// TestLogIsNotReadUntilAsked: startup reads HEAD and the status, and nothing
// else. A log nobody has looked at yet is a process spawn for no one.
func TestLogIsNotReadUntilAsked(t *testing.T) {
	h := newHarness(t)
	if h.m.logLoaded || h.m.log.Len() != 0 {
		t.Errorf("the log was read before the log view was opened (%d commits)", h.m.log.Len())
	}
	h.enterLog()
	if !h.m.logLoaded {
		t.Error("the log view opened without reading the log")
	}
}

// TestCommitInvalidatesLog: HEAD moved, so the log on screen the next time the
// view is opened must not be the one from before the commit.
func TestCommitInvalidatesLog(t *testing.T) {
	h := newHarness(t)
	h.enterLog()
	before := h.m.log.Len()

	h.maybeRun(h.key("1"))
	h.press("c")
	h.typeText("a new commit")
	h.run(h.confirm())

	if h.m.logLoaded {
		t.Error("the log was left marked as loaded after HEAD moved")
	}
	h.enterLog()
	if h.m.log.Len() != before+1 {
		t.Errorf("the log holds %d commits, want %d; it was not re-read",
			h.m.log.Len(), before+1)
	}
	// The cursor stayed on the commit it was on, which is now one row further
	// down, rather than jumping to the new HEAD.
	if h.m.log.SelectedSHA() == "" {
		t.Error("the log lost its selection across the reload")
	}
	// Checked through the model rather than the rendered rows: behind its
	// `(HEAD -> main)` label the subject does not survive a 38-column pane.
	h.m.log.Top()
	if c, _ := h.m.log.Selected(); c.Subject != "a new commit" {
		t.Errorf("the top of the log is %q, want the commit just made", c.Subject)
	}
}

// TestStaleDetailIsDiscarded: holding j in the log list issues a read per
// keystroke and they return out of order. Without matching on the SHA the pane
// settles on whichever answer arrives last rather than on the commit under the
// cursor.
func TestStaleDetailIsDiscarded(t *testing.T) {
	h := newHarness(t)
	h.enterLog()

	h.send(detailMsg{
		sha:     strings.Repeat("f", 40),
		content: pane.RenderDetail(git.Detail{Commit: git.Commit{SHA: strings.Repeat("f", 40), Short: "fffffff"}}),
	})
	if got := h.m.detail.Title(); got == "fffffff" {
		t.Error("a superseded detail was rendered")
	}
}

// TestLogViewRenders pins the whole frame: graph column, SHA, author tag, subject,
// and the detail pane beside it.
func TestLogViewRenders(t *testing.T) {
	h := newHarness(t)
	h.enterLog()
	teatest.RequireEqualOutput(t, []byte(h.m.Body()))
}

// TestNarrowLogViewSwapsPanes: below two minimum-width panes the log view has
// to fall back to one pane like the status view does, rather than drawing two
// slivers.
func TestNarrowLogViewSwapsPanes(t *testing.T) {
	h := newHarness(t)
	h.enterLog()
	h.send(tea.WindowSizeMsg{Width: 40, Height: 20})

	if !h.m.narrow {
		t.Fatal("a 40-column terminal is not narrow enough to drop a pane")
	}
	body := ansi.Strip(h.m.Body())
	if !strings.Contains(body, "Log — all (") {
		t.Errorf("the log pane is not the visible one:\n%s", body)
	}

	h.send(tea.KeyPressMsg{Code: tea.KeyTab})
	body = ansi.Strip(h.m.Body())
	if !strings.Contains(body, "Commit — ") {
		t.Errorf("tab did not swap to the detail pane:\n%s", body)
	}
}

// TestLogPagesWhileScrolling drives the paging path the fixture repository is
// too small to reach: jump to the bottom of what is loaded and check the next
// page arrives and extends the list rather than replacing it.
// TestPagingIsNotRequestedTwice pins the guard that keeps a held j from asking
// for the same page over and over.
//
// Every j at the bottom of the list calls loadMoreLog, and a terminal repeats a
// held key, so without the in-flight check the same page is read several times
// and the answers race each other into the list. The check is also what keeps
// Frontier — a walk over every commit loaded — from running per keystroke for
// an answer that is discarded; that ordering is not observable from here and is
// held by the comment beside it.
func TestPagingIsNotRequestedTwice(t *testing.T) {
	h := newHarness(t)
	h.enterLog()
	// Answer the load the view switch issued, with a full page whose parents
	// are not loaded: that is a list with more history behind it.
	h.send(logMsg{gen: h.m.logGen, commits: commitsFor(git.LogPageSize)})

	if cmd := h.m.loadMoreLog(); cmd == nil {
		t.Fatal("a full page with more history behind it did not ask for the next one")
	}
	if cmd := h.m.loadMoreLog(); cmd != nil {
		t.Error("a second page was requested while the first was still in flight")
	}
}

// commitsFor builds a page of commits whose parents are not loaded, which is
// what makes the list resumable rather than at its end.
func commitsFor(n int) []git.Commit {
	cs := make([]git.Commit, n)
	for i := range cs {
		cs[i] = git.Commit{
			SHA:     fmt.Sprintf("%040x", i),
			Short:   fmt.Sprintf("%07x", i),
			Parents: []string{fmt.Sprintf("%040x", i+1)},
		}
	}
	return cs
}

func TestLogPagesWhileScrolling(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping big-fixture paging test in -short mode")
	}

	dir := gittest.Big(t, 1000)
	m := New(dir)
	h := &harness{t: t, m: m, r: git.New(dir)}
	h.send(tea.WindowSizeMsg{Width: 80, Height: 24})
	h.send(headMsg{head: git.Head{Branch: "main", Commit: strings.Repeat("a", 40)}})
	h.send(statusMsg{})
	h.enterLog()

	if h.m.log.Len() != git.LogPageSize {
		t.Fatalf("the first page holds %d commits, want %d", h.m.log.Len(), git.LogPageSize)
	}
	if h.m.log.AtEnd() {
		t.Fatal("a full first page was taken as the end of history")
	}

	first := h.m.log.SelectedSHA()
	h.maybeRun(h.key("G"))

	if h.m.log.Len() <= git.LogPageSize {
		t.Errorf("scrolling to the bottom loaded no further page (%d commits)", h.m.log.Len())
	}
	// Extended, not replaced: the commit the view opened on is still there.
	c, _ := h.m.log.Selected()
	if c.SHA == first {
		t.Error("the cursor did not move to the newly loaded rows")
	}
	seen := make(map[string]bool)
	for i := 0; i < h.m.log.Len(); i++ {
		h.m.log.MoveTo(i)
		if sha := h.m.log.SelectedSHA(); seen[sha] {
			t.Fatalf("commit %s appears twice after paging", sha[:7])
		} else {
			seen[sha] = true
		}
	}
}
