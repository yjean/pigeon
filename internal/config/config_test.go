package config

import (
	"testing"
	"time"
)

func TestInitials(t *testing.T) {
	for email, want := range map[string]string{
		"yoann@42.works":      "YO",
		"yoann.amsellem@x.io": "YA",
		"j_doe@x.io":          "JD",
		"a@x.io":              "A",
	} {
		if got := (Account{Email: email}).Initials(); got != want {
			t.Errorf("%s: got %q want %q", email, got, want)
		}
	}
}

func TestAccountStore(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, e := range []string{"a@x.io", "b@x.io", "A@x.io"} {
		if _, err := UpsertAccount(Account{Email: e, AddedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := LoadAccounts()
	if len(got) != 2 || got[0].Email != "a@x.io" || got[1].Email != "b@x.io" {
		t.Fatalf("unexpected accounts: %+v", got)
	}
	if ok, _ := RemoveAccount("A@X.IO"); !ok {
		t.Fatal("remove should be case-insensitive")
	}
	got, _ = LoadAccounts()
	if len(got) != 1 || got[0].Email != "b@x.io" {
		t.Fatalf("unexpected accounts after remove: %+v", got)
	}
}
