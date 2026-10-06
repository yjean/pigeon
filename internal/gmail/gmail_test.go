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

func TestRawAndReply(t *testing.T) {
	me := Address{Name: "Yoann", Email: "yoann@42.works"}
	th := &Thread{ID: "T1", Messages: []Message{{
		InternalDate: "1700000000000",
		Payload: Part{Headers: []Header{
			{"From", "Julian Doe <j@x.io>"}, {"To", "yoann@42.works, Damon <d@x.io>"}, {"Cc", "s@x.io"},
			{"Subject", "Budget"}, {"Message-ID", "<abc@x.io>"}, {"References", "<root@x.io>"},
		}},
	}}}
	th.Messages[0].Payload.MimeType = "text/plain"
	th.Messages[0].Payload.Body.Data = base64.URLEncoding.EncodeToString([]byte("Hi Yoann"))

	r := Reply(th, me, true)
	if r.To != "Julian Doe <j@x.io>" || r.Cc != "Damon <d@x.io>, s@x.io" {
		t.Fatalf("recipients: to=%q cc=%q", r.To, r.Cc)
	}
	if r.Subject != "Re: Budget" || r.InReplyTo != "<abc@x.io>" || r.References != "<root@x.io> <abc@x.io>" || r.ThreadID != "T1" {
		t.Fatalf("headers: %+v", r)
	}
	if !strings.Contains(r.Body, "> Hi Yoann") {
		t.Fatalf("quote missing: %q", r.Body)
	}
	if one := Reply(th, me, false); one.Cc != "" {
		t.Fatalf("reply (not all) should have no cc: %q", one.Cc)
	}

	r.Body = "Ça marche, merci !\n\n" + r.Body
	raw, err := r.Raw()
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	for _, want := range []string{"From: \"Yoann\" <yoann@42.works>\r\n", "To: \"Julian Doe\" <j@x.io>\r\n", "Subject: Re: Budget\r\n",
		"In-Reply-To: <abc@x.io>\r\n", "Content-Transfer-Encoding: quoted-printable", "=C3=87a marche"} {
		if !strings.Contains(s, want) {
			t.Errorf("raw missing %q:\n%s", want, s)
		}
	}
	if _, err := (Outgoing{From: me, To: "not an address"}).Raw(); err == nil {
		t.Error("expected invalid recipient error")
	}
	if f := Forward(th, me); f.Subject != "Fwd: Budget" || f.To != "" || !strings.Contains(f.Body, "Forwarded message") {
		t.Errorf("forward: %+v", f)
	}
}

func TestLinksAndAttachments(t *testing.T) {
	enc := func(s string) string { return base64.URLEncoding.EncodeToString([]byte(s)) }
	m := Message{ID: "m1", Payload: Part{MimeType: "multipart/mixed", Parts: []Part{
		{MimeType: "multipart/alternative", Parts: []Part{
			{MimeType: "text/plain"}, {MimeType: "text/html"},
		}},
		{MimeType: "application/pdf", Filename: "devis.pdf", Headers: []Header{{"Content-Disposition", "attachment; filename=devis.pdf"}}},
		{MimeType: "image/png", Filename: "logo.png", Headers: []Header{{"Content-ID", "<logo>"}}},
	}}}
	m.Payload.Parts[0].Parts[0].Body.Data = enc("See https://example.com/a. Also https://example.com/report, thanks")
	m.Payload.Parts[0].Parts[1].Body.Data = enc(`<p><a href="https://example.com/report">Open <b>report</b></a>
		<a href="javascript:alert(1)">x</a> <a href="file:///etc/passwd">f</a> <a href="mailto:j@x.io">Julian</a></p>`)
	m.Payload.Parts[1].Body.AttachmentID = "att1"

	links := m.Links()
	var got []string
	for _, l := range links {
		got = append(got, l.Text+"|"+l.URL)
	}
	want := "Open report|https://example.com/report,Julian|mailto:j@x.io,|https://example.com/a"
	if strings.Join(got, ",") != want {
		t.Fatalf("links:\n got %v\nwant %v", strings.Join(got, ","), want)
	}

	atts := m.AttachmentParts()
	if len(atts) != 2 || atts[0].Filename != "devis.pdf" || atts[0].Inline || !atts[1].Inline {
		t.Fatalf("attachments: %+v", atts)
	}
	if names := m.Attachments(); len(names) != 1 || names[0] != "devis.pdf" {
		t.Fatalf("names: %v", names)
	}
	for _, bad := range []string{"file:///etc/passwd", "javascript:alert(1)", "vscode://x", "https://", "ftp://x"} {
		if SafeURL(bad) {
			t.Errorf("%q must not be openable", bad)
		}
	}
}
