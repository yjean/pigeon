package tui

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/yoann/pigeon/internal/gmail"
	"github.com/yoann/pigeon/internal/store"
)

// picker lists the open conversation's attachments and links (o).
type picker struct {
	items []pickItem
	sel   int
}

type pickItem struct {
	link *gmail.Link
	att  *gmail.Attachment
}

type savedMsg struct {
	path string
	open bool
	err  error
}

func (m *Model) openPicker() tea.Cmd {
	t := m.openThread
	if t == nil {
		return m.flashMsg("Conversation still loading…", true)
	}
	var atts, inline, links []pickItem
	seen := map[string]bool{}
	for i := len(t.Messages) - 1; i >= 0; i-- { // newest message first
		msg := t.Messages[i]
		for _, a := range msg.AttachmentParts() {
			// Replies often carry the same file again: list it once.
			if key := fmt.Sprintf("att|%s|%d", a.Filename, a.Size); seen[key] {
				continue
			} else {
				seen[key] = true
			}
			if a.Inline {
				inline = append(inline, pickItem{att: &a})
			} else {
				atts = append(atts, pickItem{att: &a})
			}
		}
		for _, l := range msg.Links() {
			if !seen[l.URL] {
				seen[l.URL] = true
				links = append(links, pickItem{link: &l})
			}
		}
	}
	items := append(append(atts, links...), inline...)
	if len(items) == 0 {
		return m.flashMsg("No links or attachments in this conversation", true)
	}
	m.picker, m.help = &picker{items: items}, false
	return nil
}

func (m *Model) pickerKey(key string) tea.Cmd {
	p := m.picker
	switch key {
	case "esc", "q", "o":
		m.picker = nil
	case "j", "down":
		p.sel = min(p.sel+1, len(p.items)-1)
	case "k", "up":
		p.sel = max(p.sel-1, 0)
	case "g":
		p.sel = 0
	case "G":
		p.sel = len(p.items) - 1
	case "enter", "l":
		return m.activate(p.items[p.sel], true)
	case "s":
		if it := p.items[p.sel]; it.att != nil {
			return m.activate(it, false)
		}
	case "y":
		it := p.items[p.sel]
		if it.link != nil {
			return tea.Batch(copyToClipboard(it.link.URL), m.flashMsg("Link copied", false))
		}
	default:
		if n, err := strconv.Atoi(key); err == nil && n >= 1 && n <= len(p.items) {
			p.sel = n - 1
			return m.activate(p.items[p.sel], true)
		}
	}
	return nil
}

// activate opens a link, or saves (and optionally opens) an attachment.
func (m *Model) activate(it pickItem, open bool) tea.Cmd {
	if it.link != nil {
		u := it.link.URL
		if strings.HasPrefix(strings.ToLower(u), "mailto:") {
			m.picker = nil
			return m.composeMailto(u)
		}
		if !gmail.SafeURL(u) {
			return m.flashMsg("Refusing to open this link", true)
		}
		if err := openExternal(u); err != nil {
			return m.flashMsg("✗ "+err.Error(), true)
		}
		return m.flashMsg("Opened "+host(u), false)
	}
	att, st := *it.att, m.acct().store
	return tea.Batch(m.flashMsg("Downloading "+att.Filename+"…", false), func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		path, err := st.SaveAttachment(ctx, att, store.DownloadsDir())
		return savedMsg{path: path, open: open, err: err}
	})
}

func (m *Model) onSaved(msg savedMsg) tea.Cmd {
	if msg.err != nil {
		return m.flashMsg("✗ "+msg.err.Error(), true)
	}
	short := msg.path
	if home, err := os.UserHomeDir(); err == nil {
		short = strings.Replace(short, home, "~", 1)
	}
	if msg.open {
		if err := openExternal(msg.path); err != nil {
			return m.flashMsg("Saved to "+short+" (could not open it)", true)
		}
		return m.flashMsg("Saved to "+short+" and opened", false)
	}
	return m.flashMsg("Saved to "+short, false)
}

