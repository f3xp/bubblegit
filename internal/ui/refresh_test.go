package ui

import (
	"path/filepath"
	"slices"
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
func (h *harness) filePaths() []string {
	h.t.Helper()
	before := h.m.SelectedPath()
	var paths []string
	for i := range h.m.files.Len() {
		h.m.files.MoveTo(i)
		f, _ := h.m.files.Selected()
		paths = append(paths, f.Path)
	}
	for i, p := range paths {
		if p == before {
			h.m.files.MoveTo(i)
		}
	}
	return paths
}
