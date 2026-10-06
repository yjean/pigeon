package store

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/yoann/pigeon/internal/gmail"
)

// Contact is an address learned from mail: people you wrote to (Sent) rank
// above people who only wrote to you (Received).
type Contact struct {
	Name     string    `json:"name,omitempty"`
	Email    string    `json:"email"`
	Sent     int       `json:"sent,omitempty"`
	Received int       `json:"received,omitempty"`
	Last     time.Time `json:"last"`
}

func (c Contact) Address() gmail.Address { return gmail.Address{Name: c.Name, Email: c.Email} }

type contactBook struct {
	mu        sync.Mutex
	loaded    bool
	byEmail   map[string]*Contact
	sentAfter int64 // newest harvested Sent message, epoch seconds
}

type contactsFile struct {
	Contacts  []*Contact `json:"contacts"`
	SentAfter int64      `json:"sent_after"`
}

const (
	harvestFirst       = 500 // Sent messages scanned on the first run
	harvestConcurrency = 4   // stay well under Gmail's per-user rate limit
)

func (a *Account) book() *contactBook {
	b := &a.contacts
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.loaded {
		b.byEmail = map[string]*Contact{}
		var f contactsFile
		if a.readJSON("contacts.json", &f) {
			for _, c := range f.Contacts {
				b.byEmail[strings.ToLower(c.Email)] = c
			}
			b.sentAfter = f.SentAfter
		}
		b.loaded = true
	}
	return b
}

func (a *Account) saveContacts() {
	b := a.book()
	b.mu.Lock()
	f := contactsFile{SentAfter: b.sentAfter}
	for _, c := range b.byEmail {
		cc := *c
		f.Contacts = append(f.Contacts, &cc)
	}
	b.mu.Unlock()
	a.writeJSON("contacts.json", f)
}

// learn records addresses seen in mail. sent=true for recipients of my mail.
func (a *Account) learn(addrs []gmail.Address, at time.Time, sent bool) {
	b := a.book()
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, ad := range addrs {
		key := strings.ToLower(strings.TrimSpace(ad.Email))
		if key == "" || !strings.Contains(key, "@") || key == strings.ToLower(a.Email) {
			continue
		}
		c := b.byEmail[key]
		if c == nil {
			c = &Contact{Email: ad.Email}
			b.byEmail[key] = c
		}
		if ad.Name != "" && !strings.Contains(ad.Name, "@") {
			c.Name = ad.Name
		}
		if sent {
			c.Sent++
		} else {
			c.Received++
		}
		if at.After(c.Last) {
			c.Last = at
		}
	}
}

// learnFromThreads records senders of synced threads (not my own messages).
func (a *Account) learnFromThreads(threads []*gmail.Thread) {
	for _, t := range threads {
		for _, m := range t.Messages {
			if from := gmail.ParseAddress(m.Header("From")); !strings.EqualFold(from.Email, a.Email) {
				a.learn([]gmail.Address{from}, m.Time(), false)
			}
		}
	}
}

// HarvestContacts scans Sent mail for recipients: the last 500 messages the
// first time, then only messages newer than the previous harvest.
func (a *Account) HarvestContacts(ctx context.Context) error {
	c, err := a.Client(ctx)
	if err != nil {
		return err
	}
	b := a.book()
	b.mu.Lock()
	query, limit := "in:sent", harvestFirst
	if b.sentAfter > 0 {
		query, limit = fmt.Sprintf("in:sent after:%d", b.sentAfter), 2000
	}
	b.mu.Unlock()

	ids, err := c.ListMessages(ctx, query, limit)
	if err != nil {
		return err
	}
	var mu sync.Mutex
	newest := int64(0)
	sem := make(chan struct{}, harvestConcurrency)
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			m, err := c.GetMessageHeaders(ctx, id, "To", "Cc", "Bcc")
			if err != nil {
				return
			}
			var to []gmail.Address
			for _, h := range []string{"To", "Cc", "Bcc"} {
				to = append(to, gmail.ParseAddressList(m.Header(h))...)
			}
			a.learn(to, m.Time(), true)
			mu.Lock()
			newest = max(newest, m.Time().Unix())
			mu.Unlock()
		})
	}
	wg.Wait()
	if newest > 0 {
		b.mu.Lock()
		b.sentAfter = max(b.sentAfter, newest)
		b.mu.Unlock()
	}
	a.saveContacts()
	return nil
}

// LearnSent records the recipients of a message I just sent.
func (a *Account) LearnSent(o gmail.Outgoing) {
	addrs := append(gmail.ParseAddressList(o.To), gmail.ParseAddressList(o.Cc)...)
	a.learn(addrs, time.Now(), true)
	a.saveContacts()
}

var automated = []string{"noreply", "no-reply", "donotreply", "do-not-reply", "notification", "mailer-daemon", "bounce", "postmaster"}

func isAutomated(email string) bool {
	e := strings.ToLower(email)
	return slices.ContainsFunc(automated, func(s string) bool { return strings.Contains(e, s) })
}

// Suggest returns up to n contacts matching what is being typed, best first.
// exclude lists emails already in the field.
func (a *Account) Suggest(typed string, exclude []string, n int) []Contact {
	tok := strings.ToLower(strings.TrimSpace(typed))
	if tok == "" {
		return nil
	}
	b := a.book()
	b.mu.Lock()
	defer b.mu.Unlock()

	type hit struct {
		c    Contact
		rank int // 0 = prefix match, 1 = substring
	}
	var hits []hit
	for key, c := range b.byEmail {
		if slices.ContainsFunc(exclude, func(e string) bool { return strings.EqualFold(e, key) }) {
			continue
		}
		if c.Sent == 0 && isAutomated(key) {
			continue
		}
		rank := -1
		words := strings.FieldsFunc(strings.ToLower(c.Name)+" "+key, func(r rune) bool {
			return r == ' ' || r == '.' || r == '_' || r == '-' || r == '@' || r == '+'
		})
		switch {
		case strings.HasPrefix(key, tok) || strings.HasPrefix(strings.ToLower(c.Name), tok):
			rank = 0
		case slices.ContainsFunc(words, func(w string) bool { return strings.HasPrefix(w, tok) }):
			rank = 0
		case strings.Contains(key, tok) || strings.Contains(strings.ToLower(c.Name), tok):
			rank = 1
		}
		if rank >= 0 {
			hits = append(hits, hit{*c, rank})
		}
	}
	score := func(c Contact) int {
		s := c.Sent*5 + c.Received
		if time.Since(c.Last) < 90*24*time.Hour {
			s += 10
		}
		return s
	}
	slices.SortFunc(hits, func(x, y hit) int {
		if x.rank != y.rank {
			return x.rank - y.rank
		}
		if d := score(y.c) - score(x.c); d != 0 {
			return d
		}
		return y.c.Last.Compare(x.c.Last)
	})
	out := make([]Contact, 0, n)
	for _, h := range hits[:min(len(hits), n)] {
		out = append(out, h.c)
	}
	return out
}
