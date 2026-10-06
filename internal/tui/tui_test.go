package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/yoann/pigeon/internal/gmail"
	"github.com/yoann/pigeon/internal/store"
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

func TestAutocompleteKeys(t *testing.T) {
	contacts := []store.Contact{{Name: "Damon", Email: "d@x.io"}, {Name: "Damien", Email: "dm@y.io"}, {Email: "dam@z.io"}}
	m := &Model{w: 100, h: 30}
	c, _ := newComposer("New", 0, gmail.Outgoing{}, fTo)
	c.suggest = func(string, []string, int) []store.Contact { return contacts }
	m.composer = c
	for _, r := range "dam" {
		m.composeKey(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if len(c.sugg) != 3 {
		t.Fatalf("suggestions: %d", len(c.sugg))
	}
	m.composeKey(tea.KeyPressMsg{Code: tea.KeyDown})
	if c.suggIdx != 1 {
		t.Fatalf("down: idx=%d (key %q)", c.suggIdx, tea.KeyPressMsg{Code: tea.KeyDown}.String())
	}
	m.composeKey(tea.KeyPressMsg{Code: tea.KeyTab})
	if got := c.to.Value(); got != "Damien <dm@y.io>, " {
		t.Fatalf("accepted: %q", got)
	}
}

func TestNonKeyMsgKeepsSuggestionSelection(t *testing.T) {
	c, _ := newComposer("New", 0, gmail.Outgoing{}, fTo)
	c.suggest = func(string, []string, int) []store.Contact { return []store.Contact{{Email: "a@x"}, {Email: "b@x"}} }
	c.update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	c.suggIdx = 1
	c.update(struct{}{}) // e.g. a cursor blink
	if c.suggIdx != 1 || len(c.sugg) != 2 {
		t.Fatalf("selection reset by a non-key message: idx=%d n=%d", c.suggIdx, len(c.sugg))
	}
}

func TestMisleadingLinks(t *testing.T) {
	for _, c := range []struct {
		text, url string
		want      bool
	}{
		{"www.electricien-eure.fr", "https://electricite-eure.com/", true},
		{"paypal.com", "https://paypa1-login.ru/x", true},
		{"www.bred.fr", "https://www.bred.fr", false},
		{"bred.fr", "https://client.bred.fr/login", false},
		{"Check activity", "https://evil.example", false}, // plain words: not a claim about the URL
		{"jm@x.io", "mailto:jm@x.io", false},
	} {
		if got := misleading(gmail.Link{Text: c.text, URL: c.url}); got != c.want {
			t.Errorf("%q → %q: got %v", c.text, c.url, got)
		}
	}
}
