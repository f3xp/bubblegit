package ui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/f3xp/bubblegit/internal/git"
	"github.com/f3xp/bubblegit/internal/gittest"
)

// harness drives the model through Update directly.
//
// No program loop, no goroutines, no waiting: every message is delivered in a
// known order, so these tests are deterministic and finish in microseconds.
// The end-to-end path is covered once, in app_test.go.
type harness struct {
	t *testing.T
	m Model
	r *git.Runner
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	dir := gittest.Small(t)
	h := &harness{t: t, m: New(dir), r: git.New(dir)}
	h.send(tea.WindowSizeMsg{Width: 80, Height: 24})
	h.send(headMsg{head: git.Head{Branch: "main", Commit: strings.Repeat("a", 40)}})

	files, err := git.Status(context.Background(), h.r)
	if err != nil {
		t.Fatal(err)
	}
	h.send(statusMsg{files: files})
	return h
}

func (h *harness) send(msg tea.Msg) tea.Cmd {
	h.t.Helper()
	updated, cmd := h.m.Update(msg)
	h.m = updated.(Model)
	return cmd
}

func (h *harness) press(keys ...string) {
	h.t.Helper()
	for _, k := range keys {
		h.send(tea.KeyPressMsg{Code: rune(k[0]), Text: k})
	}
}

// resolveDiff fetches the real diff for the current selection and delivers it
// as the answer to the outstanding request.
func (h *harness) resolveDiff() {
	h.t.Helper()
	sel, ok := h.m.files.Selected()
	if !ok {
		h.t.Fatal("nothing selected")
	}
	staged := sel.IsStaged() && !sel.IsUnstaged()
	d, err := git.DiffFile(context.Background(), h.r, sel.Path, staged, sel.IsUntracked())
	if err != nil {
		h.t.Fatal(err)
	}
	h.send(diffMsg{gen: h.m.diffGen, diff: d})
}

func TestNavigationMovesSelection(t *testing.T) {
	h := newHarness(t)
	if got := h.m.SelectedPath(); got != "logo.png" {
		t.Fatalf("initial selection %q, want logo.png", got)
	}
	h.press("j", "j")
	if got := h.m.SelectedPath(); got != "noeol.txt" {
		t.Errorf("after two j: %q, want noeol.txt", got)
	}
	h.press("k")
	if got := h.m.SelectedPath(); got != "main.go" {
		t.Errorf("after k: %q, want main.go", got)
	}
	h.press("G")
	if got := h.m.SelectedPath(); got != "untracked.txt" {
		t.Errorf("after G: %q, want the last row", got)
	}
}

func TestTabSwitchesFocus(t *testing.T) {
	h := newHarness(t)
	if h.m.DiffFocused() {
		t.Fatal("focus starts on the diff pane, want the file list")
	}
	h.send(tea.KeyPressMsg{Code: tea.KeyTab})
	if !h.m.DiffFocused() {
		t.Error("tab did not move focus to the diff pane")
	}
	h.send(tea.KeyPressMsg{Code: tea.KeyTab})
	if h.m.DiffFocused() {
		t.Error("tab did not move focus back to the file list")
	}
}

// TestDiffPaneRendersTextFile pins the diff body: hunk header, line-number
// gutter, and coloured +/- prefixes.
func TestDiffPaneRendersTextFile(t *testing.T) {
	h := newHarness(t)
	h.press("j", "j", "j") // logo.png -> main.go -> noeol.txt -> plain.txt
	if got := h.m.SelectedPath(); got != "plain.txt" {
		t.Fatalf("selection %q, want plain.txt", got)
	}
	h.resolveDiff()

	body := h.m.Body()
	if !strings.Contains(body, "@@") {
		t.Fatalf("no hunk header in the rendered frame:\n%s", body)
	}
	teatest.RequireEqualOutput(t, []byte(body))
}

// TestDiffPaneRendersUntrackedFile covers the case plain `git diff` says
// nothing about: without the /dev/null fallback this pane is blank.
func TestDiffPaneRendersUntrackedFile(t *testing.T) {
	h := newHarness(t)
	h.press("G") // untracked.txt is last
	sel, _ := h.m.files.Selected()
	if !sel.IsUntracked() {
		t.Fatalf("selected %+v, want an untracked file", sel)
	}
	h.resolveDiff()

	body := h.m.Body()
	if strings.Contains(body, "no changes") {
		t.Error("untracked file rendered as having no changes; the /dev/null diff is not being used")
	}
	if !strings.Contains(body, "untracked") {
		t.Errorf("the added content is missing from the frame:\n%s", body)
	}
}

func TestBinaryFileShowsPlaceholder(t *testing.T) {
	h := newHarness(t)
	h.resolveDiff() // logo.png is selected first
	if body := h.m.Body(); !strings.Contains(body, "binary file") {
		t.Errorf("no binary placeholder in the frame:\n%s", body)
	}
}

// TestErrorIsShown checks a failed git call surfaces its stderr rather than
// leaving the pane silently blank.
func TestErrorIsShown(t *testing.T) {
	h := newHarness(t)
	// An unmatched pathspec exits 0, so it is not a failure. A bogus object
	// is, and it produces the stderr the pane is supposed to surface.
	_, err := h.r.Run(context.Background(), "cat-file", "-p", strings.Repeat("0", 40))
	if err == nil {
		t.Fatal("expected a git error to render")
	}
	h.send(diffMsg{gen: h.m.diffGen, err: err})
	if body := h.m.Body(); !strings.Contains(body, "git error") {
		t.Errorf("the error is not visible in the frame:\n%s", body)
	}
}
