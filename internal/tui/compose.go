package tui

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/yoann/pigeon/internal/gmail"
)

type composeField int

const (
	fTo composeField = iota
	fCc
	fSubject
	fBody
	nFields
)

// composer is the compose/reply/forward form shown in the bottom pane.
type composer struct {
	title   string
	acct    int
	base    gmail.Outgoing // threading headers + initial values
	to, cc  textinput.Model
	subject textinput.Model
	body    textarea.Model
	field   composeField
	note    string // shown under the title, e.g. forward limitations
	confirm bool   // esc pressed on a modified message: save / discard?
	busy    string // "Sending…"
	err     error
}

type (
	sentMsg struct {
		acct  int
		draft bool
		err   error
	}
	editorMsg struct {
		path string
		err  error
	}
	clearFlash struct{ seq int }
)

func newComposer(title string, acct int, o gmail.Outgoing, field composeField) (*composer, tea.Cmd) {
	input := func(v, placeholder string) textinput.Model {
		ti := textinput.New()
		ti.Prompt = ""
		ti.Placeholder = placeholder
		ti.CharLimit = 0
		st := textinput.DefaultDarkStyles()
		st.Focused.Text, st.Blurred.Text = sBase, sMuted
		st.Focused.Placeholder, st.Blurred.Placeholder = sDim, sDim
		ti.SetStyles(st)
		ti.SetValue(v)
		return ti
	}
	c := &composer{
		title:   title,
		acct:    acct,
		base:    o,
		to:      input(o.To, "name@example.com, …"),
		cc:      input(o.Cc, ""),
		subject: input(o.Subject, "Subject"),
		body:    textarea.New(),
	}
	c.body.ShowLineNumbers = false
	c.body.Prompt = " "
	c.body.CharLimit = 0
	c.body.MaxHeight = 0
	c.body.Placeholder = "Write your message…"
	st := textarea.DefaultDarkStyles()
	st.Focused.CursorLine = lipgloss.NewStyle()
	st.Focused.Text, st.Blurred.Text = sBase, sMuted
	st.Focused.Placeholder, st.Blurred.Placeholder = sDim, sDim
	c.body.SetStyles(st)
	c.body.SetValue(o.Body)
	c.body.MoveToBegin()
	return c, c.focus(field)
}

func (c *composer) focus(f composeField) tea.Cmd {
	c.field = f
	c.to.Blur()
	c.cc.Blur()
	c.subject.Blur()
	c.body.Blur()
	switch f {
	case fTo:
		return c.to.Focus()
	case fCc:
		return c.cc.Focus()
	case fSubject:
		return c.subject.Focus()
	default:
		return c.body.Focus()
	}
}

func (c *composer) update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	switch c.field {
	case fTo:
		c.to, cmd = c.to.Update(msg)
	case fCc:
		c.cc, cmd = c.cc.Update(msg)
	case fSubject:
		c.subject, cmd = c.subject.Update(msg)
	default:
		c.body, cmd = c.body.Update(msg)
	}
	return cmd
}

func (c *composer) outgoing() gmail.Outgoing {
	o := c.base
	o.To, o.Cc, o.Subject, o.Body = c.to.Value(), c.cc.Value(), c.subject.Value(), c.body.Value()
	return o
}

func (c *composer) modified() bool {
	o := c.outgoing()
	return o.To != c.base.To || o.Cc != c.base.Cc || o.Subject != c.base.Subject || o.Body != c.base.Body
}

// setSize fits the inputs into a pane of inner size w×h.
func (c *composer) setSize(w, h int) {
	for _, ti := range []*textinput.Model{&c.to, &c.cc, &c.subject} {
		ti.SetWidth(max(w-12, 10))
	}
	c.body.SetWidth(max(w-1, 10))
	c.body.SetHeight(max(h-composeChrome, 1))
}

// composeChrome is the number of non-body lines in the compose pane.
const composeChrome = 7 // title, note, to, cc, subject, rule, footer

// --- Model integration ---

// startCompose opens the composer: kind is one of "c" (new), "r", "a" (reply all), "f".
func (m *Model) startCompose(kind string) tea.Cmd {
	a := m.acct()
	me := gmail.Address{Email: a.store.Email}
	if kind == "c" {
		c, cmd := newComposer("New message", m.cur, gmail.Outgoing{}, fTo)
		return m.openComposer(c, cmd)
	}
	s := a.selected()
	if s == nil {
		return nil
	}
	if m.openThread == nil || m.openID != s.ID || len(m.openThread.Messages) == 0 {
		return m.flashMsg("Conversation still loading…", true)
	}
	t := m.openThread
	var c *composer
	var cmd tea.Cmd
	switch kind {
	case "r":
		c, cmd = newComposer("Reply", m.cur, gmail.Reply(t, me, false), fBody)
	case "a":
		c, cmd = newComposer("Reply all", m.cur, gmail.Reply(t, me, true), fBody)
	case "f":
		c, cmd = newComposer("Forward", m.cur, gmail.Forward(t, me), fTo)
		if len(t.Messages[len(t.Messages)-1].Attachments()) > 0 {
			c.note = "⚠ attachments are not forwarded yet"
		}
	}
	return m.openComposer(c, cmd)
}

func (m *Model) openComposer(c *composer, cmd tea.Cmd) tea.Cmd {
	m.composer, m.help = c, false
	m.sizeComposer()
	return cmd
}

func (m *Model) sizeComposer() {
	if m.composer == nil || m.w == 0 {
		return
	}
	mw, _, readerH := m.layout()
	m.composer.setSize(mw-2, readerH-2)
}

