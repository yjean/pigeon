package store

import (
	"slices"
	"testing"
)

func TestViewQuery(t *testing.T) {
	for view, want := range map[string]struct {
		labels []string
		q      string
	}{
		"INBOX":        {[]string{"INBOX"}, ""},
		"":             {nil, ""},
		"INBOX+UNREAD": {nil, "in:inbox is:unread"},
		"UNREAD":       {nil, "is:unread"},
		"SENT+UNREAD":  {nil, "in:sent is:unread"},
	} {
		labels, q := viewQuery(view)
		if !slices.Equal(labels, want.labels) || q != want.q {
			t.Errorf("%q: got %v %q", view, labels, q)
		}
	}
}
