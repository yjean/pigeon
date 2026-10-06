package gmail

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestStripQuoted(t *testing.T) {
	in := "Sounds good, thanks!\n\nOn Mon, Oct 5, 2026 at 5:37 PM Julian <j@x.io>\nwrote:\n> Hi Yoann\n> are we ok?\n"
	if got := StripQuoted(in); got != "Sounds good, thanks!" {
		t.Fatalf("got %q", got)
	}
	fr := "Parfait.\n\nLe lun. 5 oct. 2026 à 17:37, Julian <j@x.io> a écrit :\n> Salut"
	if got := StripQuoted(fr); got != "Parfait." {
		t.Fatalf("got %q", got)
	}
}

func TestHTMLToText(t *testing.T) {
	got := HTMLToText(`<html><head><style>p{}</style></head><body><p>Hello <b>Yoann</b>,</p><ul><li>one</li><li>two</li></ul><div>Bye<br>J</div></body></html>`)
	for _, want := range []string{"Hello Yoann,", "• one", "• two", "Bye\nJ"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
	if strings.Contains(got, "p{}") {
		t.Errorf("style leaked: %q", got)
	}
}

func TestBodyPrefersPlainAndDecodesCharset(t *testing.T) {
	enc := func(b []byte) string { return base64.URLEncoding.EncodeToString(b) }
	m := Message{Payload: Part{MimeType: "multipart/alternative", Parts: []Part{
		{MimeType: "text/plain", Headers: []Header{{"Content-Type", "text/plain; charset=iso-8859-1"}}},
		{MimeType: "text/html"},
	}}}
	m.Payload.Parts[0].Body.Data = enc([]byte("Caf\xe9 cr\xe8me"))
	m.Payload.Parts[1].Body.Data = enc([]byte("<p>html</p>"))
	if got := m.Body(); got != "Café crème" {
		t.Fatalf("got %q", got)
	}
}

func TestSummarize(t *testing.T) {
	msg := func(from string, labels ...string) Message {
		return Message{LabelIDs: labels, InternalDate: "1700000000000", Payload: Part{Headers: []Header{{"From", from}, {"Subject", "Hi"}}}}
	}
	s := Summarize(&Thread{ID: "t", Messages: []Message{
		msg(`"LEITAO, Marc" <m@x.io>`), msg("Yoann <yoann@42.works>"), msg("Julian Doe <j@x.io>", "UNREAD"),
	}}, "yoann@42.works")
	if s.From != "LEITAO, me, Julian" || !s.Unread || s.Count != 3 || s.Subject != "Hi" {
		t.Fatalf("got %+v", s)
	}
}
