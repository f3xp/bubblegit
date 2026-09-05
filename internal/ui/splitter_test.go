package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// In the status view of an 80-column terminal the left pane — the diff — is 52
// columns, so the panes meet at columns 51 and 52: the left pane's right border
// and the right pane's left one. Both are the splitter.
const (
	leftEdge  = 51
	rightEdge = 52
)

func (h *harness) drag(x, y int) tea.Cmd {
	h.t.Helper()
	return h.send(tea.MouseMotionMsg{X: x, Y: y, Button: tea.MouseLeft})
}

func (h *harness) release(x, y int) tea.Cmd {
	h.t.Helper()
	return h.send(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
}

// TestSplitterTracksWhicheverColumnWasGrabbed is the property the grab offset
// exists for. The splitter is two columns wide, so a drag that ignored which
// one the pointer took hold of would jump the boundary by a column before it
// began to track — visible, and invisible to a test that only checks the
// boundary moved.
func TestSplitterTracksWhicheverColumnWasGrabbed(t *testing.T) {
	for _, tc := range []struct {
		name string
		grab int
		want int
	}{
		// Grabbing the left pane's own border puts that border on the target,
		// so the pane is one column wider than the target.
		{"the left pane's border", leftEdge, 41},
		// Grabbing the right pane's border puts the right pane's first column
		// on the target, so the left pane ends one column earlier.
		{"the right pane's border", rightEdge, 40},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			if h.m.leftW != 52 {
				t.Fatalf("the left pane starts at %d columns, want the fixture's 52", h.m.leftW)
			}

			if cmd := h.click(tc.grab, 10); cmd != nil {
				t.Error("taking hold of the splitter produced a command")
			}
			if !h.m.dragging {
				t.Fatal("clicking the splitter did not start a drag")
			}
			h.drag(40, 10)
			if h.m.leftW != tc.want {
				t.Errorf("dragging to column 40 left the pane %d columns wide, want %d",
					h.m.leftW, tc.want)
			}
			h.release(40, 10)
			if h.m.dragging {
				t.Error("the drag outlived the release")
			}
		})
	}
}

// TestSplitterDragDoesNotTakeFocus: the splitter is neither pane, so grabbing
// it must not move focus into one. A drag that stole focus would leave the
// keyboard pointed somewhere the user never asked for.
func TestSplitterDragDoesNotTakeFocus(t *testing.T) {
	h := newHarness(t)
	h.click(rightEdge, 10)
	if h.m.DiffFocused() {
		t.Error("taking hold of the splitter focused the diff beside it")
	}
	if got := h.m.SelectedPath(); got != "logo.png" {
		t.Errorf("taking hold of the splitter selected %q", got)
	}
}

// TestSplitterClampsToMinPaneWidth: dragging past either end leaves both panes
// legible rather than collapsing one to nothing.
func TestSplitterClampsToMinPaneWidth(t *testing.T) {
	h := newHarness(t)

	h.click(rightEdge, 10)
	h.drag(0, 10)
	if h.m.leftW != minPaneWidth {
		t.Errorf("dragging to the left edge left the pane %d columns wide, want %d",
			h.m.leftW, minPaneWidth)
	}
	h.drag(79, 10)
	if got := h.m.width - h.m.leftW; got != minPaneWidth {
		t.Errorf("dragging to the right edge left the other pane %d columns wide, want %d",
			got, minPaneWidth)
	}
	h.release(79, 10)

	// And the clamp is stored, not merely applied on the way out: what a later
	// resize is read against is the fraction, so an unclamped one would come
	// back as a two-column pane on a wider terminal.
	h.send(tea.WindowSizeMsg{Width: 200, Height: 24})
	if got := h.m.width - h.m.leftW; got < minPaneWidth {
		t.Errorf("after a resize the right pane is %d columns wide, want at least %d",
			got, minPaneWidth)
	}
}

// TestSplitterSurvivesResizeAsAFraction pins what is stored. A column count
// would come back after a resize as a proportion nobody chose.
func TestSplitterSurvivesResizeAsAFraction(t *testing.T) {
	h := newHarness(t)
	h.click(rightEdge, 10)
	h.drag(40, 10)
	h.release(40, 10)
	if h.m.leftW != 40 {
		t.Fatalf("the drag left the pane %d columns wide, want 40", h.m.leftW)
	}

	h.send(tea.WindowSizeMsg{Width: 100, Height: 24})
	if h.m.leftW != 50 {
		t.Errorf("half of an 80-column terminal came back as %d columns of a 100-column one, want 50",
			h.m.leftW)
	}
}

// TestSplitterIsPerView: the three pairs of panes want different proportions
// of the same terminal, so moving one must not move the others.
func TestSplitterIsPerView(t *testing.T) {
	h := newHarness(t)
	h.click(rightEdge, 10)
	h.drag(30, 10)
	h.release(30, 10)
	if h.m.leftW != 30 {
		t.Fatalf("the drag left the diff pane %d columns wide, want 30", h.m.leftW)
	}

	h.enterLog()
	if h.m.leftW != 40 {
		t.Errorf("the log pane is %d columns wide, want its own default of 40", h.m.leftW)
	}
	h.press("l", "l")
	if h.m.leftW != 42 {
		t.Fatalf("two l left the log pane %d columns wide, want 42", h.m.leftW)
	}

	h.maybeRun(h.key("1"))
	if h.m.leftW != 30 {
		t.Errorf("back in the status view the pane is %d columns wide, want the 30 it was left at", h.m.leftW)
	}
}

