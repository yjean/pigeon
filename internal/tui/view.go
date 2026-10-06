package tui

import (
	"fmt"
	"image/color"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/yoann/pigeon/internal/gmail"
)

// Tokyo Night-ish palette, close to slk's.
var (
	cAccent = lipgloss.Color("#7aa2f7")
	cGreen  = lipgloss.Color("#9ece6a")
	cYellow = lipgloss.Color("#e0af68")
	cRed    = lipgloss.Color("#f7768e")
	cFg     = lipgloss.Color("#c0caf5")
	cDim    = lipgloss.Color("#565f89")
	cMuted  = lipgloss.Color("#828bb8")
	cSelBg  = lipgloss.Color("#283457")
	cDark   = lipgloss.Color("#1a1b26")
	cBorder = lipgloss.Color("#3b4261")

	sBase    = lipgloss.NewStyle().Foreground(cFg)
	sDim     = lipgloss.NewStyle().Foreground(cDim)
	sMuted   = lipgloss.NewStyle().Foreground(cMuted)
	sBold    = lipgloss.NewStyle().Foreground(cFg).Bold(true)
	sAccent  = lipgloss.NewStyle().Foreground(cAccent).Bold(true)
	sDate    = lipgloss.NewStyle().Foreground(cDim).Italic(true)
	sMode    = lipgloss.NewStyle().Foreground(cDark).Background(cAccent).Bold(true)
	sModeAlt = lipgloss.NewStyle().Foreground(cDark).Background(cGreen).Bold(true)
)

const railW = 6

func (m *Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.WindowTitle = m.acct().store.Email + " | Pigeon"
	return v
}

// layout returns the main area width and the heights of the list and reader panes.
func (m *Model) layout() (mw, listH, readerH int) {
	mw = max(m.w-railW, 20)
	avail := max(m.h-1, 8) // minus status bar
	listH = max(avail*2/5, 5)
	return mw, listH, avail - listH
}

func (m *Model) render() string {
	if m.w == 0 || m.h == 0 {
		return ""
	}
	mw, listH, readerH := m.layout()
	main := lipgloss.JoinVertical(lipgloss.Left, m.renderList(mw, listH), m.renderReader(mw, readerH))
	body := lipgloss.JoinHorizontal(lipgloss.Top, m.renderRail(m.h-1), main)
	return body + "\n" + m.renderStatus()
}

// --- account rail ---

func (m *Model) renderRail(h int) string {
	lines := make([]string, 0, h)
	lines = append(lines, "")
	for i, a := range m.accts {
		badge := " " + a.badge + " "
		if i == m.cur {
			badge = sMode.Render(badge)
		} else {
			badge = sMuted.Render(badge)
		}
		dot := " "
		if a.box == inbox && a.unread() > 0 {
			dot = lipgloss.NewStyle().Foreground(cYellow).Render("•")
		}
		lines = append(lines, " "+badge+dot, "")
	}
	for i := range lines {
		lines[i] = fit(lines[i], railW)
	}
	for len(lines) < h {
		lines = append(lines, strings.Repeat(" ", railW))
	}
	return strings.Join(lines[:h], "\n")
}

// --- thread list (top pane) ---

func (m *Model) renderList(w, h int) string {
	a := m.acct()
	iw, ih := w-2, h-2
	lines := make([]string, 0, ih)

	head := sAccent.Render(a.box.name) + sDim.Render("  ·  "+a.store.Email)
	if n := a.unread(); n > 0 {
		head += lipgloss.NewStyle().Foreground(cYellow).Render(fmt.Sprintf("  ·  %d unread", n))
	}
	lines = append(lines, " "+head)

	rows := ih - 1
	switch {
	case !a.loaded && a.err == nil:
		lines = append(lines, sDim.Render(" Loading…"))
	case a.loaded && len(a.threads) == 0:
		lines = append(lines, sDim.Render(" No conversations."))
	default:
		// Keep the selection visible with a small scroll margin.
		if a.sel < a.top {
			a.top = a.sel
		}
		if a.sel >= a.top+rows {
			a.top = a.sel - rows + 1
		}
		a.top = clamp(a.top, 0, max(len(a.threads)-rows, 0))
		for i := a.top; i < len(a.threads) && i < a.top+rows; i++ {
			lines = append(lines, renderRow(a.threads[i], iw, i == a.sel, m.focus == focusList))
		}
	}
	return pane(lines, w, h, m.focus == focusList)
}

