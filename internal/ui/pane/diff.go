package pane

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/f3xp/bubblegit/internal/git"
	"github.com/f3xp/bubblegit/internal/highlight"
	"github.com/f3xp/bubblegit/internal/ui/theme"
)

// Diff renders one file's diff.
type Diff struct {
	vp    viewport.Model
	title string

	loading bool
	err     error
	empty   string // why there is nothing to show, when there is nothing
}

func NewDiff() Diff {
	vp := viewport.New()
	// Horizontal scrolling rather than wrapping: a wrapped diff line silently
	// desynchronises the visual line count from the real one, which makes
	// line-level staging in M2 point at the wrong line.
	vp.SoftWrap = false
	vp.MouseWheelEnabled = true
	return Diff{vp: vp, empty: "no file selected"}
}

func (d *Diff) SetSize(w, h int) {
	d.vp.SetWidth(w)
	d.vp.SetHeight(h)
}

// SetLoading clears the body while a new diff is in flight.
//
// This is not cosmetic. With a ~14ms process spawn, holding j in the file
// list leaves the previous file's diff on screen for a visible beat — correct
// data, rendered against the wrong filename, which reads as a bug.
func (d *Diff) SetLoading(title string) {
	d.loading = true
	d.err = nil
	d.title = title
	d.vp.SetContent("")
	d.vp.GotoTop()
}

func (d *Diff) SetError(err error) {
	d.loading = false
	d.err = err
}

func (d *Diff) SetEmpty(reason string) {
	d.loading = false
	d.err = nil
	d.empty = reason
	d.vp.SetContent("")
}

func (d *Diff) SetDiff(fd git.FileDiff) {
	d.loading = false
	d.err = nil
	d.title = fd.Path

	switch {
	case fd.Binary:
		d.empty = "binary file — no textual diff"
		d.vp.SetContent("")
		return
	case fd.IsEmpty():
		d.empty = "no changes"
		d.vp.SetContent("")
		return
	}

	d.vp.SetContentLines(renderLines(fd))
	d.vp.GotoTop()
}

func (d *Diff) Update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	d.vp, cmd = d.vp.Update(msg)
	return cmd
}

func (d *Diff) ScrollBy(n int) {
	if n > 0 {
		d.vp.ScrollDown(n)
		return
	}
	d.vp.ScrollUp(-n)
}

func (d *Diff) HalfPageDown() { d.vp.HalfPageDown() }
func (d *Diff) HalfPageUp()   { d.vp.HalfPageUp() }
func (d *Diff) Top()          { d.vp.GotoTop() }
func (d *Diff) Bottom()       { d.vp.GotoBottom() }

func (d *Diff) Title() string { return d.title }

func (d *Diff) View() string {
	switch {
	case d.err != nil:
		return theme.Err.Render("git error: " + d.err.Error())
	case d.loading:
		return theme.Dim.Render("loading…")
	case d.vp.TotalLineCount() == 0:
		return theme.Dim.Render(d.empty)
	}
	return d.vp.View()
}

// gutterWidth is the width of the line-number column, wide enough for a
// six-figure file without reflowing.
const gutterWidth = 5

func renderLines(fd git.FileDiff) []string {
	hl := highlight.For(fd.Path)

	var lines []string
	for _, h := range fd.Hunks {
		lines = append(lines, theme.Meta.Render(h.Header))
		for _, l := range h.Lines {
			lines = append(lines, renderLine(hl, l))
		}
	}
	return lines
}

func renderLine(hl *highlight.Highlighter, l git.Line) string {
	if l.Kind == git.LineNoEOL {
		return theme.Dim.Render(strings.Repeat(" ", gutterWidth+1) + "\\ " + l.Text)
	}

	// Syntax-highlight the payload only. The +/- prefix is not part of the
	// language: a leading '-' lexes as an operator and corrupts the rest of
	// the line's tokens.
	body := hl.Line(l.Text)

	var gutter, prefix string
	var style = theme.Context
	switch l.Kind {
	case git.LineAdd:
		gutter, prefix, style = num(l.NewNum), "+", theme.Add
	case git.LineDel:
		gutter, prefix, style = num(l.OldNum), "-", theme.Del
	default:
		gutter, prefix = num(l.NewNum), " "
	}

	return theme.Dim.Render(gutter) + style.Render(prefix) + body
}

func num(n int) string {
	if n == 0 {
		return strings.Repeat(" ", gutterWidth)
	}
	return fmt.Sprintf("%*d", gutterWidth, n)
}
