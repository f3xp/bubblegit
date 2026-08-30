package git

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ErrNoEOLPartial is returned when a line-level selection lands in a hunk that
// carries a "\ No newline at end of file" marker.
//
// The marker annotates the line above it. Dropping that line strands the
// marker, and demoting it to context claims the index copy has no trailing
// newline while another line follows it. git apply accepts both and silently
// concatenates two lines into one — a wrong blob with a clean exit status,
// which is the worst failure shape available. Whole hunks only.
//
// ponytail: the general fix is to rewrite the marker as the selection changes
// which line is last. Worth it if anyone actually stages half a hunk of a
// file with no trailing newline.
var ErrNoEOLPartial = errors.New("hunk has no trailing newline; stage the whole hunk")

// Patch synthesises a one-hunk unified patch for `git apply --cached`.
//
// lineIdx picks a single line inside the hunk; -1 takes the whole hunk. A
// selection that changes nothing — the stage key pressed on a context line —
// returns a nil patch rather than an error, because that is a no-op and not a
// mistake.
//
// One hunk per patch is deliberate. A multi-hunk patch has to recompute each
// hunk's new-side start from the sizes of the hunks before it, and getting
// that wrong produces a patch that still applies cleanly to the wrong place.
// Since every M2 action is single-hunk anyway, the accumulator never has to
// exist.
func Patch(fd FileDiff, hunkIdx, lineIdx int, reverse bool) ([]byte, error) {
	if hunkIdx < 0 || hunkIdx >= len(fd.Hunks) {
		return nil, fmt.Errorf("patch: hunk %d of %d", hunkIdx, len(fd.Hunks))
	}
	h := fd.Hunks[hunkIdx]
	if lineIdx >= 0 && hasNoEOL(h) {
		return nil, ErrNoEOLPartial
	}

	var body strings.Builder
	oldLines, newLines, changed := 0, 0, false

	for i, l := range h.Lines {
		if l.Kind == LineNoEOL {
			// Only reachable for a whole-hunk selection, where no line is
			// dropped, so the marker always still follows the line it
			// annotates.
			body.WriteString("\\ " + l.Text + "\n")
			continue
		}

		kind := l.Kind
		if kind != LineContext && lineIdx >= 0 && i != lineIdx {
			// An unselected change. On the side the patch is applied against
			// the line already exists, so it has to appear as context; on the
			// other side it does not, so it has to vanish. Which is which
			// flips with the direction of the apply.
			if (kind == LineDel) == reverse {
				continue
			}
			kind = LineContext
		}

		switch kind {
		case LineContext:
			oldLines++
			newLines++
		case LineAdd:
			newLines++
			changed = true
		case LineDel:
			oldLines++
			changed = true
		}
		body.WriteByte(byte(kind))
		body.WriteString(l.Text)
		body.WriteByte('\n')
	}

	if !changed {
		return nil, nil
	}

	// Both starts are git's own, unmodified: a forward apply locates the hunk
	// by the old start and a reverse apply by the new one, so inventing either
	// would break one direction.
	//
	// The header is the traditional two-line form with no `diff --git`, no
	// index line and no mode. git apply creates a missing index entry from it
	// at mode 100644, which is what staging an untracked file needs.
	//
	// ponytail: an untracked executable therefore stages as 0644. Emit a mode
	// line if anyone stages a script by hunk and notices.
	var b strings.Builder
	b.WriteString("--- " + quotePath("a/"+fd.Path) + "\n")
	b.WriteString("+++ " + quotePath("b/"+fd.Path) + "\n")
	fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n", h.OldStart, oldLines, h.NewStart, newLines)
	b.WriteString(body.String())
	return []byte(b.String()), nil
}

func hasNoEOL(h Hunk) bool {
	for _, l := range h.Lines {
		if l.Kind == LineNoEOL {
			return true
		}
	}
	return false
}

// quotePath renders a path as a C-quoted patch header field.
//
// It quotes unconditionally rather than only when the path needs it. The
// alternative is a rule for when to quote, and the unquoted form splits the
// header field at the first tab: a path containing a tab then stages a
// *different, shorter path* with no error at all. One code path cannot get
// that wrong.
//
// High bytes are passed through raw. git accepts UTF-8 inside the quotes, and
// escaping it would only have to agree with core.quotepath.
func quotePath(p string) string {
	var b strings.Builder
	b.Grow(len(p) + 2)
	b.WriteByte('"')
	for i := 0; i < len(p); i++ {
		switch c := p[i]; {
		case c == '"' || c == '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
		case c == '\t':
			b.WriteString(`\t`)
		case c == '\n':
			b.WriteString(`\n`)
		case c < 0x20 || c == 0x7f:
			fmt.Fprintf(&b, `\%03o`, c)
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// ApplyCached pipes a patch into the index. reverse un-stages.
func ApplyCached(ctx context.Context, r *Runner, patch []byte, reverse bool) error {
	// --whitespace=nowarn: the patch is git's own diff output fed back, so any
	// whitespace it complains about is content the user already has.
	args := []string{"apply", "--cached", "--whitespace=nowarn"}
	if reverse {
		args = append(args, "--reverse")
	}
	_, err := r.RunStdin(ctx, patch, append(args, "-")...)
	return err
}

// StageFile stages a whole path.
//
// This is not a shortcut for building a patch covering every hunk: `git add`
// handles the cases a synthesised patch cannot. A patch that deletes every
// line leaves an empty blob in the index instead of removing the entry, and a
// binary file has no textual hunks to select at all.
func StageFile(ctx context.Context, r *Runner, path string) error {
	_, err := r.Run(ctx, "add", "--", path)
	return err
}

// UnstageFile drops a whole path's staged change.
//
// ponytail: `restore --staged` resolves HEAD, so this reports git's own error
// in a repository with no commits yet. Fixing it needs the `rm --cached`
// branch for files with no HEAD entry; M3 has to answer the same question.
func UnstageFile(ctx context.Context, r *Runner, path string) error {
	_, err := r.Run(ctx, "restore", "--staged", "--", path)
	return err
}
