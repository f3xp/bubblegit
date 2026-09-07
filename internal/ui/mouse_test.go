package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/f3xp/bubblegit/internal/git"
)

// The harness builds an 80x24 frame: the app header on row 0, the key bar on
// row 23, and two bordered panes filling the rest. framed() draws a top border,
// a title, a rule, the body, then a closing border, so body row 0 is terminal
// row 4 and the last body row is 21.
//
// The views split the width differently — the status view's left pane, the
// diff, takes 52 columns and the log view's commit 40 — so the columns here are
// chosen to sit inside a body in every view rather than being derived from one
// view's split. Every view draws its document on the left and its list on the
// right, so leftBody is always in the document and rightBody always in the list.
const (
	bodyTop   = 4
	leftBody  = 3
	rightBody = 60
)

func (h *harness) click(x, y int) tea.Cmd {
	h.t.Helper()
	return h.send(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
}

func (h *harness) wheel(x, y int, button tea.MouseButton) tea.Cmd {
	h.t.Helper()
	return h.send(tea.MouseWheelMsg{X: x, Y: y, Button: button})
}

// TestHitTest pins the arithmetic that turns a terminal cell into a pane and a
// row. Every other mouse test is written in coordinates, so all of them are
// wrong together if this is wrong.
//
// The pane comes back as a role: the left pane is the document and the right
// one the list. The log view is checked last, at its own split, to pin that the
// rule holds in every view rather than only at the status view's boundary.
func TestHitTest(t *testing.T) {
	h := newHarness(t)

	for _, tc := range []struct {
		name string
		x, y int
		pane focus
		row  int
		ok   bool
	}{
		{"the app header is not a pane", 3, 0, focusList, -1, false},
		{"the key bar is not a pane", 3, 23, focusList, -1, false},
		{"below the frame", 3, 24, focusList, -1, false},
		{"right of the frame", 80, 3, focusList, -1, false},
		{"negative", -1, 3, focusList, -1, false},

		{"first body row of the left pane", leftBody, bodyTop, focusDoc, 0, true},
		{"last body row of the left pane", leftBody, 21, focusDoc, 17, true},
		{"the left pane's top border", leftBody, 1, focusDoc, -1, true},
		{"the left pane's title", leftBody, 2, focusDoc, -1, true},
		{"the rule under the left pane's title", leftBody, 3, focusDoc, -1, true},
		{"the left pane's bottom border", leftBody, 22, focusDoc, -1, true},
		{"the left pane's left border", 0, 10, focusDoc, -1, true},
		{"the left pane's right border", 51, 10, focusDoc, -1, true},

		{"the right pane's left border", 52, 10, focusList, -1, true},
		{"first body row of the right pane", 53, bodyTop, focusList, 0, true},
		{"the right pane's right border", 79, bodyTop, focusList, -1, true},
		{"the last body column of the right pane", 78, bodyTop, focusList, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hit, ok := h.m.hitTest(tc.x, tc.y)
			if ok != tc.ok {
				t.Fatalf("(%d,%d) ok=%v, want %v", tc.x, tc.y, ok, tc.ok)
			}
			if !ok {
				return
			}
			if hit.pane != tc.pane || hit.row != tc.row {
				t.Errorf("(%d,%d) landed on pane %d row %d, want pane %d row %d",
					tc.x, tc.y, hit.pane, hit.row, tc.pane, tc.row)
			}
		})
	}

	h.enterLog()
	if hit, _ := h.m.hitTest(leftBody, bodyTop); hit.pane != focusDoc {
		t.Errorf("in the log view the left pane is role %d, want the commit", hit.pane)
	}
	if hit, _ := h.m.hitTest(rightBody, bodyTop); hit.pane != focusList {
		t.Errorf("in the log view the right pane is role %d, want the list", hit.pane)
	}
}

// TestHitTestWithoutBorders covers the short terminal where layout() drops the
// pane chrome. There is no border and no title row, so the body starts
// directly under the app header, ends above the key bar, and the edge columns
// are content.
func TestHitTestWithoutBorders(t *testing.T) {
	h := newHarness(t)
	h.send(tea.WindowSizeMsg{Width: 80, Height: 4})
	if h.m.bordered() {
		t.Fatal("a four-row terminal still drew pane chrome")
	}

	for _, tc := range []struct {
		x, y int
		pane focus
		row  int
	}{
		{0, 1, focusDoc, 0},
		{leftBody, 2, focusDoc, 1},
		{52, 1, focusList, 0},
		{79, 2, focusList, 1},
	} {
		hit, ok := h.m.hitTest(tc.x, tc.y)
		if !ok {
			t.Fatalf("(%d,%d) landed outside the frame", tc.x, tc.y)
		}
		if hit.pane != tc.pane || hit.row != tc.row {
			t.Errorf("(%d,%d) landed on pane %d row %d, want pane %d row %d",
				tc.x, tc.y, hit.pane, hit.row, tc.pane, tc.row)
		}
	}
}

