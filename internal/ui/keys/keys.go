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

	// The whole-tree writes. StageAll and UnstageAll are the two halves of
	// the index; Discard throws away every unstaged change to a tracked file
	// and Clean deletes every untracked one — the two are kept apart because
	// they lose different things, and each asks before it does.
	StageAll   []string
	UnstageAll []string
	Discard    []string
	Clean      []string

	// Stash puts every change, untracked files included, on the stash;
	// StashTracked leaves the untracked files where they are. StashPop takes
	// the newest stash back in the status view, and the one under the cursor
	// in the stash view, where StashApply and StashDrop join it.
	Stash        []string
	StashTracked []string
	StashPop     []string
	StashApply   []string
	StashDrop    []string

	// Commit opens the message editor; Amend opens it pre-filled with HEAD's
	// message. Two keys rather than a toggle inside the editor: the editor is
	// already a mode, and a mode inside a mode is one too many.
	Commit []string
	Amend  []string

	// Branch checks out the branch under the cursor. It is the branch view's
	// one write, and it is bound nowhere else: in the status and log views
	// there is no branch under a cursor for it to mean.
	Branch []string

	// BranchLog scopes the log view to the branch under the cursor, the way
	// tig's refs view opens the main view on a ref. It is what keeps a
	// branch's history reachable without a third pane: the log view already
	// walks from a set of tips, so pointing it at another branch reuses the
	// pane rather than adding one.
	BranchLog []string

	// BranchJump opens the log on every ref with the cursor on the branch's
	// tip: the branch in its context, where BranchLog shows it alone.
	BranchJump []string

	// The log view's own keys. LogAll flips an unscoped log between every ref
	// and HEAD's history. Search opens the prompt; SearchNext and SearchPrev
	// walk its matches once it is closed. Parent and Child follow the graph
	// down to the first parent and back up to the nearest child.
	LogAll     []string
	Search     []string
	SearchNext []string
	SearchPrev []string
	Parent     []string
	Child      []string

	// Confirm only means anything while the message editor has focus. It is
	// not "enter": enter is a newline in a multi-line message, and a commit
	// message body is the normal case, not the rare one.
	//
	// Cancel closes the editor, and backs a branch-scoped log out to the
	// branch list it was opened from. Both are the same key because both are
	// the same gesture — leaving something that was opened on purpose.
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
	StashView  []string

	// Refresh re-reads the view on screen now rather than at the next tick.
	Refresh []string

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

		StageAll:   []string{"A"},
		UnstageAll: []string{"U"},
		Discard:    []string{"D"},
		Clean:      []string{"X"},

		Stash:        []string{"s"},
		StashTracked: []string{"S"},
		StashPop:     []string{"p"},
		// "a" again: the stash view has no hunks, so the key is free there,
		// and apply is the stash's stage.
		StashApply: []string{"a"},
		StashDrop:  []string{"d"},

		Commit: []string{"c"},
		Amend:  []string{"C"},
		Branch: []string{"b"},

		// "enter", which is free everywhere the branch list has focus: the
		// editor owns it as a newline, but the editor only opens from the
		// status view.
		BranchLog: []string{"enter"},
		// "space" is free in the branch view: nothing there is stageable.
		BranchJump: []string{"space"},

		// "a" again, as the stash view does: the log has no hunks to stage.
		LogAll:     []string{"a"},
		Search:     []string{"/"},
		SearchNext: []string{"n"},
		SearchPrev: []string{"N"},
		Parent:     []string{"["},
		Child:      []string{"]"},

		Confirm: []string{"ctrl+s"},
		Cancel:  []string{"esc"},

		ToggleStaged: []string{"t"},

		StatusView: []string{"1"},
		LogView:    []string{"2"},
		BranchView: []string{"3"},
		StashView:  []string{"4"},

		Refresh: []string{"R"},
		Help:    []string{"?"},
		Quit:    []string{"q", "ctrl+c"},
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

// Binding pairs a keymap field with the text the help popup shows for it.
type Binding struct {
	Keys []string
	Desc string
}

// Bindings returns the keys in the order the help popup lists them, in groups
// of what they act on. The grouping is the popup's layout unit: it breaks
// between groups rather than mid-group, so a column never starts halfway
// through the motions.
//
// It is a hand-written table rather than reflection over Map: field names are
// identifiers, not descriptions, so the text has to be written out either way
// — and keeping it in this file means a new binding and its description land
// in the same diff.
//
// The descriptions are terse on purpose. Three columns of them have to fit an
// 80-column terminal, and the README carries the sentence each one stands for.
func (m Map) Bindings() [][]Binding {
	return [][]Binding{
		{
			{m.Up, "up"},
			{m.Down, "down"},
			{m.Left, "split ←"},
			{m.Right, "split →"},
			{m.Top, "top"},
			{m.Bottom, "bottom"},
			{m.PageUp, "page up"},
			{m.PageDown, "page down"},
		},
		{
			{m.NextPane, "next pane"},
			{m.PrevPane, "prev pane"},
		},
		{
			{m.Stage, "stage"},
			{m.StageHunk, "stage hunk"},
			{m.ToggleStaged, "staged side"},
		},
		{
			{m.StageAll, "stage all"},
			{m.UnstageAll, "unstage all"},
			{m.Discard, "discard changes"},
			{m.Clean, "delete untracked"},
		},
		{
			{m.Stash, "stash all"},
			{m.StashTracked, "stash tracked"},
			{m.StashPop, "pop stash"},
			{m.StashApply, "apply stash"},
			{m.StashDrop, "drop stash"},
		},
		{
			{m.Commit, "commit"},
			{m.Amend, "amend HEAD"},
			{m.Confirm, "confirm (editor)"},
			{m.Cancel, "cancel (editor)"},
		},
		// The log keys ride in the branch group rather than one of their own:
		// the popup has seventeen rows on an 80x24 terminal and a new group
		// costs a blank row, which spills a fourth column off the box. The
		// paired keys are one row each, spelled the way the footer spells the
		// view keys.
		{
			{m.Branch, "checkout branch"},
			{m.BranchLog, "log this branch"},
			{m.BranchJump, "tip in log"},
			{m.Cancel, "branch list"},
			{m.LogAll, "all / HEAD"},
			{m.Search, "search log"},
			{[]string{m.SearchNext[0] + " / " + m.SearchPrev[0]}, "next/prev match"},
			{[]string{m.Parent[0] + " / " + m.Child[0]}, "parent / child"},
		},
		{
			{m.StatusView, "status view"},
			{m.LogView, "log view"},
			{m.BranchView, "branch view"},
			{m.StashView, "stash view"},
		},
		{
			{m.Refresh, "refresh"},
			{m.Help, "this help"},
			{m.Quit, "quit"},
		},
	}
}