// TestSplitterKeysMoveTheBoundary covers the keyboard path, which is the one
// that works without a mouse at all.
func TestSplitterKeysMoveTheBoundary(t *testing.T) {
	h := newHarness(t)

	h.press("l", "l", "l")
	if h.m.leftW != 55 {
		t.Errorf("three l left the pane %d columns wide, want 55", h.m.leftW)
	}
	h.press("h")
	if h.m.leftW != 54 {
		t.Errorf("one h left the pane %d columns wide, want 54", h.m.leftW)
	}

	// The frame still fits: layout() and framed() have to agree about the new
	// split, or the rendered pane is wider than the terminal and the display
	// tears on the next resize.
	for i, line := range strings.Split(h.m.Body(), "\n") {
		if got := ansi.StringWidth(line); got > h.m.width {
			t.Fatalf("row %d is %d columns wide, want at most %d", i, got, h.m.width)
		}
	}
}

// TestSplitterKeysWorkWithoutBorders is the asymmetry between the two paths.
// A terminal too short for pane chrome has no border column to take hold of,
// but its panes still split the width, so the keys still mean something.
func TestSplitterKeysWorkWithoutBorders(t *testing.T) {
	h := newHarness(t)
	h.send(tea.WindowSizeMsg{Width: 80, Height: 4})
	if h.m.bordered() {
		t.Fatal("a four-row terminal still drew pane chrome")
	}

	h.press("l")
	if h.m.leftW != 53 {
		t.Errorf("one l left the pane %d columns wide, want 53", h.m.leftW)
	}
	if hit, _ := h.m.hitTest(rightEdge, 1); hit.splitter {
		t.Error("an unbordered layout offered a column to grab")
	}
}

// TestSplitterIsInertWhenNarrow: below two minimum-width panes there is one
// pane, and no boundary between panes to move.
func TestSplitterIsInertWhenNarrow(t *testing.T) {
	h := newHarness(t)
	h.send(tea.WindowSizeMsg{Width: 40, Height: 24})
	if !h.m.narrow {
		t.Fatal("a 40-column terminal is not narrow")
	}

	before := h.m.leftW
	h.press("l", "l", "h")
	if h.m.leftW != before {
		t.Errorf("the splitter keys moved a pane that fills the width: %d, want %d",
			h.m.leftW, before)
	}
	if hit, _ := h.m.hitTest(20, 10); hit.splitter {
		t.Error("a one-pane layout offered a splitter to grab")
	}
}

// TestFrameEdgesAreNotHandles: the outer borders of the frame are indexed
// inside their pane exactly like the splitter's two columns, so reading the
// handle after that offset is applied would turn the edges of the terminal
// into drag handles.
func TestFrameEdgesAreNotHandles(t *testing.T) {
	h := newHarness(t)
	for _, x := range []int{0, 79} {
		if hit, _ := h.m.hitTest(x, 10); hit.splitter {
			t.Errorf("column %d is a drag handle", x)
		}
		h.click(x, 10)
		if h.m.dragging {
			t.Fatalf("clicking column %d started a drag", x)
		}
	}
}

// TestReleaseOutsideTheFrameEndsTheDrag: a pointer dragged off the edge still
// reports its release, and the flag has to clear on it. Left set, the next
// press-and-move anywhere on screen would resize the splitter.
func TestReleaseOutsideTheFrameEndsTheDrag(t *testing.T) {
	h := newHarness(t)
	h.click(rightEdge, 10)
	if !h.m.dragging {
		t.Fatal("clicking the splitter did not start a drag")
	}
	h.release(200, 400)
	if h.m.dragging {
		t.Fatal("a release outside the frame left the drag running")
	}

	before := h.m.leftW
	h.drag(60, 10)
	if h.m.leftW != before {
		t.Errorf("motion after the release moved the boundary to %d, want %d", h.m.leftW, before)
	}
}

// TestDragBehindTheEditorMovesNothing: the editor covers both panes, so there
// is no boundary on screen to move. The release still has to land, which is
// why it is answered before the editor guard.
func TestDragBehindTheEditorMovesNothing(t *testing.T) {
	h := newHarness(t)
	h.click(rightEdge, 10)
	h.key("c")
	if !h.m.commit.Active() {
		t.Fatal("the editor did not open")
	}

	before := h.m.leftW
	h.drag(60, 10)
	if h.m.leftW != before {
		t.Errorf("a drag behind the editor moved the boundary to %d, want %d", h.m.leftW, before)
	}
	h.release(60, 10)
	if h.m.dragging {
		t.Error("the release did not reach the drag through the editor")
	}
}

// TestSplitterStepsOnEveryWidth: layout() truncates the stored fraction back
// into a column count, and on some widths the obvious fraction does not
// survive that round trip — 29 columns of 100 comes back as 28. The key then
// does nothing, which is indistinguishable from a broken one, so every column
// a step can land on is walked here rather than the one the fixture happens to
// use.
func TestSplitterStepsOnEveryWidth(t *testing.T) {
	for _, width := range []int{79, 80, 81, 100, 137} {
		h := newHarness(t)
		h.send(tea.WindowSizeMsg{Width: width, Height: 24})

		for want := h.m.leftW + 1; want <= width-minPaneWidth; want++ {
			h.press("l")
			if h.m.leftW != want {
				t.Fatalf("at width %d, l from %d columns stalled at %d, want %d",
					width, want-1, h.m.leftW, want)
			}
		}
		for want := h.m.leftW - 1; want >= minPaneWidth; want-- {
			h.press("h")
			if h.m.leftW != want {
				t.Fatalf("at width %d, h from %d columns stalled at %d, want %d",
					width, want+1, h.m.leftW, want)
			}
		}
	}
}
