package git

import (
	"context"
	"strconv"
	"strings"
	"time"
)

// Branch is one local branch: where it points, what it tracks, and how far it
// has diverged from that.
//
// Only refs/heads is read. A remote-tracking ref is not a branch the user can
// be on, and listing every one of them turns a pane about "which line of work
// am I on" into a directory of the remote.
type Branch struct {
	Name     string
	SHA      string
	Short    string
	Upstream string

	// Ahead and Behind count commits relative to Upstream. Both zero with an
	// Upstream set means in sync; both zero with none set means untracked.
	Ahead  int
	Behind int

	// Gone marks a branch whose upstream ref no longer exists — the state a
	// branch is left in after its remote branch is deleted, and the one a user
	// most needs pointed out, since nothing else on the row distinguishes it
	// from a branch in sync.
	Gone bool

	Current bool
	When    time.Time
	Subject string
}

// InSync reports whether the branch matches its upstream exactly.
func (b Branch) InSync() bool { return b.Upstream != "" && !b.Gone && b.Ahead == 0 && b.Behind == 0 }

// branchFormat is one record of eight fields.
//
// %(upstream:track) is what makes this one read rather than one per branch:
// git computes ahead/behind itself here, where the alternative — a
// `rev-list --count --left-right` per branch — multiplies process spawns by
// the number of branches. See TestBranchesIsOneRead.
const branchFormat = "%(HEAD)%00%(refname:short)%00%(objectname)%00%(objectname:short)%00" +
	"%(upstream:short)%00%(upstream:track)%00%(authordate:iso-strict)%00%(contents:subject)"

// branchFields is the stride parseBranches counts in.
const branchFields = 8

// Branches lists the local branches, sorted by name.
//
// This is the one read path whose records are newline-delimited rather than
// NUL-delimited, against the rule SplitZ's doc describes: `for-each-ref` has
// no -z option at all (checked on git 2.53; the flag is rejected outright).
// The fields inside a record are still NUL-separated, which is where the
// values that can contain anything live, and neither of the two things left
// touching a newline can hold one: git refuses a ref name containing one, and
// %(contents:subject) is a folded first paragraph — a two-line subject arrives
// as one line with a space. See TestBranchesFoldsMultilineSubject.
//
// Sorting is git's default, by refname. Recency (--sort=-committerdate) is the
// nicer order to read and is not usable here: the test fixtures pin every
// commit to the same second, so a date sort leaves the order for git to break
// however it likes and every golden file in the UI package depends on it.
func Branches(ctx context.Context, r *Runner) ([]Branch, error) {
	out, err := r.Run(ctx, "for-each-ref", "--format="+branchFormat, "refs/heads")
	if err != nil {
		return nil, err
	}
	return parseBranches(string(out)), nil
}

func parseBranches(text string) []Branch {
	var out []Branch
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		if line == "" {
			continue
		}
		f := strings.Split(line, "\x00")
		if len(f) != branchFields {
			continue
		}
		ahead, behind, gone := parseTrack(f[5])
		out = append(out, Branch{
			// %(HEAD) is "*" on the checked-out branch and a space on the
			// rest. A detached HEAD marks nothing, which is correct: no
			// branch is current then.
			Current:  f[0] == "*",
			Name:     f[1],
			SHA:      f[2],
			Short:    f[3],
			Upstream: f[4],
			Ahead:    ahead,
			Behind:   behind,
			Gone:     gone,
			When:     authorTime(f[6]),
			Subject:  f[7],
		})
	}
	return out
}

// parseTrack reads %(upstream:track): "[ahead 1]", "[behind 2]",
// "[ahead 1, behind 2]", "[gone]", or empty for in-sync and untracked alike.
//
// The verbose form is parsed rather than :trackshort, which collapses all of
// this to one of >, <, = and none — enough to colour a row and not enough to
// say how far. LC_ALL=C in exec.go pins the wording, so this is not parsing a
// localised string.
func parseTrack(s string) (ahead, behind int, gone bool) {
	s = strings.Trim(s, "[]")
	if s == "" {
		return 0, 0, false
	}
	if s == "gone" {
		return 0, 0, true
	}
	for _, part := range strings.Split(s, ", ") {
		word, num, ok := strings.Cut(part, " ")
		if !ok {
			continue
		}
		n, err := strconv.Atoi(num)
		if err != nil {
			continue
		}
		switch word {
		case "ahead":
			ahead = n
		case "behind":
			behind = n
		}
	}
	return ahead, behind, false
}

// Checkout switches the working tree to a branch.
//
// `switch`, not `checkout`: the old command also restores files, so
// `git checkout <name>` is ambiguous when a path shares a name with a branch,
// and its way of resolving that is to guess. `switch` only ever moves HEAD.
//
// Nothing is guarded here that git already refuses clearly. Local changes that
// the switch would overwrite, a name that is not a branch, and an unborn HEAD
// all come back as an Error carrying git's own stderr, which is what the UI
// shows. Guessing at any of them here would mean either refusing switches git
// would have allowed — carrying an uncommitted edit across branches is
// routine — or reimplementing the check git is about to do anyway.
func Checkout(ctx context.Context, r *Runner, branch string) error {
	// -- ends the option list. A branch may legitimately be named "-f", and
	// `switch` would read it as a flag.
	_, err := r.Run(ctx, "switch", "--", branch)
	return err
}
