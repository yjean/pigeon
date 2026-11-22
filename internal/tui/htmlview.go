package tui

import (
	"context"
	"os"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/yoann/pigeon/internal/gmail"
)

// Viewing a message as HTML in the browser (O, or "View as HTML" in the o picker).

type htmlOpenedMsg struct{ err error }

// openHTML writes the focused message's HTML part to a private file and opens
// it in the browser. Remote content stays blocked unless remote is set.
func (m *Model) openHTML(remote bool) tea.Cmd {
	if m.openThread == nil || len(m.openThread.Messages) == 0 {
		return m.flashMsg("Conversation still loading…", true)
	}
	msg := m.focusedMessage()
	src, ok := msg.HTMLBody()
	if !ok {
		return m.flashMsg("This message has no HTML version", true)
	}
	st := m.acct().store
	header := gmail.PageHeader{
		Subject: msg.Header("Subject"), From: msg.Header("From"), To: msg.Header("To"), Cc: msg.Header("Cc"),
		Date: msg.Time().Local().Format("Mon 2 Jan 2006 15:04"),
	}
	note := "Opening the HTML version…"
	if remote {
		note = "Opening the HTML version with remote images…"
	}
	return tea.Batch(m.flashMsg(note, false), func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		images := map[string]gmail.Image{}
		for _, img := range msg.InlineImages() {
			if data, err := st.DownloadAttachment(ctx, img); err == nil { // a missing image is not worth failing over
				images[img.ContentID] = gmail.Image{MimeType: img.MimeType, Data: data}
			}
		}
		path, err := writeHTMLPage(msg.ID, remote, gmail.HTMLPage(src, header, images, remote))
		if err != nil {
			return htmlOpenedMsg{err: err}
		}
		return htmlOpenedMsg{err: openExternal(path)}
	})
}

func (m *Model) onHTMLOpened(msg htmlOpenedMsg) tea.Cmd {
	if msg.err != nil {
		return m.flashMsg("✗ "+msg.err.Error(), true)
	}
	return m.flashMsg("Opened in the browser", false)
}

// writeHTMLPage stores the page in pigeon's cache, readable by the user only:
// mail is private, and /tmp is shared.
func writeHTMLPage(messageID string, remote bool, page string) (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "pigeon", "html")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	name := filepath.Base(messageID) // Gmail IDs are hex; Base guards against a path anyway
	if remote {
		name += "-remote"
	}
	path := filepath.Join(dir, name+".html")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return "", err
	}
	if _, err := f.WriteString(page); err != nil {
		f.Close()
		return "", err
	}
	return path, f.Close()
}
