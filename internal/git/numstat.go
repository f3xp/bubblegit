package git

import (
	"context"
	"strconv"
	"strings"
)

// emptyTree is the hash of the tree with nothing in it, which is what HEAD
// stands for in a repository with no commits yet.
const emptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// LineStat is one file's share of a diff.
type LineStat struct{ Added, Deleted int }

// Numstat returns the lines added and deleted per path across every
// uncommitted change, staged or not: the working tree against HEAD, which is
// the number that answers "how big is what I have not committed".
//
// Binary files are absent rather than zero, and so are untracked files — git
// has no diff for a file it does not know about. Renames are turned off so a
// moved file is two entries keyed by its two paths, and the map can always be
// read by the path status reports.
func Numstat(ctx context.Context, r *Runner) (map[string]LineStat, error) {
	out, err := r.Run(ctx, "diff", "--numstat", "-z", "--no-renames", "HEAD")
	if err != nil {
		// Unborn branch: there is no HEAD to diff against, so diff against
		// nothing, which counts every staged line as an addition.
		out, err = r.Run(ctx, "diff", "--numstat", "-z", "--no-renames", emptyTree)
		if err != nil {
			return nil, err
		}
	}
	stats := make(map[string]LineStat)
	for _, rec := range SplitZ(out) {
		f := strings.SplitN(rec, "\t", 3)
		if len(f) < 3 || f[0] == "-" {
			continue
		}
		a, _ := strconv.Atoi(f[0])
		d, _ := strconv.Atoi(f[1])
		stats[f[2]] = LineStat{a, d}
	}
	return stats, nil
}

// Total sums a Numstat result.
func Total(stats map[string]LineStat) (added, deleted int) {
	for _, s := range stats {
		added += s.Added
		deleted += s.Deleted
	}
	return added, deleted
}
