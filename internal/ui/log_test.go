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
	for _, want := range []string{"Log (4)", "merge feature into main", "◆", "FI"} {
		if !strings.Contains(body, want) {
			t.Errorf("the log view is missing %q:\n%s", want, body)
		}
	}

	// Asserted on the pane rather than the frame, and on a node beside a lane
	// rather than on a bare "│": the rounded pane border draws that same
	// character, so a whole-frame check for it passes with no graph at all.
	graph := ansi.Strip(h.m.log.View())
	if !strings.Contains(graph, "●│") && !strings.Contains(graph, "│●") {
		t.Errorf("the side branch has no lane of its own:\n%s", graph)
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
// keystroke and they return out of order. Without the generation tag the pane
// settles on whichever answer arrives last rather than on the commit under the
// cursor.
func TestStaleDetailIsDiscarded(t *testing.T) {
	h := newHarness(t)
	h.enterLog()

	current := h.m.detailGen
	h.send(detailMsg{
		gen:     current - 1,
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
	if !strings.Contains(body, "Log (") {
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
