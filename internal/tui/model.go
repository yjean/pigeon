// Package tui is pigeon's terminal UI: an account rail, the thread list on
// top and the open conversation below (slk-style).
package tui

import (
	"context"
	"slices"
	"strconv"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/yoann/pigeon/internal/config"
	"github.com/yoann/pigeon/internal/gmail"
	"github.com/yoann/pigeon/internal/store"
)

type mailbox struct{ name, label string }

var (
	inbox     = mailbox{"Inbox", "INBOX"}
	mailboxes = map[string]mailbox{
		"i": inbox,
		"s": {"Starred", "STARRED"},
		"t": {"Sent", "SENT"},
		"d": {"Drafts", "DRAFT"},
		"a": {"All mail", ""},
	}
)

const (
	syncEvery    = 60 * time.Second
	openDebounce = 40 * time.Millisecond // don't fetch every thread while holding j
)

type focus int

const (
	focusList focus = iota
	focusReader
)

type account struct {
	store    *store.Account
	badge    string
	box      mailbox
	threads  []gmail.Summary
	sel, top int
	loaded   bool
	syncing  map[string]bool      // by label
	hidden   map[string]time.Time // optimistically removed thread IDs
	err      error
	lastSync time.Time
}

func (a *account) selected() *gmail.Summary {
	if a.sel < 0 || a.sel >= len(a.threads) {
		return nil
	}
	return &a.threads[a.sel]
}

func (a *account) unread() int {
	n := 0
	for _, t := range a.threads {
		if t.Unread {
			n++
		}
	}
	return n
}

// Model is the Bubble Tea model.
type Model struct {
	accts []*account
	cur   int
	focus focus

	reader      viewport.Model
	openID      string
	openThread  *gmail.Thread
	openSeq     int
	openErr     error
	threadCache map[string]*gmail.Thread // "email|threadID"

	composer *composer
	lastUndo *undoable

	flash    string // transient status message
	flashErr bool
	flashSeq int

	w, h     int
	pendingG bool
	help     bool
}

type (
	listMsg struct {
		acct    int
		label   string
		threads []gmail.Summary
		err     error
	}
	threadMsg struct {
		acct   int
		id     string
		thread *gmail.Thread
		err    error
	}
	openTick struct{ seq int }
	syncTick struct{}
)

// Run starts the TUI.
func Run(accounts []config.Account) error {
	m := &Model{threadCache: map[string]*gmail.Thread{}, reader: viewport.New()}
	badges := config.Badges(accounts)
	for i, a := range accounts {
		st, err := store.Open(a.Email)
		if err != nil {
			return err
		}
		ac := &account{store: st, badge: badges[i], box: inbox, syncing: map[string]bool{}, hidden: map[string]time.Time{}}
		ac.threads = st.CachedList(ac.box.label) // instant first paint from disk
		ac.loaded = ac.threads != nil
		m.accts = append(m.accts, ac)
	}
	_, err := tea.NewProgram(m).Run()
	return err
}

func (m *Model) acct() *account { return m.accts[m.cur] }

func (m *Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.selectionChanged(), tea.Tick(syncEvery, func(time.Time) tea.Msg { return syncTick{} })}
	for i := range m.accts {
		cmds = append(cmds, m.sync(i))
	}
	return tea.Batch(cmds...)
}

func (m *Model) sync(i int) tea.Cmd {
	a := m.accts[i]
	label := a.box.label
	if a.syncing[label] {
		return nil
	}
	a.syncing[label] = true
	st := a.store
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		list, err := st.SyncList(ctx, label)
		return listMsg{acct: i, label: label, threads: list, err: err}
	}
}

