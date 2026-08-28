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

func TestDiscoverFromSubdirectory(t *testing.T) {
	root := gittest.Small(t)
	sub := filepath.Join(root, "nested", "deeper")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := git.Discover(context.Background(), sub)
	if err != nil {
		t.Fatal(err)
	}
	// macOS resolves /var to /private/var, so compare resolved paths.
	want, _ := filepath.EvalSymlinks(root)
	gotResolved, _ := filepath.EvalSymlinks(got)
	if gotResolved != want {
		t.Errorf("Discover = %q, want %q", gotResolved, want)
	}
}

func TestReadHead(t *testing.T) {
	ctx := context.Background()

	t.Run("on a branch", func(t *testing.T) {
		r := git.New(gittest.Small(t))
		h, err := git.ReadHead(ctx, r)
		if err != nil {
			t.Fatal(err)
		}
		if h.Branch != "main" || h.Detached || len(h.Commit) != 40 {
			t.Errorf("got %+v, want branch=main detached=false and a full SHA", h)
		}
	})

	t.Run("detached", func(t *testing.T) {
		r := git.New(gittest.Small(t))
		if _, err := r.Run(ctx, "checkout", "--detach", "HEAD~1"); err != nil {
			t.Fatal(err)
		}
		h, err := git.ReadHead(ctx, r)
		if err != nil {
			t.Fatal(err)
		}
		if !h.Detached || h.Branch != "" || len(h.Commit) != 40 {
			t.Errorf("got %+v, want detached with a SHA and no branch", h)
		}
	})

	t.Run("unborn branch", func(t *testing.T) {
		dir := t.TempDir()
		r := git.New(dir)
		if _, err := r.Run(ctx, "init", "-b", "main", dir); err != nil {
			t.Fatal(err)
		}
		h, err := git.ReadHead(ctx, r)
		if err != nil {
			t.Fatalf("a repo with no commits is a normal state, not an error: %v", err)
		}
		if h.Branch != "main" || h.Commit != "" {
			t.Errorf("got %+v, want branch=main with no commit", h)
		}
	})
}

// TestCommitDetailShowsMerges guards the commit-detail pane against rendering
// blank on merges.
//
// `git diff-tree -p <merge>` prints nothing at all, and `--cc` prints nothing
// for a clean merge (it only shows hunks that differ from BOTH parents).
// Only `-m --first-parent` answers the question a user is actually asking:
// what did this merge bring in?
func TestCommitDetailShowsMerges(t *testing.T) {
	ctx := context.Background()
	r := git.New(gittest.Small(t))

	head, err := r.Run(ctx, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	merge := strings.TrimSpace(string(head))

	parents, err := r.Run(ctx, "rev-list", "--parents", "-n", "1", merge)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(strings.Fields(string(parents))); n != 3 {
		t.Fatalf("fixture HEAD has %d fields from rev-list --parents, want 3 "+
			"(a merge commit); the fixture no longer covers this case", n)
	}

	t.Run("plain diff-tree is empty (the bug being guarded)", func(t *testing.T) {
		out, err := r.Run(ctx, "diff-tree", "-p", "--no-color", "-r", merge)
		if err != nil {
			t.Fatal(err)
		}
		if len(out) != 0 {
			t.Skipf("git no longer suppresses merge diffs by default (%d bytes); "+
				"the -m --first-parent workaround may be unnecessary", len(out))
		}
	})

	t.Run("with -m --first-parent", func(t *testing.T) {
		out, err := r.Run(ctx, "diff-tree", "-p", "--no-color", "-r", "-m", "--first-parent", merge)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(out), "feature.txt") {
			t.Errorf("merge detail does not mention the file the merge brought in:\n%s", out)
		}
	})
}
