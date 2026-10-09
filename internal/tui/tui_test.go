package tui

import (
	"os"
	"path/filepath"
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

func TestDroppedPaths(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "Devis signé 104.pdf")
	b := filepath.Join(dir, "notes.txt")
	os.WriteFile(a, []byte("pdf"), 0o600)
	os.WriteFile(b, []byte("txt"), 0o600)
	esc := func(p string) string { return strings.ReplaceAll(p, " ", `\ `) }

	for in, want := range map[string]int{
		esc(a):                 1, // Ghostty: backslash-escaped spaces
		"'" + a + "'":          1, // quoted
		esc(a) + " " + esc(b):  2, // several files
		"file://" + b:          1,
		"hello world":          0, // regular text paste
		dir:                    0, // a folder is not attachable
		esc(a) + " not-a-file": 0, // any non-file token: treat as text
	} {
		if got := droppedPaths(in); len(got) != want {
			t.Errorf("%q → %v, want %d paths", in, got, want)
		}
	}
	if got := droppedPaths(esc(a)); len(got) != 1 || got[0] != a {
		t.Errorf("unescaped path: %v", got)
	}
}

func TestQuitAsksFirst(t *testing.T) {
	isQuit := func(cmd tea.Cmd) bool {
		if cmd == nil {
			return false
		}
		_, ok := cmd().(tea.QuitMsg)
		return ok
	}
	m := &Model{accts: []*account{{box: inbox}}}
	if isQuit(m.onKey("q")) || !m.confirmQuit {
		t.Fatal("q should ask for confirmation, not quit")
	}
	if isQuit(m.onKey("esc")) || m.confirmQuit {
		t.Fatal("another key should cancel")
	}
	m.onKey("q")
	if !isQuit(m.onKey("q")) {
		t.Fatal("q q should quit")
	}
}

func TestWithSignature(t *testing.T) {
	if got := withSignature("", "-- \nMe"); got != "\n\n-- \nMe" {
		t.Fatalf("new message: %q", got)
	}
	if got := withSignature("\n\nOn Mon, J wrote:\n> hi", "-- \nMe"); got != "\n\n-- \nMe\n\nOn Mon, J wrote:\n> hi" {
		t.Fatalf("reply: %q", got)
	}
	if got := withSignature("\n\nquoted", ""); got != "\n\nquoted" {
		t.Fatalf("no signature: %q", got)
	}
}

func TestBatchArchiveAndUndo(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	st, err := store.Open("batch@x.io")
	if err != nil {
		t.Fatal(err)
	}
	a := &account{store: st, box: inbox, hidden: map[string]time.Time{}, marked: map[string]bool{},
		unreadOnly: map[string]bool{}, keep: map[string]bool{},
		threads: []gmail.Summary{{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}}}
	m := &Model{accts: []*account{a}}
	ids := func() string {
		var out []string
		for _, s := range a.threads {
			out = append(out, s.ID)
		}
		return strings.Join(out, "")
	}

	m.toggleMark() // a
	m.moveTo(1)
	m.toggleMark() // b
	m.toggleMark() // b again: unmarked
	m.moveTo(2)
	m.toggleMark() // c
	if a.markedCount() != 2 || a.sel != 2 {
		t.Fatalf("marked %d, sel %d", a.markedCount(), a.sel)
	}
	m.act("archive")
	if ids() != "bd" || a.markedCount() != 0 || len(m.lastUndo.items) != 2 {
		t.Fatalf("after archive: %q, %d marked", ids(), a.markedCount())
	}
	m.undo()
	if ids() != "abcd" {
		t.Fatalf("after undo: %q", ids())
	}

	m.act("star") // nothing marked: the selected thread only
	starred := 0
	for _, s := range a.threads {
		if s.Starred {
			starred++
		}
	}
	if starred != 1 || !a.threads[a.sel].Starred {
		t.Fatalf("star should apply to the selection only: %+v", a.threads)
	}
}
