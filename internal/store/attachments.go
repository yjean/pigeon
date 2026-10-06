package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/yoann/pigeon/internal/gmail"
)

// DownloadsDir is where attachments are saved: $PIGEON_DOWNLOAD_DIR or ~/Downloads.
func DownloadsDir() string {
	if d := os.Getenv("PIGEON_DOWNLOAD_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Downloads")
}

// SaveAttachment downloads an attachment into dir without overwriting
// anything, and returns the file path. On macOS the file is quarantined like
// a browser download, so Gatekeeper still checks it when opened.
func (a *Account) SaveAttachment(ctx context.Context, att gmail.Attachment, dir string) (string, error) {
	c, err := a.Client(ctx)
	if err != nil {
		return "", err
	}
	data, err := c.Download(ctx, att)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := safeFilename(att.Filename)
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 0; ; i++ {
		p := filepath.Join(dir, name)
		if i > 0 {
			p = filepath.Join(dir, fmt.Sprintf("%s (%d)%s", base, i, ext))
		}
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		_, werr := f.Write(data)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			os.Remove(p)
			return "", werr
		}
		quarantine(p)
		return p, nil
	}
}

// safeFilename keeps an attachment name inside the target directory.
func safeFilename(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.Map(func(r rune) rune {
		if r < 32 || r == '/' || r == ':' {
			return '_'
		}
		return r
	}, name)
	name = strings.TrimLeft(name, ".")
	if name == "" {
		name = "attachment"
	}
	return name
}

func quarantine(path string) {
	if runtime.GOOS != "darwin" {
		return
	}
	v := fmt.Sprintf("0081;%x;pigeon;", time.Now().Unix())
	_ = exec.Command("xattr", "-w", "com.apple.quarantine", v, path).Run()
}
