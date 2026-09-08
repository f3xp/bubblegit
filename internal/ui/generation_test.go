package ui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/f3xp/bubblegit/internal/git"
	"github.com/f3xp/bubblegit/internal/ui/pane"
)

// TestStaleDiffResultsAreDiscarded is the guard for the bug that is easy to
// create and miserable to diagnose.
//
// Each keystroke in the file list starts a git process. With a ~14ms spawn
// cost, holding j leaves several in flight at once, and they do not
// necessarily finish in the order they started. Without matching answers to
// the key the pane wants, it settles on whichever answer happens to arrive
// last, so the diff shown belongs to a file the user has already scrolled
// past. Everything looks correct — the data is real, just for the wrong
// question.
func TestStaleDiffResultsAreDiscarded(t *testing.T) {
	m := New(t.TempDir())
	m.files.SetFiles([]git.FileStatus{
		{Kind: git.KindOrdinary, Staged: '.', Unstaged: 'M', Path: "first.txt"},
		{Kind: git.KindOrdinary, Staged: '.', Unstaged: 'M', Path: "second.txt"},
	})

	// Select the first file, then move to the second before the first result
	// has come back. Two requests are now outstanding.
	m.loadDiff()
	first := m.diffWant

	m.files.MoveBy(1)
	m.loadDiff()
	second := m.diffWant

	if first == second {
		t.Fatal("the wanted key did not change between requests")
	}

	// The stale answer arrives last, which is exactly the ordering that breaks
	// a naive implementation.
	updated, _ := m.Update(diffMsg{
		epoch:   m.diffEpoch,
		key:     second,
		content: pane.RenderDiff(git.FileDiff{Path: "second.txt", Hunks: []git.Hunk{{Header: "@@ -1 +1 @@"}}}),
	})
	m = updated.(Model)

	updated, _ = m.Update(diffMsg{
		epoch:   m.diffEpoch,
		key:     first,
		content: pane.RenderDiff(git.FileDiff{Path: "first.txt", Hunks: []git.Hunk{{Header: "@@ -9 +9 @@"}}}),
	})
	m = updated.(Model)

	if got := m.diff.Title(); got != "second.txt" {
		t.Errorf("diff pane shows %q; a superseded result overwrote the current one", got)
	}
}

// TestSelectionChangeClearsTheBody covers the other half: while a new diff is
// in flight the pane must not keep showing the previous file's hunks, or the
// user reads a correct diff under the wrong filename.
func TestSelectionChangeClearsTheBody(t *testing.T) {
	m := New(t.TempDir())
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = sized.(Model)
	m.files.SetFiles([]git.FileStatus{
		{Kind: git.KindOrdinary, Staged: '.', Unstaged: 'M', Path: "first.txt"},
		{Kind: git.KindOrdinary, Staged: '.', Unstaged: 'M', Path: "second.txt"},
	})

	m.loadDiff()
	updated, _ := m.Update(diffMsg{
		epoch: m.diffEpoch,
		key:   m.diffWant,
		content: pane.RenderDiff(git.FileDiff{Path: "first.txt", Hunks: []git.Hunk{{
			Header: "@@ -1 +1 @@",
			Lines:  []git.Line{{Kind: git.LineAdd, Text: "unmistakable", NewNum: 1}},
		}}}),
	})
	m = updated.(Model)

	if body := m.diff.View(); !strings.Contains(body, "unmistakable") {
		t.Fatalf("the first diff never rendered: %q", body)
	}

	m.files.MoveBy(1)
	m.loadDiff()

	if body := m.diff.View(); strings.Contains(body, "unmistakable") {
		t.Errorf("the previous file's diff is still on screen while the next one loads: %q", body)
	}
}

