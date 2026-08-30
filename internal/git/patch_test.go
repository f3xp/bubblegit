package git_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/f3xp/bubblegit/internal/git"
	"github.com/f3xp/bubblegit/internal/gittest"
)

// These tests round-trip through a real repository rather than comparing the
// synthesised patch to a golden string. A patch that reads correctly and does
// not apply — or applies to the wrong offset — is the entire failure mode, and
// only git can tell us which happened.

type repo struct {
	t   *testing.T
	r   *git.Runner
	dir string
}

func newRepo(t *testing.T) *repo {
	t.Helper()
	dir := gittest.Small(t)
	return &repo{t: t, r: git.New(dir), dir: dir}
}

func (rp *repo) write(path, content string) {
	rp.t.Helper()
	if err := os.WriteFile(filepath.Join(rp.dir, path), []byte(content), 0o644); err != nil {
		rp.t.Fatal(err)
	}
}

func (rp *repo) remove(path string) {
	rp.t.Helper()
	if err := os.Remove(filepath.Join(rp.dir, path)); err != nil {
		rp.t.Fatal(err)
	}
}

func (rp *repo) diff(path string, staged, untracked bool) git.FileDiff {
	rp.t.Helper()
	d, err := git.DiffFile(context.Background(), rp.r, path, staged, untracked)
	if err != nil {
		rp.t.Fatalf("diff %s: %v", path, err)
	}
	return d
}

// apply synthesises and applies in one step, failing the test on either.
func (rp *repo) apply(fd git.FileDiff, hunk, line int, reverse bool) {
	rp.t.Helper()
	p, err := git.Patch(fd, hunk, line, reverse)
	if err != nil {
		rp.t.Fatalf("Patch: %v", err)
	}
	if p == nil {
		rp.t.Fatal("Patch returned no patch, want one")
	}
	if err := git.ApplyCached(context.Background(), rp.r, p, reverse); err != nil {
		rp.t.Fatalf("apply:\n%s\n%v", p, err)
	}
}

// staged returns the file's content as the index now holds it.
func (rp *repo) staged(path string) string {
	rp.t.Helper()
	out, err := rp.r.Run(context.Background(), "show", ":"+path)
	if err != nil {
		rp.t.Fatalf("show :%s: %v", path, err)
	}
	return string(out)
}

func (rp *repo) status(path string) git.FileStatus {
	rp.t.Helper()
	all, err := git.Status(context.Background(), rp.r)
	if err != nil {
		rp.t.Fatal(err)
	}
	for _, f := range all {
		if f.Path == path {
			return f
		}
	}
	rp.t.Fatalf("%s not in status", path)
	return git.FileStatus{}
}

// lineIndex finds the position of a line within a hunk, so the tests name the
// line they mean instead of hard-coding an offset that moves with the fixture.
func lineIndex(t *testing.T, h git.Hunk, kind git.LineKind, text string) int {
	t.Helper()
	for i, l := range h.Lines {
		if l.Kind == kind && l.Text == text {
			return i
		}
	}
	t.Fatalf("no %c%q in hunk %q", kind, text, h.Header)
	return -1
}

// TestPatchStagesOneLine pins what staging one side of a modification means.
//
// plain.txt replaces "line3" with "line3 dirty". Staging only the addition
// leaves the deletion unstaged, so the index gains a line and keeps the old
// one — five lines, not four. That looks like a bug and is not: it is the same
// thing `git add -p` does when a hunk is edited down to its + line, and the
// two halves are separately stageable precisely because they are separate
// changes.
//
// It also shows why the diff has to be re-read between applies: the second
// stage below is computed against an index the first one already moved.
func TestPatchStagesOneLine(t *testing.T) {
	rp := newRepo(t)
	fd := rp.diff("plain.txt", false, false)

	rp.apply(fd, 0, lineIndex(t, fd.Hunks[0], git.LineAdd, "line3 dirty"), false)
	if got, want := rp.staged("plain.txt"), "line1\nline2 edited\nline3\nline3 dirty\nline4\n"; got != want {
		t.Fatalf("after staging the addition the index holds %q, want %q", got, want)
	}

	fd = rp.diff("plain.txt", false, false)
	rp.apply(fd, 0, lineIndex(t, fd.Hunks[0], git.LineDel, "line3"), false)
	if got, want := rp.staged("plain.txt"), "line1\nline2 edited\nline3 dirty\nline4\n"; got != want {
		t.Errorf("after staging the deletion too the index holds %q, want %q", got, want)
	}
}

// TestPatchStagesSecondHunk is the offset test. A hunk starting at line 1
// applies correctly even with a wrong start, because git searches from there;
// the second hunk of two-hunks.txt starts at 15, so a start this code invents
// rather than carries over lands in the wrong place or not at all.
func TestPatchStagesSecondHunk(t *testing.T) {
	rp := newRepo(t)
	fd := rp.diff("two-hunks.txt", false, false)
	if len(fd.Hunks) != 2 {
		t.Fatalf("fixture has %d hunks, want 2", len(fd.Hunks))
	}
	if fd.Hunks[1].OldStart == 1 {
		t.Fatal("second hunk starts at 1, so this test proves nothing")
	}

	rp.apply(fd, 1, -1, false)

	staged := rp.staged("two-hunks.txt")
	if !strings.Contains(staged, "18 edited") {
		t.Errorf("second hunk not staged:\n%s", staged)
	}
	if strings.Contains(staged, "2 edited") {
		t.Errorf("first hunk staged too, should have been left alone:\n%s", staged)
	}
}