func renderRow(s gmail.Summary, w int, selected, focused bool) string {
	st := func(base lipgloss.Style) lipgloss.Style {
		if selected {
			return base.Background(cSelBg)
		}
		return base
	}
	from, subj := sBase, sBase
	if s.Unread {
		from, subj = sBold, sBold
	}

	lead := st(sBase).Render(" ")
	if selected {
		c := cGreen
		if !focused {
			c = cDim
		}
		lead = st(lipgloss.NewStyle().Foreground(c)).Render("▌")
	}
	dot := st(sBase).Render(" ")
	if s.Unread {
		dot = st(lipgloss.NewStyle().Foreground(cAccent)).Render("●")
	}
	star := st(sBase).Render("  ")
	if s.Starred {
		star = st(lipgloss.NewStyle().Foreground(cYellow)).Render("★ ")
	}

	const dateW = 10
	fromW := clamp(w/4, 12, 28)
	restW := w - 4 - fromW - 1 - dateW
	if restW < 10 { // very narrow terminal: drop the sender column
		fromW, restW = 0, w-4-dateW
	}

	sender := oneLine(s.From)
	if s.Count > 1 {
		sender += fmt.Sprintf(" (%d)", s.Count)
	}
	subject := oneLine(s.Subject)
	if subject == "" {
		subject = "(no subject)"
	}
	subject = ansi.Truncate(subject, restW, "…")
	snippet := ""
	if left := restW - ansi.StringWidth(subject); left > 4 && s.Snippet != "" {
		snippet = ansi.Truncate(" — "+oneLine(s.Snippet), left, "…")
	}
	pad := restW - ansi.StringWidth(subject) - ansi.StringWidth(snippet)

	var b strings.Builder
	b.WriteString(lead)
	b.WriteString(dot)
	b.WriteString(star)
	if fromW > 0 {
		b.WriteString(st(from).Render(padRight(ansi.Truncate(sender, fromW, "…"), fromW)))
		b.WriteString(st(sBase).Render(" "))
	}
	b.WriteString(st(subj).Render(subject))
	b.WriteString(st(sDim).Render(snippet))
	b.WriteString(st(sBase).Render(strings.Repeat(" ", max(pad, 0))))
	b.WriteString(st(sDate).Render(padLeft(listDate(s.Date), dateW-1) + " "))
	return b.String()
}

// --- reader (bottom pane) ---

func (m *Model) readerSize() (w, h int) {
	mw, _, readerH := m.layout()
	return mw - 2 - 2, readerH - 2 - 2 // borders, left gutter + scrollbar; subject header + rule
}

// refreshReader re-renders the open thread into the viewport. reset scrolls to
// the start of the latest message (like a chat), otherwise keeps the position.
func (m *Model) refreshReader(reset bool) {
	if m.w == 0 {
		return
	}
	w, h := m.readerSize()
	m.reader.SetWidth(w)
	m.reader.SetHeight(max(h, 1))
	if m.openThread == nil {
		m.reader.SetContent("")
		return
	}
	content, lastStart := renderThread(m.openThread, w-1, m.acct().store.Email)
	off := m.reader.YOffset()
	m.reader.SetContent(content)
	if reset {
		m.reader.SetYOffset(lastStart)
	} else {
		m.reader.SetYOffset(off)
	}
}

