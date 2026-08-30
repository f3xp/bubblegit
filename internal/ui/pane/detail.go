package pane

import (
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/f3xp/bubblegit/internal/git"
	"github.com/f3xp/bubblegit/internal/ui/theme"
)

// Detail shows one commit: its metadata, its message, and its patch.
//
// Unlike Diff it carries no cursor. Nothing is stageable from history, so a
// row the user can act on would be a promise the pane cannot keep; this one
// scrolls as a document.
type Detail struct {
	vp  viewport.Model
	sha string

	loading bool
	err     error
	empty   string
}

func NewDetail() Detail {
	vp := viewport.New()
	// Same reason as the diff pane: a wrapped line desynchronises the rendered
	// line count from the real one, and the two panes have to scroll alike.
	vp.SoftWrap = false
	vp.MouseWheelEnabled = true
	return Detail{vp: vp, empty: "no commit selected"}
}

func (d *Detail) SetSize(w, h int) {
	d.vp.SetWidth(w)
	d.vp.SetHeight(h)
	d.clampOffset()
}

// SetLoading clears the body while a new commit is in flight, so a stale patch
// is never on screen under a new commit's title. sha is the full object name,
// not the abbreviation: it is what SetDetail compares against to recognise a
// re-read of the same commit.
//
// The scroll offset is deliberately kept, the way the diff pane keeps its
// cursor row. Re-reading the same commit has to land where the reader left it.
func (d *Detail) SetLoading(sha string) {
	d.loading = true
	d.err = nil
	d.sha = sha
	d.vp.SetContent("")
}

func (d *Detail) SetError(err error) {
	d.loading = false
	d.err = err
}

func (d *Detail) SetEmpty(reason string) {
	d.loading = false
	d.err = nil
	d.empty = reason
	d.sha = ""
	d.vp.SetContent("")
	d.vp.SetYOffset(0)
}

// Title is the abbreviated SHA. The full one is 40 characters of a pane title
// that has a path's worth of room, and the log pane beside it already names
// the commit in git's own short form.
func (d *Detail) Title() string { return abbrev(d.sha) }

func (d *Detail) SetDetail(det git.Detail) {
	d.loading = false
	d.err = nil

	// Re-reading the same commit — after a window resize, say — must not throw
	// the reader back to the top of a long patch.
	same := det.Commit.SHA == d.sha
	off := d.vp.YOffset()
	d.sha = det.Commit.SHA

	d.vp.SetContentLines(detailLines(det))
	if same {
		d.vp.SetYOffset(off)
	} else {
		d.vp.SetYOffset(0)
	}
	d.clampOffset()
}

func (d *Detail) Update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	d.vp, cmd = d.vp.Update(msg)
	return cmd
}

func (d *Detail) MoveBy(n int) {
	d.vp.SetYOffset(d.vp.YOffset() + n)
	d.clampOffset()
}

func (d *Detail) HalfPageDown() { d.MoveBy(d.halfPage()) }
func (d *Detail) HalfPageUp()   { d.MoveBy(-d.halfPage()) }
func (d *Detail) Top()          { d.vp.SetYOffset(0) }
func (d *Detail) Bottom()       { d.vp.SetYOffset(d.maxOffset()) }

func (d *Detail) halfPage() int {
	if h := d.vp.Height() / 2; h > 0 {
		return h
	}
	return 1
}

func (d *Detail) maxOffset() int {
	if n := d.vp.TotalLineCount() - d.vp.Height(); n > 0 {
		return n
	}
	return 0
}

func (d *Detail) clampOffset() {
	switch off := d.vp.YOffset(); {
	case off < 0:
		d.vp.SetYOffset(0)
	case off > d.maxOffset():
		d.vp.SetYOffset(d.maxOffset())
	}
}

func (d *Detail) View() string {
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

// detailLines renders the header, the message and every file's patch.
//
// The patch bodies go through the same renderLines the diff pane uses, so a
// commit's diff is highlighted and gutter-numbered exactly like a working-tree
// one. The row map it also returns is dropped: there is nothing to stage here.
func detailLines(det git.Detail) []string {
	c := det.Commit

	lines := []string{
		theme.Meta.Render("commit " + c.SHA),
		theme.Dim.Render("Author: ") + c.Author + theme.Dim.Render(" <"+det.Email+">"),
		theme.Dim.Render("Date:   ") + c.When.Format("2006-01-02 15:04:05 -0700"),
	}
	if len(c.Parents) > 0 {
		lines = append(lines, theme.Dim.Render("Parent: "+strings.Join(shorten(c.Parents), " ")))
	}

	lines = append(lines, "")
	for _, l := range strings.Split(det.Message, "\n") {
		lines = append(lines, theme.Context.Render(l))
	}

	if len(det.Files) == 0 {
		// A commit that changes nothing is legal — `--allow-empty`, or a merge
		// whose first-parent diff is empty — and a blank pane below a header
		// reads as a failed load rather than as the answer.
		return append(lines, "", theme.Dim.Render("no file changes"))
	}

	for _, fd := range det.Files {
		lines = append(lines, "", theme.Title.Render(fd.Path))
		switch {
		case fd.Binary:
			lines = append(lines, theme.Dim.Render("binary file — no textual diff"))
		case fd.IsEmpty():
			// A mode change carries no hunks at all.
			lines = append(lines, theme.Dim.Render("no textual change"))
		default:
			body, _ := renderLines(fd)
			lines = append(lines, body...)
		}
	}
	return lines
}

// abbrevWidth is the width git uses for %h.
const abbrevWidth = 7

func abbrev(sha string) string {
	if len(sha) > abbrevWidth {
		return sha[:abbrevWidth]
	}
	return sha
}

func shorten(shas []string) []string {
	out := make([]string, len(shas))
	for i, s := range shas {
		out[i] = abbrev(s)
	}
	return out
}
