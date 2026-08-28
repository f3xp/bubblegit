# bubblegit

A git TUI built on [Bubble Tea v2](https://github.com/charmbracelet/bubbletea), aiming to be
fast on large repositories and more interactive than the alternatives.

> **Status: early.** Milestone 0 is complete — the git layer, fixtures, benchmark harness and
> an app skeleton. There is no usable UI yet. See the milestones below.

## Why shell out to `git`

bubblegit runs the real `git` binary rather than reimplementing git in Go. That means your
hooks, `commit.gpgsign`, credential helpers, `core.pager` and the rest of your actual git
configuration all work, with no special cases. Tools built on a pure-Go git library silently
break some of that.

The cost is a process spawn per read, so the read paths use plumbing commands with `-z`
(never porcelain), page the log by cursor rather than offset, and run off the render loop.
`internal/git/exec_bench_test.go` measures each read against a budget so that stays true.

## Requirements

- Go 1.27+
- git 2.30+
- A terminal with truecolor. Kitty keyboard protocol and mouse support are used where
  available and degrade cleanly where they are not.

## Running

```sh
go run ./cmd/bubblegit    # from anywhere inside a git repository
```

Press `q` or `ctrl+c` to quit.

## Development

```sh
go test ./...                  # unit tests and golden files
go test ./... -update          # re-record golden files after a UI change
go test ./internal/git -bench . # read-path benchmarks
go test ./... -short           # skip the 100k-commit fixture
```

Test fixtures are generated, not checked in. `testdata/fixtures/mksmall.sh` builds a repo
covering the cases that break naive parsers — paths with spaces and non-ASCII bytes, a file
with no trailing newline, a merge commit, and a tree that is simultaneously staged, unstaged
and untracked. `mkbig.sh` builds 100k commits in about eleven seconds via `git fast-import`.

Both pin author and committer identity and dates and null out global and system config, so
SHAs are byte-identical across runs and machines. Golden files depend on that.

## Milestones

| | |
| --- | --- |
| M0 | ✅ git layer, fixtures, benchmark harness, app skeleton |
| M1 | Files pane and syntax-highlighted diff, read-only |
| M2 | Staging by hunk and by line |
| M3 | Commit and amend |
| M4 | Log pane, commit detail, commit graph |
| M5 | Branch pane |
| M6 | Performance pass, mouse, resizable splitter |

## License

MIT
