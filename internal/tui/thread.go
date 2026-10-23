package tui

import (
	"github.com/yoann/pigeon/internal/gmail"
)

// Reading a conversation: one message has the focus (j/k in the reader) and
// r a f act on it; each message is expanded or collapsed to one line (enter).

// span is the range of content lines [from, to) a message takes in the reader.
type span struct{ from, to int }

// resetMessages applies the defaults to messages not seen yet: the newest and
// the unread ones open, focus on the newest. Reloading the same thread (a new
// message, a label change) keeps what the user opened and focused.
func (m *Model) resetMessages() {
	t := m.openThread
	if t == nil || len(t.Messages) == 0 {
		return
	}
	if t.ID != m.msgThread {
		m.msgThread, m.msgSel = t.ID, ""
		m.expanded, m.known = map[string]bool{}, map[string]bool{}
	}
	newest := t.Messages[len(t.Messages)-1]
	for _, msg := range t.Messages {
		if !m.known[msg.ID] {
			m.known[msg.ID] = true
			if msg.ID == newest.ID || msg.HasLabel("UNREAD") {
				m.expanded[msg.ID] = true
			}
		}
	}
	if m.msgIndex() < 0 {
		m.msgSel = newest.ID
	}
}

// msgIndex is the focused message's index in the open thread, -1 if none.
func (m *Model) msgIndex() int {
	if m.openThread == nil {
		return -1
	}
	for i, msg := range m.openThread.Messages {
		if msg.ID == m.msgSel {
			return i
		}
	}
	return -1
}

// focusedMessage is the message r a f act on: the focused one, else the newest.
func (m *Model) focusedMessage() gmail.Message {
	t := m.openThread
	if i := m.msgIndex(); i >= 0 {
		return t.Messages[i]
	}
	return t.Messages[len(t.Messages)-1]
}

// isOpen reports whether message i is shown in full. A lone message always is.
func (m *Model) isOpen(i int) bool {
	t := m.openThread
	return len(t.Messages) == 1 || m.expanded[t.Messages[i].ID]
}

// moveMsg moves the focus by d messages down the reader (towards older ones).
func (m *Model) moveMsg(d int) {
	i := m.msgIndex()
	if i < 0 {
		return
	}
	i = clamp(i-d, 0, len(m.openThread.Messages)-1) // newest is shown first
	m.msgSel = m.openThread.Messages[i].ID
	m.scrollToMsg()
}

// focusEnd focuses the newest (top) or oldest (bottom) message.
func (m *Model) focusEnd(bottom bool) {
	t := m.openThread
	if t == nil || len(t.Messages) == 0 {
		return
	}
	if bottom {
		m.msgSel = t.Messages[0].ID
		m.reader.GotoBottom()
	} else {
		m.msgSel = t.Messages[len(t.Messages)-1].ID
		m.reader.GotoTop()
	}
}

// toggleMsg expands or collapses the focused message.
func (m *Model) toggleMsg() {
	if m.msgIndex() < 0 || len(m.openThread.Messages) == 1 {
		return
	}
	m.expanded[m.msgSel] = !m.expanded[m.msgSel]
	m.refreshReader(false)
	m.scrollToMsg()
}

// toggleAllMsgs expands every message, or if all are, collapses all but the focused one.
func (m *Model) toggleAllMsgs() {
	t := m.openThread
	if m.msgIndex() < 0 || len(t.Messages) == 1 {
		return
	}
	all := true
	for _, msg := range t.Messages {
		all = all && m.expanded[msg.ID]
	}
	for _, msg := range t.Messages {
		m.expanded[msg.ID] = !all || msg.ID == m.msgSel
	}
	m.refreshReader(false)
	m.scrollToMsg()
}

// scrollToMsg scrolls the reader so the focused message shows: all of it if
// it fits, else from its header.
func (m *Model) scrollToMsg() {
	i := m.msgIndex()
	if i < 0 || i >= len(m.msgSpans) {
		return
	}
	s, off, h := m.msgSpans[i], m.reader.YOffset(), m.reader.Height()
	switch {
	case s.from < off:
		m.reader.SetYOffset(s.from)
	case s.to > off+h:
		m.reader.SetYOffset(min(s.from, s.to-h))
	}
}

// followScroll moves the focus to the top visible message when scrolling
// (space, ctrl+d/u) took the focused one out of view.
func (m *Model) followScroll() {
	i := m.msgIndex()
	if i < 0 || i >= len(m.msgSpans) {
		return
	}
	off, h := m.reader.YOffset(), m.reader.Height()
	if s := m.msgSpans[i]; s.to > off && s.from < off+h {
		return
	}
	for j := len(m.msgSpans) - 1; j >= 0; j-- { // top to bottom
		if s := m.msgSpans[j]; s.to > off {
			m.msgSel = m.openThread.Messages[j].ID
			return
		}
	}
}
