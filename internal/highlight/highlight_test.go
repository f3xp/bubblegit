package highlight_test

import (
	"strings"
	"testing"

	"github.com/f3xp/bubblegit/internal/highlight"
)

func TestLine(t *testing.T) {
	t.Run("known language is coloured", func(t *testing.T) {
		got := highlight.For("main.go").Line("func main() {}")
		if !strings.Contains(got, "\x1b[") {
			t.Errorf("no ANSI escapes in %q", got)
		}
		if !strings.Contains(got, "func") || !strings.Contains(got, "main") {
			t.Errorf("content lost: %q", got)
		}
	})

	t.Run("unknown extension passes through unchanged", func(t *testing.T) {
		const in = "some plain text"
		if got := highlight.For("logo.png").Line(in); got != in {
			t.Errorf("got %q, want it unchanged", got)
		}
	})

	t.Run("empty line", func(t *testing.T) {
		if got := highlight.For("main.go").Line(""); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})

	t.Run("no trailing newline is added", func(t *testing.T) {
		got := highlight.For("main.go").Line("x := 1")
		if strings.HasSuffix(got, "\n") {
			t.Errorf("%q ends with a newline; the pane lays out lines itself", got)
		}
	})

	// A line taken out of the middle of a block comment or multi-line string
	// cannot be lexed correctly without carrying state across lines. It must
	// still round-trip the text rather than mangling or dropping it.
	t.Run("mid-construct line keeps its text", func(t *testing.T) {
		const in = `   still inside a block comment`
		got := highlight.For("main.go").Line(in)
		if !strings.Contains(stripANSI(got), strings.TrimSpace(in)) {
			t.Errorf("text mangled: %q", got)
		}
	})
}

// TestForIsMemoised pins the cache rather than its effect. lexers.Match walks
// every registered lexer's globs — about a millisecond — and it is reached once
// per file of a commit and once per keystroke while a cursor moves down the
// file list, so a lookup repeated per call is a stall the user feels rather
// than a number a benchmark reports.
//
// A miss is cached as firmly as a hit: a file with no lexer at all is the one
// that pays the whole walk before coming back empty.
func TestForIsMemoised(t *testing.T) {
	for _, path := range []string{"main.go", "a/deep/path/notes.noext"} {
		if a, b := highlight.For(path), highlight.For(path); a != b {
			t.Errorf("For(%q) returned two instances; the lexer lookup is being redone per call", path)
		}
	}

	// Chroma matches on the base name and nothing else, which is what makes the
	// base a sound cache key: two paths that share a filename share an answer.
	if a, b := highlight.For("main.go"), highlight.For("internal/ui/main.go"); a != b {
		t.Error("two paths with the same filename got different highlighters")
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\x1b' {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
