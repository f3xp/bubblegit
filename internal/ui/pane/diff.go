package pane

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/viewport"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/f3xp/bubblegit/internal/git"
	"github.com/f3xp/bubblegit/internal/highlight"
	"github.com/f3xp/bubblegit/internal/ui/theme"
)

// Diff renders one file's diff and carries the cursor that staging keys off.
type Diff struct {
	vp    viewport.Model
	title string

	// fd and rows are what turn a screen position back into a patch. rows has
	// exactly one entry per rendered row, which only holds because SoftWrap is
	// off — a wrapped line would occupy two rows and slide every row below it
	// onto the wrong hunk.
	fd     git.FileDiff
	rows   []rowRef
	cursor int
	// path is the diff currently loaded, as opposed to title, which changes
	// the moment a new one is requested. Reloading the same file after staging
	// has to leave the cursor where the user left it.
	path string
	// staged is the side of the last request. Request-side rather than
	// loaded-side, like Detail.sha: the generation check discards stale
	// answers, so the last request is what the next SetDiff will load, and
	// the render path keeps its signature.
	staged bool

	loading bool
	err     error
	empty   string // why there is nothing to show, when there is nothing
}

// rowRef maps a rendered row back to the diff it came from. Line is -1 for a
// hunk header row, which is what makes putting the cursor on a header mean
// "the whole hunk" without a mode of its own.
type rowRef struct{ hunk, line int }

func NewDiff() Diff {
	vp := viewport.New()
	// Horizontal scrolling rather than wrapping: a wrapped diff line silently
	// desynchronises the visual line count from the real one, which makes
	// line-level staging in M2 point at the wrong line.
	vp.SoftWrap = false
	return Diff{vp: vp, empty: "no file selected"}
}

func (d *Diff) SetSize(w, h int) {
	d.vp.SetWidth(w)
	d.vp.SetHeight(h)
	d.scrollToCursor()
}

// SetLoading marks a new diff in flight, clearing the body when it is for a
// different diff than the one on screen.
//
// The clear is not cosmetic. With a ~14ms process spawn, holding j in the file
// list leaves the previous file's diff on screen for a visible beat — correct
// data, rendered against the wrong filename, which reads as a bug. The side
// is part of the identity for the same reason: worktree rows under a
// "(staged)" title are the same lie.
//
// A re-read of the same diff — after a stage, a refresh, a save in an editor
// — keeps its rows instead. Blanking there paints "loading…" for a frame on
// every keystroke and every tick, which is the flicker. Ready() is false
// meanwhile, so a stage key still waits for the answer rather than building a
// patch from rows the index has moved out from under.
func (d *Diff) SetLoading(path string, staged bool) {
	d.loading = true
	d.err = nil
	d.title = path
	same := path == d.path && staged == d.staged
	d.staged = staged
	if same {
		return
	}
	// The row map goes with the content: it points into a diff that is no
	// longer what this pane stands for. The cursor row is kept, so reloading
	// the same path lands where the user left it.
	d.fd, d.rows = git.FileDiff{}, nil
	d.vp.SetContent("")
}

// Ready reports whether the pane is showing a diff it has actually received,
// as opposed to one still in flight or an error.
func (d *Diff) Ready() bool { return !d.loading && d.err == nil }

func (d *Diff) SetError(err error) {
	d.loading = false
	d.err = err
}

func (d *Diff) SetEmpty(reason string) {
	d.loading = false
	d.err = nil
	d.empty = reason
	d.clear()
}

// clear drops the diff and everything derived from it, so a stale rowRef can
// never outlive the hunks it points into.
func (d *Diff) clear() {
	d.fd, d.rows, d.cursor = git.FileDiff{}, nil, 0
	d.vp.SetContent("")
	d.vp.SetYOffset(0)
}

// DiffContent is a diff already turned into rows.
//
// Building one is the expensive half of showing a diff — the highlighter costs
// tens of microseconds a line, so a large file runs to hundreds of milliseconds
// — and it is pure computation over a value the git layer has already produced.
// So it is built by RenderDiff inside the command that read the diff, off the
// loop that answers keystrokes, and handed to the pane finished.
//
// Nothing here is exported. lines and rows are produced by one function and
// travel together, which is what keeps them one-to-one: the invariant the whole
// pane rests on cannot be broken by two callers rendering separately.
type DiffContent struct {
	fd    git.FileDiff
	lines []string
	rows  []rowRef
}

