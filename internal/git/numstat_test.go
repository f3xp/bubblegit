package git_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/f3xp/bubblegit/internal/git"
	"github.com/f3xp/bubblegit/internal/gittest"
)

// The totals are what `git diff --shortstat HEAD` prints for the fixture:
// staged and unstaged together, and nothing for the binary or the untracked
// files.
func TestNumstatPerPathAgainstHead(t *testing.T) {
	stats, err := git.Numstat(context.Background(), git.New(gittest.Small(t)))
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]git.LineStat{
		"plain.txt":  {1, 1},
		"main.go":    {0, 3}, // rm --cached: the whole file is a deletion
		"staged.txt": {1, 0},
	} {
		if got := stats[path]; got != want {
			t.Errorf("%s: got %+v, want %+v", path, got, want)
		}
	}
	for _, absent := range []string{"logo.png", "untracked.txt"} {
		if _, ok := stats[absent]; ok {
			t.Errorf("%s has a line count; binary and untracked files should have none", absent)
		}
	}
	if a, d := git.Total(stats); a != 8 || d != 10 {
		t.Errorf("total +%d -%d, want +8 -10", a, d)
	}
}

// A rename is two entries, one per path, so a lookup by the path status
// reports always lands.
func TestNumstatKeysRenamesByBothPaths(t *testing.T) {
	rp := newRepo(t)
	rp.git("mv", "two-hunks.txt", "moved.txt")
	stats, err := git.Numstat(context.Background(), rp.r)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := stats["moved.txt"]; !ok {
		t.Error("the new path of a rename has no entry")
	}
	if _, ok := stats["two-hunks.txt"]; !ok {
		t.Error("the old path of a rename has no entry")
	}
}

func TestNumstatOnUnbornBranch(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"add", "f"}} {
		if args[0] == "add" {
			os.WriteFile(filepath.Join(dir, "f"), []byte("a\nb\n"), 0o644)
		}
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	stats, err := git.Numstat(context.Background(), git.New(dir))
	if err != nil {
		t.Fatal(err)
	}
	if got := stats["f"]; got != (git.LineStat{2, 0}) {
		t.Errorf("got %+v, want +2 -0", got)
	}
}
