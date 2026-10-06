// Package store syncs one Gmail account and caches it on disk, so the UI
// renders instantly from cache and only fetches what changed.
//
// Cache layout ($XDG_CACHE_HOME or ~/.cache)/pigeon/<email>/:
//
//	list-<label>.vN.json thread summaries of a mailbox, newest first
//	t/<threadID>.json   full thread (bodies), keyed by historyId
package store

import (
	"context"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/yoann/pigeon/internal/auth"
	"github.com/yoann/pigeon/internal/config"
	"github.com/yoann/pigeon/internal/gmail"
)

// cacheVersion is bumped whenever gmail.Summarize changes, invalidating cached lists.
const cacheVersion = 4

// PageSize is how many threads a mailbox shows.
const PageSize = 50

// fetchConcurrency bounds parallel Gmail calls (quota is 250 units/s/user).
const fetchConcurrency = 8

type Account struct {
	Email string
	dir   string

	clientOnce sync.Once
	client     *gmail.Client
	clientErr  error

	mu        sync.Mutex
	summaries map[string]gmail.Summary // by thread ID, across mailboxes

	meOnce sync.Once
	me     gmail.Address

	historyID string // last seen mailbox history ID (Changes)
}

func Open(email string) (*Account, error) {
	base := os.Getenv("XDG_CACHE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		base = filepath.Join(home, ".cache")
	}
	dir := filepath.Join(base, "pigeon", strings.ToLower(email))
	if err := os.MkdirAll(filepath.Join(dir, "t"), 0o700); err != nil {
		return nil, err
	}
	return &Account{Email: email, dir: dir, summaries: map[string]gmail.Summary{}}, nil
}

// Client lazily builds the Gmail client (reads the token from the keychain once).
func (a *Account) Client(ctx context.Context) (*gmail.Client, error) {
	a.clientOnce.Do(func() {
		hc, err := auth.Client(context.Background(), a.Email)
		a.client, a.clientErr = gmail.New(hc), err
	})
	return a.client, a.clientErr
}

// A view names a list of threads: label IDs joined by "+", e.g. "INBOX",
// "INBOX+UNREAD", or "" for all mail.

// CachedList returns the view as last synced (nil if never synced).
func (a *Account) CachedList(view string) []gmail.Summary {
	var list []gmail.Summary
	if !a.readJSON(listFile(view), &list) {
		return nil
	}
	a.remember(list, false)
	return list
}

// SyncList fetches the view, re-downloading only threads whose historyId changed.
func (a *Account) SyncList(ctx context.Context, view string) ([]gmail.Summary, error) {
	c, err := a.Client(ctx)
	if err != nil {
		return nil, err
	}
	labels, query := viewQuery(view)
	refs, err := c.ListThreads(ctx, labels, query, PageSize)
	if err != nil {
		return nil, err
	}
	out := make([]gmail.Summary, len(refs))
	sem := make(chan struct{}, fetchConcurrency)
	var wg sync.WaitGroup
	var firstErr error
	var errOnce sync.Once
	for i, ref := range refs {
		a.mu.Lock()
		known, ok := a.summaries[ref.ID]
		a.mu.Unlock()
		if ok && known.HistoryID == ref.HistoryID {
			out[i] = known
			continue
		}
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			t, err := c.GetThread(ctx, ref.ID, false)
			if err != nil {
				errOnce.Do(func() { firstErr = err })
				return
			}
			out[i] = gmail.Summarize(t, a.Email)
		})
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	a.remember(out, true)
	a.writeJSON(listFile(view), out)
	return out, nil
}

// Thread returns a full thread, from disk when its historyId is unchanged.
func (a *Account) Thread(ctx context.Context, id, historyID string) (*gmail.Thread, error) {
	var t gmail.Thread
	if a.readJSON(filepath.Join("t", id+".json"), &t) && (historyID == "" || t.HistoryID == historyID) {
		return &t, nil
	}
	c, err := a.Client(ctx)
	if err != nil {
		return nil, err
	}
	full, err := c.GetThread(ctx, id, true)
	if err != nil {
		return nil, err
	}
	a.writeJSON(filepath.Join("t", id+".json"), full)
	return full, nil
}

// Modify adds/removes labels on a thread.
func (a *Account) Modify(ctx context.Context, id string, add, remove []string) error {
	c, err := a.Client(ctx)
	if err != nil {
		return err
	}
	if err := c.ModifyThread(ctx, id, add, remove); err != nil {
		return err
	}
	a.invalidate(id)
	return nil
}

// MarkUnread marks the latest message of a thread unread, like Gmail does
// (marking the whole thread would flag every message).
func (a *Account) MarkUnread(ctx context.Context, threadID, lastMsgID string) error {
	if lastMsgID == "" {
		return a.Modify(ctx, threadID, []string{"UNREAD"}, nil)
	}
	c, err := a.Client(ctx)
	if err != nil {
		return err
	}
	if err := c.ModifyMessage(ctx, lastMsgID, []string{"UNREAD"}, nil); err != nil {
		return err
	}
	a.invalidate(threadID)
	return nil
}