// selectionChanged shows the selected thread in the reader, loading it if needed.
func (m *Model) selectionChanged() tea.Cmd {
	a := m.acct()
	s := a.selected()
	if s == nil {
		m.openID, m.openThread, m.openErr = "", nil, nil
		m.refreshReader(true)
		return nil
	}
	if s.ID == m.openID && m.openThread != nil && m.openThread.HistoryID == s.HistoryID {
		return nil
	}
	if s.ID != m.openID {
		m.openID, m.openThread, m.openErr = s.ID, nil, nil
	}
	if t, ok := m.threadCache[a.store.Email+"|"+s.ID]; ok && t.HistoryID == s.HistoryID {
		m.openThread = t
		m.refreshReader(true)
		return m.prefetch(m.cur, a.sel+1, 2)
	}
	m.refreshReader(true)
	m.openSeq++
	seq := m.openSeq
	return tea.Tick(openDebounce, func(time.Time) tea.Msg { return openTick{seq} })
}

func (m *Model) loadThread() tea.Cmd {
	a := m.acct()
	s := a.selected()
	if s == nil || s.ID != m.openID {
		return nil
	}
	idx, id, hist, st := m.cur, s.ID, s.HistoryID, a.store
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		t, err := st.Thread(ctx, id, hist)
		return threadMsg{acct: idx, id: id, thread: t, err: err}
	}
}

// prefetch loads up to n threads starting at index from into the cache, so
// opening them is instant. Already-cached threads cost one disk read at most.
func (m *Model) prefetch(acctIdx, from, n int) tea.Cmd {
	a := m.accts[acctIdx]
	var cmds []tea.Cmd
	for i := max(from, 0); i < len(a.threads) && i < from+n; i++ {
		s := a.threads[i]
		if t, ok := m.threadCache[a.store.Email+"|"+s.ID]; ok && t.HistoryID == s.HistoryID {
			continue
		}
		id, hist, st := s.ID, s.HistoryID, a.store
		cmds = append(cmds, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			t, err := st.Thread(ctx, id, hist)
			if err != nil {
				return nil // best effort
			}
			return threadMsg{acct: acctIdx, id: id, thread: t}
		})
	}
	return tea.Batch(cmds...)
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.refreshReader(false)
		m.sizeComposer()

	case tea.KeyPressMsg:
		if m.composer != nil {
			return m, m.composeKey(msg)
		}
		return m, m.onKey(msg.String())

	case sentMsg:
		return m, m.onSent(msg)

	case editorMsg:
		return m, m.onEditorDone(msg)

	case clearFlash:
		if msg.seq == m.flashSeq {
			m.flash = ""
		}

	case listMsg:
		a := m.accts[msg.acct]
		delete(a.syncing, msg.label)
		if msg.err != nil {
			a.err = msg.err
			return m, nil
		}
		a.err, a.lastSync = nil, time.Now()
		if msg.label != a.box.label {
			return m, nil // user switched mailbox meanwhile
		}
		var selID string
		if s := a.selected(); s != nil {
			selID = s.ID
		}
		a.threads, a.loaded = a.visible(msg.threads), true
		if i := slices.IndexFunc(a.threads, func(s gmail.Summary) bool { return s.ID == selID }); i >= 0 {
			a.sel = i
		}
		a.sel = clamp(a.sel, 0, len(a.threads)-1)
		prefetch := m.prefetch(msg.acct, 0, 5)
		if msg.acct == m.cur {
			return m, tea.Batch(m.selectionChanged(), prefetch)
		}
		return m, prefetch

	case openTick:
		if msg.seq == m.openSeq {
			return m, m.loadThread()
		}

	case threadMsg:
		if msg.err != nil {
			if msg.acct == m.cur && msg.id == m.openID {
				m.openErr = msg.err
				m.refreshReader(true)
			}
			return m, nil
		}
		m.threadCache[m.accts[msg.acct].store.Email+"|"+msg.id] = msg.thread
		if msg.acct == m.cur && msg.id == m.openID &&
			(m.openThread == nil || m.openThread.HistoryID != msg.thread.HistoryID) { // don't reset scroll on prefetch dupes
			m.openThread = msg.thread
			m.refreshReader(true)
			return m, m.prefetch(m.cur, m.acct().sel+1, 2)
		}

	case actionMsg:
		return m, m.onAction(msg)

	case syncTick:
		cmds := []tea.Cmd{tea.Tick(syncEvery, func(time.Time) tea.Msg { return syncTick{} })}
		for i := range m.accts {
			cmds = append(cmds, m.sync(i))
		}
		return m, tea.Batch(cmds...)

	default:
		if m.composer != nil { // cursor blink, paste, …
			return m, m.composer.update(msg)
		}
	}
	return m, nil
}