// TestHitTestWhenNarrow covers the one-pane layout. Both panes occupy the same
// columns there, so the click lands on whichever one is actually on screen —
// the focused one — rather than on whichever the columns would suggest.
func TestHitTestWhenNarrow(t *testing.T) {
	h := newHarness(t)
	h.send(tea.WindowSizeMsg{Width: 40, Height: 24})
	if !h.m.narrow {
		t.Fatal("a 40-column terminal is not narrow")
	}

	hit, ok := h.m.hitTest(leftBody, bodyTop)
	if !ok || hit.pane != focusList || hit.row != 0 {
		t.Fatalf("with the list showing: pane %d row %d ok %v", hit.pane, hit.row, ok)
	}

	h.send(tea.KeyPressMsg{Code: tea.KeyTab})
	if hit, _ := h.m.hitTest(leftBody, bodyTop); hit.pane != focusDoc {
		t.Errorf("with the diff showing, the same cell landed on pane %d", hit.pane)
	}
}

// TestClickSelectsFileRow is the whole point of the feature: a row is picked
// by pointing at it, and the diff for it is fetched exactly as j would.
func TestClickSelectsFileRow(t *testing.T) {
	h := newHarness(t)

	// Row 0 is the Tracked heading, so the fourth file is on row 4.
	cmd := h.click(rightBody, bodyTop+4)
	if got := h.m.SelectedPath(); got != "plain.txt" {
		t.Fatalf("clicking the fifth row selected %q, want plain.txt", got)
	}
	if cmd == nil {
		t.Fatal("the click did not request the diff for the row it selected")
	}
	h.run(cmd)
	if got := h.m.diff.Title(); got != "plain.txt" {
		t.Errorf("the diff pane holds %q, want plain.txt", got)
	}
}

// TestClickOnSelectedRowSpawnsNothing pins the shared guard: a click that does
// not move the cursor must not spawn git, the same way a j at the end of the
// list does not.
func TestClickOnSelectedRowSpawnsNothing(t *testing.T) {
	h := newHarness(t)
	if cmd := h.click(rightBody, bodyTop+1); cmd != nil {
		t.Error("clicking the already-selected row requested a diff")
	}
	if got := h.m.SelectedPath(); got != "logo.png" {
		t.Errorf("the selection moved to %q", got)
	}
}

// TestClickOnSectionHeaderIsInert: the Tracked heading is row 0 of the list.
// It is a label, so a click on it neither moves the cursor nor reads a diff.
func TestClickOnSectionHeaderIsInert(t *testing.T) {
	h := newHarness(t)
	h.press("j")
	if cmd := h.click(rightBody, bodyTop); cmd != nil {
		t.Error("clicking the heading requested a diff")
	}
	if got := h.m.SelectedPath(); got != "main.go" {
		t.Errorf("clicking the heading moved the selection to %q", got)
	}
}

// TestClickPastTheListKeepsSelection covers the empty space below the last
// row. Clicking it is not a request for the last file.
func TestClickPastTheListKeepsSelection(t *testing.T) {
	h := newHarness(t)
	if cmd := h.click(rightBody, bodyTop+15); cmd != nil {
		t.Error("clicking below the list requested a diff")
	}
	if got := h.m.SelectedPath(); got != "logo.png" {
		t.Errorf("clicking below the list selected %q, want logo.png", got)
	}
}

// TestClickMovesFocusAndDiffCursor covers the diff pane, where the row under
// the pointer is what the staging keys act on.
func TestClickMovesFocusAndDiffCursor(t *testing.T) {
	h := newHarness(t)
	h.selectFile("two-hunks.txt")

	h.click(leftBody, bodyTop+2)
	if !h.m.DiffFocused() {
		t.Fatal("clicking the diff pane did not focus it")
	}

	fd, hunk, line, ok := h.m.diff.Selection()
	if !ok {
		t.Fatal("the diff pane has no cursor")
	}
	wantHunk, wantLine := rowAt(fd, 2)
	if hunk != wantHunk || line != wantLine {
		t.Errorf("the cursor is on hunk %d line %d, want hunk %d line %d",
			hunk, line, wantHunk, wantLine)
	}
}

