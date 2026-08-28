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
