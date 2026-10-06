package store

import (
	"testing"
	"time"

	"github.com/yoann/pigeon/internal/gmail"
)

func TestSuggest(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	a, _ := Open("yoann@42.works")
	now := time.Now()
	a.learn([]gmail.Address{{Name: "Julian Doe", Email: "julian@x.io"}}, now, true)
	a.learn([]gmail.Address{{Name: "Julia Roberts", Email: "jr@y.io"}}, now, false)
	a.learn([]gmail.Address{{Email: "noreply@julius.io"}}, now, false)
	a.learn([]gmail.Address{{Name: "Me", Email: "Yoann@42.works"}}, now, true)

	names := func(cs []Contact) (out []string) {
		for _, c := range cs {
			out = append(out, c.Email)
		}
		return
	}
	got := names(a.Suggest("jul", nil, 5))
	if len(got) != 2 || got[0] != "julian@x.io" || got[1] != "jr@y.io" {
		t.Fatalf("jul → %v (want sent-to first, no noreply)", got)
	}
	if got := names(a.Suggest("doe", nil, 5)); len(got) != 1 || got[0] != "julian@x.io" {
		t.Fatalf("last name match → %v", got)
	}
	if got := a.Suggest("jul", []string{"JULIAN@x.io"}, 5); len(got) != 1 {
		t.Fatalf("exclude failed: %v", got)
	}
	if got := a.Suggest("yoann", nil, 5); len(got) != 0 {
		t.Fatalf("own address suggested: %v", got)
	}

	// Persisted and reloaded.
	a.saveContacts()
	b, _ := Open("yoann@42.works")
	if got := b.Suggest("julian", nil, 5); len(got) != 1 || got[0].Name != "Julian Doe" || got[0].Sent != 1 {
		t.Fatalf("reload: %+v", got)
	}
}
