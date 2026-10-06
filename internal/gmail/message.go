package gmail

import (
	"html"
	"mime"
	"net/mail"
	"slices"
	"strconv"
	"strings"
	"time"
)

var wordDecoder = mime.WordDecoder{}

// Header returns the first header with that name (case-insensitive), decoded.
func (p Part) Header(name string) string {
	for _, h := range p.Headers {
		if strings.EqualFold(h.Name, name) {
			if strings.Contains(h.Value, "=?") {
				if d, err := wordDecoder.DecodeHeader(h.Value); err == nil {
					return d
				}
			}
			return h.Value
		}
	}
	return ""
}

func (m Message) Header(name string) string { return m.Payload.Header(name) }

// Time is when Gmail received the message.
func (m Message) Time() time.Time {
	ms, _ := strconv.ParseInt(m.InternalDate, 10, 64)
	return time.UnixMilli(ms)
}

func (m Message) HasLabel(l string) bool { return slices.Contains(m.LabelIDs, l) }

// Address is a parsed mailbox: display name + email.
type Address struct{ Name, Email string }

// Short is the display name, or the email when there is none.
func (a Address) Short() string {
	if a.Name != "" {
		return a.Name
	}
	return a.Email
}

// ParseAddress parses a single From-like header, tolerating malformed input.
func ParseAddress(s string) Address {
	if a, err := mail.ParseAddress(s); err == nil {
		return Address{Name: a.Name, Email: a.Address}
	}
	if i, j := strings.LastIndex(s, "<"), strings.LastIndex(s, ">"); i >= 0 && j > i {
		return Address{Name: strings.Trim(strings.TrimSpace(s[:i]), `"`), Email: s[i+1 : j]}
	}
	return Address{Email: strings.TrimSpace(s)}
}

// ParseAddressList parses To/Cc headers.
func ParseAddressList(s string) []Address {
	if s == "" {
		return nil
	}
	if list, err := mail.ParseAddressList(s); err == nil {
		out := make([]Address, len(list))
		for i, a := range list {
			out[i] = Address{Name: a.Name, Email: a.Address}
		}
		return out
	}
	var out []Address
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, ParseAddress(p))
		}
	}
	return out
}

// Summary is what the thread list shows. It is cached on disk.
type Summary struct {
	ID        string    `json:"id"`
	HistoryID string    `json:"history_id"`
	Subject   string    `json:"subject"`
	From      string    `json:"from"` // "Julian, me"
	Snippet   string    `json:"snippet"`
	Date      time.Time `json:"date"`
	Count     int       `json:"count"`
	Unread    bool      `json:"unread"`
	Starred   bool      `json:"starred"`
	LastID    string    `json:"last_id"` // latest message, the one "mark unread" applies to
}

// Summarize builds the list row of a thread fetched with GetThread.
// me is the account address, displayed as "me" like Gmail does.
func Summarize(t *Thread, me string) Summary {
	s := Summary{ID: t.ID, HistoryID: t.HistoryID, Count: len(t.Messages), Snippet: html.UnescapeString(t.Snippet)}
	if len(t.Messages) == 0 {
		return s
	}
	s.Subject = t.Messages[0].Header("Subject")
	last := t.Messages[len(t.Messages)-1]
	s.Date, s.LastID = last.Time(), last.ID
	if last.Snippet != "" {
		s.Snippet = html.UnescapeString(last.Snippet)
	}
	var names []string
	for _, m := range t.Messages {
		s.Unread = s.Unread || m.HasLabel("UNREAD")
		s.Starred = s.Starred || m.HasLabel("STARRED")
		a := ParseAddress(m.Header("From"))
		name := a.Short()
		if strings.EqualFold(a.Email, me) {
			name = "me"
		}
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	if len(names) > 1 { // several participants: first names only, like Gmail
		for i, n := range names {
			if f, _, ok := strings.Cut(n, " "); ok && !strings.Contains(n, "@") {
				names[i] = strings.TrimRight(f, ",;")
			}
		}
	}
	s.From = strings.Join(names, ", ")
	return s
}
