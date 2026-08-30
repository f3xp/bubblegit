package git

import (
	"context"
	"strings"
)

// Commit records the index. amend replaces HEAD instead of adding to it.
//
// The message goes over stdin as `-F -` rather than `-m`: -m takes the message
// as an argument, so a message containing anything git treats as an option is
// a problem, and a multi-line one has to be split across repeated flags. stdin
// carries arbitrary bytes verbatim.
//
// No --cleanup flag: the default for -F strips trailing whitespace and blank
// lines and leaves '#' lines alone, which is right here. Nothing in this
// message came from a template — the user typed every line of it, so a '#'
// line is content, not a comment, and stripping it would be silent data loss.
//
// Nothing is guarded here that git already refuses clearly: an empty message,
// an empty index, and `--amend` on an unborn branch all come back as an Error
// carrying git's own stderr, which is what the UI shows.
func Commit(ctx context.Context, r *Runner, msg string, amend bool) error {
	args := []string{"commit", "-F", "-"}
	if amend {
		args = append(args, "--amend")
	}
	_, err := r.RunStdin(ctx, []byte(msg), args...)
	return err
}

// HeadMessage returns HEAD's commit message, to pre-fill an amend. An unborn
// branch has no HEAD, and reports an empty message rather than an error.
func HeadMessage(ctx context.Context, r *Runner) (string, error) {
	out, err := r.Run(ctx, "log", "-1", "--format=%B")
	if err != nil {
		if h, herr := ReadHead(ctx, r); herr == nil && h.Commit == "" {
			return "", nil
		}
		return "", err
	}
	// %B ends with the newline git stores plus one log adds. Trailing blank
	// lines are not the user's text, and re-committing them round-trips a
	// growing tail of them through every amend.
	return strings.TrimRight(string(out), "\n"), nil
}
