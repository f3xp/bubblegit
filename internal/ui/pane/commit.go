package pane

import (
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/f3xp/bubblegit/internal/ui/theme"
)

// Commit is the message editor. It is only on screen while a commit is being
// written, and while it is, it owns every key: the app routes to it before its
// own bindings, so `q` types a q rather than quitting.
type Commit struct {
	ta     textarea.Model
	amend  bool
	active bool
	err    error
	w, h   int
}

func NewCommit() Commit {
	ta := textarea.New()
	// Line numbers belong on a diff, not on a three-line commit message, and
	// the placeholder says what the two bindings are, since the pane title has
	// only room for the mode.
	ta.ShowLineNumbers = false
	ta.Placeholder = "commit message — ctrl+s to commit, esc to cancel"
	ta.CharLimit = 0 // a message is as long as the user needs it to be
	return Commit{ta: ta}
}

func (c *Commit) Active() bool   { return c.active }
func (c *Commit) Amending() bool { return c.amend }

// Open starts an edit. msg pre-fills the buffer, which is what makes an amend
// a reword rather than a retype.
func (c *Commit) Open(msg string, amend bool) tea.Cmd {
	c.active, c.amend, c.err = true, amend, nil
	c.ta.Reset()
	c.ta.SetValue(msg)
	c.ta.MoveToEnd()
	return c.ta.Focus()
}

// Close hides the editor. The buffer is dropped: a message kept across a
// cancel would reappear under the next `c` as text the user did not just type.
func (c *Commit) Close() {
	c.active, c.amend, c.err = false, false, nil
	c.ta.Blur()
	c.ta.Reset()
}

// SetError leaves the editor open. A rejected commit — a pre-commit hook, a
// failed signature — must not discard the message the user just wrote.
func (c *Commit) SetError(err error) {
	c.err = err
	c.resize()
}

func (c *Commit) SetSize(w, h int) {
	c.w, c.h = w, h
	c.resize()
}

// resize is called from both setters because the error occupies a row: the
// editor has to give one back when an error appears and take it again when the
// next attempt clears it, and either can happen after the pane was last sized.
func (c *Commit) resize() {
	h := c.h
	if c.err != nil && h > 1 {
		h--
	}
	c.ta.SetWidth(c.w)
	c.ta.SetHeight(h)
}

func (c *Commit) Value() string { return c.ta.Value() }

// Empty reports whether there is no message to commit. git refuses an empty
// one anyway, but it refuses after running the pre-commit hook, which may take
// seconds and have side effects.
func (c *Commit) Empty() bool { return strings.TrimSpace(c.ta.Value()) == "" }

func (c *Commit) Update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	c.ta, cmd = c.ta.Update(msg)
	return cmd
}

func (c *Commit) Title() string {
	if c.amend {
		return "Amend HEAD"
	}
	return "Commit"
}

func (c *Commit) View() string {
	if c.err == nil {
		return c.ta.View()
	}
	// One line, truncated: git's own errors already name the command, and a
	// wrapped one would push the editor out of the pane it was sized for.
	return theme.Err.Render(ansi.Truncate(oneLine(c.err.Error()), c.w, "…")) + "\n" + c.ta.View()
}

// oneLine flattens a multi-line git error so it cannot claim more rows than
// the one resize() reserved for it.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
