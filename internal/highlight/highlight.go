// Package highlight applies syntax colouring to diff content.
package highlight

import (
	"path/filepath"
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
	// mu is load-bearing, not defensive. For hands the same Highlighter to
	// every caller asking about a filename, and rendering now happens in the
	// command goroutines that fetch a diff, a commit and a branch tip — so
	// several of them can be lexing through this one lexer at once, and chroma
	// lexers are not documented as concurrency-safe.
	mu sync.Mutex
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

// cache memoises For by filename.
//
// ponytail: unbounded, one small entry per distinct filename a session looks
// at. Evict when a session is shown to hold enough filenames for that to
// matter, which is not a repository anyone has.
var cache sync.Map // filename -> *Highlighter

// For returns a Highlighter for the given path. Coalescing the lexer lookup
// per file matters: lexers.Match walks every registered lexer's filename
// globs, which is far too slow to redo for every line of a large diff — and
// too slow to redo per file, which is what a commit touching a vendored tree
// asks for, or per keystroke, which is what holding j down the file list does.
//
// The cache is keyed by the base name because that is what chroma matches on:
// its registry globs the base and nothing else, so two paths sharing a
// filename share an answer by construction. Misses are cached too. A file with
// no lexer at all — .lock, .noext, a plain README — is exactly the one that
// pays the full walk over every registered lexer before coming back empty.
func For(path string) *Highlighter {
	name := filepath.Base(path)
	if h, ok := cache.Load(name); ok {
		return h.(*Highlighter)
	}

	h := &Highlighter{}
	if lx := lexers.Match(name); lx != nil {
		// Coalesce runs of identical token types; fewer, larger tokens mean
		// fewer escape sequences per line.
		h.lexer = chroma.Coalesce(lx)
	}
	actual, _ := cache.LoadOrStore(name, h)
	return actual.(*Highlighter)
}

// Line returns text with ANSI colour applied.
//
// ponytail: each line is lexed independently, so lexer state does not carry
// across lines — the interior of a multi-line string or block comment is
// coloured as ordinary code. It is wrong only in appearance, and it keeps the
// diff pane a pure function of the line it is drawing. Upgrade path if it
// grates: lex the reconstructed post-image as one document and map tokens
// back to lines with chroma.SplitTokensIntoLines.
//
// That upgrade buys correctness and nothing else. Measured over 5000 lines of
// this repository's own Go: 70.4ms lexed a line at a time, 72.4ms lexed as one
// document and split. The cost is in regexp2 matching runes, not in per-call
// setup, so there is no per-line overhead to amortise away — and the document
// version has to be built per hunk from two reconstructed images, and re-records
// every golden file. Worth doing the day the miscolouring grates; not worth
// doing for speed.
func (h *Highlighter) Line(text string) string {
	if h == nil || h.lexer == nil || text == "" {
		return text
	}

	// The lock covers the formatting too, not just the Tokenise call that
	// returns the iterator: chroma lexes lazily, so the actual work happens as
	// Format drains it. Releasing in between would leave the shared lexer being
	// driven by two goroutines at once, which is the thing this guards.
	h.mu.Lock()
	defer h.mu.Unlock()

	it, err := h.lexer.Tokenise(nil, text)
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
