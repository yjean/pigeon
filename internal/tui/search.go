package tui

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
)

// startSearch opens the "/" prompt in the status bar, prefilled with the
// last query so it can be refined.
func (m *Model) startSearch() tea.Cmd {
	ti := textinput.New()
	ti.Prompt = ""
	ti.CharLimit = 0
	ti.Placeholder = "from:julian has:attachment after:2026/09/01 …"
	st := textinput.DefaultDarkStyles()
	st.Focused.Text, st.Focused.Placeholder = sBase, sFaint
	st.Cursor.Color = cCursor
	ti.SetStyles(st)
	ti.SetWidth(max(m.w-14, 10))
	ti.SetValue(m.lastQuery)
	ti.CursorEnd()
	m.search = &ti
	return ti.Focus()
}

func (m *Model) searchKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc", "ctrl+c":
		m.search = nil
		return nil
	case "enter":
		q := strings.TrimSpace(m.search.Value())
		m.search = nil
		if q == "" {
			return nil
		}
		return m.runSearch(q)
	}
	var cmd tea.Cmd
	*m.search, cmd = m.search.Update(msg)
	return cmd
}

// runSearch shows the Gmail search results as a mailbox of the current account.
func (m *Model) runSearch(q string) tea.Cmd {
	m.lastQuery = q
	a := m.acct()
	if !a.box.search {
		a.prevBox = a.box
	}
	a.box = mailbox{name: "Search", label: "q:" + q, search: true}
	return m.reloadView()
}

// exitSearch returns to the mailbox shown before searching.
func (m *Model) exitSearch() tea.Cmd {
	a := m.acct()
	a.box = a.prevBox
	if a.box.name == "" {
		a.box = inbox
	}
	return m.reloadView()
}

// query is the search text of a search mailbox.
func (b mailbox) query() string { return strings.TrimPrefix(b.label, "q:") }
