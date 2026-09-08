package ui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/f3xp/bubblegit/internal/git"
	"github.com/f3xp/bubblegit/internal/gittest"
	"github.com/f3xp/bubblegit/internal/ui/pane"
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
		h.key(k)
	}
}

// key presses one key and hands back whatever command it produced, so a test
// can choose to run the git call rather than only observe the model.
func (h *harness) key(k string) tea.Cmd {
	h.t.Helper()
	if k == " " {
		return h.send(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	}
	return h.send(tea.KeyPressMsg{Code: rune(k[0]), Text: k})
}

// run drives a command and everything it leads to until the model is quiet.
//
// One action is several round trips — an apply reloads the status, which
// reloads the diff — and a test that ran only the first would assert against a
// model that has not caught up yet. A commit fans out rather than chaining,
// reloading HEAD and the status side by side, so the pending work is a queue
// and a tea.Batch is unwrapped rather than delivered as a message.
func (h *harness) run(cmd tea.Cmd) {
	h.t.Helper()
	if cmd == nil {
		h.t.Fatal("the key produced no command")
	}
	queue := []tea.Cmd{cmd}
	for range 20 {
		if len(queue) == 0 {
			return
		}
		cmd, queue = queue[0], queue[1:]
		msg := cmd()
		// tea.Batch hands back the commands themselves rather than a message.
		if batch, ok := msg.(tea.BatchMsg); ok {
			queue = append(queue, batch...)
			continue
		}
		if next := h.send(msg); next != nil {
			queue = append(queue, next)
		}
	}
	h.t.Fatal("the model never settled")
}

// typeText enters a message the way the user would, one key at a time, so the
// test exercises the same routing a real keystroke takes.
func (h *harness) typeText(s string) {
	h.t.Helper()
	for _, r := range s {
		if r == '\n' {
			h.send(tea.KeyPressMsg{Code: tea.KeyEnter})
			continue
		}
		h.send(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func (h *harness) esc() tea.Cmd {
	h.t.Helper()
	return h.send(tea.KeyPressMsg{Code: tea.KeyEsc})
}

// enter is a key of its own rather than one h.key() can spell: key() sends the
// first byte of its argument, so "enter" would arrive as an e.
func (h *harness) enter() tea.Cmd {
	h.t.Helper()
	return h.send(tea.KeyPressMsg{Code: tea.KeyEnter})
}

// confirm is the commit key, which is deliberately not enter.
func (h *harness) confirm() tea.Cmd {
	h.t.Helper()
	return h.send(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
}

// selectFile moves the file cursor onto path and loads its diff.
func (h *harness) selectFile(path string) {
	h.t.Helper()
	for h.m.SelectedPath() != path {
		before := h.m.SelectedPath()
		h.press("j")
		if h.m.SelectedPath() == before {
			h.t.Fatalf("%s is not in the file list", path)
		}
	}
	h.resolveDiff()
}

// focusDiffOn moves the diff cursor onto the first row matching kind and text.
// Tests name the line they mean; a row offset would move with the fixture.
func (h *harness) focusDiffOn(kind git.LineKind, text string) {
	h.t.Helper()
	h.send(tea.KeyPressMsg{Code: tea.KeyTab})

	fd, _, _, ok := h.m.diff.Selection()
	if !ok {
		h.t.Fatal("the diff pane has no cursor")
	}
	for range countRows(fd) {
		_, hunk, line, _ := h.m.diff.Selection()
		if line >= 0 {
			if l := fd.Hunks[hunk].Lines[line]; l.Kind == kind && l.Text == text {
				return
			}
		}
		h.press("j")
	}
	h.t.Fatalf("no %c%q row in the diff", kind, text)
}

func countRows(fd git.FileDiff) int {
	n := 0
	for _, h := range fd.Hunks {
		n += 1 + len(h.Lines)
	}
	return n
}

// write puts content in the working tree, for the cases the fixture cannot
// carry ready-made.
func (h *harness) write(path, content string) {
	h.t.Helper()
	if err := os.WriteFile(filepath.Join(h.r.Dir, path), []byte(content), 0o644); err != nil {
		h.t.Fatal(err)
	}
}

// stagedContent is what the index now holds for a path.
func (h *harness) stagedContent(path string) string {
	h.t.Helper()
	out, err := h.r.Run(context.Background(), "show", ":"+path)
	if err != nil {
		h.t.Fatalf("show :%s: %v", path, err)
	}
	return string(out)
}

// resolveDiff fetches the real diff for the current selection and delivers it
// as the answer to the outstanding request.
func (h *harness) resolveDiff() {
	h.t.Helper()
	sel, ok := h.m.files.Selected()
	if !ok {
		h.t.Fatal("nothing selected")
	}
	d, err := git.DiffFile(context.Background(), h.r, sel.Path, h.m.stagedSide(), sel.IsUntracked())
	if err != nil {
		h.t.Fatal(err)
	}
	h.send(diffMsg{epoch: h.m.diffEpoch, key: h.m.keyFor(sel), content: pane.RenderDiff(d)})
}

// TestDiffIsRenderedInTheCommand pins where the expensive half of showing a
// diff happens.
//
// Rendering costs tens of microseconds a line — a large file runs to hundreds
// of milliseconds — and there is nothing about the pane that would notice if it
// moved back onto the Update path, where it would stall every keystroke behind
// it. So the guard is on the message the production command produces: it has to
// arrive with its rows already built.
//
// It runs the real command rather than the harness's own message, which would
// only be a test of the harness.
func TestDiffIsRenderedInTheCommand(t *testing.T) {
	h := newHarness(t)
	h.press("j") // logo.png is binary and renders as no rows at all

	// The press marked its own read in flight; without this loadDiff would
	// decline to issue a second one.
	clear(h.m.diffs)
	cmd := h.m.loadDiff()
	if cmd == nil {
		t.Fatal("no diff was requested for the selected file")
	}
	msg, ok := cmd().(diffMsg)
	if !ok {
		t.Fatalf("loadDiff produced %T, want diffMsg", cmd())
	}
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	if msg.content.Rows() == 0 {
		t.Error("the diff message carries no rendered rows; rendering has moved back onto the Update path")
	}
}

// TestConcurrentLoadsShareOneHighlighter is the race detector's way in.
//
// highlight.For memoises, so every goroutine rendering a .go file lexes through
// the same Highlighter, and a diff, a commit detail and a branch tip can all be
// in flight at once. Run with -race this is the only test that exercises that.
func TestConcurrentLoadsShareOneHighlighter(t *testing.T) {
	h := newHarness(t)
	h.press("j") // main.go

	// The presses left their reads in flight and unanswered, and a load for a
	// key already in flight is declined; drop the markers so each spawns.
	load := func(f func() tea.Cmd) tea.Cmd {
		clear(h.m.diffs)
		clear(h.m.details)
		return f()
	}
	sha := headSHA(h)
	cmds := []tea.Cmd{load(h.m.loadDiff), load(func() tea.Cmd { return h.m.loadDetailFor(sha) }), load(h.m.loadDiff)}
	var wg sync.WaitGroup
	for _, cmd := range cmds {
		if cmd == nil {
			t.Fatal("a load produced no command")
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			cmd()
		}()
	}
	wg.Wait()
}

// headSHA is the commit the detail pane is pointed at; any real one will do.
func headSHA(h *harness) string {
	h.t.Helper()
	out, err := h.r.Run(context.Background(), "rev-parse", "HEAD")
	if err != nil {
		h.t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
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
	h.send(diffMsg{epoch: h.m.diffEpoch, key: h.m.diffWant, err: err})
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
	// Both views, because each splits the terminal by a different fraction and
	// the clamps have to hold for either one.
	views := map[string]view{"status": viewStatus, "log": viewLog, "branches": viewBranches, "stash": viewStash}

	for _, sz := range sizes {
		for name, v := range views {
			t.Run(fmt.Sprintf("%s/%dx%d", name, sz.w, sz.h), func(t *testing.T) {
				m := New(t.TempDir())
				m.view = v
				updated, _ := m.Update(tea.WindowSizeMsg{Width: sz.w, Height: sz.h})
				m = updated.(Model)
				m.files.SetFiles([]git.FileStatus{
					{Kind: git.KindOrdinary, Staged: '.', Unstaged: 'M', Path: "a-fairly-long-file-name.txt"},
					{Kind: git.KindOrdinary, Staged: '.', Unstaged: 'M', Path: "ünïcødé-ファイル.txt"},
				})
				m.log.SetCommits([]git.Commit{
					{SHA: strings.Repeat("a", 40), Short: "aaaaaaa", Parents: []string{strings.Repeat("b", 40)},
						Subject: "a subject long enough to need truncating somewhere"},
					{SHA: strings.Repeat("b", 40), Short: "bbbbbbb",
						Subject: "ünïcødé-ファイル in a commit subject"},
				})
				m.branches.SetBranches([]git.Branch{
					{Name: "a-fairly-long-branch-name/with-a-slash", Current: true,
						Upstream: "origin/main", Ahead: 12, Behind: 3,
						Subject: "a subject long enough to need truncating somewhere"},
					{Name: "ünïcødé-ブランチ", Subject: "ünïcødé-ファイル in a commit subject"},
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

// TestStageLineFromDiffPane drives the whole M2 path: cursor onto one line of
// a diff, stage key, real git apply, reload.
func TestStageLineFromDiffPane(t *testing.T) {
	h := newHarness(t)
	h.selectFile("two-hunks.txt")
	h.focusDiffOn(git.LineAdd, "18 edited")

	h.run(h.key(" "))

	staged := h.stagedContent("two-hunks.txt")
	if !strings.Contains(staged, "18 edited") {
		t.Errorf("the line under the cursor was not staged:\n%s", staged)
	}
	if strings.Contains(staged, "2 edited") {
		t.Errorf("the other hunk was staged too:\n%s", staged)
	}
}

// TestStageHunkKey checks the hunk key takes the whole hunk from a line inside
// it, rather than needing the cursor on the header.
func TestStageHunkKey(t *testing.T) {
	h := newHarness(t)
	h.selectFile("two-hunks.txt")
	h.focusDiffOn(git.LineContext, "16")

	h.run(h.key("a"))

	staged := h.stagedContent("two-hunks.txt")
	if !strings.Contains(staged, "18 edited") {
		t.Errorf("the hunk containing the cursor was not staged:\n%s", staged)
	}
	if strings.Contains(staged, "2 edited") {
		t.Errorf("the other hunk was staged too:\n%s", staged)
	}
}

// TestStageOnContextLineDoesNothing: the stage key on a context line has
// nothing to apply, and must not report an error for it either.
func TestStageOnContextLineDoesNothing(t *testing.T) {
	h := newHarness(t)
	h.selectFile("two-hunks.txt")
	h.focusDiffOn(git.LineContext, "16")

	if cmd := h.key(" "); cmd != nil {
		t.Fatal("the stage key on a context line issued a git command")
	}
	if body := h.m.Body(); strings.Contains(body, "git error") {
		t.Errorf("a context line reported an error:\n%s", body)
	}
}

func TestStageFileFromFilesPane(t *testing.T) {
	h := newHarness(t)
	h.selectFile("plain.txt")

	h.run(h.key(" "))

	if got := h.stagedContent("plain.txt"); !strings.Contains(got, "line3 dirty") {
		t.Errorf("the whole file was not staged:\n%s", got)
	}
}

// TestUnstageFollowsTheVisibleSide checks the stage key reverses when the pane
// is showing the staged half. staged.txt has no worktree change, so it shows
// its staged side without the toggle — and the key has to agree with the
// title, not with the toggle.
func TestUnstageFollowsTheVisibleSide(t *testing.T) {
	h := newHarness(t)
	h.selectFile("staged.txt")
	if !h.m.stagedSide() {
		t.Fatal("staged.txt should be showing its staged side")
	}

	h.run(h.key(" "))

	if sel, _ := h.m.files.Selected(); !sel.IsUntracked() {
		t.Errorf("staged.txt is %c%c, want untracked after un-staging", sel.Staged, sel.Unstaged)
	}
}

// TestPartialNoEOLIsRefused: staging one line of a hunk with no trailing
// newline produces a patch git applies happily and wrongly, so it has to be
// refused visibly rather than silently corrupting the index.
func TestPartialNoEOLIsRefused(t *testing.T) {
	h := newHarness(t)
	h.selectFile("noeol.txt")
	h.focusDiffOn(git.LineAdd, "no trailing newline, edited")

	if cmd := h.key(" "); cmd != nil {
		t.Fatal("a partial no-EOL selection was sent to git apply")
	}
	if body := h.m.Body(); !strings.Contains(body, "whole hunk") {
		t.Errorf("the refusal is not visible:\n%s", body)
	}
}

// TestStageKeyIgnoredWhileApplying guards the in-flight window. Holding the
// key builds every patch from the diff on screen, which the first apply has
// already invalidated.
func TestStageKeyIgnoredWhileApplying(t *testing.T) {
	h := newHarness(t)
	h.selectFile("two-hunks.txt")
	h.focusDiffOn(git.LineAdd, "18 edited")

	if cmd := h.key(" "); cmd == nil {
		t.Fatal("the first stage key produced no command")
	}
	if !h.m.applying {
		t.Fatal("the first stage key did not mark an apply in flight")
	}
	if cmd := h.key(" "); cmd != nil {
		t.Error("a second stage key was accepted while the first was in flight")
	}
}

// TestDiffCursorStaysPutAcrossReload: staging a hunk reloads the same file, and
// the cursor has to survive it or every keystroke walks back to the top.
func TestDiffCursorStaysPutAcrossReload(t *testing.T) {
	h := newHarness(t)
	h.selectFile("two-hunks.txt")
	h.focusDiffOn(git.LineAdd, "18 edited")

	_, _, before, _ := h.m.diff.Selection()
	h.m.diff.SetLoading("two-hunks.txt", false)
	h.resolveDiff()

	if _, _, after, _ := h.m.diff.Selection(); after != before {
		t.Errorf("cursor moved to line %d across a reload, want %d", after, before)
	}
}

// TestStageIsRefusedWhileTheDiffIsLoading closes the window between a diff
// request and its answer. The pane still shows the previous file, so a stage
// key that trusted it would build a patch for the wrong path and git would
// apply it without complaint.
func TestStageIsRefusedWhileTheDiffIsLoading(t *testing.T) {
	h := newHarness(t)
	h.selectFile("two-hunks.txt")
	h.focusDiffOn(git.LineAdd, "18 edited")

	// Move to another file without answering the diff request it issues.
	h.send(tea.KeyPressMsg{Code: tea.KeyTab})
	h.press("k")
	if h.m.SelectedPath() == "two-hunks.txt" {
		t.Fatal("the selection did not move")
	}
	h.send(tea.KeyPressMsg{Code: tea.KeyTab})

	if cmd := h.key(" "); cmd != nil {
		t.Error("a stage key was accepted against a diff that had not arrived")
	}
	if _, _, _, ok := h.m.diff.Selection(); ok {
		t.Error("the pane still offers rows from the previous file")
	}
}

// TestWholeFileOnly pins the changes that must not be split into a patch.
// Each of them produces a patch git accepts and gets wrong, so the fallback to
// `git add` is a correctness guard, not a convenience.
func TestWholeFileOnly(t *testing.T) {
	for _, tc := range []struct {
		name   string
		file   git.FileStatus
		staged bool
		want   bool
	}{
		{"worktree modification", git.FileStatus{Kind: git.KindOrdinary, Staged: '.', Unstaged: 'M'}, false, false},
		{"staged modification", git.FileStatus{Kind: git.KindOrdinary, Staged: 'M', Unstaged: '.'}, true, false},
		{"worktree deletion", git.FileStatus{Kind: git.KindOrdinary, Staged: '.', Unstaged: 'D'}, false, true},
		{"staged deletion", git.FileStatus{Kind: git.KindOrdinary, Staged: 'D', Unstaged: '.'}, true, true},
		// A deletion on the side that is not being staged is somebody else's
		// problem; the visible side is still an ordinary edit.
		{"deletion on the other side", git.FileStatus{Kind: git.KindOrdinary, Staged: 'D', Unstaged: 'M'}, false, false},
		{"untracked", git.FileStatus{Kind: git.KindUntracked}, false, false},
		{"conflict", git.FileStatus{Kind: git.KindUnmerged, Staged: 'U', Unstaged: 'U'}, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := wholeFileOnly(tc.file, tc.staged); got != tc.want {
				t.Errorf("wholeFileOnly(%+v, %v) = %v, want %v", tc.file, tc.staged, got, tc.want)
			}
		})
	}
}

// TestPartialStageOfUntrackedFile walks the path where the diff's base changes
// under the pane. An untracked file is diffed against /dev/null because it has
// no index entry; staging one line of it gives it one, so the rest has to be
// diffed against the index instead. Getting that wrong re-offers the whole file
// as an addition and builds the next patch against the wrong base.
func TestPartialStageOfUntrackedFile(t *testing.T) {
	h := newHarness(t)
	// Three lines, so staging one leaves something behind to diff. The
	// fixture's untracked.txt is a single line, where partial and whole are
	// the same thing.
	h.write("untracked.txt", "u1\nu2\nu3\n")
	h.run(h.m.loadStatus())

	h.selectFile("untracked.txt")
	if sel, _ := h.m.files.Selected(); !sel.IsUntracked() {
		t.Fatal("untracked.txt should start with no index entry")
	}
	h.focusDiffOn(git.LineAdd, "u2")

	h.run(h.key(" "))

	if sel, _ := h.m.files.Selected(); sel.IsUntracked() {
		t.Fatal("untracked.txt is still reported as untracked after acquiring an index entry")
	}
	if got, want := h.stagedContent("untracked.txt"), "u2\n"; got != want {
		t.Errorf("index holds %q, want %q", got, want)
	}

	// The remaining two lines have to arrive as additions on top of the index,
	// not as the whole file against /dev/null.
	fd, _, _, ok := h.m.diff.Selection()
	if !ok {
		t.Fatal("the reloaded diff has no rows")
	}
	if fd.Untracked {
		t.Error("the diff is still being taken against /dev/null")
	}
	var added []string
	for _, hunk := range fd.Hunks {
		for _, l := range hunk.Lines {
			if l.Kind == git.LineAdd {
				added = append(added, l.Text)
			}
		}
	}
	if want := []string{"u1", "u3"}; !slices.Equal(added, want) {
		t.Errorf("the remaining diff adds %q, want %q", added, want)
	}
}