// TestClickOnPaneChromeOnlyTakesFocus covers a click on a border or a title. A
// pane was clicked, so it takes focus; there is no row under the pointer, so
// nothing else moves.
func TestClickOnPaneChromeOnlyTakesFocus(t *testing.T) {
	h := newHarness(t)
	h.selectFile("two-hunks.txt")
	_, _, before, _ := h.m.diff.Selection()

	if cmd := h.click(leftBody, 1); cmd != nil {
		t.Error("clicking a pane border produced a command")
	}
	if !h.m.DiffFocused() {
		t.Fatal("clicking the diff pane's border did not focus it")
	}
	if _, _, after, _ := h.m.diff.Selection(); after != before {
		t.Errorf("the diff cursor moved to line %d, want %d", after, before)
	}
}

// TestWheelScrollsUnderPointerWithoutFocus pins the wheel's one difference
// from a click: it scrolls whatever the pointer is over and leaves focus
// alone, which is what a wheel does everywhere else.
func TestWheelScrollsUnderPointerWithoutFocus(t *testing.T) {
	h := newHarness(t)
	h.selectFile("two-hunks.txt")

	h.wheel(leftBody, bodyTop, tea.MouseWheelDown)
	if h.m.DiffFocused() {
		t.Error("the wheel moved focus to the pane under the pointer")
	}

	fd, hunk, line, ok := h.m.diff.Selection()
	if !ok {
		t.Fatal("the diff pane has no cursor")
	}
	wantHunk, wantLine := rowAt(fd, wheelStep)
	if hunk != wantHunk || line != wantLine {
		t.Errorf("one notch left the cursor on hunk %d line %d, want hunk %d line %d",
			hunk, line, wantHunk, wantLine)
	}

	// And back, to prove the direction is not a coincidence.
	h.wheel(leftBody, bodyTop, tea.MouseWheelUp)
	if _, hunk, line, _ := h.m.diff.Selection(); hunk != 0 || line != -1 {
		t.Errorf("scrolling back left the cursor on hunk %d line %d, want the first hunk header", hunk, line)
	}
}

// TestWheelOverTheFileListMovesSelection covers the file list, where the
// wheel moves the cursor rather than a scroll offset — the cursor is what the
// staging keys act on, so a view that could scroll away from it would stage a
// file the user cannot see.
func TestWheelOverTheFileListMovesSelection(t *testing.T) {
	h := newHarness(t)

	cmd := h.wheel(rightBody, bodyTop+1, tea.MouseWheelDown)
	if got := h.m.SelectedPath(); got != "plain.txt" {
		t.Fatalf("one notch down selected %q, want plain.txt", got)
	}
	if cmd == nil {
		t.Error("the wheel did not request the diff for the row it landed on")
	}
}

// TestClickSelectsCommitRow covers the log view: a different list in the same
// role, a different width, and a detail read rather than a diff one.
func TestClickSelectsCommitRow(t *testing.T) {
	h := newHarness(t)
	h.enterLog()

	first := h.m.log.SelectedSHA()
	cmd := h.click(rightBody, bodyTop+2)
	if h.m.log.SelectedSHA() == first {
		t.Fatal("clicking the third row did not move the log cursor")
	}
	if cmd == nil {
		t.Fatal("the click did not request the detail for the commit it selected")
	}
	h.run(cmd)

	sel, _ := h.m.log.Selected()
	if got := h.m.detail.Title(); got != sel.SHA[:len(got)] {
		t.Errorf("the commit pane holds %q, want the selected commit %q", got, sel.SHA)
	}
}

// TestClickSelectsBranchRow covers the third list, and the one whose
// cursor does not start at row 0: the view opens on the branch you are on, so
// a click above that row only lands right if the row is read as a position on
// screen rather than as a distance from the cursor.
func TestClickSelectsBranchRow(t *testing.T) {
	h := newHarness(t)
	h.enterBranches()

	if got := h.m.branches.SelectedName(); got != "main" {
		t.Fatalf("the branch view opened on %q, want the current branch main", got)
	}

	cmd := h.click(rightBody, bodyTop+1)
	if got := h.m.branches.SelectedName(); got != "feature" {
		t.Fatalf("clicking the second row selected %q, want feature", got)
	}
	if cmd == nil {
		t.Fatal("the click did not request the tip of the branch it selected")
	}
	h.run(cmd)

	sel, _ := h.m.branches.Selected()
	if got := h.m.detail.Title(); got != sel.SHA[:len(got)] {
		t.Errorf("the commit pane holds %q, want the tip of feature %q", got, sel.SHA)
	}
}

