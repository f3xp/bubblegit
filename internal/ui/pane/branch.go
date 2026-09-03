package pane

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/f3xp/bubblegit/internal/git"
	"github.com/f3xp/bubblegit/internal/ui/theme"
)

// Branches lists the local branches.
//
// ponytail: this is the third pane carrying its own copy of the same
// cursor/offset/clamp arithmetic (see Files and Log). Two copies were a
// coincidence; three is the point at which a shared embedded list type pays
// for itself. Extract it when a fourth pane wants one, not before — the three
// rows render nothing alike, so only the scrolling would be shared.
type Branches struct {
	branches []git.Branch
	cursor   int
	offset   int
	width    int
	height   int
}

func (b *Branches) SetSize(w, h int) { b.width, b.height = w, h; b.clampOffset() }

func (b *Branches) Len() int { return len(b.branches) }

// SetBranches replaces the list, keeping the cursor on the same branch where
// it still exists. A reload after a commit must not walk the selection off the
// branch the user was reading.
//
// The fallback is the current branch rather than row zero, which covers both
// the first read and a branch that has since been deleted or renamed: where
// the user is standing is a better answer than whatever sorts first.
func (b *Branches) SetBranches(list []git.Branch) {
	want := b.SelectedName()
	b.branches = list

	b.cursor = 0
	for i, br := range list {
		if br.Name == want || (want == "" && br.Current) {
			b.cursor = i
			break
		}
	}
	if want != "" && b.SelectedName() != want {
		for i, br := range list {
			if br.Current {
				b.cursor = i
				break
			}
		}
	}
	b.clampOffset()
}

func (b *Branches) Selected() (git.Branch, bool) {
	if b.cursor < 0 || b.cursor >= len(b.branches) {
		return git.Branch{}, false
	}
	return b.branches[b.cursor], true
}

func (b *Branches) SelectedName() string {
	br, ok := b.Selected()
	if !ok {
		return ""
	}
	return br.Name
}

func (b *Branches) MoveBy(delta int) { b.cursor += delta; b.clampCursor() }
func (b *Branches) MoveTo(i int)     { b.cursor = i; b.clampCursor() }
func (b *Branches) Top()             { b.MoveTo(0) }
func (b *Branches) Bottom()          { b.MoveTo(len(b.branches) - 1) }

func (b *Branches) HalfPageDown() { b.MoveBy(b.halfPage()) }
func (b *Branches) HalfPageUp()   { b.MoveBy(-b.halfPage()) }

func (b *Branches) halfPage() int {
	if h := b.height / 2; h > 0 {
		return h
	}
	return 1
}

func (b *Branches) clampCursor() {
	if b.cursor >= len(b.branches) {
		b.cursor = len(b.branches) - 1
	}
	if b.cursor < 0 {
		b.cursor = 0
	}
	b.clampOffset()
}

// clampOffset keeps the cursor inside the visible window.
func (b *Branches) clampOffset() {
	if b.height <= 0 {
		b.offset = 0
		return
	}
	if b.cursor < b.offset {
		b.offset = b.cursor
	}
	if b.cursor >= b.offset+b.height {
		b.offset = b.cursor - b.height + 1
	}
	if max := len(b.branches) - b.height; b.offset > max {
		b.offset = max
	}
	if b.offset < 0 {
		b.offset = 0
	}
}

func (b *Branches) View() string {
	if len(b.branches) == 0 {
		// Not "no branches": a repository always has the one HEAD points at,
		// so an empty list here means the first commit has not landed yet.
		return theme.Dim.Render("no branches yet")
	}

	end := b.offset + b.height
	if end > len(b.branches) {
		end = len(b.branches)
	}

	rows := make([]string, 0, end-b.offset)
	for i := b.offset; i < end; i++ {
		rows = append(rows, b.row(i))
	}
	return strings.Join(rows, "\n")
}

func (b *Branches) row(i int) string {
	br := b.branches[i]

	name := br.Name
	marker := "  "
	if br.Current {
		// The checked-out branch is marked and bold rather than only bold:
		// a bold row is not distinguishable from a plain one under the
		// selection background.
		marker = theme.Add.Render("* ")
		name = theme.Title.Render(name)
	}

	line := marker + name + trackCell(br) + " " +
		theme.Dim.Render(br.When.Format(dateFormat)) + " " + br.Subject

	// Truncate on display width, not byte length: the line carries ANSI
	// escapes and a branch name or subject may contain wide characters.
	if b.width > 0 {
		line = ansi.Truncate(line, b.width, "…")
	}
	if i == b.cursor {
		return theme.Selected.Width(b.width).Render(line)
	}
	return lipgloss.NewStyle().Width(b.width).Render(line)
}

// trackCell renders how far the branch has drifted from its upstream.
//
// A branch with no upstream gets nothing at all, and one that matches its
// upstream gets a dim "=". Those two are different facts and read as
// different rows: an empty cell means there is nothing to be in sync with.
func trackCell(br git.Branch) string {
	switch {
	case br.Gone:
		// Louder than a count, because it is the state that needs a decision:
		// the upstream this branch tracks does not exist any more.
		return " " + theme.Del.Render("gone")
	case br.Upstream == "":
		return ""
	case br.InSync():
		return " " + theme.Dim.Render("=")
	}

	var s string
	if br.Ahead > 0 {
		s += theme.Add.Render("↑" + strconv.Itoa(br.Ahead))
	}
	if br.Behind > 0 {
		s += theme.Del.Render("↓" + strconv.Itoa(br.Behind))
	}
	return " " + s
}
