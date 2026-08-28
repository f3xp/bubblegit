package git

import (
	"context"
	"strings"
)

// Kind distinguishes the record types in `status --porcelain=v2`.
type Kind byte

const (
	KindOrdinary  Kind = '1'
	KindRenamed   Kind = '2'
	KindUnmerged  Kind = 'u'
	KindUntracked Kind = '?'
	KindIgnored   Kind = '!'
)

// FileStatus is one record from `git status --porcelain=v2`.
//
// There is deliberately one FileStatus per git record, not per path. Git can
// legitimately report the same path twice — `git rm --cached foo` while foo
// still exists on disk yields both a deleted-from-index entry and an
// untracked entry — and those are two different facts. Merging them here
// would invent an XY code git never emits and lose information the pane may
// want. Display-level grouping is the UI's decision, not the parser's.
type FileStatus struct {
	Kind Kind

	// Staged and Unstaged are the two halves of git's XY code: Staged is the
	// index-vs-HEAD change, Unstaged is the worktree-vs-index change.
	// '.' means unchanged on that side.
	Staged   byte
	Unstaged byte

	Path string

	// OrigPath is set only for renames and copies.
	OrigPath string

	// Score is the rename/copy similarity, e.g. "R100". Empty otherwise.
	Score string
}

func (f FileStatus) IsStaged() bool   { return f.Staged != '.' && f.Staged != 0 }
func (f FileStatus) IsUnstaged() bool { return f.Unstaged != '.' && f.Unstaged != 0 }

// IsUntracked reports whether the file is not in the index at all.
func (f FileStatus) IsUntracked() bool { return f.Kind == KindUntracked }

// IsUnmerged reports whether the file has a conflict.
func (f FileStatus) IsUnmerged() bool { return f.Kind == KindUnmerged }

// Status returns the working tree state.
//
// -z is not optional: without it git applies core.quotepath and escapes
// non-ASCII paths, and separates rename pairs with a tab that a real filename
// may contain.
func Status(ctx context.Context, r *Runner) ([]FileStatus, error) {
	out, err := r.Run(ctx, "status", "--porcelain=v2", "-z", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	return parseStatus(SplitZ(out))
}

// parseStatus consumes NUL-separated records. A rename record spans two
// fields — the new path and the original — so the caller cannot assume one
// record per field.
func parseStatus(fields []string) ([]FileStatus, error) {
	var out []FileStatus

	for i := 0; i < len(fields); i++ {
		rec := fields[i]
		if rec == "" {
			continue
		}

		switch rec[0] {
		case '#':
			// `# branch.oid` and friends, only present with --branch.
			continue

		case byte(KindUntracked), byte(KindIgnored):
			// "? path" / "! path"
			if len(rec) < 3 {
				continue
			}
			out = append(out, FileStatus{Kind: Kind(rec[0]), Path: rec[2:]})

		case byte(KindOrdinary):
			// 1 <XY> <sub> <mH> <mI> <mW> <hH> <hI> <path>
			f, ok := parseEntry(rec, 8)
			if !ok {
				continue
			}
			out = append(out, f)

		case byte(KindRenamed):
			// 2 <XY> <sub> <mH> <mI> <mW> <hH> <hI> <X><score> <path>
			// followed by a separate NUL field holding the original path.
			f, ok := parseEntry(rec, 9)
			if !ok {
				continue
			}
			if parts := strings.SplitN(rec, " ", 10); len(parts) == 10 {
				f.Score = parts[8] // e.g. "R100"
			}
			if i+1 < len(fields) {
				i++
				f.OrigPath = fields[i]
			}
			out = append(out, f)

		case byte(KindUnmerged):
			// u <XY> <sub> <m1> <m2> <m3> <mW> <h1> <h2> <h3> <path>
			// Three stages, so the field count differs from an ordinary entry.
			// M1 is read-only and only needs the path and the conflict marker.
			f, ok := parseEntry(rec, 10)
			if !ok {
				continue
			}
			out = append(out, f)
		}
	}
	return out, nil
}

// parseEntry splits a record into pathIndex leading space-separated fields
// plus a trailing path, which may itself contain spaces.
func parseEntry(rec string, pathIndex int) (FileStatus, bool) {
	parts := strings.SplitN(rec, " ", pathIndex+1)
	if len(parts) < pathIndex+1 {
		return FileStatus{}, false
	}
	xy := parts[1]
	if len(xy) != 2 {
		return FileStatus{}, false
	}
	return FileStatus{
		Kind:     Kind(rec[0]),
		Staged:   xy[0],
		Unstaged: xy[1],
		Path:     parts[pathIndex],
	}, true
}
