package git_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/f3xp/bubblegit/internal/git"
	"github.com/f3xp/bubblegit/internal/gittest"
)

// fixtureBranches are mksmall.sh's five branches in the order Branches
// returns them, which is git's refname sort.
var fixtureBranches = []string{"behind", "feature", "gone-upstream", "main", "synced"}

func branchesByName(tb testing.TB, dir string) map[string]git.Branch {
	tb.Helper()
	list, err := git.Branches(context.Background(), git.New(dir))
	if err != nil {
		tb.Fatal(err)
	}
	out := make(map[string]git.Branch, len(list))
	for _, b := range list {
		out[b.Name] = b
	}
	return out
}

// gitOut runs git in the fixture and returns its stdout, pinning the identity
// the fixture scripts pin so a commit made here needs no global config.
func gitOut(tb testing.TB, dir string, args ...string) string {
	tb.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=fixture", "GIT_AUTHOR_EMAIL=fixture@example.com",
		"GIT_COMMITTER_NAME=fixture", "GIT_COMMITTER_EMAIL=fixture@example.com")
	out, err := cmd.Output()
	if err != nil {
		tb.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return string(out)
}

func TestBranchesReadsEveryField(t *testing.T) {
	dir := gittest.Small(t)

	list, err := git.Branches(context.Background(), git.New(dir))
	if err != nil {
		t.Fatal(err)
	}

	var names []string
	for _, b := range list {
		names = append(names, b.Name)
	}
	if strings.Join(names, ",") != strings.Join(fixtureBranches, ",") {
		t.Fatalf("branches %v, want %v", names, fixtureBranches)
	}

	main := list[3]
	switch {
	case !main.Current:
		t.Error("main is checked out in the fixture but is not marked current")
	case len(main.SHA) != 40:
		t.Errorf("SHA %q is not a full object name", main.SHA)
	case main.Short == "" || !strings.HasPrefix(main.SHA, main.Short):
		t.Errorf("short %q does not abbreviate %q", main.Short, main.SHA)
	case main.When.IsZero():
		t.Error("branch tip has no timestamp")
	case main.Subject != "merge feature into main":
		t.Errorf("subject %q, want the merge subject", main.Subject)
	}

	// Exactly one branch is current. %(HEAD) marks the checked-out one with a
	// "*" and every other with a space, so a parser that trims the field
	// before comparing marks all five.
	current := 0
	for _, b := range list {
		if b.Current {
			current++
		}
	}
	if current != 1 {
		t.Errorf("%d branches marked current, want exactly 1", current)
	}
}

// TestBranchesTrackingStates covers every shape %(upstream:track) emits. They
// are read from one call for all five branches — see TestBranchesIsOneRead.
func TestBranchesTrackingStates(t *testing.T) {
	by := branchesByName(t, gittest.Small(t))

	for _, want := range []struct {
		name     string
		upstream string
		ahead    int
		behind   int
		gone     bool
		inSync   bool
	}{
		{name: "main", upstream: "origin/main", ahead: 2},
		{name: "behind", upstream: "origin/main", behind: 1},
		{name: "synced", upstream: "origin/synced", inSync: true},
		{name: "gone-upstream", upstream: "origin/gone-upstream", gone: true},
		{name: "feature"}, // tracks nothing
	} {
		got, ok := by[want.name]
		if !ok {
			t.Errorf("%s missing from the branch list", want.name)
			continue
		}
		if got.Upstream != want.upstream || got.Ahead != want.ahead ||
			got.Behind != want.behind || got.Gone != want.gone {
			t.Errorf("%s: upstream %q ahead %d behind %d gone %v, want %q/%d/%d/%v",
				want.name, got.Upstream, got.Ahead, got.Behind, got.Gone,
				want.upstream, want.ahead, want.behind, want.gone)
		}
		if got.InSync() != want.inSync {
			t.Errorf("%s: InSync %v, want %v", want.name, got.InSync(), want.inSync)
		}
	}

	// A branch with no upstream and one in sync both report 0/0. Telling them
	// apart is the whole reason InSync exists rather than a zero check at the
	// call site.
	if by["feature"].InSync() {
		t.Error("a branch tracking nothing reports as in sync")
	}
}

