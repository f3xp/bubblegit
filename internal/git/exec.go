// Package git shells out to the git CLI. It returns plain structs and must
// never import bubbletea — the UI layer wraps every call in a tea.Cmd.
//
// Shelling out (rather than go-git) is deliberate: hooks, commit.gpgsign,
// credential helpers and the user's real git config all work for free.
package git

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Runner executes git commands in a single repository.
//
// ponytail: one process spawn per call, no pooling and no long-lived
// `cat-file --batch`. The benchmarks in exec_bench_test.go say when that
// stops being good enough; add a batch process for the one call that breaks.
type Runner struct {
	Dir string
}

func New(dir string) *Runner { return &Runner{Dir: dir} }

// Error carries git's stderr, which is what the UI should show the user.
type Error struct {
	Args     []string
	ExitCode int
	Stderr   string
	err      error
}

func (e *Error) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" {
		msg = e.err.Error()
	}
	return fmt.Sprintf("git %s: %s", strings.Join(e.Args, " "), msg)
}

func (e *Error) Unwrap() error { return e.err }

// Run executes git with the given arguments and returns stdout.
func (r *Runner) Run(ctx context.Context, args ...string) ([]byte, error) {
	return r.run(ctx, nil, args)
}

// RunStdin is Run with data piped to git's stdin — used by `apply --cached`.
func (r *Runner) RunStdin(ctx context.Context, stdin []byte, args ...string) ([]byte, error) {
	return r.run(ctx, stdin, args)
}

func (r *Runner) run(ctx context.Context, stdin []byte, args []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = r.Dir

	// Inherit the user's environment so credential helpers, SSH agents and
	// gpg-agent keep working, then override only what would corrupt parsing.
	//
	// Note what is deliberately NOT set here: GIT_OPTIONAL_LOCKS=0.
	// It looks like the safe choice for a read-only TUI, but it stops git
	// from writing back the refreshed index, so `status` re-stats the whole
	// worktree on every single call — measured at ~14ms of pure waste per
	// call on the 100k fixture, forever, versus ~2ms once the index can be
	// written. Contention is not the tradeoff it appears to be: with locks
	// allowed and a foreign index.lock present, `status` still exits 0 and
	// silently skips the refresh write. Only real write operations fail,
	// and those must fail. See TestStatusIndexRefresh.
	cmd.Env = append(os.Environ(),
		"GIT_PAGER=cat",
		"PAGER=cat",
		"NO_COLOR=1",
		"GIT_TERMINAL_PROMPT=0", // never block the TUI on a credential prompt
		"LC_ALL=C",
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}

	if err := cmd.Run(); err != nil {
		return stdout.Bytes(), &Error{
			Args:     args,
			ExitCode: cmd.ProcessState.ExitCode(),
			Stderr:   stderr.String(),
			err:      err,
		}
	}
	return stdout.Bytes(), nil
}

// SplitZ splits NUL-delimited git output, dropping the trailing empty field.
// Every read path uses -z, so filenames with spaces, newlines or non-ASCII
// bytes arrive verbatim and core.quotepath never applies.
func SplitZ(b []byte) []string {
	b = bytes.TrimSuffix(b, []byte{0})
	if len(b) == 0 {
		return nil
	}
	parts := bytes.Split(b, []byte{0})
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = string(p)
	}
	return out
}
