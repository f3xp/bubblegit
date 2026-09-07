package git

import (
	"context"
	"strings"
	"time"
)

// Stash is one entry of the stash list.
type Stash struct {
	// Ref is the "stash@{n}" name, which is what drop, pop and apply take:
	// they refuse a bare object name.
	Ref     string
	SHA     string
	Short   string
	When    time.Time
	Subject string
}

// stashFormat follows logFormat's rule: -z terminates the record, so the last
// field carries no %x00 of its own.
const stashFormat = "%gd%x00%H%x00%h%x00%aI%x00%s"

const stashFields = 5

// Stashes lists the stash, newest first.
func Stashes(ctx context.Context, r *Runner) ([]Stash, error) {
	out, err := r.Run(ctx, "stash", "list", "-z", "--format="+stashFormat)
	if err != nil {
		return nil, err
	}
	fields := SplitZ(out)
	var stashes []Stash
	for i := 0; i+stashFields <= len(fields); i += stashFields {
		f := fields[i : i+stashFields]
		stashes = append(stashes, Stash{
			Ref:     f[0],
			SHA:     f[1],
			Short:   f[2],
			When:    authorTime(f[3]),
			Subject: strings.TrimSpace(f[4]),
		})
	}
	return stashes, nil
}

// StashPush stashes every change and, when untracked is set, every untracked
// file too. The working tree is left clean either way; ignored files stay.
func StashPush(ctx context.Context, r *Runner, untracked bool) error {
	args := []string{"stash", "push", "-q"}
	if untracked {
		args = append(args, "--include-untracked")
	}
	_, err := r.Run(ctx, args...)
	return err
}

// StashPop applies a stash and drops it. ref is a Stash.Ref, or empty for the
// newest. A pop that conflicts leaves the stash in place and reports it.
func StashPop(ctx context.Context, r *Runner, ref string) error {
	return stashOp(ctx, r, "pop", ref)
}

// StashApply applies a stash and keeps it.
func StashApply(ctx context.Context, r *Runner, ref string) error {
	return stashOp(ctx, r, "apply", ref)
}

// StashDrop deletes a stash without applying it.
func StashDrop(ctx context.Context, r *Runner, ref string) error {
	return stashOp(ctx, r, "drop", ref)
}

// ponytail: a Ref is a position, not an identity — stash@{1} names whichever
// entry is second by the time the command runs. Another process stashing in
// between drops the wrong one. Address by SHA if that ever bites; `drop`
// would then need the reflog walked to find the position.
func stashOp(ctx context.Context, r *Runner, op, ref string) error {
	args := []string{"stash", op, "-q"}
	if ref != "" {
		args = append(args, ref)
	}
	_, err := r.Run(ctx, args...)
	return err
}
