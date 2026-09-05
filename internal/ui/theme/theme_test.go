package theme_test

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/f3xp/bubblegit/internal/ui/theme"
)

// TestKeepBackgroundLeavesNoReset covers the whole point of the rewrite: a
// reset anywhere inside a row clears the selection background along with the
// colour, so none may survive. Both spellings occur in practice — lipgloss
// emits \x1b[m and chroma's terminal formatter \x1b[0m — and a row carries
// both, because the diff pane's gutter is lipgloss and its body is chroma.
func TestKeepBackgroundLeavesNoReset(t *testing.T) {
	line := theme.Dim.Render("  12") + "\x1b[38;2;1;2;3mfunc\x1b[0m main()"
	if !strings.Contains(line, "\x1b[m") || !strings.Contains(line, "\x1b[0m") {
		t.Fatalf("the fixture no longer carries both resets: %q", line)
	}

	got := theme.KeepBackground(line)
	if strings.Contains(got, "\x1b[m") || strings.Contains(got, "\x1b[0m") {
		t.Errorf("a reset survived: %q", got)
	}
	if want := ansi.StringWidth(line); ansi.StringWidth(got) != want {
		t.Errorf("width changed from %d to %d; the rewrite emitted printable text", want, ansi.StringWidth(got))
	}
	if a, b := ansi.Strip(got), ansi.Strip(line); a != b {
		t.Errorf("text changed from %q to %q", b, a)
	}
}

// TestSelectRowCarriesTheBackgroundPastEveryFragment is the regression itself.
// Before the rewrite the background opened once, died at the first fragment's
// reset, and came back only for the trailing padding — a bright block, a dark
// row, and a bar at the end.
func TestSelectRowCarriesTheBackgroundPastEveryFragment(t *testing.T) {
	const width = 40
	line := theme.Meta.Render("de67d15") + " " + theme.Dim.Render("2020-01-01") + " subject"

	got := theme.SelectRow(line, width)
	bg := ansi.Style{}.BackgroundColor(lipgloss.Color("#43293a")).String()

	// Every column of the row has to be printed with the background open.
	// Resets are allowed, but only where one is immediately reopened — which
	// is what lipgloss does at the boundary between the text and the padding
	// it adds itself. A reset with anything printable after it is the bug.
	if i := strings.Index(got, bg); i < 0 {
		t.Fatalf("the selection background was never opened: %q", got)
	} else if col := unbackedColumns(got[i+len(bg):], bg); col > 0 {
		t.Errorf("%d columns of the row are printed with no background", col)
	}

	if w := ansi.StringWidth(got); w != width {
		t.Errorf("row is %d columns wide, want %d", w, width)
	}
}

// unbackedColumns counts the printable columns that follow a reset without the
// background bg being reopened first.
func unbackedColumns(s, bg string) int {
	var n int
	for {
		i := strings.Index(s, "\x1b[m")
		if i < 0 {
			return n
		}
		s = s[i+len("\x1b[m"):]
		if strings.HasPrefix(s, bg) {
			continue // reopened straight away, nothing was printed bare
		}
		if j := strings.Index(s, bg); j >= 0 {
			n += ansi.StringWidth(s[:j])
			continue
		}
		return n + ansi.StringWidth(s)
	}
}