func (m *Model) onKey(key string) tea.Cmd {
	a := m.acct()
	if m.help {
		if key == "?" || key == "esc" || key == "q" {
			m.help = false
		}
		return nil
	}
	if m.pendingG {
		m.pendingG = false
		if key == "g" {
			if m.focus == focusReader {
				m.reader.GotoTop()
				return nil
			}
			return m.moveTo(0)
		}
		if box, ok := mailboxes[key]; ok {
			return m.switchMailbox(box)
		}
		return nil
	}

	// Global keys.
	switch key {
	case "ctrl+c":
		return tea.Quit
	case "?":
		m.help = true
		return nil
	case "tab":
		if m.focus == focusList {
			return m.openReader()
		}
		m.focus = focusList
		return nil
	case "[":
		return m.switchAccount((m.cur - 1 + len(m.accts)) % len(m.accts))
	case "]":
		return m.switchAccount((m.cur + 1) % len(m.accts))
	case "ctrl+r":
		return m.sync(m.cur)
	case "c", "r", "a", "f":
		return m.startCompose(key)
	case "e":
		return m.act("archive")
	case "#":
		return m.act("trash")
	case "s":
		return m.act("star")
	case "u":
		return m.act("unread")
	case "z":
		return m.undo()
	case "g":
		m.pendingG = true
		return nil
	case "J":
		return m.moveTo(a.sel + 1)
	case "K":
		return m.moveTo(a.sel - 1)
	case "ctrl+d":
		m.reader.HalfPageDown()
		return nil
	case "ctrl+u":
		m.reader.HalfPageUp()
		return nil
	case "space":
		m.reader.PageDown()
		return nil
	}
	if n, err := strconv.Atoi(key); err == nil && n >= 1 && n <= len(m.accts) {
		return m.switchAccount(n - 1)
	}

	if m.focus == focusReader {
		switch key {
		case "j", "down":
			m.reader.ScrollDown(1)
		case "k", "up":
			m.reader.ScrollUp(1)
		case "G":
			m.reader.GotoBottom()
		case "esc", "h", "q", "left":
			m.focus = focusList
		}
		return nil
	}

	switch key {
	case "q":
		return tea.Quit
	case "j", "down":
		return m.moveTo(a.sel + 1)
	case "k", "up":
		return m.moveTo(a.sel - 1)
	case "G":
		return m.moveTo(len(a.threads) - 1)
	case "enter", "l", "right":
		return m.openReader()
	}
	return nil
}

func (m *Model) moveTo(i int) tea.Cmd {
	a := m.acct()
	a.sel = clamp(i, 0, len(a.threads)-1)
	return m.selectionChanged()
}

// openReader focuses the conversation and marks it read.
func (m *Model) openReader() tea.Cmd {
	a := m.acct()
	s := a.selected()
	if s == nil {
		return nil
	}
	m.focus = focusReader
	if !s.Unread {
		return nil
	}
	s.Unread = false
	id, st := s.ID, a.store
	st.SaveCachedList(a.box.label, a.threads)
	return m.background(m.cur, func(ctx context.Context) error { return st.Modify(ctx, id, nil, []string{"UNREAD"}) })
}

func (m *Model) switchAccount(i int) tea.Cmd {
	if i == m.cur {
		return nil
	}
	m.cur, m.focus = i, focusList
	return tea.Batch(m.selectionChanged(), m.sync(i))
}

func (m *Model) switchMailbox(box mailbox) tea.Cmd {
	a := m.acct()
	if a.box == box {
		return nil
	}
	a.box, a.sel, a.top, m.focus = box, 0, 0, focusList
	a.threads = a.store.CachedList(box.label)
	a.loaded = a.threads != nil
	return tea.Batch(m.selectionChanged(), m.sync(m.cur))
}

func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	return max(lo, min(v, hi))
}
