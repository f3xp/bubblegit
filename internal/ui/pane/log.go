package pane

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/f3xp/bubblegit/internal/git"
	"github.com/f3xp/bubblegit/internal/ui/theme"
)

// Log lists commits with a lane graph down the left.
type Log struct {
	list
	commits []git.Commit
	graph   []graphRow
	// graphW is the widest row of the graph, so every row is padded to it and
	// the hash column lines up down the page.
	graphW int

	// end marks the log as fully loaded, so scrolling to the bottom stops
	// asking for a page that does not exist.
	end bool

	// tips are the refs the walk started from, so a page can resume from a
	// tip the first page never reached. Empty for a single-ref walk.
	tips []string

	// query is the search in force; its matches are highlighted and n/N walk
	// them. editing is true while the prompt still takes keystrokes. Either
	// one puts the prompt row at the bottom of the pane, which is why the
	// pane keeps its full height apart from the list's.
	query   string
	editing bool
	fullH   int
}

// SetSize gives the prompt row its line back out of the list's height.
func (l *Log) SetSize(w, h int) {
	l.fullH = h
	if l.promptShown() {
		h--
	}
	l.list.SetSize(w, h)
}

func (l *Log) promptShown() bool { return l.editing || l.query != "" }

// SetSearch sets the query and whether it is still being typed.
func (l *Log) SetSearch(query string, editing bool) {
	l.query, l.editing = query, editing
	l.SetSize(l.width, l.fullH)
}

func (l *Log) SetTips(tips []string) { l.tips = tips }

// graphRow is one commit's place in the lane graph: the column its node sits
// in, one glyph per lane — the node itself, a line passing by, or the
// connector that joins the node to another lane on this row — and the lane
// each glyph takes its colour from.
type graphRow struct {
	col   int
	cells []rune
	lane  []int
}

func (l *Log) AtEnd() bool   { return l.end }
func (l *Log) SetEnd(v bool) { l.end = v }

// SetCommits replaces the log, keeping the cursor on the same commit where it
// still exists — a reload after a commit must not walk the selection away.
func (l *Log) SetCommits(commits []git.Commit) {
	want := l.SelectedSHA()
	l.commits = childrenFirst(commits)
	l.setGraph(buildGraph(l.commits))
	l.cursor = 0
	for i, c := range commits {
		if c.SHA == want {
			l.cursor = i
			break
		}
	}
	l.setLen(len(commits))
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
	// A hoisted child can land above the cursor and shift it, so the cursor
	// is put back on its commit rather than its row.
	want := l.SelectedSHA()
	l.commits = childrenFirst(l.commits)
	if i := l.IndexOf(want); i >= 0 {
		l.cursor = i
	}
	l.setGraph(buildGraph(l.commits))
	l.setLen(len(l.commits))
}

// childrenFirst reorders the log so no commit sits below a loaded child of
// its. git lists commits newest first, which puts children above parents
// except when their timestamps tie — a rebased series, a bot, a fixture —
// where git may emit the parent first and the graph would draw a lane ending
// at a commit whose history continues above it. Each commit is emitted where
// git put it, after any of its children that were still waiting; everything
// else keeps git's order.
//
// ponytail: this fixes ties within the loaded list only. A child on a later
// page than its parent still draws as a lane end, as before. --topo-order
// would fix that too, and walks the whole history first on a repository
// without a commit-graph, which the paging budget forbids.
func childrenFirst(commits []git.Commit) []git.Commit {
	children := make(map[string][]int, len(commits))
	for i, c := range commits {
		for _, p := range c.Parents {
			children[p] = append(children[p], i)
		}
	}
	out := make([]git.Commit, 0, len(commits))
	done := make([]bool, len(commits))
	var emit func(i int)
	emit = func(i int) {
		if done[i] {
			return
		}
		done[i] = true
		for _, k := range children[commits[i].SHA] {
			emit(k)
		}
		out = append(out, commits[i])
	}
	for i := range commits {
		emit(i)
	}
	return out
}

