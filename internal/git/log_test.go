package git_test

import (
	"context"
	"strings"
	"testing"

	"github.com/f3xp/bubblegit/internal/git"
	"github.com/f3xp/bubblegit/internal/gittest"
)

// fixtureCommits is how many commits mksmall.sh builds: base, an edit, a
// feature commit and the merge.
const fixtureCommits = 4

func TestLogReadsEveryField(t *testing.T) {
	r := git.New(gittest.Small(t))

	commits, err := git.Log(context.Background(), r, nil, git.LogPageSize)
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != fixtureCommits {
		t.Fatalf("got %d commits, want %d", len(commits), fixtureCommits)
	}

	head := commits[0]
	switch {
	case len(head.SHA) != 40:
		t.Errorf("SHA %q is not a full object name", head.SHA)
	case head.Short == "" || !strings.HasPrefix(head.SHA, head.Short):
		t.Errorf("short %q does not abbreviate %q", head.Short, head.SHA)
	case head.Author != "fixture":
		t.Errorf("author %q, want fixture", head.Author)
	case head.When.IsZero():
		t.Error("commit has no timestamp")
	case head.Subject != "merge feature into main":
		t.Errorf("subject %q, want the merge subject", head.Subject)
	case !head.IsMerge():
		t.Errorf("HEAD has %d parents, want a merge", len(head.Parents))
	}

	// The root commit's empty parent field is the case that breaks a parser
	// chunking by stride: it is an empty field between two NULs, not a missing
	// one, and reading it as missing slides every later record by one.
	root := commits[len(commits)-1]
	if len(root.Parents) != 0 {
		t.Errorf("root commit has parents %v, want none", root.Parents)
	}
	if root.Subject == "" {
		t.Error("the record after the empty parent field did not survive the stride")
	}
}

// TestLogPagingKeepsSideBranches is the reason paging resumes from a frontier
// rather than from the last SHA on screen.
//
// The fixture's history is main, a feature branch, and a merge. `git log`
// interleaves the two lines, so a page boundary can fall with a side-branch
// commit still unlisted. Resuming with `git log <last-shown>` walks only that
// commit's ancestors, and the side-branch commit — not among them — is never
// listed by any later page either. It disappears from the log entirely.
//
// This test pages the whole fixture two commits at a time and insists every
// commit shows up exactly once.
func TestLogPagingKeepsSideBranches(t *testing.T) {
	ctx := context.Background()
	r := git.New(gittest.Small(t))

	var loaded []git.Commit
	for page := 0; page < fixtureCommits; page++ {
		from := git.Frontier(loaded)
		if len(loaded) > 0 && len(from) == 0 {
			break
		}
		got, err := git.Log(ctx, r, from, 2)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) == 0 {
			break
		}
		loaded = append(loaded, got...)
	}

	seen := make(map[string]bool)
	for _, c := range loaded {
		if seen[c.SHA] {
			t.Errorf("commit %s was listed twice", c.Short)
		}
		seen[c.SHA] = true
	}

	full, err := git.Log(ctx, r, nil, git.LogPageSize)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range full {
		if !seen[c.SHA] {
			t.Errorf("paging never listed %s %q; a side branch was skipped",
				c.Short, c.Subject)
		}
	}
	if len(seen) != len(full) {
		t.Errorf("paging saw %d distinct commits, the full walk has %d", len(seen), len(full))
	}
}

// TestFrontierEndsAtRoot: an empty frontier is how the pane knows to stop
// asking for pages, so a fully loaded history must produce one.
func TestFrontierEndsAtRoot(t *testing.T) {
	r := git.New(gittest.Small(t))

	commits, err := git.Log(context.Background(), r, nil, git.LogPageSize)
	if err != nil {
		t.Fatal(err)
	}
	if from := git.Frontier(commits); len(from) != 0 {
		t.Errorf("a fully loaded history still has a frontier %v", from)
	}
	if from := git.Frontier(commits[:1]); len(from) != 2 {
		t.Errorf("the frontier below a merge is %v, want both its parents", from)
	}
}

// TestLogOnUnbornBranch: a repository with no commits is a normal state, and
// an empty log rather than an error is what the pane has to render.
func TestLogOnUnbornBranch(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	r := git.New(dir)
	if _, err := r.Run(ctx, "init", "-b", "main", dir); err != nil {
		t.Fatal(err)
	}

	commits, err := git.Log(ctx, r, nil, git.LogPageSize)
	if err != nil {
		t.Fatalf("an unborn branch is not an error: %v", err)
	}
	if len(commits) != 0 {
		t.Errorf("got %d commits from a repo with none", len(commits))
	}
}

