package git_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/f3xp/bubblegit/internal/git"
	"github.com/f3xp/bubblegit/internal/gittest"
)

func byPath(fs []git.FileStatus, path string) []git.FileStatus {
	var out []git.FileStatus
	for _, f := range fs {
		if f.Path == path {
			out = append(out, f)
		}
	}
	return out
}

func TestStatusParsesFixture(t *testing.T) {
	r := git.New(gittest.Small(t))
	fs, err := git.Status(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("path with a space", func(t *testing.T) {
		got := byPath(fs, "spaced name.txt")
		if len(got) != 1 {
			t.Fatalf("got %d entries, want 1: %+v", len(got), fs)
		}
		if got[0].Staged != '.' || got[0].Unstaged != 'M' {
			t.Errorf("XY = %q%q, want \".M\"", got[0].Staged, got[0].Unstaged)
		}
	})

	t.Run("non-ASCII path arrives unescaped", func(t *testing.T) {
		// Without -z this is core.quotepath'd to "\303\274n...".
		if got := byPath(fs, "ünïcødé-ファイル.txt"); len(got) != 1 {
			t.Fatalf("got %d entries, want 1; quotepath escaping leaked through", len(got))
		}
	})

	t.Run("staged add", func(t *testing.T) {
		got := byPath(fs, "staged.txt")
		if len(got) != 1 {
			t.Fatalf("got %d entries, want 1", len(got))
		}
		if !got[0].IsStaged() || got[0].IsUnstaged() {
			t.Errorf("%+v: want staged and not unstaged", got[0])
		}
	})

	t.Run("untracked", func(t *testing.T) {
		got := byPath(fs, "untracked.txt")
		if len(got) != 1 || !got[0].IsUntracked() {
			t.Fatalf("got %+v, want a single untracked entry", got)
		}
	})

	// git reports the same path twice when it is deleted from the index but
	// still present on disk. Both facts are kept; merging them would invent
	// an XY code git never emits.
	t.Run("same path reported twice", func(t *testing.T) {
		got := byPath(fs, "main.go")
		if len(got) != 2 {
			t.Fatalf("got %d entries for main.go, want 2 (deleted-from-index and untracked): %+v", len(got), got)
		}
		var sawDeleted, sawUntracked bool
		for _, f := range got {
			if f.Kind == git.KindOrdinary && f.Staged == 'D' {
				sawDeleted = true
			}
			if f.IsUntracked() {
				sawUntracked = true
			}
		}
		if !sawDeleted || !sawUntracked {
			t.Errorf("got %+v, want one deleted-from-index and one untracked", got)
		}
	})
}

func TestStatusParsesRename(t *testing.T) {
	dir := gittest.Small(t)
	r := git.New(dir)
	ctx := context.Background()

	if _, err := r.Run(ctx, "mv", "noeol.txt", "renamed noeol.txt"); err != nil {
		t.Fatal(err)
	}
	fs, err := git.Status(ctx, r)
	if err != nil {
		t.Fatal(err)
	}

	got := byPath(fs, "renamed noeol.txt")
	if len(got) != 1 {
		t.Fatalf("got %d entries, want 1: %+v", len(got), fs)
	}
	f := got[0]
	if f.Kind != git.KindRenamed {
		t.Errorf("Kind = %q, want %q", f.Kind, git.KindRenamed)
	}
	// A rename record spans two NUL fields; if the second is not consumed,
	// the original path leaks in as a bogus extra entry.
	if f.OrigPath != "noeol.txt" {
		t.Errorf("OrigPath = %q, want %q", f.OrigPath, "noeol.txt")
	}
	if len(f.Score) < 2 || f.Score[0] != 'R' {
		t.Errorf("Score = %q, want something like \"R100\"", f.Score)
	}
	if extra := byPath(fs, "noeol.txt"); len(extra) != 0 {
		t.Errorf("original path leaked in as its own entry: %+v", extra)
	}
}

// TestStatusParsesConflict covers the `u` record, whose field layout differs
// from an ordinary entry (three stages, three hashes rather than two). M1 is
// read-only, so this only has to parse without losing the path.
func TestStatusParsesConflict(t *testing.T) {
	dir := t.TempDir()
	r := git.New(dir)
	ctx := context.Background()

	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	t.Setenv("GIT_AUTHOR_NAME", "fixture")
	t.Setenv("GIT_AUTHOR_EMAIL", "fixture@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "fixture")
	t.Setenv("GIT_COMMITTER_EMAIL", "fixture@example.com")

	mustRun := func(args ...string) {
		t.Helper()
		if _, err := r.Run(ctx, args...); err != nil {
			t.Fatal(err)
		}
	}
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	mustRun("init", "-b", "main", dir)
	write("conflicted.txt", "base\n")
	mustRun("add", "-A")
	mustRun("commit", "-m", "base")

	mustRun("checkout", "-b", "other")
	write("conflicted.txt", "from other\n")
	mustRun("commit", "-am", "other side")

	mustRun("checkout", "main")
	write("conflicted.txt", "from main\n")
	mustRun("commit", "-am", "main side")

	// Expected to fail: that is the point.
	if _, err := r.Run(ctx, "merge", "other"); err == nil {
		t.Fatal("merge succeeded; the test no longer produces a conflict")
	}

	fs, err := git.Status(ctx, r)
	if err != nil {
		t.Fatalf("status must not fail on a conflicted tree: %v", err)
	}

	got := byPath(fs, "conflicted.txt")
	if len(got) != 1 {
		t.Fatalf("got %d entries for the conflicted file, want 1: %+v", len(got), fs)
	}
	if !got[0].IsUnmerged() {
		t.Errorf("Kind = %q, want %q (unmerged)", got[0].Kind, git.KindUnmerged)
	}
	for _, f := range fs {
		if f.Path == "" {
			t.Errorf("parsed an entry with an empty path: %+v", f)
		}
	}
}