func (l *Log) setGraph(g []graphRow) {
	l.graph, l.graphW = g, 0
	for _, r := range g {
		l.graphW = max(l.graphW, len(r.cells))
	}
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
func (l *Log) Frontier() []string { return git.Frontier(l.commits, l.tips...) }

// IndexOf is the row of a commit, or -1 when it is not loaded.
func (l *Log) IndexOf(sha string) int {
	for i, c := range l.commits {
		if c.SHA == sha {
			return i
		}
	}
	return -1
}

// FindNext and FindPrev are the row of the nearest commit below or above the
// cursor that matches q, or -1. A match is a case-insensitive substring of the
// subject, author, short SHA or refs.
func (l *Log) FindNext(q string) int { return l.find(q, l.cursor+1, 1) }
func (l *Log) FindPrev(q string) int { return l.find(q, l.cursor-1, -1) }

func (l *Log) find(q string, from, step int) int {
	if q == "" {
		return -1
	}
	for i := from; i >= 0 && i < len(l.commits); i += step {
		if matches(l.commits[i], q) {
			return i
		}
	}
	return -1
}

func matches(c git.Commit, q string) bool {
	return strings.Contains(strings.ToLower(c.Subject+" "+c.Author+" "+c.Short+" "+c.Refs), strings.ToLower(q))
}

// matchCounts is how many loaded commits match q, and how many of those sit
// at or above the cursor — the k of the prompt's k/N.
func (l *Log) matchCounts(q string) (k, n int) {
	for i, c := range l.commits {
		if matches(c, q) {
			n++
			if i <= l.cursor {
				k++
			}
		}
	}
	return k, n
}

// ChildOf is the row of the nearest commit above the cursor that has sha as
// a parent, or -1. Children are always above their parent in the loaded
// list, so the scan only ever looks upward.
func (l *Log) ChildOf(sha string) int {
	for i := l.cursor - 1; i >= 0; i-- {
		for _, p := range l.commits[i].Parents {
			if p == sha {
				return i
			}
		}
	}
	return -1
}

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

// Bottom is the deepest commit loaded, not the root: the rest is a page that
// has not been read yet. The motion itself is the embedded list's.

func (l *Log) View() string {
	if len(l.commits) == 0 {
		return theme.Dim.Render("no commits yet")
	}

	start, end := l.window()
	rows := make([]string, 0, end-start+1)
	for i := start; i < end; i++ {
		rows = append(rows, l.row(i))
	}
	if l.promptShown() {
		for len(rows) < l.height {
			rows = append(rows, "")
		}
		rows = append(rows, l.promptRow())
	}
	return strings.Join(rows, "\n")
}

// promptRow is the search line under the list: the query, and which of its
// matches the cursor is on.
func (l *Log) promptRow() string {
	k, n := l.matchCounts(l.query)
	line := theme.Cursor.Render("/") + " " + l.query
	if l.editing {
		line += theme.Cursor.Render("▏")
	}
	if l.query != "" {
		line += "  " + theme.Dim.Render(strconv.Itoa(k)+"/"+strconv.Itoa(n))
	}
	if l.width > 0 {
		line = ansi.Truncate(line, l.width, "…")
	}
	return line
}

func (l *Log) row(i int) string {
	c := l.commits[i]

	short := theme.Meta.Render(c.Short)
	if l.query != "" && strings.Contains(strings.ToLower(c.Short), strings.ToLower(l.query)) {
		short = theme.Match.Render(c.Short)
	}
	// Refs go before the subject, where git, tig and lazygit put them.
	line := l.graphCell(i) + " " +
		short + " " +
		theme.Author(c.Author).Render(initials(c.Author)) + " " +
		refPills(c.Refs) + markMatches(c.Subject, l.query)

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

// markMatches marks every case-insensitive occurrence of q in the plain text s.
func markMatches(s, q string) string {
	if q == "" {
		return s
	}
	lower, lq := strings.ToLower(s), strings.ToLower(q)
	if len(lower) != len(s) {
		// Case folding changed the byte length, so the offsets below would
		// not line up; a row without highlights beats a torn one.
		return s
	}
	var b strings.Builder
	for {
		i := strings.Index(lower, lq)
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:i])
		b.WriteString(theme.Match.Render(s[i : i+len(q)]))
		s, lower = s[i+len(q):], lower[i+len(q):]
	}
}