func (m *Model) renderReader(w, h int) string {
	if m.composer != nil {
		return m.renderComposer(w, h)
	}
	iw := w - 2
	var lines []string
	if m.help {
		lines = append(lines, " "+sAccent.Render("Keybindings"), sDim.Render(" "+strings.Repeat("─", iw-2)))
		lines = append(lines, helpLines()...)
		return pane(lines, w, h, true)
	}
	a := m.acct()
	s := a.selected()
	if s == nil {
		return pane(nil, w, h, m.focus == focusReader)
	}
	subject := oneLine(s.Subject)
	if subject == "" {
		subject = "(no subject)"
	}
	lines = append(lines, " "+sBold.Render(subject), sDim.Render(" "+strings.Repeat("─", iw-2)))

	switch {
	case m.openErr != nil:
		lines = append(lines, lipgloss.NewStyle().Foreground(cRed).Render(" ✗ "+m.openErr.Error()))
	case m.openThread == nil:
		lines = append(lines, sDim.Render(" Loading…"))
	default:
		body := strings.Split(m.reader.View(), "\n")
		bar := scrollbar(m.reader.YOffset(), m.reader.Height(), m.reader.TotalLineCount())
		for i := 0; i < m.reader.Height(); i++ {
			l := ""
			if i < len(body) {
				l = body[i]
			}
			lines = append(lines, fit(" "+l, iw-1)+bar[i])
		}
	}
	return pane(lines, w, h, m.focus == focusReader)
}

// renderThread formats every message of the thread, returning the content and
// the line where the last message starts.
func renderThread(t *gmail.Thread, w int, me string) (string, int) {
	w = max(w, 10)
	var lines []string
	last := 0
	for i, msg := range t.Messages {
		if i > 0 {
			lines = append(lines, "", sDim.Render(strings.Repeat("┄", w)), "")
		}
		last = len(lines)
		from := gmail.ParseAddress(msg.Header("From"))
		name := from.Short()
		if strings.EqualFold(from.Email, me) {
			name += " (me)"
		}
		head := sAccent.Render(name)
		if from.Name != "" {
			head += "  " + sDim.Render(from.Email)
		}
		head += "  " + sDate.Render(msg.Time().Local().Format("Mon Jan 2, 3:04 PM"))
		if msg.HasLabel("UNREAD") {
			head += lipgloss.NewStyle().Foreground(cYellow).Render("  ●")
		}
		lines = append(lines, ansi.Truncate(head, w, "…"))

		var to []string
		for _, addr := range append(gmail.ParseAddressList(msg.Header("To")), gmail.ParseAddressList(msg.Header("Cc"))...) {
			if strings.EqualFold(addr.Email, me) {
				to = append(to, "me")
			} else {
				to = append(to, addr.Short())
			}
		}
		if len(to) > 0 {
			lines = append(lines, sDim.Render(ansi.Truncate("to "+strings.Join(to, ", "), w, "…")))
		}
		if att := msg.Attachments(); len(att) > 0 {
			lines = append(lines, sMuted.Render(ansi.Truncate("📎 "+strings.Join(att, ", "), w, "…")))
		}
		lines = append(lines, "")

		body := msg.Body()
		if stripped := gmail.StripQuoted(body); stripped != "" {
			body = stripped
		}
		if body == "" {
			body = sDim.Render("(no text content)")
		}
		lines = append(lines, strings.Split(ansi.Wrap(body, w, ""), "\n")...)
	}
	return strings.Join(lines, "\n"), last
}

// --- status bar ---

func (m *Model) renderStatus() string {
	a := m.acct()
	mode := sMode.Render(" NORMAL ")
	switch {
	case m.composer != nil:
		mode = lipgloss.NewStyle().Foreground(cDark).Background(cYellow).Bold(true).Render(" INSERT ")
	case m.focus == focusReader:
		mode = sModeAlt.Render("  READ  ")
	}
	left := mode + " " + sBase.Render("● "+a.box.name) + sDim.Render(" · "+a.store.Email)
	center := sDim.Italic(true).Render("? for keybindings")

	var right string
	switch {
	case m.flash != "":
		c := cGreen
		if m.flashErr {
			c = cYellow
		}
		right = lipgloss.NewStyle().Foreground(c).Render(m.flash)
	case a.err != nil:
		right = lipgloss.NewStyle().Foreground(cRed).Render("✗ " + ansi.Truncate(a.err.Error(), max(m.w/3, 10), "…"))
	case len(a.syncing) > 0:
		right = lipgloss.NewStyle().Foreground(cYellow).Render("⟳ syncing")
	case !a.lastSync.IsZero():
		right = lipgloss.NewStyle().Foreground(cGreen).Render("● synced " + a.lastSync.Format("3:04 PM"))
	}
	right += " "

	lw, cw, rw := lipgloss.Width(left), lipgloss.Width(center), lipgloss.Width(right)
	gap := m.w - lw - cw - rw
	if gap < 2 {
		return fit(left+strings.Repeat(" ", max(m.w-lw-rw, 1))+right, m.w)
	}
	g1 := (m.w-cw)/2 - lw
	if g1 < 1 {
		g1 = 1
	}
	g2 := max(m.w-lw-g1-cw-rw, 1)
	return fit(left+strings.Repeat(" ", g1)+center+strings.Repeat(" ", g2)+right, m.w)
}

