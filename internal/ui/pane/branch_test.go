package pane

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/f3xp/bubblegit/internal/git"
)

func testBranches() []git.Branch {
	return []git.Branch{
		{Name: "behind", Upstream: "origin/main", Behind: 1},
		{Name: "feature"},
		{Name: "gone-upstream", Upstream: "origin/gone-upstream", Gone: true},
		{Name: "main", Upstream: "origin/main", Ahead: 2, Current: true},
		{Name: "synced", Upstream: "origin/synced"},
	}
}

// TestBranchesOpenOnTheCurrentBranch: the first read has no previous selection
// to keep, and the branch the user is standing on is a better landing place
// than whatever sorts first.
func TestBranchesOpenOnTheCurrentBranch(t *testing.T) {
	var b Branches
	b.SetSize(60, 10)
	b.SetBranches(testBranches())

	if got := b.SelectedName(); got != "main" {
		t.Errorf("the pane opened on %q, want the current branch main", got)
	}
}

// TestBranchesKeepSelectionAcrossReload covers both halves of the reload rule:
// a branch that is still there keeps the cursor, and one that has been deleted
// hands it back to the current branch rather than to row zero.
func TestBranchesKeepSelectionAcrossReload(t *testing.T) {
	var b Branches
	b.SetSize(60, 10)
	b.SetBranches(testBranches())

	b.MoveTo(1)
	if b.SelectedName() != "feature" {
		t.Fatalf("the cursor is on %q, want feature", b.SelectedName())
	}
	b.SetBranches(testBranches())
	if got := b.SelectedName(); got != "feature" {
		t.Errorf("the selection became %q across a reload, want feature", got)
	}

	// feature deleted: the cursor cannot stay, and row zero — a branch the
	// user was not looking at — is not where it should land.
	var without []git.Branch
	for _, br := range testBranches() {
		if br.Name != "feature" {
			without = append(without, br)
		}
	}
	b.SetBranches(without)
	if got := b.SelectedName(); got != "main" {
		t.Errorf("after its branch was deleted the cursor went to %q, want main", got)
	}
}

// TestTrackCellStates pins the five row shapes. In sync and tracking nothing
// both count 0/0, so an empty cell has to be the one that means "no upstream".
func TestTrackCellStates(t *testing.T) {
	for _, tc := range []struct {
		name string
		br   git.Branch
		want string
	}{
		{"ahead", git.Branch{Upstream: "origin/main", Ahead: 2}, "↑2"},
		{"behind", git.Branch{Upstream: "origin/main", Behind: 1}, "↓1"},
		{"diverged", git.Branch{Upstream: "origin/main", Ahead: 2, Behind: 1}, "↑2↓1"},
		{"in sync", git.Branch{Upstream: "origin/main"}, "="},
		{"gone", git.Branch{Upstream: "origin/gone", Gone: true}, "gone"},
		{"no upstream", git.Branch{}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := strings.TrimSpace(ansi.Strip(trackCell(tc.br)))
			if got != tc.want {
				t.Errorf("track cell %q, want %q", got, tc.want)
			}
		})
	}
}

// TestBranchRowsFitTheWidth: a long branch name and a long subject must not
// push the row past its column, which would tear the pane border beside it.
func TestBranchRowsFitTheWidth(t *testing.T) {
	var b Branches
	const width = 30
	b.SetSize(width, 10)
	b.SetBranches([]git.Branch{{
		Name:    strings.Repeat("long-branch-name/", 4),
		Subject: strings.Repeat("and a subject to match ", 4),
		Current: true,
	}})

	for _, line := range strings.Split(b.View(), "\n") {
		if w := ansi.StringWidth(line); w != width {
			t.Errorf("row is %d cells wide, want exactly %d:\n%s", w, width, line)
		}
	}
}