// TestBranchesDivergedBothWays pins the two-part track string. "[ahead 1,
// behind 2]" is the one form a parser reading a single number gets wrong, and
// it is the state that matters most: it is when a plain push will be refused.
func TestBranchesDivergedBothWays(t *testing.T) {
	dir := gittest.Small(t)

	// Two commits are built onto the upstream side with commit-tree rather
	// than by checking it out and committing: the fixture's working tree
	// carries an untracked main.go that a checkout refuses to overwrite, and
	// the upstream branch is not the thing under test anyway.
	commitTree := func(parent, msg string) string {
		t.Helper()
		out := gitOut(t, dir, "commit-tree", "HEAD~1^{tree}", "-p", parent, "-m", msg)
		return strings.TrimSpace(out)
	}
	base := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD~1"))
	one := commitTree(base, "upstream commit one")
	two := commitTree(one, "upstream commit two")
	gitOut(t, dir, "update-ref", "refs/remotes/origin/main", two)

	main := branchesByName(t, dir)["main"]
	if main.Ahead != 2 || main.Behind != 2 {
		t.Errorf("diverged main reads ahead %d behind %d, want 2 and 2",
			main.Ahead, main.Behind)
	}
}

// TestBranchesFoldsMultilineSubject is why newline-delimited records are safe
// here even though nothing else in this package relies on that.
//
// `for-each-ref` has no -z, so a record ends at a newline. The only field that
// could carry one is the subject, and %(contents:subject) folds the whole
// first paragraph onto one line — a two-line subject arrives with a space
// where the newline was. If that ever stops being true this test fails and
// the parser needs a different record terminator, not a nudge.
func TestBranchesFoldsMultilineSubject(t *testing.T) {
	dir := gittest.Small(t)

	msg := filepath.Join(t.TempDir(), "msg")
	if err := os.WriteFile(msg, []byte("subject one\nsubject two\n\nbody\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitOut(t, dir, "commit", "-q", "--allow-empty", "--no-verify", "-F", msg)

	list, err := git.Branches(context.Background(), git.New(dir))
	if err != nil {
		t.Fatal(err)
	}
	// The record count is the assertion: a subject that kept its newline
	// would split main's row in two and leave a sixth, malformed record.
	if len(list) != len(fixtureBranches) {
		t.Fatalf("got %d branches, want %d — a subject newline split a record",
			len(list), len(fixtureBranches))
	}
	if got := list[3].Subject; got != "subject one subject two" {
		t.Errorf("subject %q, want the two lines folded with a space", got)
	}
}

// TestBranchesIsOneRead pins the design the work budget measures: ahead/behind
// for every branch comes back from a single process, not one per branch.
func TestBranchesIsOneRead(t *testing.T) {
	dir := gittest.Small(t)
	count := filepath.Join(t.TempDir(), "count")

	// A git shim that appends a line per invocation, ahead of the real git on
	// PATH. Counting spawns is the only way to catch a regression here: a
	// per-branch rev-list returns exactly the same numbers, just slower, and
	// the fixture is too small for the timing budget to notice.
	bin := t.TempDir()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	shim := "#!/bin/sh\necho x >> " + count + "\nexec " + real + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(shim), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	if _, err := git.Branches(context.Background(), git.New(dir)); err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(count)
	if err != nil {
		t.Fatalf("shim recorded no git invocation at all: %v", err)
	}
	if n := strings.Count(string(b), "\n"); n != 1 {
		t.Errorf("Branches spawned git %d times, want 1 — ahead/behind must come "+
			"from %%(upstream:track), not a rev-list per branch", n)
	}
}
