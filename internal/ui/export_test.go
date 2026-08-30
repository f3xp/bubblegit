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

func (m Model) DiffFocused() bool { return m.focus == focusRight }

func (m Model) InLogView() bool { return m.view == viewLog }

func (m Model) SelectedCommit() string { return m.log.SelectedSHA() }
