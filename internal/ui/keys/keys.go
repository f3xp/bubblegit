// Package keys holds the keymap.
//
// The bindings are basic motions plus lazygit-style single-key actions: no
// counts, no visual mode, no operator-pending grammar, so dispatch stays a
// switch rather than an input state machine. The map is a struct so that
// making it user-configurable later is a decoder, not a refactor.
package keys

type Map struct {
	Up       []string
	Down     []string
	Left     []string
	Right    []string
	Top      []string
	Bottom   []string
	PageUp   []string
	PageDown []string

	NextPane []string
	PrevPane []string

	// Stage acts on whatever has focus: a whole file in the list, the line or
	// hunk header under the cursor in the diff. StageHunk takes the whole hunk
	// from anywhere inside it, so staging one does not mean scrolling back up
	// to its header.
	Stage     []string
	StageHunk []string

	// Commit opens the message editor; Amend opens it pre-filled with HEAD's
	// message. Two keys rather than a toggle inside the editor: the editor is
	// already a mode, and a mode inside a mode is one too many.
	Commit []string
	Amend  []string

	// Branch checks out the branch under the cursor. It is the branch view's
	// one write, and it is bound nowhere else: in the status and log views
	// there is no branch under a cursor for it to mean.
	Branch []string

	// Confirm and Cancel only mean anything while the message editor has
	// focus. Confirm is not "enter": enter is a newline in a multi-line
	// message, and a commit message body is the normal case, not the rare one.
	Confirm []string
	Cancel  []string

	// ToggleStaged switches the diff pane between the worktree change and
	// the staged change.
	ToggleStaged []string

	// StatusView, LogView and BranchView switch which pair of panes is on
	// screen. They are views rather than more panes because the layout splits
	// the terminal in two, and a third simultaneous pane on an 80-column
	// terminal is three unreadable slivers. Numbered rather than mnemonic so
	// each new view extends the row instead of re-teaching the ones before it.
	StatusView []string
	LogView    []string
	BranchView []string

	Help []string
	Quit []string
}

func Default() Map {
	return Map{
		Up:       []string{"k", "up"},
		Down:     []string{"j", "down"},
		Left:     []string{"h", "left"},
		Right:    []string{"l", "right"},
		Top:      []string{"g g", "home"},
		Bottom:   []string{"G", "end"},
		PageUp:   []string{"ctrl+u", "pgup"},
		PageDown: []string{"ctrl+d", "pgdown"},

		NextPane: []string{"tab"},
		PrevPane: []string{"shift+tab"},

		// "space", not " ": Key.String() deliberately skips the text form for
		// the space key and falls back to the keystroke name, so a binding of
		// " " matches nothing at all.
		Stage:     []string{"space"},
		StageHunk: []string{"a"},

		Commit: []string{"c"},
		Amend:  []string{"C"},
		Branch: []string{"b"},

		Confirm: []string{"ctrl+s"},
		Cancel:  []string{"esc"},

		ToggleStaged: []string{"t"},

		StatusView: []string{"1"},
		LogView:    []string{"2"},
		BranchView: []string{"3"},

		Help: []string{"?"},
		Quit: []string{"q", "ctrl+c"},
	}
}

// Matches reports whether a key string from tea.KeyPressMsg.String() is bound.
func Matches(binding []string, key string) bool {
	for _, b := range binding {
		if b == key {
			return true
		}
	}
	return false
}
