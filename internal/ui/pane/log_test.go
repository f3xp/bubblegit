package pane

import (
	"strings"
	"testing"

	"github.com/f3xp/bubblegit/internal/git"
)

// commit builds a log entry from a one-letter name and its parents, so a test
// can state a topology instead of a table of SHAs.
func commit(sha string, parents ...string) git.Commit {
	return git.Commit{SHA: sha, Short: sha, Subject: sha, Parents: parents}
}

// TestGraphLanesFollowTopology walks the shape the fixture repository has: a
// merge, a side branch, and the trunk they both came from.
//
//	D   merge of B and C
//	|\
//	B C
//	|/
//	A   root
func TestGraphLanesFollowTopology(t *testing.T) {
	rows := buildGraph([]git.Commit{
		commit("D", "B", "C"),
		commit("B", "A"),
		commit("C", "A"),
		commit("A"),
	})

	if len(rows) != 4 {
		t.Fatalf("got %d rows, want 4", len(rows))
	}
	if !rows[0].merge {
		t.Error("the merge commit is not marked as one")
	}
	if rows[0].col != 0 || rows[1].col != 0 {
		t.Errorf("the merge and its first parent are in lanes %d and %d, want both in 0",
			rows[0].col, rows[1].col)
	}
	// The second parent has to get a lane of its own, or the side branch is
	// drawn on top of the trunk and the graph claims a history that never
	// happened.
	if rows[2].col == rows[1].col {
		t.Errorf("the side branch shares lane %d with the trunk", rows[2].col)
	}
	// Both lanes converge on the root, and only one of them may keep drawing.
	// The other has to end, or it trails a line down past the last commit.
	if got := len(rows[3].lanes); got != 2 {
		t.Fatalf("the root row has %d lanes, want 2", got)
	}
	if rows[3].lanes[0] && rows[3].lanes[1] {
		t.Error("both lanes are still live at the root; one never terminated")
	}
}

// TestGraphLanesDoNotShiftUnderneath: a column has to mean the same line of
// history all the way down the page. Compacting a lane when it ends would slide
// every lane to its right, which reads as the graph redrawing itself rather
// than as history.
func TestGraphLanesDoNotShiftUnderneath(t *testing.T) {
	// E's side branch ends at C; the trunk continues past it.
	rows := buildGraph([]git.Commit{
		commit("E", "D", "C"),
		commit("D", "B"),
		commit("C"),
		commit("B", "A"),
		commit("A"),
	})

	// The trunk is lane 0 on every row; only C, the side branch, sits anywhere
	// else. Stated per row rather than as a loop, so a lane that shifted shows
	// up as the row it shifted on.
	want := []int{0, 0, 1, 0, 0}
	for i, r := range rows {
		if r.col != want[i] {
			t.Errorf("row %d is in lane %d, want %d", i, r.col, want[i])
		}
	}
}

// TestGraphHandlesUnloadedParents: the first page's oldest commits have parents
// that have not been read yet. Their lanes must stay open — that is what the
// graph shows continuing off the bottom of the page — without the builder
// tripping over a SHA it has never seen.
func TestGraphHandlesUnloadedParents(t *testing.T) {
	rows := buildGraph([]git.Commit{
		commit("C", "B"),
		commit("B", "unloaded-parent"),
	})
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	if !rows[1].lanes[rows[1].col] {
		t.Error("the last row's lane is not drawn")
	}
}

// TestAppendSkipsCommitsAlreadyLoaded: a page resumes from several tips at
// once, so a commit reachable from one of them may already have arrived.
// Appending it twice gives one commit two rows and two lanes.
func TestAppendSkipsCommitsAlreadyLoaded(t *testing.T) {
	var l Log
	l.SetSize(20, 10)
	l.SetCommits([]git.Commit{commit("C", "B"), commit("B", "A")})
	l.Append([]git.Commit{commit("B", "A"), commit("A")})

	if l.Len() != 3 {
		t.Fatalf("log holds %d commits after a page that overlapped, want 3", l.Len())
	}
	var got []string
	for i := 0; i < l.Len(); i++ {
		l.MoveTo(i)
		got = append(got, l.SelectedSHA())
	}
	if strings.Join(got, "") != "CBA" {
		t.Errorf("log holds %v, want C B A once each in order", got)
	}
}

// TestCursorSurvivesReload: reloading after a commit must not walk the
// selection back to the top of the log.
func TestCursorSurvivesReload(t *testing.T) {
	var l Log
	l.SetSize(20, 10)
	l.SetCommits([]git.Commit{commit("C"), commit("B"), commit("A")})
	l.MoveBy(2)
	if l.SelectedSHA() != "A" {
		t.Fatalf("cursor on %q, want A", l.SelectedSHA())
	}

	// A new commit lands on top: the same commit is now one row further down.
	l.SetCommits([]git.Commit{commit("D"), commit("C"), commit("B"), commit("A")})
	if l.SelectedSHA() != "A" {
		t.Errorf("cursor moved to %q after a reload, want A", l.SelectedSHA())
	}
}
