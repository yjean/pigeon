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

const railW = 6

func (m *Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.ReportFocus = true
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
			badge = sBadge.Render(badge)
		} else {
			badge = sMuted.Render(badge)
		}
		dot := " "
		if a.inboxUnread > 0 || a.inboxUnread < 0 && a.box == inbox && a.unread() > 0 {
			dot = fg(cCount).Render("•")
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

	head := sTitle.Render(a.box.name)
	if a.box.search {
		head += " " + sBase.Render(a.box.query())
	}
	if a.unreadOnly[a.box.label] {
		head += fg(cCount).Render(" · unread")
	}
	head += sDim.Render("  ·  " + a.store.Email)
	n := a.unread()
	if a.box == inbox && a.inboxUnread >= 0 {
		n = a.inboxUnread // the true count, not just the loaded page
	}
	if n > 0 {
		head += fg(cCount).Render(fmt.Sprintf("  ·  %d unread", n))
	}
	lines = append(lines, " "+head)

	rows := ih - 1
	switch {
	case !a.loaded && a.err == nil:
		lines = append(lines, sDim.Render(" Loading…"))
	case a.loaded && len(a.threads) == 0 && a.unreadOnly[a.box.label]:
		lines = append(lines, "", fg(cOK).Render(" ✓ Woohoo! You've read everything here.")+sDim.Render("  U to show all conversations"))
	case a.loaded && len(a.threads) == 0 && a.box.search:
		lines = append(lines, sDim.Render(" No results.")+sFaint.Render("  / to refine · esc to go back"))
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
		c := cSelBar
		if !focused {
			c = overlay0
		}
		lead = st(lipgloss.NewStyle().Foreground(c)).Render("▌")
	}
	dot := st(sBase).Render(" ")
	if s.Unread {
		dot = st(fg(cUnread)).Render("●")
	}
	star := st(sBase).Render("  ")
	if s.Starred {
		star = st(fg(cStar)).Render("★ ")
	}

	const dateW, clipW = 10, 3
	fromW := clamp(w/4, 12, 28)
	restW := w - 4 - fromW - 1 - clipW - dateW
	if restW < 10 { // very narrow terminal: drop the sender column
		fromW, restW = 0, w-4-clipW-dateW
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
	clip := strings.Repeat(" ", clipW)
	if s.Attached {
		clip = "📎 "
	}
	b.WriteString(st(sBase).Render(clip))
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
	if m.picker != nil {
		return m.renderPicker(w, h)
	}
	iw := w - 2
	var lines []string
	if m.help {
		lines = append(lines, " "+sTitle.Render("Keybindings"), sRule.Render(" "+strings.Repeat("─", iw-2)))
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
	lines = append(lines, " "+sBold.Render(subject), sRule.Render(" "+strings.Repeat("─", iw-2)))

	switch {
	case m.openErr != nil:
		lines = append(lines, fg(cErr).Render(" ✗ "+m.openErr.Error()))
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
			lines = append(lines, "", sRule.Render(strings.Repeat("┄", w)), "")
		}
		last = len(lines)
		from := gmail.ParseAddress(msg.Header("From"))
		name := from.Short()
		if strings.EqualFold(from.Email, me) {
			name += " (me)"
		}
		head := sName.Render(name)
		if from.Name != "" {
			head += "  " + sDim.Render(from.Email)
		}
		head += "  " + sDate.Render(msg.Time().Local().Format("Mon Jan 2, 3:04 PM"))
		if msg.HasLabel("UNREAD") {
			head += fg(cUnread).Render("  ●")
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
			lines = append(lines, sMuted.Render(ansi.Truncate("📎 "+strings.Join(att, ", "), w-14, "…"))+sFaint.Render("  o to open"))
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
	if m.search != nil {
		return fit(sSearch.Render(" SEARCH ")+" "+sKey.Render("/")+m.search.View(), m.w)
	}
	mode := sNormal.Render(" NORMAL ")
	switch {
	case m.composer != nil:
		mode = sInsert.Render(" INSERT ")
	case m.focus == focusReader:
		mode = sRead.Render("  READ  ")
	}
	boxName := a.box.name
	if a.box.search {
		boxName += ": " + a.box.query()
	}
	if a.unreadOnly[a.box.label] {
		boxName += " (unread)"
	}
	left := mode + " " + sBase.Render("● "+boxName) + sDim.Render(" · "+a.store.Email)
	center := sDim.Italic(true).Render("? for keybindings")

	var right string
	switch {
	case m.confirmQuit:
		right = fg(cWarn).Render("Quit pigeon? q/y to quit, any other key to stay")
	case m.flash != "":
		c := cOK
		if m.flashErr {
			c = cWarn
		}
		right = fg(c).Render(m.flash)
	case a.err != nil:
		right = fg(cErr).Render("✗ " + ansi.Truncate(a.err.Error(), max(m.w/3, 10), "…"))
	case len(a.syncing) > 0:
		right = fg(cBusy).Render("⟳ syncing")
	case !a.lastSync.IsZero():
		right = fg(cOK).Render("● synced " + a.lastSync.Format("3:04 PM"))
	}
	right += " "

	lw, cw, rw := lipgloss.Width(left), lipgloss.Width(center), lipgloss.Width(right)
	gap := m.w - lw - cw - rw
	if gap < 2 || (m.w-cw)/2 <= lw || (m.w+cw)/2 >= m.w-rw { // no room to center the hint: drop it
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
		{"  ctrl+a", "  attach a file (or drop files on the window)"},
		{"o", "links & attachments: open, save, copy"},
		{"U", "toggle unread only / all conversations"},
		{"/", "search (Gmail syntax: from: subject: has:attachment …)"},
		{"  esc", "  leave the search results"},
		{"ctrl+r", "sync now"},
		{"?", "toggle this help"},
		{"q / ctrl+c", "quit (q asks first: q or y to confirm)"},
	}
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = "  " + sKey.Render(padRight(r[0], 24)) + sBase.Render(r[1])
	}
	return out
}

// --- helpers ---

// pane draws a bordered box of exactly w×h cells around lines.
func pane(lines []string, w, h int, focused bool) string {
	c := cBorder
	if focused {
		c = cBorderActive
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
		bar[i] = fg(overlay1).Render("┃")
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

func truncate(s string, w int) string { return ansi.Truncate(s, w, "…") }
func ansiWidth(s string) int          { return ansi.StringWidth(s) }

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
