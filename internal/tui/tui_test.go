package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/yoann/pigeon/internal/gmail"
)

func TestCtrlEnterSends(t *testing.T) {
	if got := (tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModCtrl}).String(); got != "ctrl+enter" {
		t.Fatalf("ctrl+enter reported as %q", got)
	}
}

func TestWithKeptMergesReadThreadsByDate(t *testing.T) {
	now := time.Now()
	a := &account{box: inbox, unreadOnly: map[string]bool{"INBOX": true}, keep: map[string]bool{"read": true},
		threads: []gmail.Summary{{ID: "read", Date: now.Add(-time.Hour)}, {ID: "gone", Date: now}}}
	if a.view() != "INBOX+UNREAD" {
		t.Fatalf("view = %q", a.view())
	}
	got := a.withKept([]gmail.Summary{{ID: "new", Date: now}, {ID: "old", Date: now.Add(-2 * time.Hour)}})
	ids := []string{}
	for _, s := range got {
		ids = append(ids, s.ID)
	}
	if strings.Join(ids, ",") != "new,read,old" {
		t.Fatalf("got %v", ids)
	}
	a.unreadOnly["INBOX"] = false
	if a.view() != "INBOX" || len(a.withKept(nil)) != 0 {
		t.Fatal("kept threads must not leak into the all-conversations view")
	}
}