// Rows reports how many rows this content occupies.
//
// It exists so a test can tell rendered content from unrendered: the whole
// point of DiffContent is that the rows are built where the diff is read, and
// nothing else about the pane would notice if that quietly moved back onto the
// Update path.
func (c DiffContent) Rows() int { return len(c.rows) }

// RenderDiff renders a diff for the pane to show. It is a plain function, not
// a tea.Cmd, so that the command stays in the layer that owns the git call and
// this package keeps knowing nothing about the event loop.
func RenderDiff(fd git.FileDiff) DiffContent {
	c := DiffContent{fd: fd}
	if fd.Binary || fd.IsEmpty() {
		// Neither renders as rows, and both are cheap to recognise here rather
		// than making the pane ask twice.
		return c
	}
	c.lines, c.rows = renderLines(fd)
	return c
}

func (d *Diff) SetDiff(c DiffContent) {
	d.loading = false
	d.err = nil
	d.title = c.fd.Path

	// Staging a hunk reloads the same file with that hunk gone. Starting over
	// at the top each time would walk the cursor back to the first hunk after
	// every keystroke, so the row is kept and clamped instead.
	same := c.fd.Path == d.path
	cursor := d.cursor
	d.clear()
	d.path = c.fd.Path

	switch {
	case c.fd.Binary:
		d.empty = "binary file — no textual diff"
		return
	case c.fd.IsEmpty():
		d.empty = "no changes"
		return
	}

	d.fd, d.rows = c.fd, c.rows
	// ponytail: this is the part of showing a diff that is still on the Update
	// path. SetContentLines walks every line to find the longest — about 0.4µs
	// a line, so 4ms at ten thousand and 40ms at a hundred thousand, paid again
	// on every stage keystroke, which reloads the file. It is a fortieth of the
	// render it replaced. Bound it the day a file that large is worth showing.
	d.vp.SetContentLines(c.lines)
	if same {
		d.cursor = cursor
	}
	d.clampCursor()
}

// Selection reports what the cursor is on. line is -1 on a hunk header row,
// meaning the whole hunk. ok is false when there is nothing stageable.
func (d *Diff) Selection() (fd git.FileDiff, hunk, line int, ok bool) {
	if d.cursor < 0 || d.cursor >= len(d.rows) {
		return git.FileDiff{}, 0, 0, false
	}
	r := d.rows[d.cursor]
	return d.fd, r.hunk, r.line, true
}

// MoveBy moves the cursor, scrolling only as far as it takes to keep it on
// screen. The viewport's own scroll methods are not used for navigation: the
// cursor is what staging acts on, so a view that can scroll away from it would
// stage a line the user cannot see.
func (d *Diff) MoveBy(n int) {
	d.cursor += n
	d.clampCursor()
}

// SelectRow puts the cursor on a visible row, counted from the top of the
// pane: a click reports where on screen it landed, and the diff may be
// scrolled under it. A row past the end of the diff is ignored rather than
// clamped, so clicking the blank space below a short diff selects nothing.
func (d *Diff) SelectRow(row int) {
	if row < 0 || row >= d.vp.Height() {
		return
	}
	if i := d.vp.YOffset() + row; i < len(d.rows) {
		d.cursor = i
		d.clampCursor()
	}
}

func (d *Diff) HalfPageDown() { d.MoveBy(d.halfPage()) }
func (d *Diff) HalfPageUp()   { d.MoveBy(-d.halfPage()) }
func (d *Diff) Top()          { d.cursor = 0; d.clampCursor() }
func (d *Diff) Bottom()       { d.cursor = len(d.rows) - 1; d.clampCursor() }

func (d *Diff) halfPage() int {
	if h := d.vp.Height() / 2; h > 0 {
		return h
	}
	return 1
}

func (d *Diff) clampCursor() {
	if d.cursor >= len(d.rows) {
		d.cursor = len(d.rows) - 1
	}
	if d.cursor < 0 {
		d.cursor = 0
	}
	d.scrollToCursor()
}

// scrollToCursor keeps the cursor inside the visible window, moving the view
// by the minimum. viewport.EnsureVisible would do this in one call but parks
// the target line at the top of the pane, so a single j at the bottom edge
// jumps a whole page.
func (d *Diff) scrollToCursor() {
	h := d.vp.Height()
	if h <= 0 {
		return
	}
	switch off := d.vp.YOffset(); {
	case d.cursor < off:
		d.vp.SetYOffset(d.cursor)
	case d.cursor >= off+h:
		d.vp.SetYOffset(d.cursor - h + 1)
	}
}

func (d *Diff) Title() string { return d.title }