func TestPatchStagesLineInSecondHunk(t *testing.T) {
	rp := newRepo(t)
	fd := rp.diff("two-hunks.txt", false, false)

	i := lineIndex(t, fd.Hunks[1], git.LineAdd, "18 edited")
	rp.apply(fd, 1, i, false)

	if staged := rp.staged("two-hunks.txt"); !strings.Contains(staged, "18 edited") ||
		strings.Contains(staged, "2 edited") {
		t.Errorf("wrong lines staged:\n%s", staged)
	}
}

// TestPatchUnstagesOneLine drives the reverse direction, where the roles of
// the unselected lines swap: an unselected addition has to become context
// because it is present in the index the patch is applied against.
func TestPatchUnstagesOneLine(t *testing.T) {
	rp := newRepo(t)
	if err := git.StageFile(context.Background(), rp.r, "two-hunks.txt"); err != nil {
		t.Fatal(err)
	}

	fd := rp.diff("two-hunks.txt", true, false)
	i := lineIndex(t, fd.Hunks[1], git.LineAdd, "18 edited")
	rp.apply(fd, 1, i, true)

	staged := rp.staged("two-hunks.txt")
	if strings.Contains(staged, "18 edited") {
		t.Errorf("line not unstaged:\n%s", staged)
	}
	if !strings.Contains(staged, "2 edited") {
		t.Errorf("the other hunk was unstaged too:\n%s", staged)
	}
}

// TestPatchAwkwardPaths covers the two header shapes that go wrong silently:
// an unquoted space truncates nothing but an unquoted tab stages a different,
// shorter path, and non-ASCII must survive core.quotepath.
func TestPatchAwkwardPaths(t *testing.T) {
	for _, tc := range []struct{ path, add string }{
		{"spaced name.txt", "x"},
		{"ünïcødé-ファイル.txt", "unicode edited"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			rp := newRepo(t)
			fd := rp.diff(tc.path, false, false)
			rp.apply(fd, 0, lineIndex(t, fd.Hunks[0], git.LineAdd, tc.add), false)

			if got := rp.staged(tc.path); !strings.Contains(got, tc.add) {
				t.Errorf("index holds %q, want it to contain %q", got, tc.add)
			}
		})
	}
}

func TestPatchTabInPathStagesThatPath(t *testing.T) {
	rp := newRepo(t)
	const path = "tab\there.txt"
	rp.write(path, "a\nb\n")

	fd := rp.diff(path, false, true)
	rp.apply(fd, 0, lineIndex(t, fd.Hunks[0], git.LineAdd, "a"), false)

	// The bug this guards: an unquoted header splits at the tab, so git stages
	// a file called "tab" and reports success.
	if got := rp.staged(path); got != "a\n" {
		t.Errorf("index holds %q for the tabbed path, want %q", got, "a\n")
	}
}

func TestPatchStagesUntrackedLines(t *testing.T) {
	rp := newRepo(t)
	rp.write("untracked.txt", "u1\nu2\nu3\n")

	fd := rp.diff("untracked.txt", false, true)
	rp.apply(fd, 0, lineIndex(t, fd.Hunks[0], git.LineAdd, "u2"), false)

	if got, want := rp.staged("untracked.txt"), "u2\n"; got != want {
		t.Errorf("index holds %q, want %q", got, want)
	}
}

// TestPatchRefusesPartialNoEOL pins the one case git accepts and gets wrong.
// Selecting a single line of noeol.txt's hunk leaves a no-newline marker on a
// line that is not last, and git apply silently concatenates the two lines.
func TestPatchRefusesPartialNoEOL(t *testing.T) {
	rp := newRepo(t)
	fd := rp.diff("noeol.txt", false, false)

	i := lineIndex(t, fd.Hunks[0], git.LineAdd, "no trailing newline, edited")
	if _, err := git.Patch(fd, 0, i, false); !errors.Is(err, git.ErrNoEOLPartial) {
		t.Fatalf("line-level patch of a no-EOL hunk: %v, want ErrNoEOLPartial", err)
	}

	// The whole hunk is still stageable: nothing is dropped, so every marker
	// still follows the line it annotates.
	rp.apply(fd, 0, -1, false)
	if got, want := rp.staged("noeol.txt"), "no trailing newline, edited"; got != want {
		t.Errorf("index holds %q, want %q", got, want)
	}
}

func TestPatchContextLineIsNoOp(t *testing.T) {
	rp := newRepo(t)
	fd := rp.diff("two-hunks.txt", false, false)

	i := lineIndex(t, fd.Hunks[0], git.LineContext, "1")
	p, err := git.Patch(fd, 0, i, false)
	if err != nil {
		t.Fatalf("Patch on a context line: %v", err)
	}
	if p != nil {
		t.Errorf("Patch on a context line produced:\n%s", p)
	}
}

// TestStageFileRemovesDeletedEntry is why whole-file staging is git add and
// not a patch covering every hunk: an all-deletion patch writes the empty blob
// into the index and leaves the entry behind.
func TestStageFileRemovesDeletedEntry(t *testing.T) {
	rp := newRepo(t)
	rp.remove("plain.txt")
	if err := git.StageFile(context.Background(), rp.r, "plain.txt"); err != nil {
		t.Fatal(err)
	}

	if got := rp.status("plain.txt"); got.Staged != 'D' {
		t.Errorf("staged code %q, want D — the index entry is still there", string(got.Staged))
	}
}

func TestUnstageFile(t *testing.T) {
	rp := newRepo(t)
	if err := git.UnstageFile(context.Background(), rp.r, "staged.txt"); err != nil {
		t.Fatal(err)
	}
	if got := rp.status("staged.txt"); !got.IsUntracked() {
		t.Errorf("staged.txt is %c%c, want untracked again", got.Staged, got.Unstaged)
	}
}
