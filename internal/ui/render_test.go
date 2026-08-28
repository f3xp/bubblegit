package ui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
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

// TestLayoutFitsTerminal guards against the frame being larger than the
// terminal it is drawn into.
//
// The clamps in layout() and framed() have to agree; when they drifted apart,
// a 20x3 terminal produced a 40x5 frame — twice the width and taller than the
// screen — which tears the display on resize.
func TestLayoutFitsTerminal(t *testing.T) {
	sizes := []struct{ w, h int }{
		{80, 24}, // ordinary
		{48, 10}, // exactly two minimum-width panes
		{47, 10}, // one column short: must drop to a single pane
		{40, 6},
		{20, 3}, // too short for pane chrome at all
		{10, 2},
		{1, 1}, // degenerate
	}
	for _, sz := range sizes {
		t.Run(fmt.Sprintf("%dx%d", sz.w, sz.h), func(t *testing.T) {
			m := New(t.TempDir())
			updated, _ := m.Update(tea.WindowSizeMsg{Width: sz.w, Height: sz.h})
			m = updated.(Model)
			m.files.SetFiles([]git.FileStatus{
				{Kind: git.KindOrdinary, Staged: '.', Unstaged: 'M', Path: "a-fairly-long-file-name.txt"},
				{Kind: git.KindOrdinary, Staged: '.', Unstaged: 'M', Path: "ünïcødé-ファイル.txt"},
			})

			lines := strings.Split(m.Body(), "\n")
			if len(lines) > sz.h {
				t.Errorf("frame is %d rows for a %d-row terminal", len(lines), sz.h)
			}
			for i, l := range lines {
				if w := ansi.StringWidth(l); w > sz.w {
					t.Errorf("row %d is %d cells wide for a %d-column terminal: %q", i, w, sz.w, l)
				}
			}
		})
	}
}

// TestWidePathDoesNotOverflow checks a row containing double-width characters
// is truncated on display width, not byte length.
func TestWidePathDoesNotOverflow(t *testing.T) {
	m := New(t.TempDir())
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 30, Height: 12})
	m = updated.(Model)
	m.files.SetFiles([]git.FileStatus{
		{Kind: git.KindOrdinary, Staged: '.', Unstaged: 'M', Path: "ünïcødé-ファイル-ファイル-ファイル.txt"},
	})
	for i, l := range strings.Split(m.Body(), "\n") {
		if w := ansi.StringWidth(l); w > 30 {
			t.Errorf("row %d is %d cells wide, want at most 30: %q", i, w, l)
		}
	}
}

// TestToggleStagedSwitchesSide checks the staged half of a partially staged
// file is reachable. staged.txt is added to the index and not modified after,
// so its worktree diff is empty and only the staged side has content.
func TestToggleStagedSwitchesSide(t *testing.T) {
	h := newHarness(t)
	for h.m.SelectedPath() != "staged.txt" {
		before := h.m.SelectedPath()
		h.press("j")
		if h.m.SelectedPath() == before {
			t.Fatal("staged.txt is not in the file list")
		}
	}

	h.resolveDiff()
	if body := h.m.Body(); !strings.Contains(body, "staged") {
		t.Fatalf("expected the staged side to render:\n%s", body)
	}

	h.press("t")
	if !h.m.showStaged {
		t.Error("t did not toggle the staged view")
	}
	h.resolveDiff()
	if body := h.m.Body(); !strings.Contains(body, "(staged)") {
		t.Errorf("the title does not say which side is shown:\n%s", body)
	}
}
