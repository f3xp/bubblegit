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

// TestInitialRender is the end-to-end proof that the chosen test strategy
// works: drive the real program with teatest, assert the rendered frame
// against a golden file. Update goldens with `go test ./... -update`.
func TestInitialRender(t *testing.T) {
	repo := gittest.Small(t)
	tm := teatest.NewTestModel(t, ui.New(repo), teatest.WithInitialTermSize(80, 24))

	// Wait for the async HEAD load to land, rather than sleeping.
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return bytes.Contains(b, []byte("main"))
	}, teatest.WithDuration(5*time.Second))

	tm.Send(tea.KeyPressMsg{Code: 'q', Text: "q"})
	tm.WaitFinished(t, teatest.WithFinalTimeout(5*time.Second))

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
			tm.WaitFinished(t, teatest.WithFinalTimeout(5*time.Second))
		})
	}
}