// TestRepeatedKeyAtListEndDoesNotRefetch: holding j past the last row must
// not spawn a git process per keystroke.
func TestRepeatedKeyAtListEndDoesNotRefetch(t *testing.T) {
	m := New(t.TempDir())
	m.files.SetFiles([]git.FileStatus{
		{Kind: git.KindOrdinary, Staged: '.', Unstaged: 'M', Path: "only.txt"},
	})

	updated, cmd := m.handleFilesKey("j")
	m = updated.(Model)
	if cmd != nil {
		t.Error("moving down in a one-row list issued a diff request")
	}
	for i := 0; i < 5; i++ {
		updated, cmd = m.handleFilesKey("j")
		m = updated.(Model)
		if cmd != nil {
			t.Fatalf("keystroke %d at the end of the list issued a diff request", i)
		}
	}
}

// TestRevisitedDiffPaintsWithoutBlank: a diff the pane has already shown is
// cached, so moving back to its file paints it in the same Update. The blank
// "loading…" frame is the flicker this exists to remove.
func TestRevisitedDiffPaintsWithoutBlank(t *testing.T) {
	h := newHarness(t)
	h.selectFile("two-hunks.txt")
	h.press("j")
	h.resolveDiff()

	if cmd := h.key("k"); cmd != nil {
		h.run(cmd) // neighbours may be prefetched; the diff itself must not be
	}
	body := h.m.Body()
	if h.m.SelectedPath() != "two-hunks.txt" || strings.Contains(body, "loading…") || !strings.Contains(body, "18 edited") {
		t.Fatalf("a cached diff blanked on the way back:\n%s", body)
	}
	if _, seen := h.m.diffs[h.m.diffWant]; !seen || h.m.diffs[h.m.diffWant] == nil {
		t.Error("the revisited diff was re-read rather than served from the cache")
	}
}

// TestMoveIssuesNeighbourPrefetch: a move reads the files around the new
// selection too, so that the next keystroke finds its diff already rendered.
func TestMoveIssuesNeighbourPrefetch(t *testing.T) {
	h := newHarness(t)
	// The harness never answers the read its setup issued for the first file;
	// drop that in-flight marker or the prefetch declines to read it again.
	clear(h.m.diffs)
	h.run(h.key("j"))

	next := h.m.files.Near(1)
	if len(next) < 2 {
		t.Fatal("precondition: the fixture needs a file on each side of the cursor")
	}
	for _, f := range next {
		if h.m.diffs[h.m.keyFor(f)] == nil {
			t.Fatalf("%s was not read ahead of the cursor", f.Path)
		}
	}

	h.key("j")
	if body := h.m.Body(); strings.Contains(body, "loading…") {
		t.Fatalf("a prefetched diff still blanked when the cursor reached it:\n%s", body)
	}
}

// TestStatusRefreshDropsTheDiffCache: a status read means the tree or the
// index changed, so a diff read before it is neither shown nor kept.
func TestStatusRefreshDropsTheDiffCache(t *testing.T) {
	h := newHarness(t)
	h.selectFile("two-hunks.txt")
	if h.m.diffs[h.m.diffWant] == nil {
		t.Fatal("precondition: the selected diff should be cached")
	}
	stale := diffMsg{epoch: h.m.diffEpoch, key: h.m.diffWant, content: pane.RenderDiff(git.FileDiff{
		Path:  "two-hunks.txt",
		Hunks: []git.Hunk{{Header: "@@ -1 +1 @@", Lines: []git.Line{{Kind: git.LineAdd, Text: "from before the refresh", NewNum: 1}}}},
	})}

	files, err := git.Status(context.Background(), h.r)
	if err != nil {
		t.Fatal(err)
	}
	h.send(statusMsg{files: files})
	if len(h.m.diffs) > 1 || h.m.diffs[h.m.diffWant] != nil {
		t.Fatal("the cache survived a status read")
	}

	h.send(stale)
	if h.m.diffs[h.m.diffWant] != nil || strings.Contains(h.m.Body(), "from before the refresh") {
		t.Error("a diff read before the status refresh was accepted")
	}
}
