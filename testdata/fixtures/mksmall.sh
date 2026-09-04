#!/usr/bin/env bash
# Small fixture repo covering the parser edge cases that break naive git TUIs.
# Usage: mksmall.sh <dest-dir>
cd "$(dirname "$0")"
. ./common.sh

dest="${1:?usage: mksmall.sh <dest-dir>}"
rm -rf "$dest"
git_init "$dest"
cd "$dest"

# --- base commit -------------------------------------------------------------
printf 'line1\nline2\nline3\n' > plain.txt
printf 'a\nb\nc\nd\ne\nf\ng\nh\n' > "spaced name.txt"
printf 'unicode\n' > "ünïcødé-ファイル.txt"
printf 'no trailing newline' > noeol.txt          # \ No newline at end of file
printf 'package main\n\nfunc main() {}\n' > main.go
# Twenty lines so two edits at opposite ends stay two separate hunks under the
# default -U3. Every other file here collapses into one, and hunk-level staging
# is only really tested when there is a second hunk to leave alone.
seq 1 20 > two-hunks.txt
# A binary file: git emits "Binary files ... differ" with no hunks, and the
# diff pane needs a placeholder rather than an empty body.
printf '\x89PNG\r\n\x1a\n\x00\x01\x02\x03binary payload' > logo.png
git add -A
git commit -qm "base: files with spaces, unicode, and no trailing newline"

# --- second commit on main ---------------------------------------------------
printf 'line1\nline2 edited\nline3\nline4\n' > plain.txt
git add -A
git commit -qm "edit plain.txt"

# --- a branch that will be merged -------------------------------------------
git checkout -q -b feature
printf 'feature content\n' > feature.txt
git add -A
git commit -qm "add feature.txt"

git checkout -q main
git merge -q --no-ff -m "merge feature into main" feature

# --- branch tracking states --------------------------------------------------
# `for-each-ref`'s %(upstream:track) is the only thing that reports ahead/behind
# without a process per branch, and nothing above exercises it: there is no
# remote here and no branch tracks anything. These five branches cover every
# state a row can be in — ahead, behind, in sync, upstream deleted, and no
# upstream at all.
#
# All of it is refs and no commits, so every SHA already written above stays
# put and the golden files that name them do not move. The remote is this
# repository itself, named by absolute path: nothing here fetches, and a URL
# that resolves keeps `git branch --set-upstream-to` from warning — but a
# relative "." would resolve against whatever directory later reads it.
git remote add origin "$dest"
# One commit back on the first-parent line, which leaves main ahead by two:
# the merge itself and the side-branch commit it brought in.
git update-ref refs/remotes/origin/main HEAD~1
git branch -q --set-upstream-to=origin/main main
git branch -q behind HEAD~2                                 # one behind origin/main
git branch -q --set-upstream-to=origin/main behind
git branch -q synced
git update-ref refs/remotes/origin/synced refs/heads/synced
git branch -q --set-upstream-to=origin/synced synced
git branch -q gone-upstream
git update-ref refs/remotes/origin/gone-upstream refs/heads/gone-upstream
git branch -q --set-upstream-to=origin/gone-upstream gone-upstream
git update-ref -d refs/remotes/origin/gone-upstream         # the branch is now [gone]
# `feature` is left tracking nothing, which is the fifth state.

# --- dirty working tree, so `status --porcelain=v2` has something to say ------
printf 'line1\nline2 edited\nline3 dirty\nline4\n' > plain.txt   # unstaged modify
printf 'x\nb\nc\nd\ne\nf\ng\nH\n' > "spaced name.txt"            # unstaged, two hunks
printf 'staged\n' > staged.txt && git add staged.txt              # staged add
printf 'untracked\n' > untracked.txt                              # untracked
printf 'no trailing newline, edited' > noeol.txt                  # no-eol on both sides
sed -e 's/^2$/2 edited/' -e 's/^18$/18 edited/' two-hunks.txt > tmp && mv tmp two-hunks.txt   # two separate hunks
printf 'unicode edited\n' > "ünïcødé-ファイル.txt"                    # core.quotepath escapes this path
printf '\x89PNG\r\n\x1a\n\x00\x09\x08\x07different bytes' > logo.png  # binary: no hunks, needs a placeholder
git rm -q --cached main.go && printf 'package main\n\nfunc main() {}\n' > main.go  # unstaged delete-from-index

echo "$dest"
