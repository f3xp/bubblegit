package pane

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/f3xp/bubblegit/internal/git"
)

// goDiff is one indented Go hunk: the case where the selected row has to carry
// the background across chroma's own escapes as well as lipgloss's.
func goDiff() git.FileDiff {
	h := git.Hunk{Header: "@@ -1,3 +1,3 @@"}
	for i, text := range []string{
		"func main() {",
		"\tfmt.Println(\"hello\")",
		"}",
	} {
		h.Lines = append(h.Lines, git.Line{Kind: git.LineContext, Text: text, OldNum: i + 1, NewNum: i + 1})
	}
	return git.FileDiff{Path: "main.go", Hunks: []git.Hunk{h}}
}

// row returns the pane's rendered line at index i.
func row(t *testing.T, d *Diff, i int) string {
	t.Helper()
	lines := strings.Split(d.View(), "\n")
	if i >= len(lines) {
		t.Fatalf("the pane rendered %d lines, wanted line %d", len(lines), i)
	}
	return lines[i]
}

// TestSelectedDiffRowIsFullWidth is the diff pane's half of the selection fix.
// The row is syntax-highlighted, so it carries chroma's resets as well as
// lipgloss's, and every one of them used to close the background where it fell.
func TestSelectedDiffRowIsFullWidth(t *testing.T) {
	const width = 60
	var d Diff
	d.SetSize(width, 10)
	d.SetDiff(RenderDiff(goDiff()))
	d.MoveBy(2) // the fmt.Println line: indented, and lexed as several tokens

	got := row(t, &d, 2)
	if !strings.Contains(ansi.Strip(got), "fmt.Println") {
		t.Fatalf("row 2 is not the line under the cursor: %q", ansi.Strip(got))
	}
	if w := ansi.StringWidth(got); w != width {
		t.Errorf("the selected row is %d columns wide, want the full %d", w, width)
	}
	if n := unbackedColumns(got); n > 0 {
		t.Errorf("%d columns of the selected row are printed with no background", n)
	}
}

// unbackedColumns counts the printable columns that follow a reset with no
// background reopened before them. A reset is fine where one is reopened
// straight away — lipgloss does that between a row's text and its own padding.
func unbackedColumns(s string) int {
	const bg = "\x1b[48;2;67;41;58m"
	i := strings.Index(s, bg)
	if i < 0 {
		return ansi.StringWidth(s)
	}
	var n int
	for s = s[i+len(bg):]; ; {
		j := strings.Index(s, "\x1b[m")
		if j < 0 {
			return n
		}
		s = s[j+len("\x1b[m"):]
		k := strings.Index(s, bg)
		if k < 0 {
			return n + ansi.StringWidth(s)
		}
		n += ansi.StringWidth(s[:k])
		s = s[k+len(bg):]
	}
}
