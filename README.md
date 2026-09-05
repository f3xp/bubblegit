<p align="center">
  <img src="docs/assets/bubblegit.svg" alt="bubblegit" width="200">
</p>

<h1 align="center">bubblegit</h1>

A git TUI built on [Bubble Tea v2](https://github.com/charmbracelet/bubbletea). The goals are
to stay fast on large repositories and to be more interactive than the alternatives.

> **Status: early.** What is documented below works. Anything not documented is not built yet.

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

`1` working tree, `2` log, `3` branches. Each view is a pair of panes, and `tab` switches
between them.

**Working tree (`1`).** Files on the left, the diff of the selected file on the right. `t`
toggles the diff between the worktree and the staged side, and the diff pane title says which
side you are looking at.

**Log (`2`).** Commits with a graph column on the left, the selected commit on the right: its
message and its patch, highlighted the same way a working-tree diff is. A merge shows what it
brought in over its first parent. The initial commit shows its whole tree. Nothing in this view
writes, so the staging and commit keys do nothing here.

**Branches (`3`).** Local branches on the left, the tip commit of the selected branch on the
right. The view opens on the branch you are on. Only local branches are listed, since a
remote-tracking ref is not a branch you can be on.

Each row shows how far the branch has drifted from its upstream:

| Marker | Meaning |
| --- | --- |
| `↑2` | 2 commits ahead |
| `↓1` | 1 commit behind |
| `↑2↓1` | both |
| dim `=` | matches the upstream |
| `gone` | the upstream ref was deleted |
| blank | the branch tracks nothing |

## Keys

**Anywhere**

| Key | Action |
| --- | --- |
| `1` `2` `3` | switch view |
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

`space` and `a` reverse into un-staging when the pane shows the staged side.

**Commit message editor**

| Key | Action |
| --- | --- |
| `ctrl+s` | commit |
| `esc` | cancel and drop the message |
| `enter` | newline |
| `ctrl+c` | quit |

The editor owns every other key while it is open, so `q` types a q.

**Branches**

| Key | Action |
| --- | --- |
| `b` | check out the branch under the cursor |

There is no confirmation step. A switch is reversible, and the one way it loses work is the
case git refuses on its own. When git refuses, the pane shows what it said.

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
