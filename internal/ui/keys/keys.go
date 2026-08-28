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

	Stage  []string
	Commit []string
	Branch []string

	// ToggleStaged switches the diff pane between the worktree change and
	// the staged change.
	ToggleStaged []string

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

		Stage:  []string{" "},
		Commit: []string{"c"},
		Branch: []string{"b"},

		ToggleStaged: []string{"t"},

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
