package ui

// Test-only accessors. They keep the assertions in app_test.go readable
// without widening the package's real API.

func (m Model) SelectedPath() string {
	f, ok := m.files.Selected()
	if !ok {
		return ""
	}
	return f.Path
}

// DiffFocused reports whether the document pane — the diff, or the commit in
// the log and branch views — has focus rather than the list.
func (m Model) DiffFocused() bool { return m.focus == focusDoc }

func (m Model) InLogView() bool { return m.view == viewLog }

func (m Model) SelectedCommit() string { return m.log.SelectedSHA() }
