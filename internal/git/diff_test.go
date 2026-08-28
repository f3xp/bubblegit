package git_test

import (
	"context"
	"strings"
	"testing"

	"github.com/f3xp/bubblegit/internal/git"
	"github.com/f3xp/bubblegit/internal/gittest"
)

func TestDiffFile(t *testing.T) {
	r := git.New(gittest.Small(t))
	ctx := context.Background()

	t.Run("unstaged modification", func(t *testing.T) {
		d, err := git.DiffFile(ctx, r, "plain.txt", false, false)
		if err != nil {
			t.Fatal(err)
		}
		if len(d.Hunks) == 0 {
			t.Fatal("no hunks for a modified file")
		}
		var adds, dels int
		for _, h := range d.Hunks {
			for _, l := range h.Lines {
				switch l.Kind {
				case git.LineAdd:
					adds++
				case git.LineDel:
					dels++
				}
			}
		}
		if adds == 0 || dels == 0 {
			t.Errorf("got %d adds and %d dels, want both non-zero", adds, dels)
		}
	})

	t.Run("staged add", func(t *testing.T) {
		d, err := git.DiffFile(ctx, r, "staged.txt", true, false)
		if err != nil {
			t.Fatal(err)
		}
		if len(d.Hunks) != 1 {
			t.Fatalf("got %d hunks, want 1", len(d.Hunks))
		}
	})

	// An untracked file has no index entry, so plain `git diff` says nothing
	// about it and the pane would render blank. --no-index against /dev/null
	// gives a whole-file addition, at the cost of git exiting 1 to mean
	// "differs" — which the runner must not treat as a failure.
	t.Run("untracked renders as a whole-file addition", func(t *testing.T) {
		plain, err := git.DiffFile(ctx, r, "untracked.txt", false, false)
		if err != nil {
			t.Fatal(err)
		}
		if !plain.IsEmpty() {
			t.Skip("git now diffs untracked files without --no-index")
		}

		d, err := git.DiffFile(ctx, r, "untracked.txt", false, true)
		if err != nil {
			t.Fatalf("--no-index exits 1 when files differ; that is not an error: %v", err)
		}
		if len(d.Hunks) != 1 {
			t.Fatalf("got %d hunks, want 1: %+v", len(d.Hunks), d)
		}
		for _, l := range d.Hunks[0].Lines {
			if l.Kind != git.LineAdd {
				t.Errorf("line %+v is not an addition; a new file is all additions", l)
			}
		}
	})

	t.Run("binary file", func(t *testing.T) {
		d, err := git.DiffFile(ctx, r, "logo.png", false, false)
		if err != nil {
			t.Fatal(err)
		}
		if !d.Binary {
			t.Fatalf("logo.png not detected as binary: %+v", d)
		}
		if len(d.Hunks) != 0 {
			t.Errorf("binary diff has %d hunks, want 0", len(d.Hunks))
		}
		if d.IsEmpty() {
			t.Error("a binary diff is not empty; the pane must show a placeholder, not nothing")
		}
	})

	// "\ No newline at end of file" annotates the preceding line. It must be
	// preserved: dropping it silently appends a newline when the patch is
	// later applied.
	t.Run("no newline at end of file", func(t *testing.T) {
		d, err := git.DiffFile(ctx, r, "noeol.txt", false, false)
		if err != nil {
			t.Fatal(err)
		}
		var markers int
		for _, h := range d.Hunks {
			for _, l := range h.Lines {
				if l.Kind == git.LineNoEOL {
					markers++
					if strings.HasPrefix(l.Text, "\\") {
						t.Errorf("marker text %q still carries its prefix", l.Text)
					}
				}
			}
		}
		if markers != 2 {
			t.Errorf("got %d no-newline markers, want 2 (one per side)", markers)
		}
	})
}

func TestParseHunkHeaderLineNumbers(t *testing.T) {
	r := git.New(gittest.Small(t))
	d, err := git.DiffFile(context.Background(), r, "spaced name.txt", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Hunks) == 0 {
		t.Fatal("no hunks")
	}
	for _, h := range d.Hunks {
		if h.OldStart <= 0 || h.NewStart <= 0 {
			t.Errorf("hunk %q has non-positive start lines: %+v", h.Header, h)
		}
		// Line numbers must advance monotonically on the side each line exists.
		lastOld, lastNew := 0, 0
		for _, l := range h.Lines {
			if l.OldNum != 0 {
				if l.OldNum <= lastOld {
					t.Errorf("old line numbers not increasing at %+v", l)
				}
				lastOld = l.OldNum
			}
			if l.NewNum != 0 {
				if l.NewNum <= lastNew {
					t.Errorf("new line numbers not increasing at %+v", l)
				}
				lastNew = l.NewNum
			}
		}
	}
}
