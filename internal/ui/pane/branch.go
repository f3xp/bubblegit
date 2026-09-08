package pane

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/f3xp/bubblegit/internal/git"
	"github.com/f3xp/bubblegit/internal/ui/theme"
)

// dateFormat is absolute rather than relative ("3 days ago").
//
// A relative date is the nicer thing to read and it is not free: it depends on
// time.Now(), so every golden file in this repo would change with the calendar
// and the pane would need a clock injected to be testable. That machinery buys
// nothing else, so the date stays absolute until something else needs a clock.
const dateFormat = "2006-01-02"

// Branches lists the local branches.
type Branches struct {
	list
	branches []git.Branch
}

// SetBranches replaces the list, keeping the cursor on the same branch where
// it still exists. A reload after a commit must not walk the selection off the
// branch the user was reading.
//
// The fallback is the current branch rather than row zero, which covers both
// the first read and a branch that has since been deleted or renamed: where
// the user is standing is a better answer than whatever sorts first.
func (b *Branches) SetBranches(branches []git.Branch) {
	want := b.SelectedName()
	b.branches = branches

	b.cursor = 0
	for i, br := range branches {
		if br.Name == want || (want == "" && br.Current) {
			b.cursor = i
			break
		}
	}
	if want != "" && b.SelectedName() != want {
		for i, br := range branches {
			if br.Current {
				b.cursor = i
				break
			}
		}
	}
	b.setLen(len(branches))
}

func (b *Branches) Selected() (git.Branch, bool) {
	if b.cursor < 0 || b.cursor >= len(b.branches) {
		return git.Branch{}, false
	}
	return b.branches[b.cursor], true
}

// Near is the tip SHAs within k rows of the cursor, for reading ahead of it.
func (b *Branches) Near(k int) []string {
	var out []string
	for _, i := range b.near(k) {
		out = append(out, b.branches[i].SHA)
	}
	return out
}

func (b *Branches) SelectedName() string {
	br, ok := b.Selected()
	if !ok {
		return ""
	}
	return br.Name
}

func (b *Branches) View() string {
	if len(b.branches) == 0 {
		// Not "no branches": a repository always has the one HEAD points at,
		// so an empty list here means the first commit has not landed yet.
		return theme.Dim.Render("no branches yet")
	}

	start, end := b.window()
	rows := make([]string, 0, end-start)
	for i := start; i < end; i++ {
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
		return theme.SelectRow(line, b.width)
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
