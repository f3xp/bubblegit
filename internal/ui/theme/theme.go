// Package theme holds the shared lipgloss styles.
package theme

import (
	"hash/fnv"
	"strings"

	"charm.land/lipgloss/v2"
)

var (
	Add     = lipgloss.NewStyle().Foreground(lipgloss.Color("#a6e3a1"))
	Del     = lipgloss.NewStyle().Foreground(lipgloss.Color("#f38ba8"))
	Context = lipgloss.NewStyle().Foreground(lipgloss.Color("#cdd6f4"))
	Meta    = lipgloss.NewStyle().Foreground(lipgloss.Color("#f5c2e7"))
	Dim     = lipgloss.NewStyle().Foreground(lipgloss.Color("#6c7086"))
	Err     = lipgloss.NewStyle().Foreground(lipgloss.Color("#f38ba8")).Bold(true)

	// Staged and Unstaged colour the two halves of git's XY status code.
	Staged   = lipgloss.NewStyle().Foreground(lipgloss.Color("#a6e3a1"))
	Unstaged = lipgloss.NewStyle().Foreground(lipgloss.Color("#f9e2af"))

	Selected = lipgloss.NewStyle().Background(lipgloss.Color("#43293a"))

	// Match marks the text a log search matched, in the row itself.
	Match = lipgloss.NewStyle().Underline(true).Foreground(lipgloss.Color("#f9e2af"))

	// Branch is the badge for HEAD in the app header and the pill for a local
	// branch beside a commit in the log: the yellow git itself uses for
	// decorations, as a filled block, since which branch is checked out is the
	// one thing worth reading before touching the index.
	Branch = lipgloss.NewStyle().Bold(true).Padding(0, 1).
		Foreground(lipgloss.Color("#1e1e2e")).Background(lipgloss.Color("#f9e2af"))

	// Remote and Tag are the other two kinds of pill a log row carries. Same
	// shape as Branch so the three read as one family, in the blue and green
	// git colours remotes and tags by default.
	Remote = Branch.Background(lipgloss.Color("#89b4fa"))
	Tag    = Branch.Background(lipgloss.Color("#a6e3a1"))
	// More is the "+N" pill for the refs a row had no room for: the same
	// shape, in the dim grey, so it reads as one of the family rather than
	// as a stray count.
	More = Branch.Foreground(lipgloss.Color("#cdd6f4")).Background(lipgloss.Color("#45475a"))

	// Rule is the line under a pane title. It takes the unfocused border
	// colour whatever the focus: the border already says which pane is
	// focused, and a second signal in the same colour would only be louder.
	Rule = lipgloss.NewStyle().Foreground(lipgloss.Color("#5a4049"))

	// Cursor colours the key names in the help overlay.
	Cursor   = lipgloss.NewStyle().Foreground(lipgloss.Color("#f5c2e7"))
	Title    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#cdd6f4"))
	TitleDim = lipgloss.NewStyle().Foreground(lipgloss.Color("#6c7086"))

	// Authors are the colours an author's tag in the log can take, and the
	// palette the graph lanes cycle through. None of them is the pink of the
	// hash, so a node never blends into its neighbours.
	Authors = []lipgloss.Style{
		lipgloss.NewStyle().Foreground(lipgloss.Color("#89b4fa")),
		lipgloss.NewStyle().Foreground(lipgloss.Color("#a6e3a1")),
		lipgloss.NewStyle().Foreground(lipgloss.Color("#fab387")),
		lipgloss.NewStyle().Foreground(lipgloss.Color("#cba6f7")),
		lipgloss.NewStyle().Foreground(lipgloss.Color("#94e2d5")),
		lipgloss.NewStyle().Foreground(lipgloss.Color("#f38ba8")),
	}
)

// Author returns the style for an author's tag. Hashing the name keeps one
// person the same colour across reloads and repositories; two authors can
// still share a colour, which is why the tag carries their initials too.
func Author(name string) lipgloss.Style {
	h := fnv.New32a()
	h.Write([]byte(name))
	return Authors[int(h.Sum32()%uint32(len(Authors)))]
}

// Lane returns the style for graph column i. Columns cycle through the
// palette, so two lanes far enough apart can share a colour; what matters is
// that neighbouring lanes never do.
func Lane(i int) lipgloss.Style {
	return Authors[i%len(Authors)]
}

// keepBG is a reset that leaves the background alone: default foreground, and
// off for every attribute the two things that colour a row can turn on —
// lipgloss, and chroma's terminal formatter, which emits bold, italic and
// underline.
const keepBG = "\x1b[22;23;24;27;29;39m"

// KeepBackground rewrites the resets inside an already-styled line so that an
// enclosing background survives them.
//
// Every styled fragment ends in a reset — lipgloss's \x1b[m and chroma's
// \x1b[0m after each highlighted token — and a reset clears the background
// along with the colour. So a row simply wrapped in Selected lights up only as
// far as its first fragment, and then again across the trailing padding, which
// lipgloss paints itself: one bright block, a dark row, and a bar at the end.
// Resetting the foreground and the attributes but not the background leaves
// the row whole.
func KeepBackground(s string) string {
	s = strings.ReplaceAll(s, "\x1b[0m", keepBG)
	return strings.ReplaceAll(s, "\x1b[m", keepBG)
}

// SelectRow draws one row of a list under the selection background, across the
// full width of the pane.
func SelectRow(line string, width int) string {
	return Selected.Width(width).Render(KeepBackground(line))
}

// Border returns the frame for a pane. The focused pane is the only one with
// a bright border, so focus is readable without colour vision.
func Border(focused bool) lipgloss.Style {
	s := lipgloss.NewStyle().Border(lipgloss.RoundedBorder())
	if focused {
		return s.BorderForeground(lipgloss.Color("#f5c2e7"))
	}
	return s.BorderForeground(lipgloss.Color("#5a4049"))
}
