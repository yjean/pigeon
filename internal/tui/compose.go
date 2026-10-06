package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"charm.land/bubbles/v2/filepicker"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/yoann/pigeon/internal/config"
	"github.com/yoann/pigeon/internal/gmail"
	"github.com/yoann/pigeon/internal/store"
)

// suggestionLines renders the autocomplete dropdown under field f.
func (c *composer) suggestionLines(f composeField, w int) []string {
	if c.field != f || len(c.sugg) == 0 {
		return nil
	}
	out := make([]string, len(c.sugg))
	for i, s := range c.sugg {
		name, email := s.Name, s.Email
		if name == "" {
			name, email = s.Email, ""
		}
		if i == c.suggIdx {
			sel := lipgloss.NewStyle().Background(cSelBg)
			line := sel.Foreground(cSelBar).Render("▸ ") + sel.Foreground(text).Bold(true).Render(name) +
				sel.Foreground(overlay1).Render("  "+email+" ")
			out[i] = strings.Repeat(" ", 11) + line
		} else {
			out[i] = strings.Repeat(" ", 13) + sBase.Render(name) + sDim.Render("  "+email)
		}
		out[i] = fit(out[i], w)
	}
	return out
}

type composeField int

const (
	fTo composeField = iota
	fCc
	fSubject
	fAttach
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

	// Address autocomplete for To/Cc.
	suggest func(typed string, exclude []string, n int) []store.Contact
	sugg    []store.Contact
	suggIdx int

	// Attachments.
	atts     []composeAtt
	attSel   int
	initAtts int               // attachments at open (forward), to detect changes
	browse   *filepicker.Model // file picker shown in place of the body
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
		st.Focused.Placeholder, st.Blurred.Placeholder = sFaint, sFaint
		st.Cursor.Color = cCursor
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
	st.Focused.Placeholder, st.Blurred.Placeholder = sFaint, sFaint
	st.Cursor.Color = cCursor
	c.body.SetStyles(st)
	c.body.SetValue(o.Body)
	c.body.MoveToBegin()
	return c, c.focus(field)
}

func (c *composer) focus(f composeField) tea.Cmd {
	c.field, c.sugg = f, nil
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
	if c.browse != nil {
		return c.browseUpdate(msg)
	}
	if p, ok := msg.(tea.PasteMsg); ok {
		if paths := droppedPaths(p.Content); len(paths) > 0 { // files dropped on the window
			for _, path := range paths {
				if err := c.addPath(path); err != nil {
					c.err = err
				}
			}
			return nil
		}
	}
	var cmd tea.Cmd
	switch c.field {
	case fAttach:
		return nil
	case fTo, fCc:
		// Only re-suggest when the text changed: cursor blinks also pass here
		// and must not reset the highlighted suggestion.
		ti := c.addressInput()
		before := ti.Value()
		*ti, cmd = ti.Update(msg)
		if ti.Value() != before {
			c.refreshSuggestions()
		}
	case fSubject:
		c.subject, cmd = c.subject.Update(msg)
	default:
		c.body, cmd = c.body.Update(msg)
	}
	return cmd
}

// addressInput is the focused To/Cc input, or nil.
func (c *composer) addressInput() *textinput.Model {
	switch c.field {
	case fTo:
		return &c.to
	case fCc:
		return &c.cc
	}
	return nil
}

// splitLast splits "a@x, Bob <b@y>, jul" into the complete part and the token being typed.
func splitLast(v string) (done, typed string) {
	if i := strings.LastIndexAny(v, ",;"); i >= 0 {
		return v[:i+1], v[i+1:]
	}
	return "", v
}

func (c *composer) refreshSuggestions() {
	ti := c.addressInput()
	c.sugg, c.suggIdx = nil, 0
	if ti == nil || c.suggest == nil {
		return
	}
	done, typed := splitLast(ti.Value())
	var exclude []string
	for _, a := range append(gmail.ParseAddressList(done), gmail.ParseAddressList(c.to.Value()+","+c.cc.Value())...) {
		exclude = append(exclude, a.Email)
	}
	c.sugg = c.suggest(typed, exclude, 5)
}

// acceptSuggestion replaces the token being typed with the chosen address.
func (c *composer) acceptSuggestion() {
	ti := c.addressInput()
	done, _ := splitLast(ti.Value())
	if done = strings.TrimSpace(done); done != "" {
		done += " "
	}
	ti.SetValue(done + c.sugg[c.suggIdx].Address().Format() + ", ")
	ti.CursorEnd()
	c.sugg = nil
}

func (c *composer) outgoing() gmail.Outgoing {
	o := c.base
	o.To, o.Cc, o.Subject, o.Body = c.to.Value(), c.cc.Value(), c.subject.Value(), c.body.Value()
	return o
}

