package git

import "context"

// The whole-tree writes. Each is one git command with no pathspec: the Runner
// runs in the repository root, so "everything" needs no spelling out.

// StageAll stages every change, deletions and untracked files included.
func StageAll(ctx context.Context, r *Runner) error {
	_, err := r.Run(ctx, "add", "-A")
	return err
}

// UnstageAll empties the index back to HEAD, leaving the working tree alone.
//
// `reset` rather than `restore --staged .`: the latter resolves HEAD and
// fails on an unborn branch, where there is still an index to empty.
func UnstageAll(ctx context.Context, r *Runner) error {
	_, err := r.Run(ctx, "reset", "-q")
	return err
}

// DiscardWorktree throws away every unstaged change to a tracked file,
// restoring it from the index. Staged changes survive, and so do untracked
// files — see CleanUntracked for those.
func DiscardWorktree(ctx context.Context, r *Runner) error {
	_, err := r.Run(ctx, "restore", "--", ".")
	return err
}

// CleanUntracked deletes every untracked file and directory. Ignored files
// are left alone: they are untracked on purpose.
func CleanUntracked(ctx context.Context, r *Runner) error {
	_, err := r.Run(ctx, "clean", "-fd")
	return err
}