func helpLines() []string {
	rows := [][2]string{
		{"j / k  ↓ / ↑", "next / previous conversation (scroll when reading)"},
		{"J / K", "next / previous conversation, from anywhere"},
		{"enter  l  tab", "read conversation (marks it read)"},
		{"esc  h  q", "back to the list"},
		{"gg / G", "top / bottom"},
		{"space  ctrl+d / ctrl+u", "scroll the conversation"},
		{"1-9  [ / ]", "switch account"},
		{"gi gs gt gd ga", "inbox, starred, sent, drafts, all mail"},
		{"e / #", "archive / move to trash"},
		{"s / u", "star / mark unread (toggles)"},
		{"z", "undo archive / trash"},
		{"c", "compose a new message"},
		{"r / a / f", "reply / reply all / forward"},
		{"  ctrl+enter", "  send (in compose; ctrl+s also works)"},
		{"  ctrl+e", "  edit the body in $EDITOR"},
		{"  tab / esc", "  next field / close (save draft or discard)"},
		{"ctrl+r", "sync now"},
		{"?", "toggle this help"},
		{"q  ctrl+c", "quit"},
	}
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = "  " + sAccent.Render(padRight(r[0], 24)) + sBase.Render(r[1])
	}
	return out
}

// --- helpers ---

// pane draws a bordered box of exactly w×h cells around lines.
func pane(lines []string, w, h int, focused bool) string {
	c := cBorder
	if focused {
		c = cAccent
	}
	return paneColor(lines, w, h, c)
}

func paneColor(lines []string, w, h int, border color.Color) string {
	iw, ih := w-2, h-2
	out := make([]string, ih)
	for i := range out {
		if i < len(lines) {
			out[i] = fit(lines[i], iw)
		} else {
			out[i] = strings.Repeat(" ", iw)
		}
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(border).Render(strings.Join(out, "\n"))
}

func scrollbar(offset, visible, total int) []string {
	bar := make([]string, visible)
	for i := range bar {
		bar[i] = " "
	}
	if total <= visible || visible <= 0 {
		return bar
	}
	thumb := max(visible*visible/total, 1)
	pos := offset * (visible - thumb) / max(total-visible, 1)
	for i := pos; i < pos+thumb && i < visible; i++ {
		bar[i] = lipgloss.NewStyle().Foreground(cAccent).Render("┃")
	}
	return bar
}

// fit truncates or pads a styled string to exactly w cells.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	s = ansi.Truncate(s, w, "…")
	if pad := w - ansi.StringWidth(s); pad > 0 {
		s += strings.Repeat(" ", pad)
	}
	return s
}

func padRight(s string, w int) string { return s + strings.Repeat(" ", max(w-ansi.StringWidth(s), 0)) }
func padLeft(s string, w int) string  { return strings.Repeat(" ", max(w-ansi.StringWidth(s), 0)) + s }

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func listDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	t, now := t.Local(), time.Now()
	y1, m1, d1 := t.Date()
	y2, m2, d2 := now.Date()
	switch {
	case y1 == y2 && m1 == m2 && d1 == d2:
		return t.Format("3:04 PM")
	case now.Sub(t) < 6*24*time.Hour:
		return t.Format("Mon")
	case y1 == y2:
		return t.Format("Jan 2")
	default:
		return t.Format("2006-01-02")
	}
}
