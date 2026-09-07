// Package pane holds the individual UI panes. Panes are plain structs rather
// than tea.Models: the app owns message dispatch, so a pane is a pure
// function of its state and never issues commands of its own.
package pane

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/f3xp/bubblegit/internal/git"
	"github.com/f3xp/bubblegit/internal/ui/theme"
)

// Files lists the working tree in two sections, tracked and untracked, each
// under a heading row.
//
// The cursor indexes rows, not files, and never rests on a heading: a heading
// is a label, and nothing the keys do means anything on one. Three facts
// about the rows keep that cheap — row 0 is always a heading, the last row is
// always a file, and two headings are never adjacent — so a cursor that lands
// on a heading is always one step from a file in the direction it was going.
type Files struct {
	files []git.FileStatus
	stats map[string]git.LineStat
	rows  []fileRow

	cursor int
	offset int // first visible row, for scrolling
	height int
	width  int
}

// fileRow is one row of the pane: a heading, or an index into files.
type fileRow struct {
	header string
	file   int // -1 on a heading
}

func (f *Files) SetSize(w, h int) { f.width, f.height = w, h; f.clampOffset() }

// SetFiles replaces the list, keeping the cursor on the same record where it
// still exists. Without this, staging a file would jump the cursor to an
// unrelated row every time the list refreshes.
//
// The same record first, the same path second. Git reports a file removed
// from the index and still on disk as two records with one path, one in each
// section, and the cursor should stay on the one it was on; but staging an
// untracked file moves its only record across the line, and the cursor should
// follow it there rather than fall back to the top.
func (f *Files) SetFiles(files []git.FileStatus) {
	var want string
	wantUntracked := false
	if sel, ok := f.Selected(); ok {
		want, wantUntracked = sel.Path, sel.IsUntracked()
	}
	f.files = files

	f.rows = f.rows[:0]
	for _, section := range []struct {
		header    string
		untracked bool
	}{{"Tracked", false}, {"Untracked", true}} {
		start := len(f.rows)
		for i, file := range files {
			if file.IsUntracked() == section.untracked {
				f.rows = append(f.rows, fileRow{file: i})
			}
		}
		if len(f.rows) > start {
			f.rows = append(f.rows[:start], append([]fileRow{{header: section.header, file: -1}}, f.rows[start:]...)...)
		}
	}

	f.cursor = 0
	for _, exact := range []bool{true, false} {
		for i, row := range f.rows {
			if row.file < 0 || files[row.file].Path != want {
				continue
			}
			if !exact || files[row.file].IsUntracked() == wantUntracked {
				f.cursor = i
				f.settle(1)
				return
			}
		}
	}
	f.settle(1)
}

// SetStats gives the tracked rows their line counts.
func (f *Files) SetStats(stats map[string]git.LineStat) { f.stats = stats }

// Len is the number of files, not rows: it is what the title counts.
func (f *Files) Len() int { return len(f.files) }

// At is the i'th file, in status order.
func (f *Files) At(i int) git.FileStatus { return f.files[i] }

func (f *Files) Selected() (git.FileStatus, bool) {
	if f.cursor < 0 || f.cursor >= len(f.rows) || f.rows[f.cursor].file < 0 {
		return git.FileStatus{}, false
	}
	return f.files[f.rows[f.cursor].file], true
}

func (f *Files) MoveBy(delta int) {
	f.cursor += delta
	f.clampCursor()
	dir := 1
	if delta < 0 {
		dir = -1
	}
	f.settle(dir)
}

func (f *Files) MoveTo(i int) { f.cursor = i; f.clampCursor(); f.settle(1) }

// SelectRow puts the cursor on a visible row, counted from the top of the
// pane rather than from the top of the list: a click reports where on screen
// it landed, and the list may be scrolled under it.
//
// A row past the end of the list is ignored rather than clamped to the last
// one, and so is a heading: neither is a request for the file nearest it.
func (f *Files) SelectRow(row int) {
	if row < 0 || row >= f.height || f.offset+row >= len(f.rows) || f.isHeader(f.offset+row) {
		return
	}
	f.MoveTo(f.offset + row)
}

func (f *Files) Top()    { f.MoveTo(0) }
func (f *Files) Bottom() { f.MoveTo(len(f.rows) - 1) }

func (f *Files) HalfPageDown() { f.MoveBy(f.halfPage()) }
func (f *Files) HalfPageUp()   { f.MoveBy(-f.halfPage()) }

func (f *Files) halfPage() int {
	if h := f.height / 2; h > 0 {
		return h
	}
	return 1
}

func (f *Files) isHeader(i int) bool {
	return i >= 0 && i < len(f.rows) && f.rows[i].file < 0
}

// settle steps a cursor that landed on a heading onto the file beside it, in
// the direction it was travelling. Above the first heading there is nothing,
// so a cursor pushed up past it comes back down to the first file.
func (f *Files) settle(dir int) {
	if f.isHeader(f.cursor) {
		f.cursor += dir
	}
	if f.cursor < 0 {
		f.cursor = 1
	}
	f.clampOffset()
}

func (f *Files) clampCursor() {
	if f.cursor >= len(f.rows) {
		f.cursor = len(f.rows) - 1
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
	if max := len(f.rows) - f.height; f.offset > max {
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
	if end > len(f.rows) {
		end = len(f.rows)
	}

	rows := make([]string, 0, end-f.offset)
	for i := f.offset; i < end; i++ {
		rows = append(rows, f.row(i))
	}
	return strings.Join(rows, "\n")
}

// minStatRoom is the narrowest a row may get before the line counts are
// dropped for the path's sake.
const minStatRoom = 6

func (f *Files) row(i int) string {
	if h := f.rows[i].header; h != "" {
		return lipgloss.NewStyle().Width(f.width).Render(theme.Dim.Render(h))
	}
	file := f.files[f.rows[i].file]

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
		room := f.width
		// The line counts sit against the right edge. An untracked record
		// has none, even when a tracked record of the same path does.
		stat := ""
		if s, ok := f.stats[file.Path]; ok && !file.IsUntracked() {
			stat = theme.Add.Render("+"+strconv.Itoa(s.Added)) + " " + theme.Del.Render("−"+strconv.Itoa(s.Deleted))
			if r := f.width - ansi.StringWidth(stat) - 1; r >= minStatRoom {
				room = r
			} else {
				stat = ""
			}
		}
		line = ansi.Truncate(line, room, "…")
		if stat != "" {
			line = lipgloss.NewStyle().Width(room).Render(line) + " " + stat
		}
	}
	if i == f.cursor {
		return theme.SelectRow(line, f.width)
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
