package ui

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The refresh tests never run a tickMsg or watchMsg result: both re-arm a
// command that blocks — on a ten-second timer, on the watcher's channel — and
// h.run would sit on it. The refresh itself is reached through R, which
// returns the same reads without the re-arm.

// TestRefreshPicksUpExternalChange: a file written behind the app's back is
// in the list after R, and the cursor has not moved.
func TestRefreshPicksUpExternalChange(t *testing.T) {
	h := newHarness(t)
	h.selectFile("plain.txt")
	h.write("brand-new.txt", "x\n")

	h.run(h.key("R"))

	if !slices.Contains(h.filePaths(), "brand-new.txt") {
		t.Fatalf("brand-new.txt is not in the list: %v", h.filePaths())
	}
	if got := h.m.SelectedPath(); got != "plain.txt" {
		t.Fatalf("cursor moved to %q", got)
	}
}

// TestRefreshIsSkippedWhileApplying: a write in flight owns the next reload;
// a refresh on top of it would race the index.
func TestRefreshIsSkippedWhileApplying(t *testing.T) {
	h := newHarness(t)
	h.selectFile("plain.txt")
	if h.key(" ") == nil {
		t.Fatal("space did not start a stage")
	}
	if !h.m.applying {
		t.Fatal("applying is not set")
	}
	if h.key("R") != nil {
		t.Fatal("R refreshed while a write was in flight")
	}
}

// TestRefreshMarksHiddenViewsStale: the view on screen is re-read, the others
// are only marked, so re-entering them re-reads.
func TestRefreshMarksHiddenViewsStale(t *testing.T) {
	h := newHarness(t)
	h.enterLog()
	h.maybeRun(h.key("1"))
	if !h.m.logLoaded {
		t.Fatal("precondition: the log should be loaded")
	}

	h.run(h.key("R"))
	if h.m.logLoaded || h.m.branchesLoaded {
		t.Fatal("hidden lists were not marked stale")
	}

	h.enterLog()
	h.run(h.key("R"))
	if !h.m.logLoaded {
		t.Fatal("the visible log was not re-read")
	}
	if h.m.branchesLoaded {
		t.Fatal("the hidden branch list was not marked stale")
	}
}

// TestTickAndWatchReturnCommands pins that both triggers produce work without
// running it — see the note at the top of the file.
func TestTickAndWatchReturnCommands(t *testing.T) {
	h := newHarness(t)
	if h.send(tickMsg{}) == nil {
		t.Fatal("tick produced no command")
	}
	if h.m.watch == nil {
		t.Skip("no watcher on this platform")
	}
	if h.send(watchMsg{}) == nil {
		t.Fatal("watch produced no command")
	}
}

// TestStatusRepointsWatcher: every status read hands the watcher the files it
// named, absolute, so a save in an editor is seen without waiting for a tick.
func TestStatusRepointsWatcher(t *testing.T) {
	h := newHarness(t)
	if h.m.watch == nil {
		t.Skip("no watcher on this platform")
	}
	want := make([]string, 0, len(h.filePaths()))
	for _, p := range h.filePaths() {
		want = append(want, filepath.Join(h.r.Dir, p))
	}
	got := h.m.watch.Paths()
	if !slices.Equal(got, want) {
		t.Fatalf("watching %v, want %v", got, want)
	}
}

// filePaths reads the whole list by walking the cursor over it, and puts the
// cursor back: the pane exposes a selection, not a slice.
// selectFileFromTop puts the cursor back on path, searching from the top.
func (h *harness) selectFileFromTop(path string) {
	h.m.files.Top()
	for h.m.SelectedPath() != path {
		before := h.m.SelectedPath()
		h.m.files.MoveBy(1)
		if h.m.SelectedPath() == before {
			return
		}
	}
}

func (h *harness) filePaths() []string {
	h.t.Helper()
	before := h.m.SelectedPath()
	var paths []string
	// Walk the rows rather than index the files: the pane's cursor counts
	// section headings, and it stops moving at the last file. No two
	// neighbouring rows share a path, so a repeat means the bottom.
	h.m.files.Top()
	for {
		f, _ := h.m.files.Selected()
		if n := len(paths); n > 0 && paths[n-1] == f.Path {
			break
		}
		paths = append(paths, f.Path)
		h.m.files.MoveBy(1)
	}
	h.selectFileFromTop(before)
	return paths
}

// TestRefreshKeepsDiffOnScreen: a re-read of the diff already on screen keeps
// its rows until the new ones land. Blanking to "loading…" for the round trip
// is a flicker on every tick and every stage keystroke.
func TestRefreshKeepsDiffOnScreen(t *testing.T) {
	h := newHarness(t)
	h.selectFile("two-hunks.txt")

	cmd := h.key("R")
	if body := h.m.Body(); strings.Contains(body, "loading…") || !strings.Contains(body, "18 edited") {
		t.Fatalf("diff blanked while its own re-read was in flight:\n%s", body)
	}
	h.run(cmd)
	if body := h.m.Body(); !strings.Contains(body, "18 edited") {
		t.Fatalf("diff missing after the re-read landed:\n%s", body)
	}
}

// TestRefreshKeepsDetailOnScreen is the same for the commit pane.
func TestRefreshKeepsDetailOnScreen(t *testing.T) {
	h := newHarness(t)
	h.enterLog()
	if !strings.Contains(h.m.Body(), "commit ") {
		t.Fatal("precondition: the detail pane should show a commit")
	}

	h.key("R")
	if body := h.m.Body(); strings.Contains(body, "loading…") || !strings.Contains(body, "commit ") {
		t.Fatalf("detail blanked while its own re-read was in flight:\n%s", body)
	}
}

// TestToggleSideBlanksDiff: the other side of the same file is a different
// diff, so it blanks the way a different file does. Worktree rows under a
// "(staged)" title would be the wrong data under the right name.
func TestToggleSideBlanksDiff(t *testing.T) {
	h := newHarness(t)
	h.selectFile("two-hunks.txt")

	h.key("t")
	if body := h.m.Body(); !strings.Contains(body, "loading…") {
		t.Fatalf("diff kept the worktree rows under the staged title:\n%s", body)
	}
}
