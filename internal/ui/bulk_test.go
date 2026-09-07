package ui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/f3xp/bubblegit/internal/git"
)

// plainBody is the frame with its colour stripped, for asserting on text.
func (h *harness) plainBody() string { return ansi.Strip(h.m.Body()) }

func (h *harness) statusOf(path string) (git.FileStatus, bool) {
	h.t.Helper()
	all, err := git.Status(context.Background(), h.r)
	if err != nil {
		h.t.Fatal(err)
	}
	for _, f := range all {
		if f.Path == path {
			return f, true
		}
	}
	return git.FileStatus{}, false
}

// TestFilesPaneGroupsSections: the list is two sections under two headings,
// and the cursor never rests on a heading from any direction.
func TestFilesPaneGroupsSections(t *testing.T) {
	h := newHarness(t)
	body := h.plainBody()
	tracked, untracked := strings.Index(body, "Tracked"), strings.Index(body, "Untracked")
	if tracked < 0 || untracked < 0 || tracked > untracked {
		t.Fatalf("headings missing or out of order:\n%s", body)
	}
	// With the status code, so the diff title's mention of logo.png is not
	// the one found.
	if strings.Index(body, "M logo.png") < tracked || strings.Index(body, "?? untracked.txt") < untracked {
		t.Errorf("files are not under their headings:\n%s", body)
	}

	if got := h.m.SelectedPath(); got != "logo.png" {
		t.Fatalf("the cursor opens on %q, want the first file", got)
	}
	h.press("k")
	if got := h.m.SelectedPath(); got != "logo.png" {
		t.Errorf("k at the first file moved to %q", got)
	}
	h.press("G")
	if got := h.m.SelectedPath(); got != "untracked.txt" {
		t.Errorf("G landed on %q, want untracked.txt", got)
	}
	// Two up from the bottom crosses the Untracked heading.
	h.press("k", "k")
	if sel, _ := h.m.files.Selected(); sel.IsUntracked() || !strings.HasPrefix(sel.Path, "ünïcødé") {
		t.Errorf("k k from the bottom landed on %+v, want the last tracked file", sel)
	}
	h.press("j")
	if sel, _ := h.m.files.Selected(); !sel.IsUntracked() || sel.Path != "main.go" {
		t.Errorf("j across the heading landed on %+v, want the untracked main.go", sel)
	}
	for range 4 {
		h.send(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
		if _, ok := h.m.files.Selected(); !ok {
			t.Fatal("a half page up left the cursor on a heading")
		}
	}
}

// TestFilesRowShowsNumstat: tracked rows carry their line counts, the binary
// and the untracked ones nothing.
func TestFilesRowShowsNumstat(t *testing.T) {
	h := newHarness(t)
	// The harness seeds the list without counts; a real status read has them.
	h.run(h.m.loadStatus())
	for _, line := range strings.Split(h.plainBody(), "\n") {
		switch {
		case strings.Contains(line, "plain.txt") && !strings.Contains(line, "+1 −1"):
			t.Errorf("plain.txt row has no count: %q", line)
		case strings.Contains(line, "M logo.png") && strings.Contains(line, "+"):
			t.Errorf("the binary row has a count: %q", line)
		case strings.Contains(line, "?? main.go") && strings.Contains(line, "−"):
			t.Errorf("the untracked main.go borrowed the tracked count: %q", line)
		}
	}
}

func TestStageAllKey(t *testing.T) {
	h := newHarness(t)
	h.run(h.key("A"))
	for _, path := range []string{"plain.txt", "untracked.txt"} {
		if f, ok := h.statusOf(path); !ok || f.IsUnstaged() || f.IsUntracked() {
			t.Errorf("%s after A: %+v", path, f)
		}
	}
}

func TestUnstageAllKey(t *testing.T) {
	h := newHarness(t)
	h.run(h.key("U"))
	if f, _ := h.statusOf("staged.txt"); !f.IsUntracked() {
		t.Errorf("staged.txt after U: %+v, want untracked", f)
	}
}

// TestDiscardAsksFirst: D is a question, not an action. Any key but y is no
// and is swallowed; y runs it.
func TestDiscardAsksFirst(t *testing.T) {
	h := newHarness(t)
	if cmd := h.key("D"); cmd != nil {
		t.Fatal("D wrote without asking")
	}
	if !strings.Contains(h.plainBody(), "Discard") {
		t.Fatal("the key bar does not show the question")
	}
	if cmd := h.key("j"); cmd != nil {
		t.Fatal("no ran something")
	}
	if got := h.m.SelectedPath(); got != "logo.png" {
		t.Errorf("the key that said no also moved the cursor to %q", got)
	}
	if f, _ := h.statusOf("plain.txt"); !f.IsUnstaged() {
		t.Fatal("no discarded anyway")
	}

	h.key("D")
	h.run(h.key("y"))
	if f, ok := h.statusOf("plain.txt"); ok && f.IsUnstaged() {
		t.Errorf("plain.txt still modified after D y: %+v", f)
	}
	if f, ok := h.statusOf("staged.txt"); !ok || !f.IsStaged() {
		t.Error("discard took the staged file with it")
	}
}

func TestCleanAsksFirst(t *testing.T) {
	h := newHarness(t)
	h.key("X")
	if !strings.Contains(h.plainBody(), "Delete 2 untracked") {
		t.Fatalf("the key bar does not count the untracked files:\n%s", h.plainBody())
	}
	h.run(h.key("y"))
	if _, ok := h.statusOf("untracked.txt"); ok {
		t.Error("untracked.txt survived X y")
	}
	if f, _ := h.statusOf("plain.txt"); !f.IsUnstaged() {
		t.Error("clean touched a tracked change")
	}
}

func TestConfirmIgnoresMouse(t *testing.T) {
	h := newHarness(t)
	h.key("D")
	if cmd := h.click(rightBody, bodyTop+4); cmd != nil {
		t.Error("a click under the question selected a row")
	}
	if got := h.m.SelectedPath(); got != "logo.png" {
		t.Errorf("the click moved the cursor to %q", got)
	}
	if !h.m.confirm.pending() {
		t.Error("the click answered the question")
	}
}

// stashHarness is a harness whose repository can take a stash: git needs an
// identity to write the stash commit, and the fixture's main.go — deleted
// from the index and untracked on disk at once — cannot come back from one
// (see TestStashPushAndPop in package git).
func stashHarness(t *testing.T) *harness {
	t.Setenv("GIT_AUTHOR_NAME", "test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.com")
	h := newHarness(t)
	if _, err := h.r.Run(context.Background(), "add", "main.go"); err != nil {
		t.Fatal(err)
	}
	h.run(h.m.loadStatus())
	return h
}

func TestStashAndPop(t *testing.T) {
	h := stashHarness(t)
	h.run(h.key("s"))
	if !strings.Contains(h.plainBody(), "working tree clean") {
		t.Fatalf("s left changes behind:\n%s", h.plainBody())
	}
	h.run(h.key("p"))
	if h.m.files.Len() == 0 {
		t.Error("p brought nothing back")
	}
}

func (h *harness) enterStashes() {
	h.t.Helper()
	h.maybeRun(h.key("4"))
	if h.m.view != viewStash {
		h.t.Fatal("the stash view did not open")
	}
}

func TestStashViewListsAppliesAndDrops(t *testing.T) {
	h := stashHarness(t)
	h.run(h.key("s"))
	h.enterStashes()

	body := h.plainBody()
	if !strings.Contains(body, "Stashes (1)") || !strings.Contains(body, "stash@{0}") {
		t.Fatalf("the stash view does not list the stash:\n%s", body)
	}
	if !strings.Contains(body, "Stash — stash@{0}") {
		t.Errorf("the detail pane is not titled by the stash ref:\n%s", body)
	}
	if !strings.Contains(body, "plain.txt") {
		t.Errorf("the detail pane does not show the stashed patch:\n%s", body)
	}

	// Apply keeps the entry and puts the change back.
	h.run(h.key("a"))
	if h.m.stashes.Len() != 1 {
		t.Fatalf("apply left %d stashes, want 1", h.m.stashes.Len())
	}
	if f, ok := h.statusOf("plain.txt"); !ok || !f.IsUnstaged() {
		t.Error("apply did not restore plain.txt")
	}

	// Drop asks, then empties the list.
	if cmd := h.key("d"); cmd != nil {
		t.Fatal("d dropped without asking")
	}
	h.run(h.key("y"))
	if !strings.Contains(h.plainBody(), "no stashes") {
		t.Errorf("the list is not empty after d y:\n%s", h.plainBody())
	}
}
