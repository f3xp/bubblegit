package git_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/f3xp/bubblegit/internal/git"
	"github.com/f3xp/bubblegit/internal/gittest"
)

// message is HEAD's commit message, read without going through the code the
// tests are checking.
func message(t *testing.T, r *git.Runner) string {
	t.Helper()
	out, err := r.Run(context.Background(), "log", "-1", "--format=%B")
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimRight(string(out), "\n")
}

func TestCommit(t *testing.T) {
	ctx := context.Background()
	r := git.New(gittest.Small(t))
	before, err := git.ReadHead(ctx, r)
	if err != nil {
		t.Fatal(err)
	}

	if err := git.CreateCommit(ctx, r, "subject line\n\nA body.\n", false); err != nil {
		t.Fatal(err)
	}

	after, err := git.ReadHead(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if after.Commit == before.Commit {
		t.Error("HEAD did not move")
	}
	if got, want := message(t, r), "subject line\n\nA body."; got != want {
		t.Errorf("message = %q, want %q", got, want)
	}
}

// A '#' line is content here, not a comment: nothing in the message came from
// a template, so the user typed it. Stripping it would be silent data loss.
func TestCommitKeepsHashLines(t *testing.T) {
	ctx := context.Background()
	r := git.New(gittest.Small(t))
	if err := git.CreateCommit(ctx, r, "fix #12\n\n# still content\n", false); err != nil {
		t.Fatal(err)
	}
	if got, want := message(t, r), "fix #12\n\n# still content"; got != want {
		t.Errorf("message = %q, want %q", got, want)
	}
}

func TestAmendReplacesHead(t *testing.T) {
	ctx := context.Background()
	r := git.New(gittest.Small(t))
	if err := git.CreateCommit(ctx, r, "first", false); err != nil {
		t.Fatal(err)
	}
	head, err := git.ReadHead(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	n := len(strings.Fields(revList(t, r)))

	if err := git.CreateCommit(ctx, r, "reworded", true); err != nil {
		t.Fatal(err)
	}
	after, err := git.ReadHead(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if after.Commit == head.Commit {
		t.Error("amend left HEAD where it was")
	}
	if got := len(strings.Fields(revList(t, r))); got != n {
		t.Errorf("history is %d commits after amend, want %d", got, n)
	}
	if got := message(t, r); got != "reworded" {
		t.Errorf("message = %q, want reworded", got)
	}
}

func revList(t *testing.T, r *git.Runner) string {
	t.Helper()
	out, err := r.Run(context.Background(), "rev-list", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// A message-only amend is legal and useful, so an empty index must not block
// it — only a plain commit.
func TestAmendWithNothingStaged(t *testing.T) {
	ctx := context.Background()
	r := git.New(gittest.Small(t))
	if err := git.CreateCommit(ctx, r, "first", false); err != nil {
		t.Fatal(err)
	}
	if err := git.CreateCommit(ctx, r, "reworded only", true); err != nil {
		t.Fatalf("message-only amend: %v", err)
	}
}

func TestCommitWithNothingStagedFails(t *testing.T) {
	ctx := context.Background()
	r := git.New(gittest.Small(t))
	if err := git.CreateCommit(ctx, r, "first", false); err != nil {
		t.Fatal(err)
	}
	err := git.CreateCommit(ctx, r, "nothing to record", false)
	if err == nil {
		t.Fatal("committing an empty index succeeded")
	}
	if !strings.Contains(err.Error(), "no changes added to commit") {
		t.Errorf("error %q does not explain that there is nothing to commit", err)
	}
}

// Hooks working is the reason this project shells out to git at all, so a
// hook that rejects a commit has to reach the user with what it said.
func TestPreCommitHookFailureCarriesStderr(t *testing.T) {
	ctx := context.Background()
	dir := gittest.Small(t)
	r := git.New(dir)
	writeHook(t, dir, "pre-commit", "#!/bin/sh\necho 'lint failed: trailing whitespace' >&2\nexit 1\n")

	err := git.CreateCommit(ctx, r, "blocked", false)
	if err == nil {
		t.Fatal("the pre-commit hook did not block the commit")
	}
	if !strings.Contains(err.Error(), "lint failed") {
		t.Errorf("error %q does not carry the hook's stderr", err)
	}
}

func writeHook(t *testing.T, dir, name, body string) {
	t.Helper()
	// core.hooksPath may be set globally; ask git where hooks actually live.
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

// An editor spawned under the TUI would corrupt the terminal, so git must
// never be able to reach one. GIT_EDITOR=false makes the attempt fail loudly
// instead of committing whatever was in the buffer.
func TestEditorIsNeverSpawned(t *testing.T) {
	r := git.New(gittest.Small(t))
	if _, err := r.Run(context.Background(), "commit", "--allow-empty"); err == nil {
		t.Fatal("git opened an editor and committed; GIT_EDITOR is not blocking it")
	}
}

func TestHeadMessage(t *testing.T) {
	ctx := context.Background()
	r := git.New(gittest.Small(t))
	if err := git.CreateCommit(ctx, r, "subject\n\nbody\n", false); err != nil {
		t.Fatal(err)
	}
	got, err := git.HeadMessage(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if want := "subject\n\nbody"; got != want {
		t.Errorf("HeadMessage = %q, want %q", got, want)
	}
}

// An unborn branch has no HEAD to read a message from. That is a normal state,
// not a failure, so amend can pre-fill with nothing and let git refuse.
func TestHeadMessageOnUnbornBranch(t *testing.T) {
	dir := t.TempDir()
	r := git.New(dir)
	if _, err := r.Run(context.Background(), "init", "-q", "."); err != nil {
		t.Fatal(err)
	}
	got, err := git.HeadMessage(context.Background(), r)
	if err != nil {
		t.Fatalf("unborn branch reported an error: %v", err)
	}
	if got != "" {
		t.Errorf("HeadMessage = %q, want empty", got)
	}
}
