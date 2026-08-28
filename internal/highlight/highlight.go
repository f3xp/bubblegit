// Package highlight applies syntax colouring to diff content.
package highlight

import (
	"strings"
	"sync"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

// Highlighter colours lines of one file.
//
// A zero Highlighter, and one for a path with no known lexer, passes text
// through unchanged — which is exactly what is wanted for binary blobs and
// unrecognised extensions.
type Highlighter struct {
	lexer chroma.Lexer
	mu    sync.Mutex // chroma lexers are not documented as concurrency-safe
}

var (
	formatter = formatters.Get("terminal16m")
	style     = styles.Get("catppuccin-mocha")
)

func init() {
	if formatter == nil {
		formatter = formatters.Fallback
	}
	if style == nil {
		style = styles.Fallback
	}
}

// For returns a Highlighter for the given path. Coalescing the lexer lookup
// per file matters: lexers.Match walks every registered lexer's filename
// globs, which is far too slow to redo for every line of a large diff.
func For(path string) *Highlighter {
	lx := lexers.Match(path)
	if lx == nil {
		return &Highlighter{}
	}
	// Coalesce runs of identical token types; fewer, larger tokens mean fewer
	// escape sequences per line.
	return &Highlighter{lexer: chroma.Coalesce(lx)}
}

// Line returns text with ANSI colour applied.
//
// ponytail: each line is lexed independently, so lexer state does not carry
// across lines — the interior of a multi-line string or block comment is
// coloured as ordinary code. It is wrong only in appearance, and it keeps the
// diff pane a pure function of the line it is drawing. Upgrade path if it
// grates: lex the reconstructed post-image as one document and map tokens
// back to lines by number.
func (h *Highlighter) Line(text string) string {
	if h == nil || h.lexer == nil || text == "" {
		return text
	}

	h.mu.Lock()
	it, err := h.lexer.Tokenise(nil, text)
	h.mu.Unlock()
	if err != nil {
		return text
	}

	var b strings.Builder
	if err := formatter.Format(&b, style, it); err != nil {
		return text
	}
	// chroma appends the trailing newline it was given; the caller lays out
	// lines itself.
	return strings.TrimSuffix(b.String(), "\n")
}
