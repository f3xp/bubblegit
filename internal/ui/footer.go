package ui

import (
	"github.com/charmbracelet/x/ansi"

	"github.com/f3xp/bubblegit/internal/ui/keys"
	"github.com/f3xp/bubblegit/internal/ui/theme"
)

// footer draws the one-row key bar under the panes: the bindings that do
// something from where the cursor is now, not the whole keymap — that is what
// the help popup is for. It changes with the view, the pane that has focus and
// the side of the index the diff is showing, so a key never advertises an
// action the dispatcher would swallow.
//
// The list is written out per state rather than derived from Bindings(): the
// popup's descriptions are written for a table, and the bar wants the shorter,
// situated word — "unstage" while the staged diff is up, not "stage / unstage".
func (m Model) footer() string {
	k := m.keys
	var hints []keys.Binding
	switch {
	case m.confirm.pending():
		return ansi.Truncate(theme.Err.Render(m.confirm.prompt)+"  "+
			theme.Cursor.Render("y")+" "+theme.Dim.Render("yes")+"  "+
			theme.Cursor.Render("any key")+" "+theme.Dim.Render("no"), m.width, "…")
	case m.showHelp:
		return theme.Cursor.Render("any key") + " " + theme.Dim.Render("close help")
	case m.commit.Active():
		hints = []keys.Binding{{Keys: k.Confirm, Desc: "commit"}, {Keys: k.Cancel, Desc: "cancel"}}
	case m.logPrompt:
		hints = []keys.Binding{{Keys: []string{"enter"}, Desc: "jump"}, {Keys: k.Cancel, Desc: "close"}}
	default:
		hints = append(m.viewHints(),
			keys.Binding{Keys: k.NextPane, Desc: "pane"},
			// The view keys as one hint: spelled out they are a third of an
			// 80-column bar, and the help popup names each one.
			keys.Binding{Keys: []string{k.StatusView[0] + "/" + k.LogView[0] + "/" + k.BranchView[0] + "/" + k.StashView[0]}, Desc: "view"},
			keys.Binding{Keys: k.Quit, Desc: "quit"},
		)
	}

	// Whole hints, from the front, as many as fit: the view's own keys come
	// first because they are the ones a user cannot guess, and a hint cut
	// mid-word ("3 bra…") teaches less than its absence. The help key is the
	// exception — it is the way to everything the bar left out, so its room
	// is set aside before the others are dealt in and it goes last.
	help := renderHint(keys.Binding{Keys: k.Help, Desc: "help"})
	room := m.width - ansi.StringWidth(help) - 2
	var bar string
	for _, h := range hints {
		next := renderHint(h)
		if bar != "" {
			next = bar + "  " + next
		}
		if ansi.StringWidth(next) > room {
			break
		}
		bar = next
	}
	if bar == "" {
		return ansi.Truncate(help, m.width, "…")
	}
	return bar + "  " + help
}

func renderHint(b keys.Binding) string {
	return theme.Cursor.Render(bindingKeys(b)) + " " + theme.Dim.Render(b.Desc)
}

// viewHints is the part of the bar that is specific to the view on screen.
// Motions are left out: j/k and the arrows work everywhere, and a bar that
// says so on every screen has spent its width teaching nothing.
func (m Model) viewHints() []keys.Binding {
	k := m.keys
	switch m.view {
	case viewLog:
		if m.logRef != "" {
			return []keys.Binding{{Keys: k.Cancel, Desc: "back to branches"}}
		}
		if m.focus != focusList {
			return nil
		}
		scope := "HEAD only"
		if !m.logAll {
			scope = "all refs"
		}
		return []keys.Binding{
			{Keys: k.Search, Desc: "search"},
			{Keys: k.Parent, Desc: "parent"},
			{Keys: k.Child, Desc: "child"},
			{Keys: k.LogAll, Desc: scope},
		}
	case viewBranches:
		if m.focus != focusList {
			return nil
		}
		return []keys.Binding{{Keys: k.Branch, Desc: "checkout"}, {Keys: k.BranchLog, Desc: "log branch"}, {Keys: k.BranchJump, Desc: "tip in log"}}
	case viewStash:
		if m.focus != focusList {
			return nil
		}
		return []keys.Binding{{Keys: k.StashPop, Desc: "pop"}, {Keys: k.StashApply, Desc: "apply"}, {Keys: k.StashDrop, Desc: "drop"}}
	}

	// In the diff pane space takes the line under the cursor and a the hunk
	// around it; in the file list both take the file, so only one is shown.
	verb, side := "stage", "staged"
	if m.stagedSide() {
		verb, side = "unstage", "worktree"
	}
	hints := []keys.Binding{{Keys: k.Stage, Desc: verb}}
	if m.focus == focusDoc {
		hints = append(hints, keys.Binding{Keys: k.StageHunk, Desc: verb + " hunk"})
	}
	hints = append(hints,
		keys.Binding{Keys: k.ToggleStaged, Desc: side},
		keys.Binding{Keys: k.Commit, Desc: "commit"},
		keys.Binding{Keys: k.Amend, Desc: "amend"},
	)
	if m.focus == focusList {
		hints = append(hints,
			keys.Binding{Keys: k.StageAll, Desc: "stage all"},
			keys.Binding{Keys: k.UnstageAll, Desc: "unstage all"},
			keys.Binding{Keys: k.Discard, Desc: "discard"},
			keys.Binding{Keys: k.Clean, Desc: "delete untracked"},
			keys.Binding{Keys: k.Stash, Desc: "stash"},
			keys.Binding{Keys: k.StashPop, Desc: "pop"},
		)
	}
	return hints
}
