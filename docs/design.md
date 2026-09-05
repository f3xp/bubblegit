# Design notes

Why bubblegit is built the way it is. For how to use it, see the
[README](../README.md).

## Why shell out to `git`

bubblegit runs the real `git` binary instead of reimplementing git in Go. So your hooks,
`commit.gpgsign`, credential helpers, `core.pager` and the rest of your git config all work,
with no special cases. Tools built on a pure-Go git library break some of that, and they break
it quietly.

The cost is one process spawn per read. To keep that cheap, the read paths use plumbing
commands with `-z`, resume the log walk from a set of SHAs instead of an offset, and run off
the render loop. `internal/git/exec_bench_test.go` checks each read against a budget so it
stays that way. It calls the same exported functions the UI calls, instead of repeating their
argument lists. A budget that guards a command nothing runs any more is worse than no budget,
because the numbers still look fine.

Commit detail is the one read that uses porcelain. `git diff-tree` carries no commit message,
and for the initial commit it prints nothing at all unless you ask. So a detail pane built on
it costs two processes to answer what `git show --format=…` answers in one.

## Layout

Each view is a pair of panes rather than a single pane: two panes are what an 80-column
terminal has room for, and three would just be slivers. Below 48 columns even two do not fit,
so the layout drops to one.

`h`/`l` move the boundary one column at a time, and you can also drag it. The boundary is the
two border columns where the panes meet. Each view keeps its own split, because the three pairs
of panes want different proportions of the same terminal. What gets stored is a fraction, not a
column count, so resizing the terminal keeps the proportion you chose instead of a column count
that no longer means the same thing.

The keys work in one place the drag cannot: a terminal too short for pane chrome has no border
column to grab, but its panes still split the width.

The working tree view puts the diff on the left and the file list on the right; the log and
branch views put their list on the left. The diff is the pane that gets read, so it takes the
side and the room a reader expects, and a file row is a status code and a path. Focus follows
the pane's role — list or document — not its side, so `tab`, the wheel and a click mean the same
thing in every view whichever way round the panes are drawn.

## Mouse

The wheel moves the cursor rather than a scroll offset, because the cursor is what the staging
keys act on. A pane that could scroll away from the cursor would stage a line that is no longer
on screen.

Cell motion is requested, not all motion. The app acts on clicks and the wheel, and asking for
pointer movement with no button held delivers one event per cell the mouse crosses. Each of
those is a full update and re-render for a message that then gets discarded. Either mode costs
the terminal's own text selection, which the app can neither read nor replace. Most terminals
still select while shift is held, and that is the escape hatch this trades against.

## Committing

`ctrl+s` commits, not `enter`. A message body is the normal case, not the rare one, so `enter`
is a newline.

## The log

The log loads one page at a time and fetches the next page a screenful before the cursor
reaches the bottom. Paging resumes from the parents it has not read yet, not from the last SHA
on screen. That matters because `git log <sha>` walks only that commit's ancestors, so on any
history with a merge the obvious cursor drops the side branch, and no later page picks it up.

A merge shows what it brought in over its first parent, which is usually why you open a merge
commit. The initial commit shows its whole tree. Both of those render blank under the obvious
git invocations.

## Branches

Tracking state comes from one `git for-each-ref`. `%(upstream:track)` makes git count ahead and
behind itself, where the obvious alternative spends one `rev-list` process per branch. A branch
that tracks nothing shows nothing at all, which is not the same fact as being in sync.

`git for-each-ref` has no `-z`, so this is the one read whose records are newline-delimited.
That is safe because git refuses a ref name that contains a newline, and it folds a multi-line
subject onto one line. Tests pin both facts, because the parser breaks quietly if either one
stops holding.

Checkout has no confirmation step: a switch is reversible, and the one way it loses work is the
case git refuses on its own. Nothing is pre-empted in Go, because such a guard would have to be
wrong in one direction or the other. Carrying an uncommitted edit to a branch where the file is
identical is routine and allowed, and the same edit is refused on a branch where the file
differs.

It runs `git switch`, not `git checkout`. The old command also restores files, so
`git checkout <name>` is ambiguous when a path and a branch share a name, and it settles that
by guessing.

## Rendering off the event loop

Shelling out to git is only half of what showing a diff costs. The other half is turning it into
coloured rows, and that half is bigger. The syntax highlighter costs about twenty microseconds a
line, so a ten-thousand-line file is a fifth of a second of pure computation. That size is
common: a lockfile, a generated file, or any untracked file, which git diffs against `/dev/null`
and so reports as one hunk of the whole thing. Done where the answer arrives, that is a fifth of
a second in which the app reads no keys.

So it is done where the read is. `RenderDiff` and `RenderDetail` run inside the same command
goroutine that ran git, and the message that comes back carries rows instead of a diff. The
generation counter already discarded superseded reads, so it discards superseded renders too,
for free.

The symptom that makes this worth doing is not opening a large file, which happens once. It is
staging: every `space` re-reads the file and re-renders it, so on a large diff the pane stalled
between keystrokes.

Two things fall out of putting rendering in a goroutine. First, the lexer lookup is memoised.
`chroma`'s registry walks every language's filename globs, which takes a millisecond, and a
commit that touches a vendored tree asks for it once per file. The memo means every goroutine
colouring a `.go` file now shares one lexer, which is why the mutex around it is load-bearing
rather than defensive. Second, the budget that guards all this is a ratio, not a duration.
`TestRenderScalesLinearly` asserts that twice the lines cost about twice the work. A wall-clock
number would only record the machine it was written on, and the way a render path really goes
wrong is by turning quadratic.

Per frame, by contrast, the cost is the height of the pane, not the length of the document. That
is what `SoftWrap = false` buys, on top of the honest line count that line-level staging needs.

## Tests and fixtures

`TestWorkBudget` measures each read against a process-spawn floor that it re-samples per op.
`go test ./...` builds and runs packages in parallel, so it can trip the budget on whichever
read happened to run during a busy stretch, by a few milliseconds. Confirm a real regression
with `go test ./internal/git -p 1` before you believe it.

Test fixtures are generated, not checked in, and they are built once per machine rather than per
test. `gittest.Small` copies a cached build, which is the difference between a 95-second
`internal/ui` and a 19-second one. `testdata/fixtures/mksmall.sh` builds a repo that covers the
cases which break naive parsers: paths with spaces and non-ASCII bytes, a file with no trailing
newline, a merge commit, and a tree that is staged, unstaged and untracked at the same time. It
also carries a file whose two edits stay two separate hunks under the default `-U3`, which is
the only way to test that staging one hunk leaves the other alone. And it carries five branches
covering every tracking state a row can be in: ahead, behind, in sync, upstream deleted, and no
upstream. Those are built entirely out of refs, so no SHA moves and no golden file churns.
`mkbig.sh` builds 100k commits in about eleven seconds via `git fast-import`.

Both scripts pin author and committer identity and dates, and null out global and system config,
so SHAs are byte-identical across runs and machines. Golden files depend on that.