// Changed reports whether anything changed in the mailbox since the last
// call, using Gmail's history (one cheap request). The first call only sets
// the baseline. Touched threads are invalidated so the next sync refetches them.
func (a *Account) Changed(ctx context.Context) (bool, error) {
	c, err := a.Client(ctx)
	if err != nil {
		return false, err
	}
	a.mu.Lock()
	start := a.historyID
	a.mu.Unlock()
	if start == "" {
		id, err := c.HistoryID(ctx)
		if err != nil {
			return false, err
		}
		a.mu.Lock()
		a.historyID = id
		a.mu.Unlock()
		return false, nil
	}
	threads, latest, err := c.Changes(ctx, start)
	if errors.Is(err, gmail.ErrHistoryExpired) {
		a.mu.Lock()
		a.historyID = ""
		a.mu.Unlock()
		return true, nil
	}
	if err != nil {
		return false, err
	}
	a.mu.Lock()
	a.historyID = latest
	a.mu.Unlock()
	for _, id := range threads {
		a.invalidate(id)
	}
	return len(threads) > 0, nil
}

// InboxUnread is the true number of unread inbox conversations.
func (a *Account) InboxUnread(ctx context.Context) (int, error) {
	c, err := a.Client(ctx)
	if err != nil {
		return 0, err
	}
	return c.UnreadThreads(ctx, "INBOX")
}

// Trash moves a thread to the trash. Gmail drops the INBOX label when
// trashing (and untrash does not restore it), so it reports whether the
// thread was in the inbox, for Untrash.
func (a *Account) Trash(ctx context.Context, id string) (wasInbox bool, err error) {
	c, err := a.Client(ctx)
	if err != nil {
		return false, err
	}
	if t, err := c.GetThread(ctx, id, false); err == nil {
		for _, m := range t.Messages {
			wasInbox = wasInbox || m.HasLabel("INBOX")
		}
	}
	if err := c.TrashThread(ctx, id); err != nil {
		return wasInbox, err
	}
	a.invalidate(id)
	return wasInbox, nil
}

// Untrash restores a thread from the trash, back into the inbox if toInbox.
func (a *Account) Untrash(ctx context.Context, id string, toInbox bool) error {
	c, err := a.Client(ctx)
	if err != nil {
		return err
	}
	if err := c.UntrashThread(ctx, id); err != nil {
		return err
	}
	if toInbox {
		if err := c.ModifyThread(ctx, id, []string{"INBOX"}, nil); err != nil {
			return err
		}
	}
	a.invalidate(id)
	return nil
}

// invalidate forces the thread summary to be re-fetched on the next sync.
func (a *Account) invalidate(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if s, ok := a.summaries[id]; ok {
		s.HistoryID = ""
		a.summaries[id] = s
	}
}

// SaveCachedList persists a locally modified list so a restart shows it as is.
func (a *Account) SaveCachedList(view string, list []gmail.Summary) {
	a.writeJSON(listFile(view), list)
}

// remember indexes summaries. Cached (possibly stale) lists never overwrite
// fresher in-memory entries; synced lists always do.
// viewQuery turns a view into a Gmail request. Unread views use search, which
// matches per message ("a message both unread and in the inbox", as in
// Gmail's Unread section); labelIds match per thread, so a thread with a read
// inbox message and an unread archived one would wrongly count as unread.
//
// A search view is "q:<gmail query>" (optionally with "+UNREAD").
func viewQuery(view string) (labels []string, query string) {
	base, unread := strings.CutSuffix(view, "+UNREAD")
	if q, ok := strings.CutPrefix(base, "q:"); ok {
		if unread {
			q += " is:unread"
		}
		return nil, q
	}
	if view == "UNREAD" {
		base, unread = "", true
	}
	if !unread {
		if view == "" {
			return nil, ""
		}
		return strings.Split(view, "+"), ""
	}
	op := map[string]string{"": "", "INBOX": "in:inbox", "STARRED": "is:starred", "SENT": "in:sent", "DRAFT": "in:drafts"}[base]
	if op == "" && base != "" {
		op = "label:" + base
	}
	return nil, strings.TrimSpace(op + " is:unread")
}

func listFile(view string) string {
	if strings.HasPrefix(view, "q:") { // free text: hash it into a safe file name
		view = fmt.Sprintf("search-%x", sha1.Sum([]byte(view)))[:19]
	}
	return fmt.Sprintf("list-%s.v%d.json", view, cacheVersion)
}

func (a *Account) remember(list []gmail.Summary, fresh bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, s := range list {
		if _, ok := a.summaries[s.ID]; fresh || !ok {
			a.summaries[s.ID] = s
		}
	}
}

func (a *Account) readJSON(name string, v any) bool {
	data, err := os.ReadFile(filepath.Join(a.dir, name))
	return err == nil && json.Unmarshal(data, v) == nil
}

func (a *Account) writeJSON(name string, v any) {
	if data, err := json.Marshal(v); err == nil {
		_ = config.WriteFileAtomic(filepath.Join(a.dir, name), data, 0o600)
	}
}

// Me returns the account's sender identity (display name fetched once from Gmail).
func (a *Account) Me(ctx context.Context) gmail.Address {
	a.meOnce.Do(func() {
		a.me = gmail.Address{Email: a.Email}
		if c, err := a.Client(ctx); err == nil {
			if name, err := c.DisplayName(ctx); err == nil {
				a.me.Name = name
			}
		}
	})
	return a.me
}

// Send sends a message from this account.
func (a *Account) Send(ctx context.Context, o gmail.Outgoing) error {
	c, err := a.Client(ctx)
	if err != nil {
		return err
	}
	o.From = a.Me(ctx)
	return c.Send(ctx, o)
}

// SaveDraft stores a message in this account's Drafts.
func (a *Account) SaveDraft(ctx context.Context, o gmail.Outgoing) error {
	c, err := a.Client(ctx)
	if err != nil {
		return err
	}
	o.From = a.Me(ctx)
	return c.SaveDraft(ctx, o)
}
