package pane

import (
	"testing"

	"github.com/f3xp/bubblegit/internal/git"
)

// TestSelectRowCountsFromTheVisibleWindow is the case the app-level mouse
// tests cannot reach: the fixture repository is smaller than the pane, so its
// scroll offset is always zero there and a SelectRow that ignored the offset
// would still pass. A click reports a screen position, so on a scrolled list
// the offset is the whole difference between the row clicked and the row
// selected.
func TestSelectRowCountsFromTheVisibleWindow(t *testing.T) {
	var l Log
	l.SetSize(40, 5)
	commits := make([]git.Commit, 20)
	for i := range commits {
		commits[i] = commit(string(rune('a' + i)))
	}
	l.SetCommits(commits)

	// Scroll: the cursor lands on the last row, so the window shows 15..19.
	l.Bottom()
	l.SelectRow(0)
	if got := l.SelectedSHA(); got != "p" {
		t.Errorf("the top visible row is %q, want p — the offset was not applied", got)
	}
	l.SelectRow(4)
	if got := l.SelectedSHA(); got != "t" {
		t.Errorf("the bottom visible row is %q, want t", got)
	}
}

// TestSelectRowIgnoresRowsOffTheList covers the blank space a short list
// leaves below itself, and a row past the bottom of the pane. Neither is a
// request to select the last entry.
func TestSelectRowIgnoresRowsOffTheList(t *testing.T) {
	var f Files
	f.SetSize(40, 10)
	f.SetFiles([]git.FileStatus{{Path: "a"}, {Path: "b"}})

	for _, row := range []int{-1, 2, 9, 10, 99} {
		f.SelectRow(row)
		if sel, _ := f.Selected(); sel.Path != "a" {
			t.Errorf("row %d moved the cursor to %q, want it left on a", row, sel.Path)
		}
	}
	f.SelectRow(1)
	if sel, _ := f.Selected(); sel.Path != "b" {
		t.Errorf("row 1 selected %q, want b", sel.Path)
	}
}

// TestSelectRowOnBranchesUsesTheOffsetToo: the third list carries its own copy
// of the arithmetic, so it needs its own proof.
func TestSelectRowOnBranchesUsesTheOffset(t *testing.T) {
	var b Branches
	b.SetSize(40, 3)
	list := make([]git.Branch, 10)
	for i := range list {
		list[i] = git.Branch{Name: string(rune('a' + i))}
	}
	b.SetBranches(list)

	b.Bottom()
	b.SelectRow(0)
	if got := b.SelectedName(); got != "h" {
		t.Errorf("the top visible row is %q, want h", got)
	}
}

// TestDiffSelectRowCountsFromTheVisibleWindow is the diff pane's version of
// the same case, and it matters more here than in the lists: the cursor this
// places is what the staging keys build a patch from, so an offset dropped
// here stages a line the user did not click.
func TestDiffSelectRowCountsFromTheVisibleWindow(t *testing.T) {
	lines := make([]git.Line, 30)
	for i := range lines {
		lines[i] = git.Line{Kind: git.LineAdd, Text: "line", NewNum: i + 1}
	}
	d := NewDiff()
	d.SetSize(40, 5)
	d.SetDiff(RenderDiff(git.FileDiff{Path: "a.txt", Hunks: []git.Hunk{{Header: "@@ -0,0 +1,30 @@", Lines: lines}}}))

	// The hunk header is row 0, so the 31 rendered rows scroll: the cursor
	// lands on the last one and the window shows rows 26..30.
	d.Bottom()
	d.SelectRow(0)
	if _, _, line, _ := d.Selection(); line != 25 {
		t.Errorf("the top visible row is line %d, want 25 — the offset was not applied", line)
	}

	// Past the end of the pane, and past the end of the diff.
	for _, row := range []int{-1, 5, 99} {
		d.SelectRow(row)
		if _, _, line, _ := d.Selection(); line != 25 {
			t.Errorf("row %d moved the cursor to line %d, want it left on 25", row, line)
		}
	}
}
