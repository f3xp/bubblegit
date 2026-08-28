package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/f3xp/bubblegit/internal/git"
)

// TestStaleDiffResultsAreDiscarded is the guard for the bug that is easy to
// create and miserable to diagnose.
//
// Each keystroke in the file list starts a git process. With a ~14ms spawn
// cost, holding j leaves several in flight at once, and they do not
// necessarily finish in the order they started. Without a generation check
// the pane settles on whichever answer happens to arrive last, so the diff
// shown belongs to a file the user has already scrolled past. Everything
// looks correct — the data is real, just for the wrong question.
func TestStaleDiffResultsAreDiscarded(t *testing.T) {
	m := New(t.TempDir())
	m.files.SetFiles([]git.FileStatus{
		{Kind: git.KindOrdinary, Staged: '.', Unstaged: 'M', Path: "first.txt"},
		{Kind: git.KindOrdinary, Staged: '.', Unstaged: 'M', Path: "second.txt"},
	})

	// Select the first file, then move to the second before the first result
	// has come back. Two requests are now outstanding.
	m.loadDiff()
	firstGen := m.diffGen

	m.files.MoveBy(1)
	m.loadDiff()
	secondGen := m.diffGen

	if firstGen == secondGen {
		t.Fatal("the generation did not advance between requests")
	}

	// The stale answer arrives last, which is exactly the ordering that breaks
	// a naive implementation.
	updated, _ := m.Update(diffMsg{
		gen:  secondGen,
		diff: git.FileDiff{Path: "second.txt", Hunks: []git.Hunk{{Header: "@@ -1 +1 @@"}}},
	})
	m = updated.(Model)

	updated, _ = m.Update(diffMsg{
		gen:  firstGen,
		diff: git.FileDiff{Path: "first.txt", Hunks: []git.Hunk{{Header: "@@ -9 +9 @@"}}},
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
		gen: m.diffGen,
		diff: git.FileDiff{Path: "first.txt", Hunks: []git.Hunk{{
			Header: "@@ -1 +1 @@",
			Lines:  []git.Line{{Kind: git.LineAdd, Text: "unmistakable", NewNum: 1}},
		}}},
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
	before := m.diffGen

	for i := 0; i < 5; i++ {
		updated, cmd = m.handleFilesKey("j")
		m = updated.(Model)
		if cmd != nil {
			t.Fatalf("keystroke %d at the end of the list issued a diff request", i)
		}
	}
	if m.diffGen != before {
		t.Errorf("generation advanced from %d to %d without a selection change", before, m.diffGen)
	}
}
