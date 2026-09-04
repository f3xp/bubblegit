package pane

import (
	"fmt"
	"testing"
	"time"

	"charm.land/bubbles/v2/viewport"

	"github.com/f3xp/bubblegit/internal/git"
	"github.com/f3xp/bubblegit/internal/highlight"
)

// bigDiff is one file changed beyond what anyone reads line by line — a
// generated file, a lockfile, or any untracked file, which git diffs against
// /dev/null and so arrives as one hunk of the whole thing.
func bigDiff(n int, path string) git.FileDiff {
	h := git.Hunk{Header: "@@ -1,1 +1,1 @@"}
	for i := 0; i < n; i++ {
		h.Lines = append(h.Lines, git.Line{
			Kind:   git.LineAdd,
			Text:   fmt.Sprintf("\tfoo := bar(%d) // some comment here to lex", i),
			NewNum: i + 1,
		})
	}
	return git.FileDiff{Path: path, Hunks: []git.Hunk{h}}
}

// TestRenderScalesLinearly is the render path's budget, and it is a ratio
// rather than a duration on purpose.
//
// The git-side budget in internal/git subtracts a measured process-spawn floor,
// because what it guards is git work and spawn cost is a property of the
// machine. A render has no floor to subtract, so an absolute number here would
// just be this laptop's clock committed to the repository — it would fail on a
// slower machine that had regressed nothing, and pass on a faster one that had.
//
// What it can pin is the shape: twice the lines, about twice the work. That is
// the thing worth guarding, because the way this path goes wrong is
// accidentally quadratic — a lexer lookup that stops being hoisted out of the
// line loop, a rows slice rebuilt per hunk — and the constant factor is
// reported for a human to read rather than asserted against.
func TestRenderScalesLinearly(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the render budget in -short mode")
	}

	// A thousand lines is enough: the ratio is scale-free, and 20ms against
	// 40ms is four orders of magnitude above the clock's resolution. Ten times
	// that would only make -race runs slow enough that nobody runs them.
	const n = 1000
	one := bestOf(5, func() { RenderDiff(bigDiff(n, "main.go")) })
	two := bestOf(5, func() { RenderDiff(bigDiff(2*n, "main.go")) })

	t.Logf("%d lines: %v (%v a line); %d lines: %v (%v a line)",
		n, one.Round(time.Microsecond), (one / n).Round(time.Nanosecond),
		2*n, two.Round(time.Microsecond), (two / (2 * n)).Round(time.Nanosecond))

	// Three rather than two: doubling the input doubles the allocation the
	// garbage collector then has to walk, so the honest linear case still
	// measures a little above 2x, and the failure this is aimed at is a
	// multiple of the input rather than a fraction over it.
	if ratio := float64(two) / float64(one); ratio > 3 {
		t.Errorf("doubling the diff cost %.1fx the time — the render is superlinear in the "+
			"number of lines, so something per-line is doing per-file work", ratio)
	}
}

// bestOf returns the fastest of n runs after a warmup, the same way the git
// benchmarks time a read: the minimum is the run the scheduler left alone.
func bestOf(n int, f func()) time.Duration {
	f() // warmup, discarded
	b := time.Duration(1<<63 - 1)
	for range n {
		start := time.Now()
		f()
		if d := time.Since(start); d < b {
			b = d
		}
	}
	return b
}

// BenchmarkRenderDiff is the whole cost of showing a file: the highlighter is
// ~95% of it, which is why this runs in the command that read the diff rather
// than in Update.
func BenchmarkRenderDiff(b *testing.B) {
	fd := bigDiff(10000, "main.go")
	for b.Loop() {
		RenderDiff(fd)
	}
}

// BenchmarkRenderDiffPlain is the same render for a path with no lexer, which
// is the floor the highlighter is measured against.
func BenchmarkRenderDiffPlain(b *testing.B) {
	fd := bigDiff(10000, "x.noext")
	for b.Loop() {
		RenderDiff(fd)
	}
}

func BenchmarkHighlightFor(b *testing.B) {
	for b.Loop() {
		highlight.For("main.go")
	}
}

func BenchmarkHighlightLine(b *testing.B) {
	hl := highlight.For("main.go")
	for b.Loop() {
		hl.Line("\tfoo := bar(12) // some comment here to lex")
	}
}

// BenchmarkSetContentLines and BenchmarkDiffView are the two costs that stay on
// the Update path and the render loop respectively, so they are the ones a
// change to the pane has to keep small.
func BenchmarkSetContentLines(b *testing.B) {
	lines, _ := renderLines(bigDiff(10000, "x.noext"))
	vp := viewport.New()
	vp.SoftWrap = false
	vp.SetWidth(80)
	vp.SetHeight(40)
	for b.Loop() {
		vp.SetContentLines(lines)
	}
}

func BenchmarkDiffView(b *testing.B) {
	d := NewDiff()
	d.SetSize(80, 40)
	d.SetDiff(RenderDiff(bigDiff(10000, "main.go")))
	for b.Loop() {
		d.View()
	}
}
