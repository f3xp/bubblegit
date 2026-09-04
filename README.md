<p align="center">
  <img src="docs/assets/bubblegit.png" alt="bubblegit" width="160">
</p>

<h1 align="center">bubblegit</h1>

A git TUI built on [Bubble Tea v2](https://github.com/charmbracelet/bubbletea), aiming to be
fast on large repositories and more interactive than the alternatives.

> **Status: early.** Milestone 5 is complete — a working-tree view with syntax-highlighted
> diffs, staging by file, by hunk and by line, and commit and amend, a log view with a commit
> graph and a commit detail pane, and a branch view that lists the local branches and checks
> one out.

## Why shell out to `git`

bubblegit runs the real `git` binary rather than reimplementing git in Go. That means your
hooks, `commit.gpgsign`, credential helpers, `core.pager` and the rest of your actual git
configuration all work, with no special cases. Tools built on a pure-Go git library silently
break some of that.

The cost is a process spawn per read, so the read paths use plumbing commands with `-z`,
resume the log walk from a set of SHAs rather than an offset, and run off the render loop.
`internal/git/exec_bench_test.go` measures each read against a budget so that stays true, and
it calls the same exported functions the UI does rather than repeating their argument lists —
a budget guarding a command nothing runs any more is worse than no budget, because the numbers
still look fine.

Commit detail is the one read that uses porcelain. `git diff-tree` carries no commit message
and prints nothing at all for the initial commit unless asked, so a detail pane built on it
costs two processes for what `git show --format=…` answers in one.

## Requirements

- Go 1.27+
- git 2.30+
- A terminal with truecolor. Kitty keyboard protocol and mouse support are used where
  available and degrade cleanly where they are not.

## Running

```sh
go run ./cmd/bubblegit    # from anywhere inside a git repository
```

`1` shows the working tree, `2` shows the log, `3` shows the branches. Each is a pair of panes
rather than a pane of its own: two is what an 80-column terminal has room for, and a third
would be three slivers.

`j`/`k` move, `g`/`G` jump to the ends, `ctrl+d`/`ctrl+u` half-page, `tab` switches pane,
`h`/`l` move the splitter, `t` toggles the diff between the worktree and staged sides, `q` or
`ctrl+c` quits.

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

In the log view (`2`) the left pane lists commits with a graph column and the right pane shows
the selected commit: its message, and its patch highlighted the same way a working-tree diff
is. A merge shows what it brought in over its first parent, which is the question a merge
commit is usually being opened to answer, and the initial commit shows its whole tree — both
of those render blank under the obvious git invocations.

Nothing in the log view writes. The staging and commit keys are inert there rather than
acting on the working-tree selection that is no longer on screen.

The log loads a page at a time and fetches the next one a screenful before the cursor reaches
the bottom. Paging resumes from the parents it has not read yet, not from the last SHA on
screen: `git log <sha>` walks only that commit's ancestors, so on any history with a merge the
obvious cursor drops the side branch entirely and no later page ever picks it up.

In the branch view (`3`) the left pane lists the local branches and the right pane shows the
tip commit of the selected one. Each row carries how far the branch has drifted from its
upstream — `↑2`, `↓1`, `↑2↓1`, a dim `=` when it matches, `gone` when the upstream ref has
been deleted, and nothing at all when the branch tracks nothing, which is a different fact
from being in sync. All of it comes from one `git for-each-ref`: `%(upstream:track)` has git
count ahead and behind itself, where the obvious alternative spends a `rev-list` process per
branch.

The view opens on the branch you are on rather than on the first one alphabetically, and
`git for-each-ref` has no `-z`, so this is the one read whose records are newline-delimited.
That is safe because git refuses a ref name containing a newline and folds a multi-line
subject onto one line — both pinned by tests, since the parser breaks quietly if either
stops holding.

Only local branches are listed. A remote-tracking ref is not a branch you can be on, and
listing every one of them turns the pane into a directory of the remote.

`b` checks out the branch under the cursor — the one write in this view, and the one key that
means nothing in the other two, where there is no branch under a cursor. There is no
confirmation step: a switch is reversible, and the one way it loses work is the case git
refuses on its own. Nothing is pre-empted in Go, because the guard would have to be wrong in
one direction or the other — carrying an uncommitted edit to a branch where the file is
identical is routine and allowed, and the same edit refuses on a branch where it is not.
When git refuses, the pane shows what it said.

It runs `git switch`, not `git checkout`: the old command also restores files, so
`git checkout <name>` is ambiguous when a path shares a name with a branch, and it resolves
that by guessing.

`h`/`l` move the boundary between the panes one column at a time, and dragging it with the
mouse moves it too — it is the two border columns where the panes meet. Each view keeps its own
split, because the three pairs of panes want different proportions of the same terminal, and
what is stored is a fraction rather than a column count, so resizing the terminal keeps the
proportion you chose rather than a column count that no longer means the same thing. Neither
pane goes below 24 columns.

The keys work in one place the drag cannot: a terminal too short for pane chrome has no border
column to take hold of, but its panes still split the width.

Below 48 columns the layout drops to a single pane, `tab` swaps which one is visible, and the
splitter is inert — there is no boundary between panes to move.

The mouse works wherever a pointer is unambiguous. A left click focuses the pane it lands on
and puts that pane's cursor on the row under it — in the diff that is the line `space` then
stages. The wheel scrolls the pane under the pointer without moving focus, which is what a
wheel does everywhere else, and it moves the cursor rather than a scroll offset: the cursor is
what the staging keys act on, so a pane that could scroll away from it would stage a line that
is no longer on screen. Clicking the empty space below a list selects nothing, and the middle
and right buttons are left to the terminal, which uses them for paste and for its own menu.

Cell motion is requested, not all motion. The app acts on clicks and the wheel, and asking for
pointer movement with no button held delivers an event per cell the mouse crosses — a full
update and re-render each, for a message that is then discarded. Either mode costs the
terminal's own text selection, which the app can neither read nor replace; most terminals
still select while shift is held, and that is the escape hatch this trades against.

## Development

```sh
go test ./...                  # unit tests and golden files
go test ./... -update          # re-record golden files after a UI change
go test ./internal/git -bench . # read-path benchmarks
go test ./... -short           # skip the 100k-commit fixture
```

`TestWorkBudget` measures each read against a process-spawn floor it re-samples per op, so
`go test ./...` — which builds and runs packages in parallel — can trip it on whichever read
happened to run during a busy stretch, a few milliseconds over the budget. Confirm a real
regression with `go test ./internal/git -p 1` before believing it.

Test fixtures are generated, not checked in. `testdata/fixtures/mksmall.sh` builds a repo
covering the cases that break naive parsers — paths with spaces and non-ASCII bytes, a file
with no trailing newline, a merge commit, and a tree that is simultaneously staged, unstaged
and untracked. It also carries a file whose two edits stay two separate hunks under the default
`-U3`, which is the only way to test that staging one hunk leaves the other alone.
It also carries five branches covering every tracking state a row can be in — ahead, behind,
in sync, upstream deleted, and no upstream — built entirely out of refs, so no SHA moves and
no golden file churns. `mkbig.sh` builds 100k commits in about eleven seconds via
`git fast-import`.

Both pin author and committer identity and dates and null out global and system config, so
SHAs are byte-identical across runs and machines. Golden files depend on that.

## Milestones

| | |
| --- | --- |
| M0 | ✅ git layer, fixtures, benchmark harness, app skeleton |
| M1 | ✅ files pane and syntax-highlighted diff, read-only |
| M2 | ✅ staging by file, hunk and line |
| M3 | ✅ commit and amend |
| M4 | ✅ Log pane, commit detail, commit graph |
| M5 | ✅ Branch pane, checkout |
| M6 | Performance pass, mouse, resizable splitter |

## License

MIT
