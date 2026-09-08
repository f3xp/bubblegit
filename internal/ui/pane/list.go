package pane

// list is the cursor and scroll window every list pane shares: n rows, a
// cursor into them, and the offset of the first row on screen. A pane embeds
// it and sets n whenever its rows change; what a row looks like stays with the
// pane, since no two of them render anything alike.
//
// Files does not embed it. Its rows include section headers the cursor must
// skip, and a cursor that has to override every motion gains nothing from
// inheriting them.
type list struct {
	n      int
	cursor int
	offset int // first visible row, for scrolling
	width  int
	height int
}

func (l *list) SetSize(w, h int) { l.width, l.height = w, h; l.clampOffset() }

func (l *list) Len() int { return l.n }

// setLen records a new row count and keeps the cursor inside it.
func (l *list) setLen(n int) { l.n = n; l.clampCursor() }

func (l *list) MoveBy(delta int) { l.cursor += delta; l.clampCursor() }
func (l *list) MoveTo(i int)     { l.cursor = i; l.clampCursor() }

// SelectRow puts the cursor on a visible row, counted from the top of the
// pane rather than from the top of the list: a click reports where on screen
// it landed, and the list may be scrolled under it.
//
// A row past the end of the list is ignored rather than clamped to the last
// one. Clicking the empty space below a two-row list is not a request to
// select the second row.
func (l *list) SelectRow(row int) {
	if row < 0 || row >= l.height || l.offset+row >= l.n {
		return
	}
	l.MoveTo(l.offset + row)
}

// near is the rows within k of the cursor on either side, nearest first per
// side, for reading ahead of the cursor.
func (l *list) near(k int) []int {
	var out []int
	for d := 1; d <= k; d++ {
		if i := l.cursor - d; i >= 0 {
			out = append(out, i)
		}
		if i := l.cursor + d; i < l.n {
			out = append(out, i)
		}
	}
	return out
}

func (l *list) Top()    { l.MoveTo(0) }
func (l *list) Bottom() { l.MoveTo(l.n - 1) }

func (l *list) HalfPageDown() { l.MoveBy(l.halfPage()) }
func (l *list) HalfPageUp()   { l.MoveBy(-l.halfPage()) }

func (l *list) halfPage() int {
	if h := l.height / 2; h > 0 {
		return h
	}
	return 1
}

// window is the range of rows on screen, [start, end).
func (l *list) window() (start, end int) {
	end = l.offset + l.height
	if end > l.n {
		end = l.n
	}
	return l.offset, end
}

func (l *list) clampCursor() {
	if l.cursor >= l.n {
		l.cursor = l.n - 1
	}
	if l.cursor < 0 {
		l.cursor = 0
	}
	l.clampOffset()
}

// clampOffset keeps the cursor inside the visible window.
func (l *list) clampOffset() {
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
	if max := l.n - l.height; l.offset > max {
		l.offset = max
	}
	if l.offset < 0 {
		l.offset = 0
	}
}
