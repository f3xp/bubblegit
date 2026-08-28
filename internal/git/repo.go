package git

import (
	"context"
	"strings"
)

// Discover returns the root of the repository containing dir.
func Discover(ctx context.Context, dir string) (string, error) {
	out, err := New(dir).Run(ctx, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// Head describes what HEAD points at. Branch is empty when detached.
type Head struct {
	Branch   string
	Commit   string
	Detached bool
}

// ReadHead resolves HEAD. A repository with no commits yet reports an empty
// Commit rather than an error — that is a normal state, not a failure.
func ReadHead(ctx context.Context, r *Runner) (Head, error) {
	// Argument order matters: --abbrev-ref applies to the args that follow it,
	// so the SHA has to be requested first. rev-parse has no -z, but neither a
	// SHA nor a ref name can contain a newline, so line splitting is safe here.
	out, err := r.Run(ctx, "rev-parse", "HEAD", "--abbrev-ref", "HEAD")
	if err != nil {
		// An unborn branch has no HEAD commit yet. That is a normal state for
		// a freshly initialised repo, and symbolic-ref still knows which
		// branch the first commit will land on.
		ref, serr := r.Run(ctx, "symbolic-ref", "--short", "HEAD")
		if serr != nil {
			return Head{}, err
		}
		return Head{Branch: strings.TrimSpace(string(ref))}, nil
	}
	lines := strings.Fields(string(out))
	if len(lines) < 2 {
		return Head{}, &Error{
			Args:   []string{"rev-parse", "HEAD", "--abbrev-ref", "HEAD"},
			Stderr: "unexpected rev-parse output: " + string(out),
		}
	}
	commit, name := lines[0], lines[1]
	if name == "HEAD" {
		return Head{Commit: commit, Detached: true}, nil
	}
	return Head{Branch: name, Commit: commit}, nil
}
