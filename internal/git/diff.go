package git

import (
	"context"
	"errors"
	"strconv"
	"strings"
)

// LineKind classifies a line inside a hunk.
type LineKind byte

const (
	LineContext LineKind = ' '
	LineAdd     LineKind = '+'
	LineDel     LineKind = '-'
	// LineNoEOL is git's "\ No newline at end of file" marker. It annotates
	// the preceding line rather than being content of its own, and it must
	// survive into any patch we later synthesise.
	LineNoEOL LineKind = '\\'
)

// Line is a single line of a hunk. OldNum and NewNum are 1-based line numbers
// in the pre- and post-image; each is 0 where the line does not exist on that
// side.
type Line struct {
	Kind   LineKind
	Text   string
	OldNum int
	NewNum int
}

// Hunk is one @@ block.
type Hunk struct {
	Header   string // the full @@ line, including any trailing section heading
	OldStart int
	OldLines int
	NewStart int
	NewLines int
	Lines    []Line
}

// FileDiff is the diff of a single path.
type FileDiff struct {
	Path string

	// Binary is set when git refused to produce a textual diff. Hunks is then
	// empty and the UI shows a placeholder.
	Binary bool

	// Untracked marks a whole-file diff synthesised against /dev/null.
	Untracked bool

	Hunks []Hunk
}

// IsEmpty reports whether there is nothing to render.
func (d FileDiff) IsEmpty() bool { return !d.Binary && len(d.Hunks) == 0 }

// DiffFile returns the diff for one path.
//
// staged selects index-vs-HEAD rather than worktree-vs-index. Untracked files
// have no index entry at all, so plain `git diff` returns nothing for them;
// they are diffed against /dev/null instead, which is what makes an untracked
// file render as a whole-file addition rather than a blank pane.
func DiffFile(ctx context.Context, r *Runner, path string, staged, untracked bool) (FileDiff, error) {
	args := []string{"diff", "--no-color", "--no-ext-diff", "--no-renames"}
	switch {
	case untracked:
		args = append(args, "--no-index", "--", "/dev/null", path)
	case staged:
		args = append(args, "--cached", "--", path)
	default:
		args = append(args, "--", path)
	}

	out, err := r.Run(ctx, args...)
	if err != nil && !isDiffFoundChanges(err) {
		return FileDiff{}, err
	}

	d := parseDiff(string(out))
	d.Path = path
	d.Untracked = untracked
	return d, nil
}

// isDiffFoundChanges reports whether err is git-diff's "there were
// differences" signal rather than a real failure. `--no-index` (and
// `--exit-code`) exit 1 to mean "differs", which is the normal case here.
func isDiffFoundChanges(err error) bool {
	var gerr *Error
	return errors.As(err, &gerr) && gerr.ExitCode == 1
}

// parseDiff turns unified diff text into hunks.
//
// It deliberately ignores everything before the first @@ except the binary
// marker: the file headers carry no information the caller does not already
// have, and --no-renames keeps the header shape simple.
func parseDiff(text string) FileDiff {
	var d FileDiff
	if text == "" {
		return d
	}

	var hunk *Hunk
	oldNum, newNum := 0, 0

	for _, raw := range strings.Split(text, "\n") {
		switch {
		case strings.HasPrefix(raw, "Binary files ") || strings.HasPrefix(raw, "GIT binary patch"):
			d.Binary = true
			return d

		case strings.HasPrefix(raw, "@@"):
			if hunk != nil {
				d.Hunks = append(d.Hunks, *hunk)
			}
			h := parseHunkHeader(raw)
			hunk = &h
			oldNum, newNum = h.OldStart, h.NewStart

		case hunk == nil:
			// Still in the file header.
			continue

		case raw == "":
			// Trailing newline from the final Split; not a content line.
			continue

		default:
			switch raw[0] {
			case '+':
				hunk.Lines = append(hunk.Lines, Line{Kind: LineAdd, Text: raw[1:], NewNum: newNum})
				newNum++
			case '-':
				hunk.Lines = append(hunk.Lines, Line{Kind: LineDel, Text: raw[1:], OldNum: oldNum})
				oldNum++
			case ' ':
				hunk.Lines = append(hunk.Lines, Line{
					Kind: LineContext, Text: raw[1:], OldNum: oldNum, NewNum: newNum,
				})
				oldNum++
				newNum++
			case '\\':
				// "\ No newline at end of file" — annotates the previous line,
				// consumes no line number on either side.
				hunk.Lines = append(hunk.Lines, Line{Kind: LineNoEOL, Text: strings.TrimPrefix(raw, "\\ ")})
			default:
				// Anything else this deep is a diff we do not understand;
				// skipping beats guessing.
				continue
			}
		}
	}
	if hunk != nil {
		d.Hunks = append(d.Hunks, *hunk)
	}
	return d
}

// parseHunkHeader reads "@@ -a,b +c,d @@ optional section heading".
// The counts are optional and default to 1, which is how git writes a
// single-line hunk.
func parseHunkHeader(line string) Hunk {
	h := Hunk{Header: line, OldLines: 1, NewLines: 1}

	rest := strings.TrimPrefix(line, "@@")
	end := strings.Index(rest, "@@")
	if end < 0 {
		return h
	}
	for _, field := range strings.Fields(rest[:end]) {
		start, count := parseRange(field[1:])
		switch field[0] {
		case '-':
			h.OldStart, h.OldLines = start, count
		case '+':
			h.NewStart, h.NewLines = start, count
		}
	}
	return h
}

func parseRange(s string) (start, count int) {
	count = 1
	if comma := strings.IndexByte(s, ','); comma >= 0 {
		count, _ = strconv.Atoi(s[comma+1:])
		s = s[:comma]
	}
	start, _ = strconv.Atoi(s)
	return start, count
}