func (m *Model) composeKey(msg tea.KeyPressMsg) tea.Cmd {
	c := m.composer
	if c.busy != "" {
		return nil
	}
	key := msg.String()
	if c.confirm {
		switch key {
		case "s":
			return m.deliver(true)
		case "d":
			m.composer = nil
			return m.flashMsg("Message discarded", false)
		case "esc":
			c.confirm = false
		}
		return nil
	}
	switch key {
	case "ctrl+enter", "ctrl+s": // ctrl+enter needs a terminal with the kitty keyboard protocol (Ghostty, kitty, WezTerm…)
		return m.deliver(false)
	case "esc", "ctrl+c":
		if !c.modified() {
			m.composer = nil
			return nil
		}
		c.confirm = true
		return nil
	case "tab":
		return c.focus((c.field + 1) % nFields)
	case "shift+tab":
		return c.focus((c.field + nFields - 1) % nFields)
	case "enter":
		if c.field != fBody {
			return c.focus(c.field + 1)
		}
	case "ctrl+e":
		return m.editInEditor()
	}
	c.err = nil
	return c.update(msg)
}

// deliver sends the message, or saves it as a draft.
func (m *Model) deliver(draft bool) tea.Cmd {
	c := m.composer
	o := c.outgoing()
	if !draft {
		probe := o
		probe.From = gmail.Address{Email: m.accts[c.acct].store.Email}
		if _, err := probe.Raw(); err != nil {
			c.err = err
			return nil
		}
		c.busy = "Sending…"
	} else {
		c.busy = "Saving draft…"
	}
	c.confirm, c.err = false, nil
	idx, st := c.acct, m.accts[c.acct].store
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		var err error
		if draft {
			err = st.SaveDraft(ctx, o)
		} else {
			err = st.Send(ctx, o)
		}
		return sentMsg{acct: idx, draft: draft, err: err}
	}
}

// editInEditor opens the body in $VISUAL/$EDITOR, then loads it back.
func (m *Model) editInEditor() tea.Cmd {
	f, err := os.CreateTemp("", "pigeon-*.eml")
	if err != nil {
		m.composer.err = err
		return nil
	}
	_, err = f.WriteString(m.composer.body.Value())
	f.Close()
	if err != nil {
		m.composer.err = err
		return nil
	}
	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	if editor == "" {
		editor = "vi"
	}
	path := f.Name()
	// Through sh so EDITOR may hold flags or a ~ path.
	cmd := exec.Command("sh", "-c", editor+" '"+strings.ReplaceAll(path, "'", `'\''`)+"'")
	return tea.ExecProcess(cmd, func(err error) tea.Msg { return editorMsg{path: path, err: err} })
}

func (m *Model) onEditorDone(msg editorMsg) tea.Cmd {
	defer os.Remove(msg.path)
	c := m.composer
	if c == nil {
		return nil
	}
	if msg.err != nil {
		c.err = msg.err
		return nil
	}
	data, err := os.ReadFile(msg.path)
	if err != nil {
		c.err = err
		return nil
	}
	c.body.SetValue(strings.TrimRight(string(data), "\n"))
	c.body.MoveToBegin()
	return c.focus(fBody)
}

func (m *Model) onSent(msg sentMsg) tea.Cmd {
	c := m.composer
	if msg.err != nil {
		if c != nil {
			c.busy, c.err = "", msg.err
		}
		return nil
	}
	m.composer = nil
	text := "✓ Sent"
	if msg.draft {
		text = "✓ Draft saved"
	}
	return tea.Batch(m.flashMsg(text, false), m.sync(msg.acct))
}

func (m *Model) flashMsg(text string, isErr bool) tea.Cmd {
	m.flash, m.flashErr = text, isErr
	m.flashSeq++
	seq := m.flashSeq
	return tea.Tick(4*time.Second, func(time.Time) tea.Msg { return clearFlash{seq} })
}

// --- rendering ---

func (m *Model) renderComposer(w, h int) string {
	c := m.composer
	iw := w - 2
	label := func(f composeField, name string) string {
		st := sDim
		if c.field == f {
			st = lipgloss.NewStyle().Foreground(cYellow).Bold(true)
		}
		return " " + st.Render(padRight(name, 9)) + " "
	}
	title := " " + lipgloss.NewStyle().Foreground(cYellow).Bold(true).Render("✎ "+c.title) +
		sDim.Render("  ·  from "+m.accts[c.acct].store.Email)
	lines := []string{
		title,
		" " + lipgloss.NewStyle().Foreground(cYellow).Render(c.note),
		label(fTo, "To") + c.to.View(),
		label(fCc, "Cc") + c.cc.View(),
		label(fSubject, "Subject") + c.subject.View(),
		sDim.Render(" " + strings.Repeat("─", iw-2)),
	}
	lines = append(lines, strings.Split(c.body.View(), "\n")...)
	for len(lines) < h-3 {
		lines = append(lines, "")
	}
	lines = lines[:h-3]

	var footer string
	switch {
	case c.busy != "":
		footer = lipgloss.NewStyle().Foreground(cYellow).Render(" ⟳ " + c.busy)
	case c.confirm:
		footer = lipgloss.NewStyle().Foreground(cYellow).Bold(true).Render(" Close this message? ") +
			sBase.Render("s") + sDim.Render(" save draft  ") + sBase.Render("d") + sDim.Render(" discard  ") + sBase.Render("esc") + sDim.Render(" keep editing")
	case c.err != nil:
		footer = lipgloss.NewStyle().Foreground(cRed).Render(" ✗ " + c.err.Error())
	default:
		footer = sDim.Render(" ctrl+enter send · tab next field · ctrl+e edit in $EDITOR · esc close")
	}
	lines = append(lines, footer)

	border := cYellow
	return paneColor(lines, w, h, border)
}
