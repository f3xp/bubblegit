package pane

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/f3xp/bubblegit/internal/git"
	"github.com/f3xp/bubblegit/internal/ui/theme"
)

// Stashes lists the stash, newest first.
type Stashes struct {
	list
	stashes []git.Stash
}

// SetStashes replaces the list, keeping the cursor on the same stash where it
// still exists. Stashes are matched by object name, not by position: dropping
// stash@{0} renumbers every other entry.
func (s *Stashes) SetStashes(stashes []git.Stash) {
	want := ""
	if sel, ok := s.Selected(); ok {
		want = sel.SHA
	}
	s.stashes = stashes
	s.cursor = 0
	for i, st := range stashes {
		if st.SHA == want {
			s.cursor = i
			break
		}
	}
	s.setLen(len(stashes))
}

func (s *Stashes) Selected() (git.Stash, bool) {
	if s.cursor < 0 || s.cursor >= len(s.stashes) {
		return git.Stash{}, false
	}
	return s.stashes[s.cursor], true
}

// SelectedSHA is the object name under the cursor, or empty.
// Near is the SHAs within k rows of the cursor, for reading ahead of it.
func (s *Stashes) Near(k int) []string {
	var out []string
	for _, i := range s.near(k) {
		out = append(out, s.stashes[i].SHA)
	}
	return out
}

func (s *Stashes) SelectedSHA() string {
	st, ok := s.Selected()
	if !ok {
		return ""
	}
	return st.SHA
}

func (s *Stashes) View() string {
	if len(s.stashes) == 0 {
		return theme.Dim.Render("no stashes")
	}

	start, end := s.window()
	rows := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		rows = append(rows, s.row(i))
	}
	return strings.Join(rows, "\n")
}

func (s *Stashes) row(i int) string {
	st := s.stashes[i]
	line := theme.Meta.Render(st.Ref) + " " + st.Subject
	if s.width > 0 {
		line = ansi.Truncate(line, s.width, "…")
	}
	if i == s.cursor {
		return theme.SelectRow(line, s.width)
	}
	return lipgloss.NewStyle().Width(s.width).Render(line)
}
