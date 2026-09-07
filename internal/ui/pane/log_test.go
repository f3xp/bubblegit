package pane

import (
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/f3xp/bubblegit/internal/git"
	"github.com/f3xp/bubblegit/internal/ui/theme"
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
// across a lane in between, an octopus merge, and the slide that closes a gap
// when a lane ends.
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
			want:    []string{"◆╮", "●│", "├●", "●"},
		},
		{
			// C is a root on a side branch: its lane ends and, being the
			// rightmost, simply disappears; the trunk keeps its column.
			name:    "side branch ends",
			commits: []git.Commit{commit("E", "D", "C"), commit("D", "B"), commit("C"), commit("B", "A"), commit("A")},
			want:    []string{"◆╮", "●│", "│●", "●", "●"},
		},
		{
			// N is a second head in lane 2 whose history rejoins the trunk
			// while lane 1 is still live, so the horizontal run crosses it.
			name: "rejoin across a lane",
			commits: []git.Commit{
				commit("M", "A", "X"), commit("N", "P"), commit("X", "Y"),
				commit("A", "R"), commit("P", "R"), commit("R"), commit("Y"),
			},
			want: []string{"◆╮", "││●", "│●│", "●││", "├┼●", "●│", "●╯"},
		},
		{
			// Each parent is a root, so the lanes end left to right and the
			// survivors slide one column per row into the gap.
			name:    "octopus",
			commits: []git.Commit{commit("M", "A", "B", "C"), commit("A"), commit("B"), commit("C")},
			want:    []string{"◆┬╮", "●││", "●╯│", " ●╯"},
		},
		{
			// The trunk ends at A, and B's lane slides into its column on the
			// next row rather than leaving a hole down the page.
			name:    "slide when the left lane ends",
			commits: []git.Commit{commit("M", "A", "B"), commit("A"), commit("B", "C"), commit("C")},
			want:    []string{"◆╮", "●│", "●╯", "●"},
		},
		{
			// A hole two lanes from home closes one column per row, so the
			// slides stack into a staircase instead of a ╭┼╯ that reads as a
			// join.
			name: "staircase under a passing lane",
			commits: []git.Commit{
				commit("M", "A", "B"), commit("A"), commit("N", "P"),
				commit("B", "C"), commit("P"), commit("C"),
			},
			want: []string{"◆╮", "●│", "╭╯●", "●╭╯", "│●", "●"},
		},
	}
	for _, tc := range cases {
		got := graphOf(tc.commits...)
		if strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
			t.Errorf("%s:\ngot\n%s\nwant\n%s", tc.name, strings.Join(got, "\n"), strings.Join(tc.want, "\n"))
		}
	}
}

// TestGraphColoursByLane: a glyph takes the colour of the column it belongs
// to, and a bend or run takes the column it lands in, so one line of history
// reads as one colour from the merge that opened it to the fork it slides
// into.
func TestGraphColoursByLane(t *testing.T) {
	rows := buildGraph([]git.Commit{
		commit("M", "A", "X"), commit("N", "P"), commit("X", "Y"),
		commit("A", "R"), commit("P", "R"), commit("R"), commit("Y"),
	})
	want := map[int][]int{
		0: {0, 1},    // ◆╮: the new lane is its own colour from the bend down
		4: {0, 1, 2}, // ├┼●: the crossed lane keeps its colour
		6: {0, 0},    // ●╯: the bend lands in lane 0
	}
	for i, lane := range want {
		if !reflect.DeepEqual(rows[i].lane, lane) {
			t.Errorf("row %d %q coloured %v, want %v", i, string(rows[i].cells), rows[i].lane, lane)
		}
	}
}

// TestGraphLaneCapFolds: past the cap the row is clipped and the node is
// drawn in the last column, so a wide history still shows every commit.
func TestGraphLaneCapFolds(t *testing.T) {
	var l Log
	l.SetSize(12, 10) // cap 4
	var heads []git.Commit
	for _, h := range "ABCDE" {
		heads = append(heads, commit(string(h), string(h)+"'"))
	}
	l.SetCommits(heads)
	if got := ansi.Strip(l.graphCell(4)); got != "│││●" {
		t.Errorf("fifth head drawn as %q, want it folded into the last of 4 columns", got)
	}
	for width, want := range map[int]int{0: 4, 6: 4, 100: 33} {
		l.SetSize(width, 10)
		if l.maxLanes() != want {
			t.Errorf("maxLanes at width %d = %d, want %d", width, l.maxLanes(), want)
		}
	}
}

