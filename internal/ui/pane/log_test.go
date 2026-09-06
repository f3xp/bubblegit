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

// graphOf renders a topology as one string per row, so a test can state the
// shape it expects and a failure prints the shape it got.
func graphOf(commits ...git.Commit) []string {
	rows := buildGraph(commits)
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = string(r.cells)
	}
	return out
}

// TestGraphDrawsConnectors states the whole graph for the shapes that matter:
// the fixture repository's merge, a side branch that ends, a fork that rejoins
// across a lane in between, and an octopus merge.
func TestGraphDrawsConnectors(t *testing.T) {
	cases := []struct {
		name    string
		commits []git.Commit
		want    []string
	}{
		{
			// D merges B and C; C forks from and rejoins A. The merge opens
			// lane 1 on its own row, and C's row runs back into lane 0, where
			// A is waiting, instead of drawing two disconnected columns.
			name:    "fixture",
			commits: []git.Commit{commit("D", "B", "C"), commit("B", "A"), commit("C", "A"), commit("A")},
			want:    []string{"◆╮", "●│", "├●", "● "},
		},
		{
			// C is a root on a side branch: its lane ends, the trunk keeps
			// its column, and the free column is not closed up.
			name:    "side branch ends",
			commits: []git.Commit{commit("E", "D", "C"), commit("D", "B"), commit("C"), commit("B", "A"), commit("A")},
			want:    []string{"◆╮", "●│", "│●", "● ", "● "},
		},
		{
			// N is a second head in lane 2 whose history rejoins the trunk
			// while lane 1 is still live, so the horizontal run crosses it.
			name: "rejoin across a lane",
			commits: []git.Commit{
				commit("M", "A", "X"), commit("N", "P"), commit("X", "Y"),
				commit("A", "R"), commit("P", "R"), commit("R"), commit("Y"),
			},
			want: []string{"◆╮", "││●", "│●│", "●││", "├┼●", "●│ ", " ● "},
		},
		{
			name:    "octopus",
			commits: []git.Commit{commit("M", "A", "B", "C"), commit("A"), commit("B"), commit("C")},
			want:    []string{"◆┬╮", "●││", " ●│", "  ●"},
		},
	}
	for _, tc := range cases {
		got := graphOf(tc.commits...)
		if strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
			t.Errorf("%s:\ngot\n%s\nwant\n%s", tc.name, strings.Join(got, "\n"), strings.Join(tc.want, "\n"))
		}
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
	if got := string(rows[1].cells); got != "●" {
		t.Errorf("the last row draws %q, want its node with the lane still open", got)
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

func TestInitials(t *testing.T) {
	for name, want := range map[string]string{
		"Goutham Das":        "GD",
		"goutham":            "GO",
		"Ada Byron Lovelace": "AL",
		"":                   "  ",
		"é":                  "É ",
		"  spaced  out  ":    "SO",
	} {
		if got := initials(name); got != want {
			t.Errorf("initials(%q) = %q, want %q", name, got, want)
		}
	}
}
