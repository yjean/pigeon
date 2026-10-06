package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"charm.land/bubbles/v2/filepicker"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/yoann/pigeon/internal/gmail"
	"github.com/yoann/pigeon/internal/store"
)

// composeAtt is a file attached to the message being composed: a local file,
// or an attachment of the forwarded message (downloaded when sending).
type composeAtt struct {
	name   string
	size   int
	path   string
	remote *gmail.Attachment
}

func (c *composer) attSize() int {
	n := 0
	for _, a := range c.atts {
		n += a.size
	}
	return n
}

// addPath attaches a local file. Directories and unreadable files are refused.
func (c *composer) addPath(p string) error {
	info, err := os.Stat(p)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a file", filepath.Base(p))
	}
	c.atts = append(c.atts, composeAtt{name: filepath.Base(p), size: int(info.Size()), path: p})
	c.attSel = len(c.atts) - 1
	return nil
}

// droppedPaths recognizes a paste made of existing file paths: what Ghostty
// (and most terminals) paste when files are dragged onto the window. Paths may
// be quoted or have backslash-escaped spaces.
func droppedPaths(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var paths []string
	var cur strings.Builder
	var quote rune
	flush := func() {
		if cur.Len() > 0 {
			paths = append(paths, cur.String())
			cur.Reset()
		}
	}
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case quote != 0 && r == quote:
			quote = 0
		case quote == 0 && (r == '\'' || r == '"'):
			quote = r
		case quote == 0 && r == '\\' && i+1 < len(rs):
			i++
			cur.WriteRune(rs[i])
		case quote == 0 && (r == ' ' || r == '\n' || r == '\t'):
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	for i, p := range paths {
		p = strings.TrimPrefix(p, "file://")
		if strings.HasPrefix(p, "~/") {
			if home, err := os.UserHomeDir(); err == nil {
				p = filepath.Join(home, p[2:])
			}
		}
		if info, err := os.Stat(p); err != nil || !info.Mode().IsRegular() {
			return nil
		}
		paths[i] = p
	}
	return paths
}

// openBrowser shows the file picker in place of the body.
func (c *composer) openBrowser() tea.Cmd {
	fp := filepicker.New()
	fp.CurrentDirectory = store.DownloadsDir()
	if _, err := os.Stat(fp.CurrentDirectory); err != nil {
		fp.CurrentDirectory, _ = os.UserHomeDir()
	}
	fp.ShowSize, fp.ShowPermissions, fp.ShowHidden = true, false, false
	fp.FileAllowed, fp.DirAllowed = true, false
	fp.Cursor = "▸"
	st := filepicker.DefaultStyles()
	st.Cursor = fg(cSelBar)
	st.Selected = fg(cSelBar).Bold(true)
	st.Directory = fg(blue)
	st.File = sBase
	st.Symlink = fg(mauve)
	st.FileSize = sDim.Width(8).AlignHorizontal(lipgloss.Right)
	st.EmptyDirectory = sFaint.SetString("(empty folder)")
	st.DisabledFile, st.DisabledCursor, st.DisabledSelected = sFaint, sFaint, sFaint
	fp.Styles = st
	fp.SetHeight(max(c.body.Height()-1, 3))
	c.browse = &fp
	return fp.Init()
}

// browseUpdate routes a message to the open file picker.
func (c *composer) browseUpdate(msg tea.Msg) tea.Cmd {
	fp, cmd := c.browse.Update(msg)
	c.browse = &fp
	if ok, path := fp.DidSelectFile(msg); ok {
		c.browse = nil
		if err := c.addPath(path); err != nil {
			c.err = err
		}
		return nil
	}
	return cmd
}

// files resolves the attachments for sending: reads local files and
// downloads forwarded ones.
func resolveFiles(ctx context.Context, st *store.Account, atts []composeAtt) ([]gmail.File, error) {
	files := make([]gmail.File, 0, len(atts))
	for _, a := range atts {
		var data []byte
		var err error
		if a.remote != nil {
			data, err = st.DownloadAttachment(ctx, *a.remote)
		} else {
			data, err = os.ReadFile(a.path)
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", a.name, err)
		}
		f := gmail.File{Name: a.name, Data: data}
		if a.remote != nil {
			f.Type = a.remote.MimeType
		}
		files = append(files, f)
	}
	return files, nil
}

// attachLine renders the Attach row: chips, total size, and hints.
func (c *composer) attachLine(w int) string {
	if len(c.atts) == 0 {
		return sFaint.Render("ctrl+a to attach, or drop a file here")
	}
	var b strings.Builder
	for i, a := range c.atts {
		chip := "📎 " + a.name + " " + humanSize(a.size)
		if c.field == fAttach && i == c.attSel {
			b.WriteString(lipgloss.NewStyle().Background(cSelBg).Foreground(cSelBar).Bold(true).Render(" " + chip + " "))
		} else {
			b.WriteString(sMuted.Render(" " + chip + " "))
		}
	}
	total := c.attSize()
	if total > gmail.MaxAttachments {
		b.WriteString(fg(cErr).Render(fmt.Sprintf("  %s · over Gmail's 25 MB limit", humanSize(total))))
	} else if len(c.atts) > 1 {
		b.WriteString(sDim.Render("  " + humanSize(total)))
	}
	return truncate(b.String(), w)
}
