package git_test

import (
	"context"
	"errors"
	"testing"

	"github.com/f3xp/bubblegit/internal/git"
)

// stashRepo is a fixture copy with the identity a stash commit needs, so the
// tests do not depend on the machine's git config.
//
// It also re-adds main.go, which the fixture leaves both deleted from the index
// and untracked on disk. A stash of that pair cannot come back: pop restores
// the tracked deletion first, finds the untracked copy in its way, and refuses
// with "main.go already exists". That is git's own limit, not the wrapper's,
// and the wrapper reports it when it happens.
func stashRepo(t *testing.T) *repo {
	t.Setenv("GIT_AUTHOR_NAME", "test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.com")
	rp := newRepo(t)
	rp.git("add", "main.go")
	return rp
}

func (rp *repo) stashes() []git.Stash {
	rp.t.Helper()
	s, err := git.Stashes(context.Background(), rp.r)
	if err != nil {
		rp.t.Fatal(err)
	}
	return s
}

func TestStashPushAndPop(t *testing.T) {
	rp := stashRepo(t)
	ctx := context.Background()
	before := len(rp.all())

	if err := git.StashPush(ctx, rp.r, true); err != nil {
		t.Fatal(err)
	}
	if n := len(rp.all()); n != 0 {
		t.Fatalf("stash -u left %d entries in status", n)
	}
	s := rp.stashes()
	if len(s) != 1 || s[0].Ref != "stash@{0}" || len(s[0].SHA) != 40 || s[0].Subject == "" {
		t.Fatalf("stash list = %+v, want one stash@{0} with a sha and subject", s)
	}

	if err := git.StashPop(ctx, rp.r, ""); err != nil {
		t.Fatal(err)
	}
	if n := len(rp.all()); n != before {
		t.Errorf("pop restored %d entries, want %d", n, before)
	}
	if len(rp.stashes()) != 0 {
		t.Error("pop left the stash in the list")
	}
}

func TestStashTrackedLeavesUntracked(t *testing.T) {
	rp := stashRepo(t)
	if err := git.StashPush(context.Background(), rp.r, false); err != nil {
		t.Fatal(err)
	}
	for _, f := range rp.all() {
		if !f.IsUntracked() {
			t.Errorf("%s survived a tracked stash: %+v", f.Path, f)
		}
	}
	if !rp.exists("untracked.txt") {
		t.Error("a tracked-only stash took an untracked file")
	}
}

func TestStashApplyAndDrop(t *testing.T) {
	rp := stashRepo(t)
	ctx := context.Background()
	if err := git.StashPush(ctx, rp.r, true); err != nil {
		t.Fatal(err)
	}
	ref := rp.stashes()[0].Ref

	if err := git.StashApply(ctx, rp.r, ref); err != nil {
		t.Fatal(err)
	}
	if len(rp.all()) == 0 {
		t.Error("apply restored nothing")
	}
	if len(rp.stashes()) != 1 {
		t.Error("apply dropped the stash")
	}

	// Back to clean so the drop is a plain drop.
	if err := git.StageAll(ctx, rp.r); err != nil {
		t.Fatal(err)
	}
	rp.git("reset", "-q", "--hard")
	if err := git.StashDrop(ctx, rp.r, ref); err != nil {
		t.Fatal(err)
	}
	if len(rp.stashes()) != 0 {
		t.Error("drop left the stash in the list")
	}
}

func TestStashPopWithNothingStashedReports(t *testing.T) {
	rp := stashRepo(t)
	err := git.StashPop(context.Background(), rp.r, "")
	var ge *git.Error
	if !errors.As(err, &ge) {
		t.Fatalf("got %v, want a *git.Error", err)
	}
}
