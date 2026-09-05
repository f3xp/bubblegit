package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/teatest/v2"
)

func TestHelpPopupRenders(t *testing.T) {
	h := newHarness(t)
	h.press("?")
	if !h.m.showHelp {
		t.Fatal("? did not open the help popup")
	}
	teatest.RequireEqualOutput(t, []byte(h.m.Body()))
}

// TestHelpPopupSwallowsTheKeyThatCloses it checks the popup is a mode: the key
// that dismisses it does not also reach the pane underneath.
func TestHelpPopupSwallowsTheKeyThatClosesIt(t *testing.T) {
	h := newHarness(t)
	before := h.m.SelectedPath()

	h.press("?")
	h.press("j")

	if h.m.showHelp {
		t.Error("j did not close the help popup")
	}
	if got := h.m.SelectedPath(); got != before {
		t.Errorf("j moved the cursor to %q behind the popup, want it left on %q", got, before)
	}
}

// TestHelpKeyIsLiteralInTheEditor checks the dispatch order: the message editor
// owns every key, and `?` is an ordinary character in a commit message.
func TestHelpKeyIsLiteralInTheEditor(t *testing.T) {
	h := newHarness(t)
	h.press("c")
	h.typeText("why?")

	if h.m.showHelp {
		t.Fatal("? opened the help popup from inside the editor")
	}
	if !h.m.commit.Active() {
		t.Fatal("the editor closed")
	}
	if body := h.m.Body(); !strings.Contains(body, "why?") {
		t.Errorf("? was not typed into the message:\n%s", body)
	}
}

func TestHelpPopupFitsSmallTerminal(t *testing.T) {
	h := newHarness(t)
	h.send(tea.WindowSizeMsg{Width: 30, Height: 12})
	h.press("?")

	rows := strings.Split(h.m.Body(), "\n")
	if len(rows) > 12 {
		t.Errorf("body is %d rows tall, want at most 12", len(rows))
	}
	for i, l := range rows {
		if w := ansi.StringWidth(l); w > 30 {
			t.Errorf("row %d is %d cells wide, want at most 30: %q", i, w, l)
		}
	}
}
