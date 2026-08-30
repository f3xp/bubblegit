package git

import (
	"context"
	"strconv"
	"strings"
	"time"
)

// LogPageSize is how many commits one read loads. Reads must stay bounded:
// "load every commit, then render" is the failure mode the benchmarks in
// exec_bench_test.go exist to catch.
const LogPageSize = 100

// Commit is one entry in the log. Formatting — relative dates, truncated
// authors — belongs to the pane; this layer returns the values git gave it.
type Commit struct {
	SHA     string
	Short   string
	Author  string
	When    time.Time
	Parents []string
	Subject string
}

// IsMerge reports whether the commit has more than one parent.
func (c Commit) IsMerge() bool { return len(c.Parents) > 1 }

// logFormat is one record of six fields.
//
// Five %x00 rather than six: -z appends the last one itself as the record
// terminator. A trailing %x00 here would emit an empty seventh field between
// records and slide the whole parse off its stride.
const logFormat = "%H%x00%h%x00%an%x00%aI%x00%P%x00%s"

// logFields is the stride parseLog counts in.
const logFields = 6

// Log returns up to n commits, walking back from the given tips, or from HEAD
// when there are none.
//
// Paging resumes from a set of SHAs, never an offset: `--skip=N` makes git
// walk and discard N commits, so page 500 costs a hundred times page 1, while
// naming where to start costs the same every time. See TestPaginationStrategy.
//
// The tips come from Frontier, not from the last row on screen — see there for
// why the obvious one-SHA cursor loses commits.
func Log(ctx context.Context, r *Runner, from []string, n int) ([]Commit, error) {
	args := []string{"log", "-z", "--no-color", "--format=" + logFormat, "-n", strconv.Itoa(n)}
	args = append(args, from...)

	out, err := r.Run(ctx, args...)
	if err != nil {
		// An unborn branch has no commits to walk. That is a normal state for
		// a freshly initialised repo, and it renders as an empty log rather
		// than an error.
		if h, herr := ReadHead(ctx, r); herr == nil && h.Commit == "" {
			return nil, nil
		}
		return nil, err
	}

	return parseLog(SplitZ(out)), nil
}

// Frontier returns the tips a paged walk has to resume from: every parent of a
// loaded commit that is not itself loaded. An empty result means the history
// is exhausted.
//
// Resuming from the last SHA on screen — the obvious cursor — is wrong on any
// history containing a merge. `git log <sha>` walks that commit's ancestry,
// and the next commit in date order may sit on a side branch that is not among
// its ancestors: it is skipped, and because paging only ever moves deeper it
// never appears at all. Commits silently missing from a log pane is a worse
// failure than a slow one. See TestLogPagingKeepsSideBranches.
//
// This is the queue git's own walk would be holding, rebuilt from the parent
// SHAs the log already carries, so it costs no extra read. It stays small:
// one entry per line of history still open, not one per commit.
func Frontier(commits []Commit) []string {
	loaded := make(map[string]bool, len(commits))
	for _, c := range commits {
		loaded[c.SHA] = true
	}

	var out []string
	seen := make(map[string]bool)
	for _, c := range commits {
		for _, p := range c.Parents {
			if !loaded[p] && !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	return out
}

// parseLog chunks the flat field list by stride. A root commit has an empty
// parent field and an --allow-empty-message commit an empty subject; both sit
// between NULs, so neither shortens a record.
func parseLog(fields []string) []Commit {
	out := make([]Commit, 0, len(fields)/logFields)
	for i := 0; i+logFields <= len(fields); i += logFields {
		out = append(out, Commit{
			SHA:     fields[i],
			Short:   fields[i+1],
			Author:  fields[i+2],
			When:    authorTime(fields[i+3]),
			Parents: strings.Fields(fields[i+4]),
			Subject: fields[i+5],
		})
	}
	return out
}

// authorTime parses %aI, git's strict-ISO author date.
//
// %at, the raw epoch, would be shorter and is wrong here: rendering it needs a
// zone, and the only one available is the machine's. That makes a commit's
// date depend on where it is being read, which is not what git shows and would
// make every golden file in this repo depend on the reader's TZ. %aI carries
// the offset the author committed in, which is the one git prints.
func authorTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// Detail is one commit with its message and its patch.
type Detail struct {
	Commit  Commit
	Email   string
	Message string
	Files   []FileDiff
}

// detailFormat ends with %x00, unlike logFormat, because `show` takes no -z:
// nothing else would mark where the message stops and the patch begins.
const detailFormat = "%H%x00%h%x00%an%x00%ae%x00%aI%x00%P%x00%B%x00"

// detailParts is the metadata field count plus the patch remainder.
const detailParts = 8

// Show returns one commit and the patch it introduced.
//
// This is the one read path that uses porcelain rather than plumbing, against
// the rule the rest of this package follows, and the reason is spawn count.
// `diff-tree` carries no commit message and prints nothing at all for the
// initial commit without --root, so a detail pane built on it costs two
// processes for what `show` answers in one. --format= pins the only part of
// the output that could drift between git versions; below it is the same
// patch text either command emits.
//
// -m --first-parent is load-bearing, not decoration: without it a merge
// commit prints no patch whatsoever and the pane renders blank on exactly the
// commits a user most wants explained. --cc is not the answer either — it
// shows only hunks differing from both parents, which for a clean merge is
// nothing. See TestShowRendersMergeAndRoot.
func Show(ctx context.Context, r *Runner, rev string) (Detail, error) {
	out, err := r.Run(ctx, "show", "--no-color", "--no-ext-diff", "--no-renames",
		"-p", "-m", "--first-parent", "--format="+detailFormat, rev)
	if err != nil {
		return Detail{}, err
	}
	return parseDetail(string(out)), nil
}

func parseDetail(text string) Detail {
	parts := strings.SplitN(text, "\x00", detailParts)
	if len(parts) < detailParts {
		return Detail{}
	}
	// %B ends with the newline git stores. Trailing blank lines are not the
	// author's text and would push the patch down the pane for nothing.
	msg := strings.TrimRight(parts[6], "\n")
	return Detail{
		Commit: Commit{
			SHA:     parts[0],
			Short:   parts[1],
			Author:  parts[2],
			When:    authorTime(parts[4]),
			Parents: strings.Fields(parts[5]),
			Subject: firstLine(msg),
		},
		Email:   parts[3],
		Message: msg,
		Files:   parseDiffFiles(parts[7]),
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
