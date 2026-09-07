<p align="center">
  <img src="docs/assets/bubblegit.svg" alt="bubblegit" width="200">
</p>

<h1 align="center">bubblegit</h1>

A git TUI built on [Bubble Tea v2](https://github.com/charmbracelet/bubbletea). The goals are
to stay fast on large repositories and to be more interactive than the alternatives.

> **Status: early beta.** The first tagged build is
> [v0.1.0](https://github.com/f3xp/bubblegit/releases/tag/v0.1.0), with binaries for macOS,
> Linux and Windows. What is documented below works. Keys, layout and behaviour will still change
> between releases, and anything not documented is not built yet.

bubblegit runs the real `git` binary, so your hooks, `commit.gpgsign`, credential helpers,
`core.pager` and the rest of your git config all work as they normally do.

## Requirements

- Go 1.27+
- git 2.30+
- A terminal with truecolor. Mouse support and the Kitty keyboard protocol are used when
  available, and things degrade cleanly when they are not.

## Running

```sh
go run ./cmd/bubblegit    # from anywhere inside a git repository
```

## Views

`1` working tree, `2` log, `3` branches, `4` stashes. Each view is a pair of panes, and `tab`
switches between them. The key bar along the bottom lists the keys that do something from
where the cursor is; `?` lists them all.

**Working tree (`1`).** The diff of the selected file on the left, files on the right. The
files are in two sections, tracked and untracked, and each tracked row ends in the lines it adds
and removes; the pane title sums them. `t` toggles the diff between the worktree and the staged
side, and the diff pane title says which side you are looking at.

**Log (`2`).** The selected commit on the left, commits with a graph column and their ref
labels on the right. It walks every ref, and `a` narrows it to HEAD's history and back; the pane
title names which. Opened with `enter` from the branch view it walks that branch alone, with the
branch in the title, and `esc` goes back to the branch list; `2` returns the log to every ref.
`/` searches the subject, author, hash and refs as you type, `enter` jumps to the next match and
`n`/`N` walk them; `esc` drops the search. `[` follows a commit to its first parent and `]` back
up to its nearest child. The commit is its message and its patch, highlighted the same way a
working-tree diff is. A merge shows what it brought in over its first parent. The initial commit
shows its whole tree. Nothing in this view writes, so the staging and commit keys do nothing here.

The graph column reads as follows:

| Glyph | Meaning |
| --- | --- |
| `●` | a commit |
| `◆` | a merge |
| `│` | a line of history passing this row, in its lane's colour |
| `╮` `╭` | a merge pulling in a branch, which continues below |
| `┤` `├` | a branch joining a line of history that is already drawn |
| `┬` `┼` | a merge's run passing a lane on its way to a farther one |
| `╭╯` | a lane sliding into the column freed above it |
| `main` | a local branch, as a yellow pill; `★` marks the one HEAD is on |
| `origin/main` | a remote-tracking ref, in blue |
| `v1` | a tag, in green |
| `+2` | two more refs on this commit than the row has room for |

**Branches (`3`).** The tip commit of the selected branch on the left, local branches on the
right. The view opens on the branch you are on. Only local branches are listed, since a
remote-tracking ref is not a branch you can be on. `enter` opens the log view on the selected
branch, which is how you read a branch's history without checking it out; `esc` there comes back
to this list.

Each row shows how far the branch has drifted from its upstream:

| Marker | Meaning |
| --- | --- |
| `↑2` | 2 commits ahead |
| `↓1` | 1 commit behind |
| `↑2↓1` | both |
| dim `=` | matches the upstream |
| `gone` | the upstream ref was deleted |
| blank | the branch tracks nothing |

**Stashes (`4`).** The selected stash on the left, the stash list on the right, newest first.
The patch is what the stash holds against the commit it was taken on; the untracked files of a
stash taken with `s` are kept by git on a separate parent and are not part of that patch. `p`
pops the selected stash, `a` applies it and keeps it, `d` drops it. Nothing else in this view
writes.

## Keys

**Anywhere**

| Key | Action |
| --- | --- |
| `1` `2` `3` `4` | switch view |
| `tab` | switch pane |
| `j` `k` | move the cursor |
| `g` `G` | jump to the ends |
| `ctrl+d` `ctrl+u` | half-page |
| `h` `l` | move the splitter |
| `q` `ctrl+c` | quit |

**Working tree**

| Key | Action |
| --- | --- |
| `space` | stage what is under the cursor: the whole file in the list, one line in the diff, or the whole hunk on a `@@` header |
| `a` | stage the hunk from anywhere inside it |
| `t` | toggle between the worktree and staged sides |
| `c` | open the commit message editor |
| `C` | open it pre-filled with HEAD's message, to amend |
| `A` | stage everything, untracked files included |
| `U` | unstage everything |
| `D` | discard every unstaged change to a tracked file (asks first) |
| `X` | delete every untracked file (asks first) |
| `s` | stash everything, untracked files included |
| `S` | stash tracked changes only |
| `p` | pop the newest stash |

`space` and `a` reverse into un-staging when the pane shows the staged side. `D` leaves the
index alone, so `U` then `D` is a full reset to HEAD.

**Commit message editor**

| Key | Action |
| --- | --- |
| `ctrl+s` | commit |
| `esc` | cancel and drop the message |
| `enter` | newline |
| `ctrl+c` | quit |

The editor owns every other key while it is open, so `q` types a q.

**Log**

| Key | Action |
| --- | --- |
| `a` | toggle between every ref and HEAD's history |
| `/` | search; `enter` jumps to the next match, `esc` closes |
| `n` `N` | next and previous match |
| `[` `]` | first parent, nearest child |

**Branches**

| Key | Action |
| --- | --- |
| `b` | check out the branch under the cursor |
| `enter` | open the log view on the branch under the cursor |
| `space` | open the log view on every ref, with the cursor on the branch's tip |
| `esc` | in a branch log, back to the branch list |

There is no confirmation step. A switch is reversible, and the one way it loses work is the
case git refuses on its own. When git refuses, the pane shows what it said.

**Stashes**

| Key | Action |
| --- | --- |
| `p` | pop the stash under the cursor |
| `a` | apply it and keep it |
| `d` | drop it (asks first) |

The keys that lose work — `D`, `X` and `d` — ask in the key bar first. `y` goes ahead; any
other key is no, and is swallowed rather than acted on.

## Mouse

A left click focuses the pane it lands on and puts that pane's cursor on the row under it,
which in the diff is the line `space` then stages. The wheel scrolls the pane under the pointer
without moving focus. Clicking the empty space below a list selects nothing, and the middle and
right buttons are left to the terminal for paste and for its own menu.

You can drag the splitter, which is the two border columns where the panes meet. Each view
keeps its own split, and it survives a terminal resize as a proportion rather than a column
count. Neither pane goes below 24 columns.

Mouse tracking costs you the terminal's own text selection, which bubblegit can neither read
nor replace. Most terminals still select while shift is held.

## When things do not go as expected

**A commit fails.** git runs your hooks and signs your commits, so a commit can fail for
reasons that have nothing to do with bubblegit. The editor stays open, keeps what you wrote,
and shows what git said.

**A change stages whole instead of by line.** A binary file, a file the change deletes
outright, and a hunk that ends without a trailing newline cannot be split. The first two are
silent. The third says so, because git applies a partial patch there happily and wrongly.

**The layout drops to one pane.** Below 48 columns there is only room for one. `tab` swaps
which one is visible, and the splitter does nothing.

## Alternatives

bubblegit is early. If it does not do what you need yet, these do:

- [lazygit](https://github.com/jesseduffield/lazygit)
- [GitUI](https://github.com/Extrawurst/gitui)
- [tig](https://github.com/jonas/tig)

## Development

```sh
go test ./...                  # unit tests and golden files
go test ./... -update          # re-record golden files after a UI change
go test ./internal/git -bench . # read-path benchmarks
go test ./internal/ui/pane -bench . # render-path benchmarks
go test ./... -short           # skip the 100k-commit fixture
```

[docs/design.md](docs/design.md) covers why it shells out to git, how the read and render paths
stay off the event loop, and how the test fixtures and performance budgets work.

## License

MIT
