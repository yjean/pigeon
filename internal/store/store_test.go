package store

import (
	"slices"
	"strings"
	"testing"
)

func TestViewQuery(t *testing.T) {
	for view, want := range map[string]struct {
		labels []string
		q      string
	}{
		"INBOX":                {[]string{"INBOX"}, ""},
		"":                     {nil, ""},
		"INBOX+UNREAD":         {nil, "in:inbox is:unread"},
		"UNREAD":               {nil, "is:unread"},
		"SENT+UNREAD":          {nil, "in:sent is:unread"},
		"q:from:julian":        {nil, "from:julian"},
		"q:from:julian+UNREAD": {nil, "from:julian is:unread"},
	} {
		labels, q := viewQuery(view)
		if !slices.Equal(labels, want.labels) || q != want.q {
			t.Errorf("%q: got %v %q", view, labels, q)
		}
	}
}

func TestListFileIsSafeForSearches(t *testing.T) {
	if f := listFile("q:from:a/b subject:\"x y\""); strings.ContainsAny(f, "/: \"") {
		t.Fatalf("unsafe file name %q", f)
	}
	if listFile("INBOX") != "list-INBOX.v4.json" {
		t.Fatal(listFile("INBOX"))
	}
}
