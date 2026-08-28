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

# --- dirty working tree, so `status --porcelain=v2` has something to say ------
printf 'line1\nline2 edited\nline3 dirty\nline4\n' > plain.txt   # unstaged modify
printf 'x\nb\nc\nd\ne\nf\ng\nH\n' > "spaced name.txt"            # unstaged, two hunks
printf 'staged\n' > staged.txt && git add staged.txt              # staged add
printf 'untracked\n' > untracked.txt                              # untracked
printf 'no trailing newline, edited' > noeol.txt                  # no-eol on both sides
printf 'unicode edited\n' > "ünïcødé-ファイル.txt"                    # core.quotepath escapes this path
git rm -q --cached main.go && printf 'package main\n\nfunc main() {}\n' > main.go  # unstaged delete-from-index

echo "$dest"
