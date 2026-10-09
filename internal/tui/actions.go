package tui

import (
	"context"
	"fmt"
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
	acct  int
	view  string
	items []undoItem // in list order
}

type undoItem struct {
	index   int
	summary gmail.Summary
	reverse func(context.Context) error
}

// targets are the indices the next action applies to: the marked threads
// (x), in list order, or else the selected one.
func (a *account) targets() []int {
	var out []int
	for i, s := range a.threads {
		if a.marked[s.ID] {
			out = append(out, i)
		}
	}
	if len(out) == 0 && a.selected() != nil {
		out = []int{a.sel}
	}
	return out
}

// markedCount is how many listed threads are marked (marks of threads gone
// from the list don't count).
func (a *account) markedCount() int {
	n := 0
	for _, s := range a.threads {
		if a.marked[s.ID] {
			n++
		}
	}
	return n
}

// toggleMark marks or unmarks the selected thread for a batch action.
func (m *Model) toggleMark() tea.Cmd {
	a := m.acct()
	s := a.selected()
	if s == nil {
		return nil
	}
	if a.marked[s.ID] {
		delete(a.marked, s.ID)
	} else {
		a.marked[s.ID] = true
	}
	return nil
}

// act applies a thread action optimistically to the targets, then calls Gmail
// in the background. kind: "archive", "trash", "star", "unread".
// On several threads, toggles go one way like Gmail: star all unless all are
// starred, mark all read unless all are read.
func (m *Model) act(kind string) tea.Cmd {
	a := m.acct()
	idx := a.targets()
	if len(idx) == 0 {
		return nil
	}
	star, unread := false, true
	for _, i := range idx {
		star = star || !a.threads[i].Starred
		unread = unread && !a.threads[i].Unread
	}
	st, label, view := a.store, a.box.label, a.view()

	var calls []func(context.Context) error
	var undo []undoItem
	var text string
	for _, i := range idx {
		s := &a.threads[i]
		id := s.ID
		modify := func(add, remove []string) func(context.Context) error {
			return func(ctx context.Context) error { return st.Modify(ctx, id, add, remove) }
		}
		var call, reverse func(context.Context) error
		removeRow := false
		switch kind {
		case "archive":
			call, reverse = modify(nil, []string{"INBOX"}), modify([]string{"INBOX"}, nil)
			text, removeRow = "Archived", label == "INBOX"
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
			text, removeRow = "Moved to trash", true
		case "star":
			if star {
				text = "★ Starred"
				if !s.Starred {
					call = modify([]string{"STARRED"}, nil)
				}
			} else {
				call, reverse = modify(nil, []string{"STARRED"}), modify([]string{"STARRED"}, nil)
				text, removeRow = "Unstarred", label == "STARRED"
			}
			s.Starred = star
		case "unread":
			if !unread {
				text = "Marked read"
				if s.Unread {
					call = modify(nil, []string{"UNREAD"})
				}
				a.keepRead(id)
			} else {
				lastID := s.LastID
				call = func(ctx context.Context) error { return st.MarkUnread(ctx, id, lastID) }
				text = "Marked unread"
				m.focus = focusList // reading it would mark it read again
			}
			a.setUnread(s, unread)
		default:
			return nil
		}
		if call != nil {
			calls = append(calls, call)
		}
		if removeRow {
			undo = append(undo, undoItem{index: i, summary: *s, reverse: reverse})
		}
	}

	if n := len(idx); n > 1 {
		text += fmt.Sprintf(" · %d conversations", n)
	}
	if len(undo) > 0 {
		text += " · z to undo"
		m.lastUndo = &undoable{acct: m.cur, view: view, items: undo}
		for j := len(undo) - 1; j >= 0; j-- {
			a.hidden[undo[j].summary.ID] = time.Now()
			a.threads = slices.Delete(a.threads, undo[j].index, undo[j].index+1)
		}
		a.sel = clamp(undo[0].index, 0, len(a.threads)-1)
		m.focus = focusList
	}
	clear(a.marked)
	st.SaveCachedList(view, a.threads)
	cmds := []tea.Cmd{m.flashMsg(text, false), m.background(m.cur, all(calls))}
	if len(undo) > 0 {
		cmds = append(cmds, m.selectionChanged())
	}
	return tea.Batch(cmds...)
}

// all runs Gmail calls one after the other, returning the first error.
func all(calls []func(context.Context) error) func(context.Context) error {
	return func(ctx context.Context) error {
		var first error
		for _, call := range calls {
			if err := call(ctx); err != nil && first == nil {
				first = err
			}
		}
		return first
	}
}

// undo reverses the last archive/trash/unstar-removal.
func (m *Model) undo() tea.Cmd {
	u := m.lastUndo
	if u == nil {
		return m.flashMsg("Nothing to undo", true)
	}
	m.lastUndo = nil
	a := m.accts[u.acct]
	var reverse []func(context.Context) error
	restored := -1
	for _, it := range u.items {
		delete(a.hidden, it.summary.ID)
		reverse = append(reverse, it.reverse)
		if a.view() == u.view && !slices.ContainsFunc(a.threads, func(s gmail.Summary) bool { return s.ID == it.summary.ID }) {
			i := clamp(it.index, 0, len(a.threads))
			a.threads = slices.Insert(a.threads, i, it.summary)
			if restored < 0 {
				restored = i
			}
		}
	}
	cmds := []tea.Cmd{m.flashMsg("Undone", false), m.background(u.acct, all(reverse))}
	if restored >= 0 {
		a.store.SaveCachedList(u.view, a.threads)
		if u.acct == m.cur {
			a.sel = restored
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
