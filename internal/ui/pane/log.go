package pane

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/f3xp/bubblegit/internal/git"
	"github.com/f3xp/bubblegit/internal/ui/theme"
)

// maxLanes caps the graph column. A repository with dozens of concurrent
// branches would otherwise spend the whole pane on vertical bars; past this
// many, commits are drawn in the last column and the topology is read from
// the detail pane instead.
const maxLanes = 6

// dateFormat is absolute rather than relative ("3 days ago").
//
// A relative date is the nicer thing to read and it is not free: it depends on
// time.Now(), so every golden file in this repo would change with the calendar
// and the pane would need a clock injected to be testable. That machinery buys
// nothing else, so the date stays absolute until something else needs a clock.
const dateFormat = "2006-01-02"

// Log lists commits with a lane graph down the left.
type Log struct {
	commits []git.Commit
	graph   []graphRow

	cursor int
	offset int
	width  int
	height int

	// end marks the log as fully loaded, so scrolling to the bottom stops
	// asking for a page that does not exist.
	end bool
}

// graphRow is one commit's place in the lane graph: the column its node sits
// in, and one glyph per lane — the node itself, a line passing by, or the
// connector that joins the node to another lane on this row.
type graphRow struct {
	col   int
	cells []rune
}

func (l *Log) SetSize(w, h int) { l.width, l.height = w, h; l.clampOffset() }

func (l *Log) Len() int      { return len(l.commits) }
func (l *Log) AtEnd() bool   { return l.end }
func (l *Log) SetEnd(v bool) { l.end = v }

// SetCommits replaces the log, keeping the cursor on the same commit where it
// still exists — a reload after a commit must not walk the selection away.
func (l *Log) SetCommits(commits []git.Commit) {
	want := l.SelectedSHA()
	l.commits = commits
	l.graph = buildGraph(commits)
	l.cursor = 0
	for i, c := range commits {
		if c.SHA == want {
			l.cursor = i
			break
		}
	}
	l.clampOffset()
}

// Append adds the next page, skipping anything already loaded.
//
// The duplicate check is not theoretical bookkeeping: a page resumes from
// several tips at once, and a commit reachable from one of them may already
// have arrived on an earlier page. A repeated SHA would give one commit two
// lanes in the graph and two rows in the list.
//
// The graph is rebuilt over the whole list rather than extended, because a
// lane's column depends on every commit above it.
//
// ponytail: that is O(total) per page, a few microseconds at 100 commits a
// page against a ~14ms process spawn. Make it incremental if a profile ever
// disagrees.
func (l *Log) Append(commits []git.Commit) {
	seen := make(map[string]bool, len(l.commits))
	for _, c := range l.commits {
		seen[c.SHA] = true
	}
	for _, c := range commits {
		if seen[c.SHA] {
			continue
		}
		seen[c.SHA] = true
		l.commits = append(l.commits, c)
	}
	l.graph = buildGraph(l.commits)
	l.clampOffset()
}

func (l *Log) Selected() (git.Commit, bool) {
	if l.cursor < 0 || l.cursor >= len(l.commits) {
		return git.Commit{}, false
	}
	return l.commits[l.cursor], true
}

func (l *Log) SelectedSHA() string {
	c, ok := l.Selected()
	if !ok {
		return ""
	}
	return c.SHA
}

// Frontier is where the next page has to resume from. Empty means the history
// is fully loaded. See git.Frontier for why it is a set rather than one SHA.
func (l *Log) Frontier() []string { return git.Frontier(l.commits) }

// NearEnd reports whether the cursor is close enough to the bottom that the
// next page should be fetched before the user reaches it.
func (l *Log) NearEnd() bool {
	return !l.end && len(l.commits) > 0 && l.cursor >= len(l.commits)-l.pageMargin()
}

// pageMargin is one screenful, so a page is requested a screen before it is
// needed rather than at the moment the cursor hits the last row.
func (l *Log) pageMargin() int {
	if l.height > 1 {
		return l.height
	}
	return 1
}

func (l *Log) MoveBy(delta int) { l.cursor += delta; l.clampCursor() }
func (l *Log) MoveTo(i int)     { l.cursor = i; l.clampCursor() }