// TestShowRendersMergeAndRoot guards the detail pane against the two commits
// that render blank under the obvious commands.
//
// A merge: `git show` and `diff-tree -p` both print no patch at all for one
// without -m, and --cc prints nothing for a clean merge because it shows only
// hunks differing from BOTH parents. A root commit: `diff-tree` prints nothing
// without --root, which is one of the reasons Show uses `show` instead.
//
// These are the first and last commits in any repository's history, so a pane
// that fails on them fails on the two a user is most likely to open first.
func TestShowRendersMergeAndRoot(t *testing.T) {
	ctx := context.Background()
	r := git.New(gittest.Small(t))

	commits, err := git.Log(ctx, r, nil, git.LogPageSize)
	if err != nil {
		t.Fatal(err)
	}
	merge, root := commits[0], commits[len(commits)-1]
	if !merge.IsMerge() {
		t.Fatalf("fixture HEAD has %d parents, want a merge; the fixture no longer "+
			"covers this case", len(merge.Parents))
	}

	t.Run("merge", func(t *testing.T) {
		d, err := git.Show(ctx, r, merge.SHA)
		if err != nil {
			t.Fatal(err)
		}
		if len(d.Files) == 0 {
			t.Fatal("a merge commit rendered no patch; -m --first-parent is not being used")
		}
		if got := d.Files[0].Path; got != "feature.txt" {
			t.Errorf("merge brought in %q, want feature.txt", got)
		}
		if d.Email != "fixture@example.com" {
			t.Errorf("email %q, want the fixture identity", d.Email)
		}
	})

	t.Run("root", func(t *testing.T) {
		d, err := git.Show(ctx, r, root.SHA)
		if err != nil {
			t.Fatal(err)
		}
		if len(d.Files) == 0 {
			t.Fatal("the initial commit rendered no patch")
		}
		if len(d.Commit.Parents) != 0 {
			t.Errorf("root commit reports parents %v", d.Commit.Parents)
		}
	})
}

// TestShowSplitsFilesAndPaths covers what single-file diff parsing never sees:
// several files in one patch, a binary among them that must not abort the
// rest, and paths git would otherwise mangle in a header it writes without -z.
func TestShowSplitsFilesAndPaths(t *testing.T) {
	ctx := context.Background()
	r := git.New(gittest.Small(t))

	commits, err := git.Log(ctx, r, nil, git.LogPageSize)
	if err != nil {
		t.Fatal(err)
	}
	root := commits[len(commits)-1]

	d, err := git.Show(ctx, r, root.SHA)
	if err != nil {
		t.Fatal(err)
	}

	got := map[string]git.FileDiff{}
	for _, fd := range d.Files {
		got[fd.Path] = fd
	}

	// A space in the path: the "diff --git a/X b/X" header has no delimiter
	// between the two names, so the path can only be read by arithmetic.
	if _, ok := got["spaced name.txt"]; !ok {
		t.Errorf("no diff for %q; paths: %v", "spaced name.txt", keys(got))
	}
	// Non-ASCII: core.quotepath turns this into octal escapes unless the
	// runner turns it off, and the pane would show \303\274 for ü.
	if _, ok := got["ünïcødé-ファイル.txt"]; !ok {
		t.Errorf("unicode path was mangled; paths: %v", keys(got))
	}
	// The binary marks one file, and the files after it still parse.
	if fd, ok := got["logo.png"]; !ok || !fd.Binary {
		t.Errorf("logo.png binary=%v present=%v, want a binary file", fd.Binary, ok)
	}
	if fd, ok := got["main.go"]; !ok || len(fd.Hunks) == 0 {
		t.Error("the file after the binary one has no hunks; the binary aborted the parse")
	}
}

// TestShowCarriesMessageBody: the log carries only the subject, so a detail
// pane that dropped the body would silently lose everything below the first
// line of every commit message.
func TestShowCarriesMessageBody(t *testing.T) {
	ctx := context.Background()
	dir := gittest.Small(t)
	r := git.New(dir)

	if _, err := r.Run(ctx, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if err := git.CreateCommit(ctx, r, "subject line\n\nA body paragraph.\n", false); err != nil {
		t.Fatal(err)
	}

	d, err := git.Show(ctx, r, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if d.Commit.Subject != "subject line" {
		t.Errorf("subject %q, want the first line only", d.Commit.Subject)
	}
	if !strings.Contains(d.Message, "A body paragraph.") {
		t.Errorf("message %q is missing the body", d.Message)
	}
	if strings.HasSuffix(d.Message, "\n") {
		t.Errorf("message %q keeps a trailing newline", d.Message)
	}
}

func keys(m map[string]git.FileDiff) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