func (c *composer) modified() bool {
	o := c.outgoing()
	return o.To != c.base.To || o.Cc != c.base.Cc || o.Subject != c.base.Subject || o.Body != c.base.Body ||
		len(c.atts) != c.initAtts
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
const composeChrome = 8 // title, note, to, cc, subject, attach, rule, footer

// --- Model integration ---

// startCompose opens the composer: kind is one of "c" (new), "r", "a" (reply all), "f".
func (m *Model) startCompose(kind string) tea.Cmd {
	a := m.acct()
	me := gmail.Address{Email: a.store.Email}
	sig := config.Signature(a.store.Email)
	if kind == "c" {
		c, cmd := newComposer("New message", m.cur, gmail.Outgoing{Body: withSignature("", sig)}, fTo)
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
	sign := func(o gmail.Outgoing) gmail.Outgoing { o.Body = withSignature(o.Body, sig); return o }
	switch kind {
	case "r":
		c, cmd = newComposer("Reply", m.cur, sign(gmail.Reply(t, me, false)), fBody)
	case "a":
		c, cmd = newComposer("Reply all", m.cur, sign(gmail.Reply(t, me, true)), fBody)
	case "f":
		c, cmd = newComposer("Forward", m.cur, sign(gmail.Forward(t, me)), fTo)
		for _, att := range t.Messages[len(t.Messages)-1].AttachmentParts() {
			if !att.Inline {
				c.atts = append(c.atts, composeAtt{name: att.Filename, size: att.Size, remote: &att})
			}
		}
		c.initAtts = len(c.atts)
	}
	return m.openComposer(c, cmd)
}

// withSignature puts the signature under the text being written, above any
// quoted or forwarded message (body starts with the blank lines to write in).
func withSignature(body, sig string) string {
	if sig == "" {
		return body
	}
	return "\n\n" + sig + body
}

func (m *Model) openComposer(c *composer, cmd tea.Cmd) tea.Cmd {
	c.suggest = m.accts[c.acct].store.Suggest
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
	if c.browse != nil {
		if key == "esc" || key == "q" {
			c.browse = nil
			return nil
		}
		return c.browseUpdate(msg)
	}
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
	if len(c.sugg) > 0 { // autocomplete dropdown open
		switch key {
		case "down", "ctrl+n":
			c.suggIdx = (c.suggIdx + 1) % len(c.sugg)
			return nil
		case "up", "ctrl+p":
			c.suggIdx = (c.suggIdx + len(c.sugg) - 1) % len(c.sugg)
			return nil
		case "tab", "enter":
			c.acceptSuggestion()
			return nil
		case "esc":
			c.sugg = nil
			return nil
		}
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
	case "ctrl+a":
		return c.openBrowser()
	}
	if c.field == fAttach {
		switch key {
		case "enter":
			return c.openBrowser()
		case "left", "h":
			c.attSel = max(c.attSel-1, 0)
		case "right", "l":
			c.attSel = min(c.attSel+1, len(c.atts)-1)
		case "backspace", "delete", "d", "x":
			if len(c.atts) > 0 {
				c.atts = append(c.atts[:c.attSel], c.atts[c.attSel+1:]...)
				c.attSel = clamp(c.attSel, 0, len(c.atts)-1)
			}
		}
		return nil
	}
	c.err = nil
	return c.update(msg)
}

// deliver sends the message, or saves it as a draft.
func (m *Model) deliver(draft bool) tea.Cmd {
	c := m.composer
	o := c.outgoing()
	if !draft && c.attSize() > gmail.MaxAttachments {
		c.err = fmt.Errorf("attachments total %s, over Gmail's 25 MB limit", humanSize(c.attSize()))
		return nil
	}
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
	idx, st, atts := c.acct, m.accts[c.acct].store, c.atts
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		files, err := resolveFiles(ctx, st, atts)
		if err != nil {
			return sentMsg{acct: idx, draft: draft, err: err}
		}
		o.Files = files
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
			st = fg(cInsert).Bold(true)
		}
		return " " + st.Render(padRight(name, 9)) + " "
	}
	title := " " + fg(cInsert).Bold(true).Render("✎ "+c.title) +
		sDim.Render("  ·  from "+m.accts[c.acct].store.Email)
	lines := []string{title, " " + fg(cWarn).Render(c.note), label(fTo, "To") + c.to.View()}
	lines = append(lines, c.suggestionLines(fTo, iw)...)
	lines = append(lines, label(fCc, "Cc")+c.cc.View())
	lines = append(lines, c.suggestionLines(fCc, iw)...)
	lines = append(lines, label(fSubject, "Subject")+c.subject.View(), label(fAttach, "Attach")+c.attachLine(iw-12),
		sRule.Render(" "+strings.Repeat("─", iw-2)))
	if c.browse != nil {
		lines = append(lines, " "+sTitle.Render("Attach a file")+sDim.Render("  "+c.browse.CurrentDirectory))
		for _, l := range strings.Split(c.browse.View(), "\n") {
			lines = append(lines, " "+l)
		}
	} else {
		lines = append(lines, strings.Split(c.body.View(), "\n")...)
	}
	for len(lines) < h-3 {
		lines = append(lines, "")
	}
	lines = lines[:h-3]

	var footer string
	switch {
	case c.busy != "":
		footer = fg(cBusy).Render(" ⟳ " + c.busy)
	case c.confirm:
		footer = fg(cWarn).Bold(true).Render(" Close this message? ") +
			sBase.Render("s") + sDim.Render(" save draft  ") + sBase.Render("d") + sDim.Render(" discard  ") + sBase.Render("esc") + sDim.Render(" keep editing")
	case c.err != nil:
		footer = fg(cErr).Render(" ✗ " + c.err.Error())
	default:
		footer = sDim.Render(" ctrl+enter send · tab next field · ctrl+e edit in $EDITOR · esc close")
	}
	lines = append(lines, footer)

	if c.busy == "" && !c.confirm && c.err == nil {
		switch {
		case c.browse != nil:
			lines[len(lines)-1] = sDim.Render(" j/k move · l/enter open or attach · h up a folder · esc cancel")
		case len(c.sugg) > 0:
			lines[len(lines)-1] = sDim.Render(" ↓/↑ choose · tab accept · esc dismiss")
		case c.field == fAttach:
			lines[len(lines)-1] = sDim.Render(" enter/ctrl+a add a file · ←/→ select · backspace remove · or drop files on the window")
		}
	}

	border := cInsert
	return paneColor(lines, w, h, border)
}