// composeMailto starts a new message from a mailto: link.
func (m *Model) composeMailto(raw string) tea.Cmd {
	u, err := url.Parse(raw)
	if err != nil {
		return m.flashMsg("Invalid mailto link", true)
	}
	to, _ := url.PathUnescape(u.Opaque)
	q := u.Query()
	o := gmail.Outgoing{To: to, Cc: q.Get("cc"), Subject: q.Get("subject"), Body: q.Get("body")}
	field := fBody
	if o.Subject == "" {
		field = fSubject
	}
	c, cmd := newComposer("New message", m.cur, o, field)
	return m.openComposer(c, cmd)
}

func openExternal(target string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", target).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", target).Start()
	default:
		return exec.Command("xdg-open", target).Start()
	}
}

func copyToClipboard(s string) tea.Cmd {
	if runtime.GOOS == "darwin" {
		return func() tea.Msg {
			cmd := exec.Command("pbcopy")
			cmd.Stdin = strings.NewReader(s)
			_ = cmd.Run()
			return nil
		}
	}
	return tea.SetClipboard(s) // OSC 52
}

func host(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return strings.TrimPrefix(u.Host, "www.")
	}
	return raw
}

// misleading reports a link whose text looks like a web address of another
// site than its real target (a common phishing trick).
func misleading(l gmail.Link) bool {
	text := strings.ToLower(strings.TrimSpace(l.Text))
	if strings.HasPrefix(strings.ToLower(l.URL), "mailto:") || strings.Contains(text, " ") || !strings.Contains(text, ".") {
		return false
	}
	shown := text
	if !strings.Contains(shown, "://") {
		shown = "https://" + shown
	}
	u, err := url.Parse(shown)
	if err != nil || u.Host == "" || !strings.Contains(u.Host, ".") {
		return false
	}
	a, b := strings.TrimPrefix(u.Hostname(), "www."), strings.TrimPrefix(strings.ToLower(host(l.URL)), "www.")
	return a != b && !strings.HasSuffix(b, "."+a) && !strings.HasSuffix(a, "."+b)
}

func humanSize(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n>>10)
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// renderPicker draws the picker in the reader pane.
func (m *Model) renderPicker(w, h int) string {
	p := m.picker
	iw, ih := w-2, h-2
	lines := []string{" " + sTitle.Render("Links & attachments") + sDim.Render(fmt.Sprintf("  ·  %d", len(p.items))),
		sRule.Render(" " + strings.Repeat("─", iw-2))}
	rows := ih - 3
	top := clamp(p.sel-rows/2, 0, max(len(p.items)-rows, 0))
	for i := top; i < len(p.items) && i < top+rows; i++ {
		it := p.items[i]
		num := "  "
		if i < 9 {
			num = strconv.Itoa(i+1) + " "
		}
		var icon, main, detail string
		if it.att != nil {
			icon, main = "📎", it.att.Filename
			detail = humanSize(it.att.Size) + " · " + it.att.MimeType
			if it.att.Inline {
				icon, detail = "🖼", detail+" · inline"
			}
		} else {
			icon, main, detail = "↗ ", it.link.Text, host(it.link.URL)
			if main == "" || main == it.link.URL {
				main, detail = it.link.URL, ""
			}
		}
		detailStyle := sDim
		if it.link != nil && misleading(*it.link) {
			detailStyle, detail = fg(cWarn), "⚠ goes to "+detail
		}
		selected := i == p.sel
		st := func(s lipgloss.Style) lipgloss.Style {
			if selected {
				return s.Background(cSelBg)
			}
			return s
		}
		bar := st(sBase).Render(" ")
		if selected {
			bar = st(fg(cSelBar)).Render("▌")
		}
		mainW := max(iw-6-ansiWidth(detail)-2, 10)
		line := bar + st(sKey).Render(num) + st(sBase).Render(icon+" ") +
			st(sBold).Render(truncate(main, mainW)) + st(detailStyle).Render("  "+detail)
		if selected {
			line += st(sBase).Render(strings.Repeat(" ", max(iw-ansiWidth(line), 0)))
		}
		lines = append(lines, line)
	}
	for len(lines) < ih-1 {
		lines = append(lines, "")
	}
	lines = append(lines, sDim.Render(" enter open · s save · y copy link · 1-9 pick · esc close"))
	return paneColor(lines, w, h, cBorderActive)
}
