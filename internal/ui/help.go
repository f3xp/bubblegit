package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/f3xp/bubblegit/internal/ui/keys"
	"github.com/f3xp/bubblegit/internal/ui/theme"
)

// gutter separates the two columns of bindings.
const gutter = "   "

// helpView draws the keybinding popup at most width by height.
//
// The box is sized to its contents rather than to the space it is given: a
// help popup that stretched to the full frame would be a view, and the point
// of the popup is that the panes stay readable around it.
//
// It is a free function rather than a method on Model because it needs nothing
// from the model but the keymap, and a golden test can then render it without
// driving an Update loop.
//
// ponytail: no scrolling. The keymap is 23 bindings, which fits two columns on
// the smallest terminal the rest of the UI targets; add paging when it
// outgrows the screen rather than before.
func helpView(k keys.Map, width, height int) string {
	// The border takes two of the columns the caller offered, and the border
	// plus the title three of the rows.
	maxInner, maxRows := width-2, height-3
	if maxInner < 1 || maxRows < 1 {
		return ""
	}

	groups := k.Bindings()
	left, right := splitGroups(groups, maxRows)
	rows := joinColumns(renderGroups(left), renderGroups(right))
	if len(rows) > maxRows {
		rows = rows[:maxRows]
	}

	inner := 0
	for i, r := range rows {
		rows[i] = ansi.Truncate(r, maxInner, "…")
		if w := ansi.StringWidth(rows[i]); w > inner {
			inner = w
		}
	}

	head := lipgloss.NewStyle().Width(inner).Render(theme.Title.Render(ansi.Truncate("Keys", inner, "…")))
	body := lipgloss.NewStyle().Width(inner).Render(strings.Join(rows, "\n"))

	return theme.Border(true).Render(lipgloss.JoinVertical(lipgloss.Left, head, body))
}

// splitGroups deals the groups into two columns, breaking at whichever group
// boundary comes closest to halving the height. Everything goes in one column
// when the list already fits, which is what makes the box tall and narrow
// rather than short and wide.
func splitGroups(groups [][]keys.Binding, maxRows int) (left, right [][]keys.Binding) {
	if rowsFor(groups) <= maxRows {
		return groups, nil
	}

	best, bestDelta := 0, -1
	for i := 1; i < len(groups); i++ {
		delta := rowsFor(groups[:i]) - rowsFor(groups[i:])
		if delta < 0 {
			delta = -delta
		}
		if bestDelta < 0 || delta < bestDelta {
			best, bestDelta = i, delta
		}
	}
	return groups[:best], groups[best:]
}

// rowsFor is the height of a run of groups: one row per binding, plus a blank
// line between groups.
func rowsFor(groups [][]keys.Binding) int {
	n := max(len(groups)-1, 0)
	for _, g := range groups {
		n += len(g)
	}
	return n
}

// renderGroups lays one column out, padding the key column to the widest key
// in that column alone — a narrow column next to a wide one should not inherit
// its neighbour's gap.
func renderGroups(groups [][]keys.Binding) []string {
	keyW := 0
	for _, g := range groups {
		for _, b := range g {
			if w := ansi.StringWidth(bindingKeys(b)); w > keyW {
				keyW = w
			}
		}
	}

	var rows []string
	for i, g := range groups {
		if i > 0 {
			rows = append(rows, "")
		}
		for _, b := range g {
			key := lipgloss.NewStyle().Width(keyW).Render(theme.Cursor.Render(bindingKeys(b)))
			rows = append(rows, key+"  "+theme.Context.Render(b.Desc))
		}
	}
	return rows
}

// joinColumns sets two columns side by side, the left one padded to its own
// widest row so the right one starts at a straight edge.
func joinColumns(left, right []string) []string {
	if len(right) == 0 {
		return left
	}

	leftW := 0
	for _, r := range left {
		if w := ansi.StringWidth(r); w > leftW {
			leftW = w
		}
	}

	rows := make([]string, max(len(left), len(right)))
	for i := range rows {
		l, r := "", ""
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		if r == "" {
			// No trailing padding on a row with nothing to its right: it
			// would widen the box by a column of blanks.
			rows[i] = l
			continue
		}
		rows[i] = lipgloss.NewStyle().Width(leftW).Render(l) + gutter + r
	}
	return rows
}

// bindingKeys names one binding by its first key only. Most bindings have a
// second, more discoverable alias — "k / up", "ctrl+d / pgdown" — and listing
// both sets the key column by its widest pair, which is what made the box wide
// enough to hide the panes it is supposed to float over. Anyone who reaches
// for an arrow key finds it without being told.
//
// "g g" is the chord notation the dispatcher matches on; written out it reads
// as a typo, so the popup shows it the way it is typed.
func bindingKeys(b keys.Binding) string {
	if len(b.Keys) == 0 {
		return ""
	}
	return strings.ReplaceAll(b.Keys[0], " ", "")
}
