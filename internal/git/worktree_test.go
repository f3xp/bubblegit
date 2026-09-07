package git_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/f3xp/bubblegit/internal/git"
)

// git runs a command in the fixture through the same Runner the code under
// test uses, failing the test on error.
func (rp *repo) git(args ...string) {
	rp.t.Helper()
	if _, err := rp.r.Run(context.Background(), args...); err != nil {
		rp.t.Fatal(err)
	}
}

func (rp *repo) all() []git.FileStatus {
	rp.t.Helper()
	all, err := git.Status(context.Background(), rp.r)
	if err != nil {
		rp.t.Fatal(err)
	}
	return all
}

func (rp *repo) exists(path string) bool {
	_, err := os.Stat(filepath.Join(rp.dir, path))
	return !errors.Is(err, os.ErrNotExist)
}

func TestStageAll(t *testing.T) {
	rp := newRepo(t)
	if err := git.StageAll(context.Background(), rp.r); err != nil {
		t.Fatal(err)
	}
	for _, f := range rp.all() {
		if f.IsUnstaged() || f.IsUntracked() {
			t.Errorf("%s still has an unstaged side: %+v", f.Path, f)
		}
	}
}

func TestUnstageAll(t *testing.T) {
	rp := newRepo(t)
	if err := git.UnstageAll(context.Background(), rp.r); err != nil {
		t.Fatal(err)
	}
	for _, f := range rp.all() {
		if f.IsStaged() {
			t.Errorf("%s still has a staged side: %+v", f.Path, f)
		}
	}
	// The new file staged.txt is back to untracked, not gone.
	if !rp.status("staged.txt").IsUntracked() {
		t.Error("unstaging a new file should leave it untracked")
	}
}

func TestDiscardWorktreeKeepsIndexAndUntracked(t *testing.T) {
	rp := newRepo(t)
	if err := git.DiscardWorktree(context.Background(), rp.r); err != nil {
		t.Fatal(err)
	}
	for _, f := range rp.all() {
		if f.IsUnstaged() && !f.IsUntracked() {
			t.Errorf("%s still has an unstaged change: %+v", f.Path, f)
		}
	}
	if !rp.status("staged.txt").IsStaged() {
		t.Error("the staged file lost its index entry")
	}
	if !rp.exists("untracked.txt") {
		t.Error("an untracked file was removed")
	}
}

func TestCleanUntrackedLeavesTrackedChanges(t *testing.T) {
	rp := newRepo(t)
	if err := git.CleanUntracked(context.Background(), rp.r); err != nil {
		t.Fatal(err)
	}
	if rp.exists("untracked.txt") {
		t.Error("untracked.txt survived clean")
	}
	if !rp.status("plain.txt").IsUnstaged() {
		t.Error("clean touched a tracked file's modification")
	}
}
