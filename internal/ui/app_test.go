package ui_test

import (
	"bytes"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/f3xp/bubblegit/internal/gittest"
	"github.com/f3xp/bubblegit/internal/ui"
)

// This file holds the end-to-end tests: a real tea.Program, a real repository,
// real git subprocesses. Everything that does not need the program loop is in
// render_test.go, driven through Update directly — that is deterministic and
// hundreds of times faster.
//
// Exactly ONE teatest.WaitFor per program is allowed here. tm.Output() is a
// consuming stream and Bubble Tea often paints the whole screen in a single
// write, so an earlier wait can swallow the token a later wait is looking for.
// That failure is timing-dependent: it passes normally and fails under -race.

// TestEndToEnd runs the real program against a real repository and pins the
// first frame. The initial selection is a binary file, so the wait token also
// proves the status load, the diff request and the placeholder path all
// completed.
func TestEndToEnd(t *testing.T) {
	tm := teatest.NewTestModel(t, ui.New(gittest.Small(t)),
		teatest.WithInitialTermSize(80, 24))

	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		// An atomic token, never a composed string: differential rendering
		// splits "Files (9)" into "Files", a cursor move, then "(9)".
		return bytes.Contains(b, []byte("binary file"))
	}, teatest.WithDuration(10*time.Second))

	tm.Send(tea.KeyPressMsg{Code: 'q', Text: "q"})
	tm.WaitFinished(t, teatest.WithFinalTimeout(10*time.Second))

	teatest.RequireEqualOutput(t, []byte(tm.FinalModel(t).(ui.Model).Body()))
}

// TestQuitKeys checks every binding that ends the program, since a TUI you
// cannot exit is the worst possible bug.
func TestQuitKeys(t *testing.T) {
	for _, k := range []tea.KeyPressMsg{
		{Code: 'q', Text: "q"},
		{Code: 'c', Mod: tea.ModCtrl},
	} {
		t.Run(k.String(), func(t *testing.T) {
			tm := teatest.NewTestModel(t, ui.New(gittest.Small(t)),
				teatest.WithInitialTermSize(80, 24))
			tm.Send(k)
			tm.WaitFinished(t, teatest.WithFinalTimeout(10*time.Second))
		})
	}
}