func (d *Diff) View() string {
	switch {
	case d.err != nil:
		return theme.Err.Render("git error: " + d.err.Error())
	// Loading with rows still on screen falls through: they are the last
	// answer for this same diff, kept until the new one lands.
	case d.loading && d.vp.TotalLineCount() == 0:
		return theme.Dim.Render("loading…")
	case d.vp.TotalLineCount() == 0:
		return theme.Dim.Render(d.empty)
	}

	// The cursor is the whole row, under the same background the file list
	// uses. renderLine has already rewritten the resets inside each line so
	// that the background survives them; without that the row would light up
	// only as far as its first coloured token.
	//
	// Inline, because Width on its own word-wraps, and a wrapped row would
	// desynchronise the viewport's one-line-per-index mapping. The padding runs
	// past the width by the horizontal offset, which the viewport's cut to the
	// visible columns then takes back off the front; the offset is zero until
	// sideways scrolling is bound to a key.
	//
	// The closure captures the row number by value: Model is copied on every
	// Update, so one holding a pointer into this struct would mark a row
	// several keystrokes stale.
	cursor := d.cursor
	// An empty style leaves the row untouched, which is the point: an unselected
	// row is closed by the reset renderLine put at its end, not by anything
	// here.
	plain := lipgloss.NewStyle()
	selected := theme.Selected.Inline(true).Width(d.vp.Width() + d.vp.XOffset())
	d.vp.StyleLineFunc = func(i int) lipgloss.Style {
		if i == cursor {
			return selected
		}
		return plain
	}
	return d.vp.View()
}

// The three +/- prefixes are constant text in a constant style, so they are
// rendered once rather than once per line. A large diff is hundreds of
// thousands of lines of this.
var (
	addPrefix     = theme.Add.Render("+")
	delPrefix     = theme.Del.Render("-")
	contextPrefix = theme.Context.Render(" ")
)

// numWidth is the width of each line-number column, wide enough for a
// five-figure file without reflowing.
const numWidth = 4

// gutterWidth covers both columns and the space between them.
const gutterWidth = numWidth*2 + 1

// renderLines returns one rendered line and one rowRef per row, in step.
//
// The slice it returns is handed straight to viewport.SetContentLines, which
// takes ownership of it — it splits embedded newlines in place — so a caller
// that ever caches these lines has to hand over a copy.
func renderLines(fd git.FileDiff) ([]string, []rowRef) {
	hl := highlight.For(fd.Path)

	var lines []string
	var rows []rowRef
	for hi, h := range fd.Hunks {
		lines = append(lines, keepBackground(theme.Meta.Render(h.Header)))
		rows = append(rows, rowRef{hunk: hi, line: -1})
		for li, l := range h.Lines {
			lines = append(lines, renderLine(hl, l))
			rows = append(rows, rowRef{hunk: hi, line: li})
		}
	}
	return lines, rows
}

// keepBackground makes one finished row survive being wrapped in a background
// by the selection, and closes it off again at the end.
//
// The rewrite is done here rather than where the row is selected because the
// viewport styles a row through a lipgloss.Style, which cannot reach inside the
// string. Here it costs one pass per line, in the command goroutine that
// already rendered the diff, rather than one per frame.
//
// The trailing reset is not decorative: with the interior resets rewritten,
// nothing else closes the line's own colours.
func keepBackground(row string) string {
	return theme.KeepBackground(row) + ansi.ResetStyle
}

func renderLine(hl *highlight.Highlighter, l git.Line) string {
	if l.Kind == git.LineNoEOL {
		return keepBackground(theme.Dim.Render(strings.Repeat(" ", gutterWidth+1) + "\\ " + l.Text))
	}

	// Syntax-highlight the payload only. The +/- prefix is not part of the
	// language: a leading '-' lexes as an operator and corrupts the rest of
	// the line's tokens.
	body := hl.Line(l.Text)

	prefix := contextPrefix
	switch l.Kind {
	case git.LineAdd:
		prefix = addPrefix
	case git.LineDel:
		prefix = delPrefix
	}

	// Two columns, old then new. A single column would have to show the old
	// number for a deletion and the new number for everything else, so a
	// deletion and the addition replacing it both render as "3" — two rows
	// claiming the same line number, in different files. That ambiguity is
	// cosmetic now and dangerous in M2, where line-level staging keys off
	// exactly this column.
	gutter := num(l.OldNum) + " " + num(l.NewNum)

	return keepBackground(theme.Dim.Render(gutter) + prefix + body)
}

func num(n int) string {
	if n == 0 {
		return strings.Repeat(" ", numWidth)
	}
	return fmt.Sprintf("%*d", numWidth, n)
}