// TestMouseModeIsCellMotion pins the mode the app asks the terminal for. The
// golden files capture Body(), not the tea.View around it, so nothing else here
// would notice a return to all motion — an event per cell the pointer crosses,
// every one of them decoded, dispatched and discarded.
func TestMouseModeIsCellMotion(t *testing.T) {
	h := newHarness(t)
	if got := h.m.View().MouseMode; got != tea.MouseModeCellMotion {
		t.Errorf("the app asks the terminal for mouse mode %v, want cell motion", got)
	}
}

// TestWheelOverTheCommitPaneScrolls covers the one pane with no cursor: it
// scrolls as a document, and a click on one of its rows means nothing beyond
// the focus it takes.
func TestWheelOverTheCommitPaneScrolls(t *testing.T) {
	h := newHarness(t)
	h.enterLog()
	h.selectCommit("base: files with spaces, unicode, and no trailing newline")

	h.wheel(leftBody, bodyTop, tea.MouseWheelDown)
	if got := h.m.detail.Offset(); got != wheelStep {
		t.Errorf("one notch left the commit pane at row %d, want %d", got, wheelStep)
	}

	h.click(leftBody, bodyTop+4)
	if got := h.m.detail.Offset(); got != wheelStep {
		t.Errorf("a click scrolled the commit pane to row %d, want it left at %d", got, wheelStep)
	}
	if !h.m.DiffFocused() {
		t.Error("clicking the commit pane did not focus it")
	}
}

// TestMouseIgnoredWhileEditing covers the editor, which replaces both panes.
// While it is open there is nothing under the pointer to click, so the mouse
// is inert for the same reason every key but ctrl+c is.
func TestMouseIgnoredWhileEditing(t *testing.T) {
	h := newHarness(t)
	h.key("c")
	if !h.m.commit.Active() {
		t.Fatal("the editor did not open")
	}

	for _, msg := range []tea.Msg{
		tea.MouseClickMsg{X: rightBody, Y: bodyTop + 3, Button: tea.MouseLeft},
		tea.MouseWheelMsg{X: rightBody, Y: bodyTop, Button: tea.MouseWheelDown},
	} {
		if cmd := h.send(msg); cmd != nil {
			t.Errorf("%T produced a command while the editor was open", msg)
		}
	}
	if got := h.m.SelectedPath(); got != "logo.png" {
		t.Errorf("a click behind the editor selected %q", got)
	}
}

// TestDragInsideAPaneIsInert: the splitter is the only thing a drag moves, so
// motion that began inside a pane rather than on the boundary must not drag
// the cursor along with the pointer. See splitter_test.go for the drags that
// do something.
func TestDragInsideAPaneIsInert(t *testing.T) {
	h := newHarness(t)

	for _, msg := range []tea.Msg{
		tea.MouseMotionMsg{X: rightBody, Y: bodyTop + 3, Button: tea.MouseLeft},
		tea.MouseReleaseMsg{X: rightBody, Y: bodyTop + 3, Button: tea.MouseLeft},
	} {
		if cmd := h.send(msg); cmd != nil {
			t.Errorf("%T produced a command", msg)
		}
	}
	if got := h.m.SelectedPath(); got != "logo.png" {
		t.Errorf("a drag selected %q, want the selection left on logo.png", got)
	}
}

// TestMiddleAndRightClickAreInert keeps the two buttons the terminal itself
// uses — paste, and its own context menu — out of the app's hands.
func TestMiddleAndRightClickAreInert(t *testing.T) {
	h := newHarness(t)

	for _, b := range []tea.MouseButton{tea.MouseMiddle, tea.MouseRight} {
		if cmd := h.send(tea.MouseClickMsg{X: rightBody, Y: bodyTop + 3, Button: b}); cmd != nil {
			t.Errorf("%v produced a command", b)
		}
	}
	if got := h.m.SelectedPath(); got != "logo.png" {
		t.Errorf("a middle or right click selected %q", got)
	}
}

// rowAt is renderLines' row map, recomputed from the diff: row 0 is the first
// hunk's header, then one row per line, then the next header. line is -1 on a
// header row.
func rowAt(fd git.FileDiff, row int) (hunk, line int) {
	n := 0
	for hi, h := range fd.Hunks {
		if row == n {
			return hi, -1
		}
		if row <= n+len(h.Lines) {
			return hi, row - n - 1
		}
		n += 1 + len(h.Lines)
	}
	return -1, -1
}
