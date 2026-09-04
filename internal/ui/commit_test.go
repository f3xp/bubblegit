package ui

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/f3xp/bubblegit/internal/git"
)

// headMessage reads HEAD's message straight from git, so the assertion does
// not depend on the code under test.
func (h *harness) headMessage() string {
	h.t.Helper()
	out, err := h.r.Run(context.Background(), "log", "-1", "--format=%B")
	if err != nil {
		h.t.Fatalf("log: %v", err)
	}
	return strings.TrimRight(string(out), "\n")
}

func (h *harness) headSHA() string {
	h.t.Helper()
	out, err := h.r.Run(context.Background(), "rev-parse", "HEAD")
	if err != nil {
		h.t.Fatalf("rev-parse: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func TestCommitKeyOpensTheEditor(t *testing.T) {
	h := newHarness(t)
	if h.m.commit.Active() {
		t.Fatal("the editor is open before anything asked for it")
	}
	h.press("c")
	if !h.m.commit.Active() {
		t.Fatal("c did not open the message editor")
	}
	if body := h.m.Body(); !strings.Contains(body, "Commit") {
		t.Errorf("the editor is not on screen:\n%s", body)
	}
}

// The editor is a mode: while it is open every single-letter binding has to
// type rather than act, or writing "quit the daemon" ends the program.
func TestEditorOwnsEveryKey(t *testing.T) {
	h := newHarness(t)
	before := h.m.SelectedPath()
	h.press("c")

	h.typeText("q j t a")
	if got := h.m.commit.Value(); got != "q j t a" {
		t.Errorf("buffer = %q, want %q", got, "q j t a")
	}
	if h.m.SelectedPath() != before {
		t.Error("j moved the file selection while the editor had focus")
	}
	if h.m.showStaged {
		t.Error("t toggled the diff side while the editor had focus")
	}
}

// ctrl+c is the one key the editor does not keep. A terminal program that
// swallows it has trapped the user.
func TestCtrlCStillQuitsFromTheEditor(t *testing.T) {
	h := newHarness(t)
	h.press("c")
	cmd := h.send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("ctrl+c produced no command while the editor was open")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("ctrl+c did not quit while the editor was open")
	}
}

func TestCommitWritesTheMessage(t *testing.T) {
	h := newHarness(t)
	before := h.headSHA()

	h.press("c")
	h.typeText("a subject\n\nand a body")
	h.run(h.confirm())

	if h.headSHA() == before {
		t.Fatal("HEAD did not move")
	}
	if got, want := h.headMessage(), "a subject\n\nand a body"; got != want {
		t.Errorf("message = %q, want %q", got, want)
	}
	if h.m.commit.Active() {
		t.Error("the editor stayed open after a successful commit")
	}
	if h.m.head.Commit != h.headSHA() {
		t.Error("the header still shows the old HEAD")
	}
}

// Cancelling drops the buffer. A message kept across a cancel reappears under
// the next c as text the user did not just type.
func TestCancelDiscardsTheMessage(t *testing.T) {
	h := newHarness(t)
	h.press("c")
	h.typeText("half written")
	h.esc()

	if h.m.commit.Active() {
		t.Fatal("esc did not close the editor")
	}
	h.press("c")
	if got := h.m.commit.Value(); got != "" {
		t.Errorf("the editor reopened holding %q, want an empty buffer", got)
	}
}

// git refuses an empty message too, but only after running the pre-commit
// hook, which may take seconds and have side effects.
func TestEmptyMessageIsRefusedWithoutRunningGit(t *testing.T) {
	h := newHarness(t)
	h.press("c")

	if cmd := h.confirm(); cmd != nil {
		t.Fatal("an empty message was sent to git")
	}
	if !h.m.commit.Active() {
		t.Error("the editor closed on an empty message")
	}
	if body := h.m.Body(); !strings.Contains(body, "empty commit message") {
		t.Errorf("the refusal is not visible:\n%s", body)
	}
}

// A rejected commit must keep what the user wrote. Hooks working is the reason
// this project shells out to git at all, so a hook rejecting a commit is the
// common case, not an edge one.
func TestRejectedCommitKeepsTheMessageAndShowsWhy(t *testing.T) {
	h := newHarness(t)
	writeHook(t, h.r.Dir, "pre-commit", "#!/bin/sh\necho 'lint failed: tabs' >&2\nexit 1\n")

	h.press("c")
	h.typeText("will be rejected")
	h.run(h.confirm())

	if !h.m.commit.Active() {
		t.Fatal("the editor closed on a rejected commit, losing the message")
	}
	if got := h.m.commit.Value(); got != "will be rejected" {
		t.Errorf("buffer = %q, want the message back", got)
	}
	if body := h.m.Body(); !strings.Contains(body, "lint failed") {
		t.Errorf("the hook's stderr is not on screen:\n%s", body)
	}
}

// git puts "nothing to commit" on stdout with an empty stderr, so a
// stderr-only error would render this as "exit status 1".
func TestNothingStagedExplainsItself(t *testing.T) {
	h := newHarness(t)
	if _, err := h.r.Run(context.Background(), "reset", "-q"); err != nil {
		t.Fatal(err)
	}

	h.press("c")
	h.typeText("nothing to record")
	h.run(h.confirm())

	body := h.m.Body()
	if strings.Contains(body, "exit status") {
		t.Errorf("the failure rendered as a bare exit code:\n%s", body)
	}
	if !strings.Contains(body, "no changes added to commit") {
		t.Errorf("git's own explanation is missing:\n%s", body)
	}
}

func TestAmendPreFillsHeadMessage(t *testing.T) {
	h := newHarness(t)
	h.press("c")
	h.typeText("original subject")
	h.run(h.confirm())

	h.run(h.key("C"))
	if !h.m.commit.Active() {
		t.Fatal("C did not open the editor")
	}
	if !h.m.commit.Amending() {
		t.Error("the editor opened as a plain commit, not an amend")
	}
	if got := h.m.commit.Value(); got != "original subject" {
		t.Errorf("buffer = %q, want HEAD's message", got)
	}
	if title := h.m.commit.Title(); !strings.Contains(title, "Amend") {
		t.Errorf("title = %q, does not say it is an amend", title)
	}
}

func TestAmendRewordsWithoutAddingACommit(t *testing.T) {
	h := newHarness(t)
	h.press("c")
	h.typeText("original")
	h.run(h.confirm())
	depth := h.revCount()

	h.run(h.key("C"))
	h.typeText(" reworded")
	h.run(h.confirm())

	if got := h.headMessage(); got != "original reworded" {
		t.Errorf("message = %q, want the reworded one", got)
	}
	if got := h.revCount(); got != depth {
		t.Errorf("history is %d commits, want %d — the amend added one", got, depth)
	}
}

func (h *harness) revCount() int {
	h.t.Helper()
	out, err := h.r.Run(context.Background(), "rev-list", "--count", "HEAD")
	if err != nil {
		h.t.Fatal(err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		h.t.Fatal(err)
	}
	return n
}

// A commit is slower than an apply — signing and a pre-commit hook can take
// seconds — so the in-flight guard matters more here than anywhere else.
func TestSecondConfirmIsIgnoredWhileCommitting(t *testing.T) {
	h := newHarness(t)
	h.press("c")
	h.typeText("once")

	if cmd := h.confirm(); cmd == nil {
		t.Fatal("the first confirm produced no command")
	}
	if !h.m.applying {
		t.Fatal("the first confirm did not mark a write in flight")
	}
	if cmd := h.confirm(); cmd != nil {
		t.Error("a second confirm spawned another git commit")
	}
}

// applying is one flag for every write. Letting the editor open mid-apply
// would put a commit in front of a guard it cannot see: doCommit refuses while
// the flag is held, so the confirm would do nothing and say nothing.
func TestCommitKeyIsRefusedWhileStaging(t *testing.T) {
	h := newHarness(t)
	h.selectFile("plain.txt")

	if cmd := h.key(" "); cmd == nil {
		t.Fatal("the stage key produced no command")
	}
	if !h.m.applying {
		t.Fatal("the stage key did not mark a write in flight")
	}

	h.press("c")
	if h.m.commit.Active() {
		t.Error("the editor opened while a stage was still in flight")
	}
	h.press("C")
	if h.m.commit.Active() {
		t.Error("the amend key opened the editor while a stage was in flight")
	}
}

// The editor replaces both panes, so it is the one thing sized against the
// full width. TestLayoutFitsTerminal covers the two-pane case.
func TestCommitEditorFitsTerminal(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {40, 10}, {20, 6}, {12, 3}} {
		h := newHarness(t)
		h.send(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		h.press("c")
		h.typeText("subject")

		body := h.m.Body()
		lines := strings.Split(body, "\n")
		if len(lines) > size[1] {
			t.Errorf("%dx%d: %d rows, want at most %d", size[0], size[1], len(lines), size[1])
		}
		for i, l := range lines {
			if w := ansi.StringWidth(l); w > size[0] {
				t.Errorf("%dx%d: row %d is %d cells wide, want at most %d", size[0], size[1], i, w, size[0])
			}
		}
	}
}

// A git error is rendered on one line however many git wrote, or it pushes the
// editor out of the pane it was sized for.
func TestCommitErrorDoesNotOverflow(t *testing.T) {
	// 12x3 is the case the two sizings can disagree on: the error claims a row
	// the editor was already using, and there is only one to share.
	for _, size := range [][2]int{{80, 24}, {12, 3}} {
		h := newHarness(t)
		h.send(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		writeHook(t, h.r.Dir, "pre-commit", "#!/bin/sh\nprintf 'one\\ntwo\\nthree\\nfour\\n' >&2\nexit 1\n")
		h.press("c")
		h.typeText("rejected")
		h.run(h.confirm())

		lines := strings.Split(h.m.Body(), "\n")
		if len(lines) > size[1] {
			t.Errorf("%dx%d: %d rows on a four-line error, want at most %d",
				size[0], size[1], len(lines), size[1])
		}
		for i, l := range lines {
			if w := ansi.StringWidth(l); w > size[0] {
				t.Errorf("%dx%d: row %d is %d cells wide, want at most %d",
					size[0], size[1], i, w, size[0])
			}
		}
	}
}

func TestCommitEditorRenders(t *testing.T) {
	h := newHarness(t)
	h.press("c")
	h.typeText("add a thing\n\nBecause the other thing needed it.")
	teatest.RequireEqualOutput(t, []byte(h.m.Body()))
}

func writeHook(t *testing.T, dir, name, body string) {
	t.Helper()
	// Ask git where hooks live rather than assuming .git/hooks: a developer
	// with core.hooksPath set globally would otherwise get a test that passes
	// because the hook never ran.
	out, err := git.New(dir).Run(context.Background(), "rev-parse", "--git-path", "hooks")
	if err != nil {
		t.Fatal(err)
	}
	hooks := filepath.Join(dir, strings.TrimSpace(string(out)))
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hooks, name), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}
