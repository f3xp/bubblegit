// Package pane holds the individual UI panes. Panes are plain structs rather
// than tea.Models: the app owns message dispatch, so a pane is a pure
// function of its state and never issues commands of its own.
package pane

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/f3xp/bubblegit/internal/git"
	"github.com/f3xp/bubblegit/internal/ui/theme"
)

// Files lists the working tree.
type Files struct {
	files  []git.FileStatus
	cursor int
	offset int // first visible row, for scrolling
	height int
	width  int
}

func (f *Files) SetSize(w, h int) { f.width, f.height = w, h; f.clampOffset() }

// SetFiles replaces the list, keeping the cursor on the same path where it
// still exists. Without this, staging a file would jump the cursor to an
// unrelated row every time the list refreshes.
func (f *Files) SetFiles(files []git.FileStatus) {
	var want string
	if sel, ok := f.Selected(); ok {
		want = sel.Path
	}
	f.files = files
	f.cursor = 0
	for i, file := range files {
		if file.Path == want {
			f.cursor = i
			break
		}
	}
	f.clampOffset()
}

func (f *Files) Len() int { return len(f.files) }

func (f *Files) Selected() (git.FileStatus, bool) {
	if f.cursor < 0 || f.cursor >= len(f.files) {
		return git.FileStatus{}, false
	}
	return f.files[f.cursor], true
}

func (f *Files) MoveBy(delta int) {
	f.cursor += delta
	f.clampCursor()
}

func (f *Files) MoveTo(i int) { f.cursor = i; f.clampCursor() }

// SelectRow puts the cursor on a visible row, counted from the top of the
// pane rather than from the top of the list: a click reports where on screen
// it landed, and the list may be scrolled under it.
//
// A row past the end of the list is ignored rather than clamped to the last
// one. Clicking the empty space below a two-file list is not a request to
// select the second file.
func (f *Files) SelectRow(row int) {
	if row < 0 || row >= f.height || f.offset+row >= len(f.files) {
		return
	}
	f.MoveTo(f.offset + row)
}

func (f *Files) Top()    { f.MoveTo(0) }
func (f *Files) Bottom() { f.MoveTo(len(f.files) - 1) }

func (f *Files) clampCursor() {
	if f.cursor >= len(f.files) {
		f.cursor = len(f.files) - 1
	}
	if f.cursor < 0 {
		f.cursor = 0
	}
	f.clampOffset()
}

// clampOffset keeps the cursor inside the visible window.
func (f *Files) clampOffset() {
	if f.height <= 0 {
		f.offset = 0
		return
	}
	if f.cursor < f.offset {
		f.offset = f.cursor
	}
	if f.cursor >= f.offset+f.height {
		f.offset = f.cursor - f.height + 1
	}
	if max := len(f.files) - f.height; f.offset > max {
		f.offset = max
	}
	if f.offset < 0 {
		f.offset = 0
	}
}

func (f *Files) View() string {
	if len(f.files) == 0 {
		return theme.Dim.Render("working tree clean")
	}

	end := f.offset + f.height
	if end > len(f.files) {
		end = len(f.files)
	}

	rows := make([]string, 0, end-f.offset)
	for i := f.offset; i < end; i++ {
		rows = append(rows, f.row(i))
	}
	return strings.Join(rows, "\n")
}

func (f *Files) row(i int) string {
	file := f.files[i]

	// Two columns, index change then worktree change, coloured separately so
	// a partially staged file reads at a glance.
	code := theme.Staged.Render(statusRune(file.Staged)) +
		theme.Unstaged.Render(statusRune(file.Unstaged))

	path := file.Path
	if file.OrigPath != "" {
		path = file.OrigPath + " → " + path
	}

	line := code + " " + path
	// Truncate on display width, not byte length: the line carries ANSI
	// escapes and the path may contain wide characters.
	if f.width > 0 {
		line = ansi.Truncate(line, f.width, "…")
	}
	if i == f.cursor {
		return theme.Selected.Width(f.width).Render(line)
	}
	return lipgloss.NewStyle().Width(f.width).Render(line)
}

// statusRune renders one half of git's XY code. Untracked files carry no XY
// at all, so they are given a marker of their own rather than a blank.
func statusRune(c byte) string {
	switch c {
	case 0:
		return "?"
	case '.':
		return " "
	default:
		return string(c)
	}
}