// SelectRow puts the cursor on a visible row. See Files.SelectRow for why the
// row is counted from the top of the pane and why one past the end is ignored.
func (l *Log) SelectRow(row int) {
	if row < 0 || row >= l.height || l.offset+row >= len(l.commits) {
		return
	}
	l.MoveTo(l.offset + row)
}

func (l *Log) Top() { l.MoveTo(0) }

// Bottom is the deepest commit loaded, not the root: the rest is a page that
// has not been read yet.
func (l *Log) Bottom() { l.MoveTo(len(l.commits) - 1) }

func (l *Log) HalfPageDown() { l.MoveBy(l.halfPage()) }
func (l *Log) HalfPageUp()   { l.MoveBy(-l.halfPage()) }

func (l *Log) halfPage() int {
	if h := l.height / 2; h > 0 {
		return h
	}
	return 1
}

func (l *Log) clampCursor() {
	if l.cursor >= len(l.commits) {
		l.cursor = len(l.commits) - 1
	}
	if l.cursor < 0 {
		l.cursor = 0
	}
	l.clampOffset()
}

// clampOffset keeps the cursor inside the visible window.
func (l *Log) clampOffset() {
	if l.height <= 0 {
		l.offset = 0
		return
	}
	if l.cursor < l.offset {
		l.offset = l.cursor
	}
	if l.cursor >= l.offset+l.height {
		l.offset = l.cursor - l.height + 1
	}
	if max := len(l.commits) - l.height; l.offset > max {
		l.offset = max
	}
	if l.offset < 0 {
		l.offset = 0
	}
}

func (l *Log) View() string {
	if len(l.commits) == 0 {
		return theme.Dim.Render("no commits yet")
	}

	end := l.offset + l.height
	if end > len(l.commits) {
		end = len(l.commits)
	}

	rows := make([]string, 0, end-l.offset)
	for i := l.offset; i < end; i++ {
		rows = append(rows, l.row(i))
	}
	return strings.Join(rows, "\n")
}

func (l *Log) row(i int) string {
	c := l.commits[i]

	line := l.graphCell(i) + " " +
		theme.Meta.Render(c.Short) + " " +
		theme.Dim.Render(c.When.Format(dateFormat)) + " "
	// Refs go before the subject, where git, tig and lazygit put them.
	if c.Refs != "" {
		line += theme.Ref.Render("("+c.Refs+")") + " "
	}
	line += c.Subject

	// Truncate on display width, not byte length: the line carries ANSI
	// escapes and a subject may contain wide characters.
	if l.width > 0 {
		line = ansi.Truncate(line, l.width, "…")
	}
	if i == l.cursor {
		return theme.SelectRow(line, l.width)
	}
	return lipgloss.NewStyle().Width(l.width).Render(line)
}

// graphCell draws one row of the lane graph.
//
// Lanes are never compacted when one dies, so a column belongs to the same
// line of history all the way down the page. Closing the gap would be denser
// and would slide every lane sideways mid-scroll, which reads as the graph
// redrawing itself rather than as the history it describes.
func (l *Log) graphCell(i int) string {
	if i >= len(l.graph) {
		return ""
	}
	g := l.graph[i]

	cells := g.cells
	if len(cells) > maxLanes {
		clipped := make([]rune, maxLanes)
		copy(clipped, cells)
		if g.col >= maxLanes {
			// The node is past the cap: draw it in the last visible column so
			// the row still shows a commit, whatever lane it really sits in.
			clipped[maxLanes-1] = cells[g.col]
		}
		cells = clipped
	}

	var b strings.Builder
	for _, r := range cells {
		switch r {
		case nodeMerge:
			b.WriteString(theme.Meta.Render(string(r)))
		case nodeCommit:
			b.WriteString(theme.Cursor.Render(string(r)))
		case ' ':
			b.WriteByte(' ')
		default:
			b.WriteString(theme.Dim.Render(string(r)))
		}
	}
	return b.String()
}

const (
	nodeCommit = '●'
	nodeMerge  = '◆'
)

// boxGlyphs is indexed by which sides of a cell carry a line: bit 0 up, bit 1
// down, bit 2 left, bit 3 right. A lane passing by is up+down, a merge opening
// a lane to its right is down+left, and so on.
var boxGlyphs = []rune(" ╵╷│╴╯╮┤╶╰╭├─┴┬┼")

