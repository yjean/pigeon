package tui

import (
	"context"
	"slices"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/yoann/pigeon/internal/gmail"
)

// hideFor keeps an archived/trashed thread out of lists returned by a sync
// that raced with the action.
const hideFor = 15 * time.Second

type actionMsg struct {
	acct int
	err  error
}

// undoable is the last list-removing action, reversible with z.
type undoable struct {
	acct    int
	view    string
	index   int
	summary gmail.Summary
	reverse func(context.Context) error
}

// act applies a thread action optimistically, then calls Gmail in the background.
// kind: "archive", "trash", "star", "unread".
func (m *Model) act(kind string) tea.Cmd {
	a := m.acct()
	s := a.selected()
	if s == nil {
		return nil
	}
	st, id, label, view := a.store, s.ID, a.box.label, a.view()
	modify := func(add, remove []string) func(context.Context) error {
		return func(ctx context.Context) error { return st.Modify(ctx, id, add, remove) }
	}

	var call, reverse func(context.Context) error
	var text string
	removeRow := false
	switch kind {
	case "archive":
		call, reverse = modify(nil, []string{"INBOX"}), modify([]string{"INBOX"}, nil)
		text, removeRow = "Archived · z to undo", label == "INBOX"
	case "trash":
		// Gmail strips INBOX on trash; remember it so undo puts the thread back.
		// The default covers an undo pressed before the trash call returns.
		var mu sync.Mutex
		wasInbox := label == "INBOX"
		call = func(ctx context.Context) error {
			was, err := st.Trash(ctx, id)
			mu.Lock()
			wasInbox = wasInbox || was
			mu.Unlock()
			return err
		}
		reverse = func(ctx context.Context) error {
			mu.Lock()
			toInbox := wasInbox
			mu.Unlock()
			return st.Untrash(ctx, id, toInbox)
		}
		text, removeRow = "Moved to trash · z to undo", true
	case "star":
		if s.Starred {
			call, reverse = modify(nil, []string{"STARRED"}), modify([]string{"STARRED"}, nil)
			text, removeRow = "Unstarred", label == "STARRED"
		} else {
			call = modify([]string{"STARRED"}, nil)
			text = "★ Starred"
		}
		s.Starred = !s.Starred
	case "unread":
		if s.Unread {
			call, text = modify(nil, []string{"UNREAD"}), "Marked read"
			a.keepRead(id)
		} else {
			lastID := s.LastID
			call = func(ctx context.Context) error { return st.MarkUnread(ctx, id, lastID) }
			text = "Marked unread"
			m.focus = focusList // reading it would mark it read again
		}
		a.setUnread(s, !s.Unread)
	default:
		return nil
	}

	cmds := []tea.Cmd{m.flashMsg(text, false)}
	if removeRow {
		m.lastUndo = &undoable{acct: m.cur, view: view, index: a.sel, summary: *s, reverse: reverse}
		a.hidden[id] = time.Now()
		a.threads = slices.Delete(a.threads, a.sel, a.sel+1)
		a.sel = clamp(a.sel, 0, len(a.threads)-1)
		m.focus = focusList
		cmds = append(cmds, m.selectionChanged())
	}
	st.SaveCachedList(view, a.threads)
	cmds = append(cmds, m.background(m.cur, call))
	return tea.Batch(cmds...)
}

// undo reverses the last archive/trash/unstar-removal.
func (m *Model) undo() tea.Cmd {
	u := m.lastUndo
	if u == nil {
		return m.flashMsg("Nothing to undo", true)
	}
	m.lastUndo = nil
	a := m.accts[u.acct]
	delete(a.hidden, u.summary.ID)
	cmds := []tea.Cmd{m.flashMsg("Undone", false), m.background(u.acct, u.reverse)}
	if a.view() == u.view && !slices.ContainsFunc(a.threads, func(s gmail.Summary) bool { return s.ID == u.summary.ID }) {
		i := clamp(u.index, 0, len(a.threads))
		a.threads = slices.Insert(a.threads, i, u.summary)
		a.store.SaveCachedList(u.view, a.threads)
		if u.acct == m.cur {
			a.sel = i
			cmds = append(cmds, m.selectionChanged())
		}
	}
	return tea.Batch(cmds...)
}

// background runs a Gmail call; failures are flashed and trigger a resync.
func (m *Model) background(acct int, call func(context.Context) error) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return actionMsg{acct: acct, err: call(ctx)}
	}
}

func (m *Model) onAction(msg actionMsg) tea.Cmd {
	if msg.err == nil {
		return nil
	}
	return tea.Batch(m.flashMsg("✗ "+msg.err.Error(), true), m.sync(msg.acct))
}

// visible drops threads hidden by a recent optimistic removal.
func (a *account) visible(list []gmail.Summary) []gmail.Summary {
	for id, at := range a.hidden {
		if time.Since(at) > hideFor {
			delete(a.hidden, id)
		}
	}
	if len(a.hidden) == 0 {
		return list
	}
	return slices.DeleteFunc(slices.Clone(list), func(s gmail.Summary) bool { _, ok := a.hidden[s.ID]; return ok })
}
