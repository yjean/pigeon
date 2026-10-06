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
	"encoding/json"
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
const cacheVersion = 2

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

// CachedList returns the mailbox as last synced (nil if never synced).
func (a *Account) CachedList(label string) []gmail.Summary {
	var list []gmail.Summary
	if !a.readJSON(listFile(label), &list) {
		return nil
	}
	a.remember(list, false)
	return list
}

// SyncList fetches the mailbox, re-downloading only threads whose historyId changed.
func (a *Account) SyncList(ctx context.Context, label string) ([]gmail.Summary, error) {
	c, err := a.Client(ctx)
	if err != nil {
		return nil, err
	}
	refs, err := c.ListThreads(ctx, label, PageSize)
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
	a.writeJSON(listFile(label), out)
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

// MarkRead removes UNREAD from every message of the thread.
func (a *Account) MarkRead(ctx context.Context, id string) error {
	c, err := a.Client(ctx)
	if err != nil {
		return err
	}
	if err := c.ModifyThread(ctx, id, nil, []string{"UNREAD"}); err != nil {
		return err
	}
	a.mu.Lock()
	if s, ok := a.summaries[id]; ok {
		s.Unread = false
		s.HistoryID = "" // force a re-fetch of the summary on next sync
		a.summaries[id] = s
	}
	a.mu.Unlock()
	return nil
}

// remember indexes summaries. Cached (possibly stale) lists never overwrite
// fresher in-memory entries; synced lists always do.
func listFile(label string) string { return fmt.Sprintf("list-%s.v%d.json", label, cacheVersion) }

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