// TestChildrenFirstFixesTiedTimestamps: git may list a parent above its
// child when their dates tie. The child is hoisted above it and everything
// else stays where git put it.
func TestChildrenFirstFixesTiedTimestamps(t *testing.T) {
	var l Log
	l.SetSize(20, 10)
	l.SetCommits([]git.Commit{commit("A"), commit("C", "B"), commit("B", "A"), commit("Z")})
	var got []string
	for _, c := range l.commits {
		got = append(got, c.SHA)
	}
	if strings.Join(got, "") != "CBAZ" {
		t.Errorf("order is %v, want C B A Z", got)
	}

	// A page that brings a child of a loaded commit shifts the rows above
	// the cursor; the cursor stays on its commit.
	l.MoveTo(3)
	l.Append([]git.Commit{commit("D", "Z")})
	if l.SelectedSHA() != "Z" {
		t.Errorf("cursor on %q after a hoisted page, want Z", l.SelectedSHA())
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

func TestParseRefs(t *testing.T) {
	cases := map[string][]ref{
		"":                                   nil,
		"feature":                            {{name: "feature"}},
		"HEAD":                               {{name: "HEAD", head: true}},
		"HEAD -> main, origin/main, tag: v1": {{name: "main", head: true}, {name: "origin/main", kind: refRemote}, {name: "v1", kind: refTag}},
		"tag: a/b":                           {{name: "a/b", kind: refTag}},
	}
	for in, want := range cases {
		if got := parseRefs(in); !reflect.DeepEqual(got, want) {
			t.Errorf("parseRefs(%q) = %+v, want %+v", in, got, want)
		}
	}
}

// TestRefPillsCapAtTwo: a commit with many refs shows two and a count, so the
// subject stays on the row.
func TestRefPillsCapAtTwo(t *testing.T) {
	got := ansi.Strip(refPills("HEAD -> main, origin/main, tag: v1"))
	if want := " ★ main   origin/main  +1 "; got != want {
		t.Errorf("refPills = %q, want %q", got, want)
	}
	if refPills("") != "" {
		t.Errorf("refPills of nothing = %q, want empty", refPills(""))
	}
}

// TestLogFindHelpers covers the lookups the jumps are built on: a row by SHA,
// the next and previous match of a query from the cursor, the nearest child
// above, and the match counts the prompt shows.
func TestLogFindHelpers(t *testing.T) {
	var l Log
	l.SetSize(40, 10)
	l.SetCommits([]git.Commit{
		{SHA: "D", Short: "ddd", Author: "Ada", Subject: "Merge branch", Parents: []string{"C", "B"}},
		{SHA: "C", Short: "ccc", Author: "Bob", Subject: "fix typo", Parents: []string{"A"}, Refs: "tag: v1"},
		{SHA: "B", Short: "bbb", Author: "Ada", Subject: "add thing", Parents: []string{"A"}},
		{SHA: "A", Short: "aaa", Author: "Bob", Subject: "init"},
	})

	if l.IndexOf("B") != 2 || l.IndexOf("nope") != -1 {
		t.Errorf("IndexOf: B at %d, nope at %d", l.IndexOf("B"), l.IndexOf("nope"))
	}
	// From the top: matches below the cursor only, case-insensitively, on
	// subject, author, short SHA and refs.
	for q, want := range map[string]int{"ada": 2, "TYPO": 1, "bbb": 2, "v1": 1, "merge": -1, "": -1} {
		if got := l.FindNext(q); got != want {
			t.Errorf("FindNext(%q) from the top = %d, want %d", q, got, want)
		}
	}
	l.MoveTo(3)
	if got := l.FindPrev("ada"); got != 2 {
		t.Errorf("FindPrev(ada) from the bottom = %d, want 2", got)
	}
	if got := l.ChildOf("A"); got != 2 {
		t.Errorf("ChildOf(A) from the bottom = %d, want the nearest child above", got)
	}
	l.MoveTo(2)
	if got := l.ChildOf("A"); got != 1 {
		t.Errorf("ChildOf(A) from row 2 = %d, want row 1", got)
	}
	if k, n := l.matchCounts("bob"); k != 1 || n != 2 {
		t.Errorf("matchCounts(bob) at row 2 = %d/%d, want 1/2", k, n)
	}

	// The prompt row takes the last line and highlights the match in the row.
	l.SetSearch("typo", false)
	view := ansi.Strip(l.View())
	if !strings.HasSuffix(view, "/ typo  1/1") {
		t.Errorf("no prompt row at the bottom:\n%s", view)
	}
	if !strings.Contains(l.View(), theme.Match.Render("typo")) {
		t.Error("the matched text is not highlighted in its row")
	}
	if l.height != 9 {
		t.Errorf("the list keeps %d rows beside the prompt, want 9", l.height)
	}
	l.SetSearch("", false)
	if l.height != 10 {
		t.Errorf("the list has %d rows after the prompt closed, want 10", l.height)
	}
}