// initials reduces an author name to a two-column tag: first and last name
// for "Goutham Das" (GD), the first two letters for a lone "goutham" (GO).
// Always two cells wide so the columns after it stay aligned, and built from
// runes so a non-ASCII name does not break it.
func initials(name string) string {
	words := strings.Fields(name)
	var tag []rune
	switch len(words) {
	case 0:
	case 1:
		tag = []rune(words[0])
	default:
		tag = append([]rune(words[0])[:1], []rune(words[len(words)-1])[0])
	}
	if len(tag) > 2 {
		tag = tag[:2]
	}
	for len(tag) < 2 {
		tag = append(tag, ' ')
	}
	return strings.ToUpper(string(tag))
}

// maxLanes caps the graph column at a third of the pane. A repository with
// dozens of concurrent branches would otherwise spend the whole row on
// vertical bars; past the cap, commits are drawn in the last column and the
// topology is read from the detail pane instead.
func (l *Log) maxLanes() int { return max(4, l.width/3) }

// graphCell draws one row of the lane graph, padded to the widest row so the
// hash column lines up down the page.
func (l *Log) graphCell(i int) string {
	if i >= len(l.graph) {
		return ""
	}
	g := l.graph[i]
	cells, lane := g.cells, g.lane

	if cap := l.maxLanes(); len(cells) > cap {
		clipped, clane := make([]rune, cap), make([]int, cap)
		copy(clipped, cells)
		copy(clane, lane)
		if g.col >= cap {
			// The node is past the cap: draw it in the last visible column so
			// the row still shows a commit, whatever lane it really sits in.
			clipped[cap-1], clane[cap-1] = cells[g.col], lane[g.col]
		}
		cells, lane = clipped, clane
	}

	var b strings.Builder
	for j, r := range cells {
		if r == ' ' {
			b.WriteByte(' ')
			continue
		}
		b.WriteString(theme.Lane(lane[j]).Render(string(r)))
	}
	for j := len(cells); j < min(l.graphW, l.maxLanes()); j++ {
		b.WriteByte(' ')
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

// drawCells lays out one row: the node in its column, the lanes live below it
// (out), where each lane live above it continues (from), and a horizontal run
// from the node to every lane it connects to on this row. Everything the row
// shows is derived here, so the renderer only has to colour glyphs.
//
// The second result is the lane each cell is coloured as. A cell that carries
// a line takes its own column; a horizontal run takes the lane it lands in, so
// a merged branch is one colour from the merge node down to its fork, and a
// lane sliding left is one colour from the bend to the column it lands in.
func drawCells(col int, merge bool, from []int, out []bool, targets []int) (cells []rune, lane []int) {
	n := len(out)
	up, down, left, right := make([]bool, n), make([]bool, n), make([]bool, n), make([]bool, n)
	lane = make([]int, n)
	for j := range lane {
		lane[j] = j
		down[j] = out[j]
	}
	for j, k := range from {
		if k < 0 {
			continue
		}
		up[j] = true
		if k < j {
			left[j], right[k] = true, true
			lane[j] = k
		}
	}

	lo, hi := col, col
	for _, t := range targets {
		lo, hi = min(lo, t), max(hi, t)
	}
	for j := 0; j < n; j++ {
		left[j] = left[j] || (j > lo && j <= hi)
		right[j] = right[j] || (j >= lo && j < hi)
	}

	cells = make([]rune, n)
	for j := range cells {
		switch {
		case j == col:
			cells[j] = nodeCommit
			if merge {
				cells[j] = nodeMerge
			}
			continue
		case !up[j] && !down[j]:
			// Only a run passes here: colour it as the lane it is heading to.
			lane[j] = nearest(targets, j, j > col)
		}
		cells[j] = boxGlyph(up[j], down[j], left[j], right[j])
	}
	return cells, lane
}

// nearest is the target closest to column j on the far side of it from the
// node: the first one at or past j when the run goes right, the last one at or
// before j when it goes left.
func nearest(targets []int, j int, rightward bool) int {
	best := j
	for _, t := range targets {
		switch {
		case rightward && t >= j && (best == j || t < best):
			best = t
		case !rightward && t <= j && (best == j || t > best):
			best = t
		}
	}
	return best
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
//
// When a lane ends, the lanes to its right slide left to close the gap — one
// column per row, so the slide is drawn as a single ╭╯ bend and never crosses
// another. New lanes are only ever appended: reusing a hole on the row a
// neighbour slides into it would put a lane under the bend and draw a join.
func buildGraph(commits []git.Commit) []graphRow {
	// lanes[i] is the SHA lane i is waiting to draw; "" is a lane that has
	// ended and not yet been closed up.
	var lanes []string
	// drawn is every commit already given a row. A lane whose next commit is
	// in here has nowhere left to go downward.
	drawn := make(map[string]bool, len(commits))
	rows := make([]graphRow, len(commits))

	for i, c := range commits {
		// from is where each lane live above this row continues below it —
		// the slide, if any, is drawn on this row.
		var from []int
		lanes, from = compact(lanes)

		col := laneOf(lanes, c.SHA)
		if col < 0 {
			lanes = append(lanes, c.SHA)
			col = len(lanes) - 1
		}
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
				lanes = append(lanes, p)
				k = len(lanes) - 1
			}
			targets = append(targets, k)
		}

		cells, lane := drawCells(col, c.IsMerge(), from, live(lanes), targets)
		rows[i] = graphRow{col: col, cells: cells, lane: lane}
	}
	return rows
}

// compact drops the holes past the last live lane and slides every lane with a
// hole directly to its left one column into it. from[j] is the column lane j
// continues in, -1 for a hole. One step per row: a lane two holes from home
// takes two rows to get there, which keeps every bend a plain ╭╯.
func compact(lanes []string) (out []string, from []int) {
	for len(lanes) > 0 && lanes[len(lanes)-1] == "" {
		lanes = lanes[:len(lanes)-1]
	}
	out, from = make([]string, len(lanes)), make([]int, len(lanes))
	for j, s := range lanes {
		from[j] = -1
		if s == "" {
			continue
		}
		k := j
		if j > 0 && lanes[j-1] == "" {
			k = j - 1
		}
		out[k], from[j] = s, k
	}
	return out, from
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

// refKind is what a decoration names: a local branch, a remote-tracking ref,
// or a tag.
type refKind int

const (
	refLocal refKind = iota
	refRemote
	refTag
)

type ref struct {
	name string
	kind refKind
	// head marks the ref HEAD is on, or a detached HEAD itself.
	head bool
}

// parseRefs splits git's %D decoration — "HEAD -> main, origin/main, tag: v1"
// — into one entry per ref. The separator is safe: a ref name cannot contain
// a space.
//
// ponytail: a ref containing "/" is taken for a remote, so a local branch
// named feature/x is coloured as one. Feed `git remote` names in if it bites.
func parseRefs(s string) []ref {
	if s == "" {
		return nil
	}
	var out []ref
	for _, part := range strings.Split(s, ", ") {
		r := ref{name: part}
		switch {
		case part == "HEAD":
			r.head = true
		case strings.HasPrefix(part, "HEAD -> "):
			r.name, r.head = strings.TrimPrefix(part, "HEAD -> "), true
		case strings.HasPrefix(part, "tag: "):
			r.name, r.kind = strings.TrimPrefix(part, "tag: "), refTag
		}
		if r.kind == refLocal && strings.Contains(r.name, "/") {
			r.kind = refRemote
		}
		out = append(out, r)
	}
	return out
}

// maxPills is how many refs a row shows before folding the rest into a count.
// Two covers the common "main, origin/main" pair; a release commit with five
// tags would otherwise push its subject off the pane.
const maxPills = 2

// refPills renders the refs on a commit as coloured pills, each followed by a
// space, so the caller can append the subject directly. Empty when the commit
// has none.
func refPills(s string) string {
	refs := parseRefs(s)
	var b strings.Builder
	for i, r := range refs {
		if i == maxPills {
			b.WriteString(theme.Dim.Render("+"+strconv.Itoa(len(refs)-maxPills)) + " ")
			break
		}
		name := r.name
		if r.head {
			// A marker rather than bold: bold is invisible on a filled block.
			name = "★ " + name
		}
		b.WriteString(pillStyle(r.kind).Render(name) + " ")
	}
	return b.String()
}

func pillStyle(k refKind) lipgloss.Style {
	switch k {
	case refRemote:
		return theme.Remote
	case refTag:
		return theme.Tag
	}
	return theme.Branch
}
