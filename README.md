# bubblegit

A git TUI built on [Bubble Tea v2](https://github.com/charmbracelet/bubbletea), aiming to be
fast on large repositories and more interactive than the alternatives.

> **Status: early.** Milestone 3 is complete — a two-pane browser over the working tree with
> syntax-highlighted diffs, staging by file, by hunk and by line, and commit and amend.
> There is no log or branch pane yet.

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

`j`/`k` move, `g`/`G` jump to the ends, `ctrl+d`/`ctrl+u` half-page, `tab` switches pane,
`t` toggles the diff between the worktree and staged sides, `q` or `ctrl+c` quits.

`space` stages what is under the cursor — the whole file in the list, one line in the diff, or
the whole hunk when the cursor is on a `@@` header. `a` takes the hunk from anywhere inside it.
Both reverse into un-staging when the pane is showing the staged side, which is what the diff
pane title says.

A few changes cannot be split and stage whole instead: a binary file, a file the change deletes
outright, and a hunk that ends without a trailing newline. The first two are silent — `space`
simply takes the file — and the third says so, because a partial patch there is one git applies
happily and wrongly.

`c` opens the commit message editor and `C` opens it pre-filled with HEAD's message, to amend.
`ctrl+s` commits, `esc` cancels and drops the message. Not `enter`: a message body is the
normal case, not the rare one, so `enter` is a newline. While the editor is open it owns every
key — `q` types a q — except `ctrl+c`, which still quits.

git runs your hooks and signs your commits, so a commit can fail for reasons that are not
bubblegit's. When it does, the editor stays open holding what you wrote and shows what git
said.

Below 48 columns the layout drops to a single pane and `tab` swaps which one is visible.

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
and untracked. It also carries a file whose two edits stay two separate hunks under the default
`-U3`, which is the only way to test that staging one hunk leaves the other alone.
`mkbig.sh` builds 100k commits in about eleven seconds via `git fast-import`.

Both pin author and committer identity and dates and null out global and system config, so
SHAs are byte-identical across runs and machines. Golden files depend on that.

## Milestones

| | |
| --- | --- |
| M0 | ✅ git layer, fixtures, benchmark harness, app skeleton |
| M1 | ✅ files pane and syntax-highlighted diff, read-only |
| M2 | ✅ staging by file, hunk and line |
| M3 | ✅ commit and amend |
| M4 | Log pane, commit detail, commit graph |
| M5 | Branch pane |
| M6 | Performance pass, mouse, resizable splitter |

## License

MIT