func boxGlyph(up, down, left, right bool) rune {
	i := 0
	if up {
		i |= 1
	}
	if down {
		i |= 2
	}
	if left {
		i |= 4
	}
	if right {
		i |= 8
	}
	return boxGlyphs[i]
}

// drawCells lays out one row: the node in its column, the lanes live above
// (in) and below (out) it, and a horizontal run from the node to every lane
// it connects to on this row. Everything the row shows is derived here, so
// the renderer only has to colour glyphs.
func drawCells(col int, merge bool, in, out []bool, targets []int) []rune {
	lo, hi := col, col
	for _, t := range targets {
		if t < lo {
			lo = t
		}
		if t > hi {
			hi = t
		}
	}

	cells := make([]rune, len(out))
	for j := range cells {
		if j == col {
			if merge {
				cells[j] = nodeMerge
			} else {
				cells[j] = nodeCommit
			}
			continue
		}
		up := j < len(in) && in[j]
		cells[j] = boxGlyph(up, out[j], j > lo && j <= hi, j >= lo && j < hi)
	}
	return cells
}

func live(lanes []string) []bool {
	out := make([]bool, len(lanes))
	for i, s := range lanes {
		out[i] = s != ""
	}
	return out
}

// buildGraph assigns each commit a lane.
//
// Lanes are derived from the parent SHAs already loaded with the log, so the
// graph costs no extra git call. `git log --graph` is not used: it imposes its
// own commit ordering and its own padding, and it does not compose with the
// -z cursor paging the log pane depends on.
//
// The whole loaded list is walked, never the visible window: which column a
// commit sits in depends on every commit above it, so seeding from the top of
// the screen would shift the lanes as the user scrolls.
func buildGraph(commits []git.Commit) []graphRow {
	// lanes[i] is the SHA lane i is waiting to draw; "" is a lane that has
	// ended and is free for reuse.
	var lanes []string
	// drawn is every commit already given a row. A lane whose next commit is
	// in here has nowhere left to go downward.
	drawn := make(map[string]bool, len(commits))
	rows := make([]graphRow, len(commits))

	for i, c := range commits {
		col := laneOf(lanes, c.SHA)
		if col < 0 {
			col = freeLane(&lanes)
			lanes[col] = c.SHA
		}

		// Snapshot before reassigning: the lanes live on the way into this
		// row are what is drawn above the node, and the state on the way out
		// is what is drawn below it.
		in := live(lanes)
		drawn[c.SHA] = true

		// targets are the lanes this node connects to sideways on its own
		// row: the lane its history continues in when that lane is already
		// open, and every lane a merge pulls in.
		var targets []int

		switch {
		case len(c.Parents) == 0:
			// A root commit ends its lane.
			lanes[col] = ""
		case drawn[c.Parents[0]]:
			// The parent is already drawn above. The continuation is upward,
			// and only downward lines are drawn, so the lane ends here. Left
			// open it would trail a bar past the bottom of the log claiming a
			// line of history that has already been shown.
			lanes[col] = ""
		case laneOf(lanes, c.Parents[0]) >= 0:
			// Another lane is already waiting for the parent: this commit's
			// history joins it, drawn as a horizontal run into that lane.
			targets = append(targets, laneOf(lanes, c.Parents[0]))
			lanes[col] = ""
		default:
			lanes[col] = c.Parents[0]
		}

		// A merge's remaining parents open lanes of their own, unless they are
		// spoken for; either way the merge connects to them. Indexed rather
		// than sliced from 1: a root commit has no parents at all, and
		// Parents[1:] panics on it.
		for i := 1; i < len(c.Parents); i++ {
			p := c.Parents[i]
			if drawn[p] {
				continue
			}
			k := laneOf(lanes, p)
			if k < 0 {
				k = freeLane(&lanes)
				lanes[k] = p
			}
			targets = append(targets, k)
		}

		rows[i] = graphRow{col: col, cells: drawCells(col, c.IsMerge(), in, live(lanes), targets)}
	}
	return rows
}

func laneOf(lanes []string, sha string) int {
	if sha == "" {
		return -1
	}
	for i, s := range lanes {
		if s == sha {
			return i
		}
	}
	return -1
}

// freeLane returns the leftmost ended lane, appending one when all are busy.
func freeLane(lanes *[]string) int {
	for i, s := range *lanes {
		if s == "" {
			return i
		}
	}
	*lanes = append(*lanes, "")
	return len(*lanes) - 1
}
