#!/usr/bin/env bash
# Large fixture repo for benchmarks. Built with `git fast-import` — a per-commit
# `git commit` would take ~20 minutes for 100k commits; this takes seconds.
# Usage: mkbig.sh <dest-dir> [commit-count]
cd "$(dirname "$0")"
. ./common.sh

dest="${1:?usage: mkbig.sh <dest-dir> [commit-count]}"
count="${2:-100000}"
rm -rf "$dest"
git_init "$dest"

python3 -c '
import sys
n = int(sys.argv[1])
w = sys.stdout.write
w("reset refs/heads/main\n")
for i in range(n):
    # 1577836800 = 2020-01-01T00:00:00Z. One second per commit, so ordering is
    # unambiguous and every run produces identical SHAs.
    msg = "commit %d: change file %d\n\nBody line for commit %d." % (i, i % 500, i)
    w("commit refs/heads/main\n")
    w("mark :%d\n" % (i + 1))
    w("author fixture <fixture@example.com> %d +0000\n" % (1577836800 + i))
    w("committer fixture <fixture@example.com> %d +0000\n" % (1577836800 + i))
    w("data %d\n%s\n" % (len(msg), msg))
    if i > 0:
        w("from :%d\n" % i)
    body = "file %d, revision %d\nline two\nline three\n" % (i % 500, i)
    w("M 100644 inline src/f%03d.txt\n" % (i % 500))
    w("data %d\n%s" % (len(body), body))
w("done\n")
' "$count" | git -C "$dest" fast-import --quiet --done

git -C "$dest" reset -q --hard main

# Age the worktree, then prime the index.
#
# git treats an index entry whose mtime is not strictly older than the index
# itself as "racy clean": it cannot trust the stat cache and re-hashes the file
# contents on every status. A repo built seconds ago is entirely racy, which
# makes `status` look ~6x slower than it is on any real repo, where files were
# written long before the index. Backdating the worktree to a fixed timestamp
# removes the artifact and keeps the fixture deterministic.
find "$dest/src" -type f -exec touch -t 202001010000.00 {} +
git -C "$dest" status --porcelain=v2 >/dev/null

echo "$dest"
