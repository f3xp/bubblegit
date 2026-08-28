package git_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/f3xp/bubblegit/internal/git"
	"github.com/f3xp/bubblegit/internal/gittest"
)

func TestSplitZ(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"only terminator", "\x00", nil},
		{"one field", "a\x00", []string{"a"}},
		{"several", "a\x00b\x00c\x00", []string{"a", "b", "c"}},
		{"no trailing terminator", "a\x00b", []string{"a", "b"}},
		{"spaces and unicode survive", "spaced name.txt\x00ünïcødé.txt\x00",
			[]string{"spaced name.txt", "ünïcødé.txt"}},
		// The whole reason for -z: a newline in a path is data, not a delimiter.
		{"newline in path", "we\nird.txt\x00ok.txt\x00", []string{"we\nird.txt", "ok.txt"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := git.SplitZ([]byte(tc.in))
			if len(got) != len(tc.want) {
				t.Fatalf("got %d fields %q, want %d %q", len(got), got, len(tc.want), tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("field %d = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestRunErrorCarriesStderr(t *testing.T) {
	r := git.New(gittest.Small(t))
	_, err := r.Run(context.Background(), "cat-file", "-p", "0000000000000000000000000000000000000000")
	if err == nil {
		t.Fatal("expected an error for a bogus object")
	}
	var gerr *git.Error
	if !errors.As(err, &gerr) {
		t.Fatalf("error is %T, want *git.Error", err)
	}
	if gerr.ExitCode == 0 {
		t.Error("exit code is 0 on a failed command")
	}
	// The UI shows this string to the user, so it has to be git's own message.
	if gerr.Stderr == "" {
		t.Error("stderr is empty; the UI would have nothing to show")
	}
	if !strings.Contains(err.Error(), "cat-file") {
		t.Errorf("error %q does not name the failing command", err)
	}
}

func TestRunRespectsContextCancellation(t *testing.T) {
	r := git.New(gittest.Small(t))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.Run(ctx, "status", "--porcelain=v2"); err == nil {
		t.Fatal("expected an error from an already-cancelled context")
	}
}

// TestStatusIndexRefresh pins the reason the runner does NOT set
// GIT_OPTIONAL_LOCKS=0.
//
// With that variable set, git cannot write the refreshed index back, so every
// `status` re-stats the entire worktree — ~14ms of pure waste per call on the
// 100k fixture, on the single most frequently run command in a git TUI. This
// asserts structurally (the index file is rewritten) rather than by timing, so
// it cannot flake.
func TestStatusIndexRefresh(t *testing.T) {
	dir := gittest.Small(t)
	r := git.New(dir)
	ctx := context.Background()

	index := filepath.Join(dir, ".git", "index")

	// Settle: let git write a fully refreshed index first.
	if _, err := r.Run(ctx, "status", "--porcelain=v2", "-z"); err != nil {
		t.Fatal(err)
	}

	// Invalidate the stat cache the way a build or a branch switch would.
	stale := time.Now().Add(-time.Hour)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() == ".git" || e.IsDir() {
			continue
		}
		if err := os.Chtimes(filepath.Join(dir, e.Name()), stale, stale); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chtimes(index, stale, stale); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(index)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := r.Run(ctx, "status", "--porcelain=v2", "-z"); err != nil {
		t.Fatal(err)
	}

	after, err := os.Stat(index)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().After(before.ModTime()) {
		t.Error("status did not rewrite .git/index; the runner is suppressing optional " +
			"locks, which makes every status re-stat the whole worktree")
	}
}

// TestStatusParsesAwkwardPaths is the end-to-end check that -z plus the small
// fixture actually round-trips the path shapes that break naive parsers.
func TestStatusParsesAwkwardPaths(t *testing.T) {
	r := git.New(gittest.Small(t))
	out, err := r.Run(context.Background(),
		"status", "--porcelain=v2", "-z", "--untracked-files=all")
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	for _, want := range []string{"spaced name.txt", "ünïcødé-ファイル.txt"} {
		if !strings.Contains(got, want) {
			t.Errorf("status output is missing %q verbatim; core.quotepath escaping leaked through\n%q",
				want, got)
		}
	}
}
